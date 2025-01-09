// SPDX-License-Identifier: AGPL-3.0-only

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v0alpha1

// GitHubRepositoryConfigApplyConfiguration represents a declarative configuration of the GitHubRepositoryConfig type for use
// with apply.
type GitHubRepositoryConfigApplyConfiguration struct {
	Owner                     *string `json:"owner,omitempty"`
	Repository                *string `json:"repository,omitempty"`
	Branch                    *string `json:"branch,omitempty"`
	Token                     *string `json:"token,omitempty"`
	BranchWorkflow            *bool   `json:"branchWorkflow,omitempty"`
	GenerateDashboardPreviews *bool   `json:"generateDashboardPreviews,omitempty"`
	PullRequestLinter         *bool   `json:"pullRequestLinter,omitempty"`
}

// GitHubRepositoryConfigApplyConfiguration constructs a declarative configuration of the GitHubRepositoryConfig type for use with
// apply.
func GitHubRepositoryConfig() *GitHubRepositoryConfigApplyConfiguration {
	return &GitHubRepositoryConfigApplyConfiguration{}
}

// WithOwner sets the Owner field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Owner field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithOwner(value string) *GitHubRepositoryConfigApplyConfiguration {
	b.Owner = &value
	return b
}

// WithRepository sets the Repository field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Repository field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithRepository(value string) *GitHubRepositoryConfigApplyConfiguration {
	b.Repository = &value
	return b
}

// WithBranch sets the Branch field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Branch field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithBranch(value string) *GitHubRepositoryConfigApplyConfiguration {
	b.Branch = &value
	return b
}

// WithToken sets the Token field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Token field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithToken(value string) *GitHubRepositoryConfigApplyConfiguration {
	b.Token = &value
	return b
}

// WithBranchWorkflow sets the BranchWorkflow field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BranchWorkflow field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithBranchWorkflow(value bool) *GitHubRepositoryConfigApplyConfiguration {
	b.BranchWorkflow = &value
	return b
}

// WithGenerateDashboardPreviews sets the GenerateDashboardPreviews field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the GenerateDashboardPreviews field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithGenerateDashboardPreviews(value bool) *GitHubRepositoryConfigApplyConfiguration {
	b.GenerateDashboardPreviews = &value
	return b
}

// WithPullRequestLinter sets the PullRequestLinter field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PullRequestLinter field is set to the value of the last call.
func (b *GitHubRepositoryConfigApplyConfiguration) WithPullRequestLinter(value bool) *GitHubRepositoryConfigApplyConfiguration {
	b.PullRequestLinter = &value
	return b
}