// Copyright 2024
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/aws"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Describe("AWS Templates", Label("provider:cloud", "provider:aws"), Ordered, func() {
	var (
		kc                     *kubeclient.KubeClient
		standaloneClient       *kubeclient.KubeClient
		hostedDeleteFuncs      []func() error
		standaloneDeleteFuncs  []func() error
		kubeconfigDeleteFuncs  []func() error
		standaloneClusterNames []string
		hostedClusterNames     []string

		providerConfigs []config.ProviderTestingConfig
	)

	BeforeAll(func() {
		By("get testing configuration")
		providerConfigs = config.Config[config.TestingProviderAWS]

		if len(providerConfigs) == 0 {
			Skip("AWS ClusterDeployment testing is skipped")
		}

		By("providing cluster identity")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		ci := clusteridentity.New(kc, clusterdeployment.ProviderAWS)
		Expect(os.Setenv(clusterdeployment.EnvVarAWSClusterIdentity, ci.IdentityName)).Should(Succeed())
	})

	AfterAll(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() && !noCleanup() {
			if kc != nil {
				By("collecting failure logs from the management controllers")
				logs.Collector{
					Client:        kc,
					ProviderTypes: []clusterdeployment.ProviderType{clusterdeployment.ProviderAWS, clusterdeployment.ProviderCAPI},
					ClusterNames:  standaloneClusterNames,
				}.CollectAll()
			}
			if standaloneClient != nil {
				By("collecting failure logs from hosted controllers")
				logs.Collector{
					Client:        standaloneClient,
					ProviderTypes: []clusterdeployment.ProviderType{clusterdeployment.ProviderAWS, clusterdeployment.ProviderCAPI},
					ClusterNames:  hostedClusterNames,
				}.CollectAll()
			}
		}

		if !noCleanup() {
			By("deleting resources")
			deleteFuncs := append(hostedDeleteFuncs, append(standaloneDeleteFuncs, kubeconfigDeleteFuncs...)...)
			for _, deleteFunc := range deleteFuncs {
				err := deleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	It("should work with an AWS provider", func() {
		for i, providerConfig := range providerConfigs {
			_, _ = fmt.Fprintf(GinkgoWriter, "Testing configuration:\n%s\n", providerConfig.String())
			// Deploy a standalone cluster and verify it is running/ready.
			// Deploy standalone with an xlarge instance since it will also be
			// hosting the hosted cluster.
			GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")

			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("aws-%d", i))
			sdTemplate := providerConfig.Standalone.Template
			templateBy(templates.TemplateAWSStandaloneCP, fmt.Sprintf("creating a ClusterDeployment %s with template %s", sdName, sdTemplate))

			sd := clusterdeployment.GetUnstructured(templates.TemplateAWSStandaloneCP, sdName, sdTemplate)

			standaloneDeleteFunc := kc.CreateClusterDeployment(context.Background(), sd)
			standaloneDeleteFuncs = append(standaloneDeleteFuncs, standaloneDeleteFunc)
			standaloneClusterNames = append(standaloneClusterNames, sd.GetName())

			templateBy(templates.TemplateAWSStandaloneCP, "waiting for infrastructure to deploy successfully")
			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateAWSStandaloneCP,
				sdName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			if providerConfig.Hosted == nil {
				continue
			}

			templateBy(templates.TemplateAWSHostedCP, "installing controller and templates on standalone cluster")

			// Download the KUBECONFIG for the standalone cluster and load it
			// so we can call Make targets against this cluster.
			// TODO(#472): Ideally we shouldn't use Make here and should just
			// convert these Make targets into Go code, but this will require a
			// helmclient.
			kubeCfgPath, kubecfgDeleteFunc := kc.WriteKubeconfig(context.Background(), sdName)
			kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

			GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
			cmd := exec.Command("make", "test-apply")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

			templateBy(templates.TemplateAWSHostedCP, "validating that the controller is ready")
			standaloneClient = kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, sdName)
			Eventually(func() error {
				err := verifyControllersUp(standaloneClient)
				if err != nil {
					_, _ = fmt.Fprintf(
						GinkgoWriter, "[%s] controller validation failed: %v\n",
						templates.TemplateAWSHostedCP, err)
					return err
				}
				return nil
			}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// Ensure AWS credentials are set in the standalone cluster.
			clusteridentity.New(standaloneClient, clusterdeployment.ProviderAWS)

			// Populate the environment variables required for the hosted
			// cluster.
			aws.PopulateHostedTemplateVars(context.Background(), kc, sdName)

			hdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("aws-hosted-%d", i))
			hdTemplate := providerConfig.Hosted.Template
			templateBy(templates.TemplateAWSHostedCP, fmt.Sprintf("creating a hosted ClusterDeployment %s with template %s", hdName, hdTemplate))
			hd := clusterdeployment.GetUnstructured(templates.TemplateAWSHostedCP, hdName, hdTemplate)

			// Deploy the hosted cluster on top of the standalone cluster.
			hostedDeleteFunc := standaloneClient.CreateClusterDeployment(context.Background(), hd)
			hostedDeleteFuncs = append(hostedDeleteFuncs, hostedDeleteFunc)
			hostedClusterNames = append(hostedClusterNames, hd.GetName())

			templateBy(templates.TemplateAWSHostedCP, "Patching AWSCluster to ready")
			clusterdeployment.PatchHostedClusterReady(standaloneClient, clusterdeployment.ProviderAWS, hdName)

			// Verify the hosted cluster is running/ready.
			templateBy(templates.TemplateAWSHostedCP, "waiting for infrastructure to deploy successfully")
			deploymentValidator = clusterdeployment.NewProviderValidator(
				templates.TemplateAWSHostedCP,
				hdName,
				clusterdeployment.ValidationActionDeploy,
			)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), standaloneClient)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// Delete the hosted ClusterDeployment and verify it is removed.
			templateBy(templates.TemplateAWSHostedCP, "deleting the ClusterDeployment")
			err = hostedDeleteFunc()
			Expect(err).NotTo(HaveOccurred())

			deletionValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateAWSHostedCP,
				hdName,
				clusterdeployment.ValidationActionDelete,
			)
			Eventually(func() error {
				return deletionValidator.Validate(context.Background(), standaloneClient)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			// Now delete the standalone ClusterDeployment and verify it is
			// removed, it is deleted last since it is the basis for the hosted
			// cluster.
			/*
				FIXME(#339): This is currently disabled as the deletion of the
				standalone cluster is failing due to outstanding issues.
				templateBy(clusterdeployment.TemplateAWSStandaloneCP, "deleting the ClusterDeployment")
				err = standaloneDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				deletionValidator = clusterdeployment.NewProviderValidator(
					clusterdeployment.TemplateAWSStandaloneCP,
					clusterName,
					clusterdeployment.ValidationActionDelete,
				)
				Eventually(func() error {
					return deletionValidator.Validate(context.Background(), kc)
				}).WithTimeout(10 * time.Minute).WithPolling(10 *
					time.Second).Should(Succeed())
			*/
		}
	})
})
