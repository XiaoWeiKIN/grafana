// SPDX-License-Identifier: AGPL-3.0-only

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v0alpha1 "github.com/grafana/grafana/pkg/apis/provisioning/v0alpha1"
	provisioningv0alpha1 "github.com/grafana/grafana/pkg/generated/applyconfiguration/provisioning/v0alpha1"
	typedprovisioningv0alpha1 "github.com/grafana/grafana/pkg/generated/clientset/versioned/typed/provisioning/v0alpha1"
	gentype "k8s.io/client-go/gentype"
)

// fakeRepositories implements RepositoryInterface
type fakeRepositories struct {
	*gentype.FakeClientWithListAndApply[*v0alpha1.Repository, *v0alpha1.RepositoryList, *provisioningv0alpha1.RepositoryApplyConfiguration]
	Fake *FakeProvisioningV0alpha1
}

func newFakeRepositories(fake *FakeProvisioningV0alpha1, namespace string) typedprovisioningv0alpha1.RepositoryInterface {
	return &fakeRepositories{
		gentype.NewFakeClientWithListAndApply[*v0alpha1.Repository, *v0alpha1.RepositoryList, *provisioningv0alpha1.RepositoryApplyConfiguration](
			fake.Fake,
			namespace,
			v0alpha1.SchemeGroupVersion.WithResource("repositories"),
			v0alpha1.SchemeGroupVersion.WithKind("Repository"),
			func() *v0alpha1.Repository { return &v0alpha1.Repository{} },
			func() *v0alpha1.RepositoryList { return &v0alpha1.RepositoryList{} },
			func(dst, src *v0alpha1.RepositoryList) { dst.ListMeta = src.ListMeta },
			func(list *v0alpha1.RepositoryList) []*v0alpha1.Repository { return gentype.ToPointerSlice(list.Items) },
			func(list *v0alpha1.RepositoryList, items []*v0alpha1.Repository) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}