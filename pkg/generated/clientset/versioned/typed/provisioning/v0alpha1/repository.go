// SPDX-License-Identifier: AGPL-3.0-only

// Code generated by client-gen. DO NOT EDIT.

package v0alpha1

import (
	context "context"

	provisioningv0alpha1 "github.com/grafana/grafana/pkg/apis/provisioning/v0alpha1"
	applyconfigurationprovisioningv0alpha1 "github.com/grafana/grafana/pkg/generated/applyconfiguration/provisioning/v0alpha1"
	scheme "github.com/grafana/grafana/pkg/generated/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	gentype "k8s.io/client-go/gentype"
)

// RepositoriesGetter has a method to return a RepositoryInterface.
// A group's client should implement this interface.
type RepositoriesGetter interface {
	Repositories(namespace string) RepositoryInterface
}

// RepositoryInterface has methods to work with Repository resources.
type RepositoryInterface interface {
	Create(ctx context.Context, repository *provisioningv0alpha1.Repository, opts v1.CreateOptions) (*provisioningv0alpha1.Repository, error)
	Update(ctx context.Context, repository *provisioningv0alpha1.Repository, opts v1.UpdateOptions) (*provisioningv0alpha1.Repository, error)
	// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
	UpdateStatus(ctx context.Context, repository *provisioningv0alpha1.Repository, opts v1.UpdateOptions) (*provisioningv0alpha1.Repository, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*provisioningv0alpha1.Repository, error)
	List(ctx context.Context, opts v1.ListOptions) (*provisioningv0alpha1.RepositoryList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *provisioningv0alpha1.Repository, err error)
	Apply(ctx context.Context, repository *applyconfigurationprovisioningv0alpha1.RepositoryApplyConfiguration, opts v1.ApplyOptions) (result *provisioningv0alpha1.Repository, err error)
	// Add a +genclient:noStatus comment above the type to avoid generating ApplyStatus().
	ApplyStatus(ctx context.Context, repository *applyconfigurationprovisioningv0alpha1.RepositoryApplyConfiguration, opts v1.ApplyOptions) (result *provisioningv0alpha1.Repository, err error)
	RepositoryExpansion
}

// repositories implements RepositoryInterface
type repositories struct {
	*gentype.ClientWithListAndApply[*provisioningv0alpha1.Repository, *provisioningv0alpha1.RepositoryList, *applyconfigurationprovisioningv0alpha1.RepositoryApplyConfiguration]
}

// newRepositories returns a Repositories
func newRepositories(c *ProvisioningV0alpha1Client, namespace string) *repositories {
	return &repositories{
		gentype.NewClientWithListAndApply[*provisioningv0alpha1.Repository, *provisioningv0alpha1.RepositoryList, *applyconfigurationprovisioningv0alpha1.RepositoryApplyConfiguration](
			"repositories",
			c.RESTClient(),
			scheme.ParameterCodec,
			namespace,
			func() *provisioningv0alpha1.Repository { return &provisioningv0alpha1.Repository{} },
			func() *provisioningv0alpha1.RepositoryList { return &provisioningv0alpha1.RepositoryList{} },
		),
	}
}