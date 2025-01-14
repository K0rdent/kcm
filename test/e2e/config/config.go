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

package config

import (
	"encoding/base64"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

type TestingProvider string

const (
	envVarE2EConfig = "E2E_CONFIG_B64"

	TestingProviderAWS     TestingProvider = "aws"
	TestingProviderAzure   TestingProvider = "azure"
	TestingProviderVsphere TestingProvider = "vsphere"
)

var (
	Config TestingConfig

	defaultConfig = map[TestingProvider][]ProviderTestingConfig{
		TestingProviderAWS:     {},
		TestingProviderAzure:   {},
		TestingProviderVsphere: {},
	}

	defaultStandaloneTemplates = map[TestingProvider]string{
		TestingProviderAWS:     "aws-standalone-cp-0-0-4",
		TestingProviderAzure:   "azure-standalone-cp-0-0-4",
		TestingProviderVsphere: "vsphere-standalone-cp-0-0-3",
	}

	defaultHostedTemplates = map[TestingProvider]string{
		TestingProviderAWS:     "aws-hosted-cp-0-0-3",
		TestingProviderAzure:   "azure-hosted-cp-0-0-3",
		TestingProviderVsphere: "vsphere-hosted-cp-0-0-3",
	}
)

type TestingConfig = map[TestingProvider][]ProviderTestingConfig

type ProviderTestingConfig struct {
	// Standalone contains the testing configuration for the standalone cluster deployment.
	Standalone *ClusterTestingConfig `yaml:"standalone,omitempty"`
	// Standalone contains the testing configuration for the hosted cluster deployment.
	Hosted *ClusterTestingConfig `yaml:"hosted,omitempty"`
}

type ClusterTestingConfig struct {
	// Upgrade is a boolean parameter that specifies whether the cluster deployment upgrade should be tested.
	Upgrade bool `yaml:"upgrade,omitempty"`
	// Template is the name of the template to use when creating a cluster deployment.
	// If unset:
	// * The latest available template will be chosen
	// * If upgrade is triggered, the latest available template with available upgrades will be chosen.
	Template string `yaml:"template,omitempty"`
	// UpgradeTemplate specifies the name of the template to upgrade to. Ignored if upgrade is set to false.
	// If unset, the latest template available for the upgrade will be chosen.
	UpgradeTemplate string `yaml:"upgradeTemplate,omitempty"`
}

func Parse() error {
	decodedConfig, err := base64.StdEncoding.DecodeString(os.Getenv(envVarE2EConfig))
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(decodedConfig, &Config)
	if err != nil {
		return err
	}

	setDefaults()
	_, _ = fmt.Fprintf(GinkgoWriter, "E2e testing configuration:\n%s\n", Show())
	return nil
}

func setDefaults() {
	if len(Config) == 0 {
		Config = defaultConfig
	}
	for provider, configs := range Config {
		if len(configs) == 0 {
			Config[provider] = []ProviderTestingConfig{
				{
					Standalone: &ClusterTestingConfig{},
					Hosted:     &ClusterTestingConfig{},
				},
			}
		}
		for i := range Config[provider] {
			config := Config[provider][i]
			if config.Standalone != nil && config.Standalone.Template == "" {
				config.Standalone.Template = defaultStandaloneTemplates[provider]
			}
			if config.Hosted != nil && config.Hosted.Template == "" {
				config.Hosted.Template = defaultHostedTemplates[provider]
			}
			Config[provider][i] = config
		}
	}
}

func Show() string {
	prettyConfig, err := yaml.Marshal(Config)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}

func (c *ProviderTestingConfig) String() string {
	prettyConfig, err := yaml.Marshal(c)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}
