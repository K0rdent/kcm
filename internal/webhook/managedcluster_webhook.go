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

package webhook

import (
	"context"
	"errors"
	"fmt"
	"sort"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/Mirantis/hmc/api/v1alpha1"
)

type ManagedClusterValidator struct {
	client.Client
}

var errInvalidManagedCluster = errors.New("the ManagedCluster is invalid")

func (v *ManagedClusterValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&v1alpha1.ManagedCluster{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ManagedClusterValidator{}
	_ webhook.CustomDefaulter = &ManagedClusterValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagedClusterValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	managedCluster, ok := obj.(*v1alpha1.ManagedCluster)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ManagedCluster but got a %T", obj))
	}
	template, err := v.getManagedClusterTemplate(ctx, managedCluster.Namespace, managedCluster.Spec.Template)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", errInvalidManagedCluster, err)
	}
	err = v.isTemplateValid(ctx, template)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", errInvalidManagedCluster, err)
	}
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagedClusterValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	newManagedCluster, ok := newObj.(*v1alpha1.ManagedCluster)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ManagedCluster but got a %T", newObj))
	}
	template, err := v.getManagedClusterTemplate(ctx, newManagedCluster.Namespace, newManagedCluster.Spec.Template)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", errInvalidManagedCluster, err)
	}
	err = v.isTemplateValid(ctx, template)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", errInvalidManagedCluster, err)
	}
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*ManagedClusterValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (v *ManagedClusterValidator) Default(ctx context.Context, obj runtime.Object) error {
	managedCluster, ok := obj.(*v1alpha1.ManagedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected ManagedCluster but got a %T", obj))
	}

	// Only apply defaults when there's no configuration provided
	if managedCluster.Spec.Config != nil {
		return nil
	}
	template, err := v.getManagedClusterTemplate(ctx, managedCluster.Namespace, managedCluster.Spec.Template)
	if err != nil {
		return fmt.Errorf("could not get template for the managedcluster: %s", err)
	}
	err = v.isTemplateValid(ctx, template)
	if err != nil {
		return fmt.Errorf("template is invalid: %s", err)
	}
	if template.Status.Config == nil {
		return nil
	}
	managedCluster.Spec.DryRun = true
	managedCluster.Spec.Config = &apiextensionsv1.JSON{Raw: template.Status.Config.Raw}
	return nil
}

func (v *ManagedClusterValidator) getManagedClusterTemplate(ctx context.Context, templateNamespace, templateName string) (*v1alpha1.ClusterTemplate, error) {
	template := &v1alpha1.ClusterTemplate{}
	templateRef := client.ObjectKey{Name: templateName, Namespace: templateNamespace}
	if err := v.Get(ctx, templateRef, template); err != nil {
		return nil, err
	}
	return template, nil
}

func (v *ManagedClusterValidator) isTemplateValid(ctx context.Context, template *v1alpha1.ClusterTemplate) error {
	if !template.Status.Valid {
		return fmt.Errorf("the template is not valid: %s", template.Status.ValidationError)
	}
	err := v.verifyProviders(ctx, template)
	if err != nil {
		return fmt.Errorf("providers verification failed: %v", err)
	}
	return nil
}

func (v *ManagedClusterValidator) verifyProviders(ctx context.Context, template *v1alpha1.ClusterTemplate) error {
	requiredProviders := template.Status.Providers
	management := &v1alpha1.Management{}
	managementRef := client.ObjectKey{Name: v1alpha1.ManagementName}
	if err := v.Get(ctx, managementRef, management); err != nil {
		return err
	}

	exposedProviders := management.Status.AvailableProviders
	missingProviders := make(map[string][]string)
	missingProviders["bootstrap"] = getMissingProviders(exposedProviders.BootstrapProviders, requiredProviders.BootstrapProviders)
	missingProviders["control plane"] = getMissingProviders(exposedProviders.ControlPlaneProviders, requiredProviders.ControlPlaneProviders)
	missingProviders["infrastructure"] = getMissingProviders(exposedProviders.InfrastructureProviders, requiredProviders.InfrastructureProviders)

	var errs []error
	for providerType, missing := range missingProviders {
		if len(missing) > 0 {
			sort.Slice(missing, func(i, j int) bool {
				return missing[i] < missing[j]
			})
			errs = append(errs, fmt.Errorf("one or more required %s providers are not deployed yet: %v", providerType, missing))
		}
	}
	if len(errs) > 0 {
		sort.Slice(errs, func(i, j int) bool {
			return errs[i].Error() < errs[j].Error()
		})
		return errors.Join(errs...)
	}
	return nil
}

func getMissingProviders(exposedProviders, requiredProviders []v1alpha1.ProviderTuple) (missing []string) {
	exposedSet := make(map[string]struct{}, len(requiredProviders))
	for _, v := range exposedProviders {
		exposedSet[v.Name] = struct{}{}
	}

	for _, v := range requiredProviders {
		if _, ok := exposedSet[v.Name]; !ok {
			missing = append(missing, v.Name)
		}
	}

	return missing
}
