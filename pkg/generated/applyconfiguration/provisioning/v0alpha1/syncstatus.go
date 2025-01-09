// SPDX-License-Identifier: AGPL-3.0-only

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v0alpha1

import (
	provisioningv0alpha1 "github.com/grafana/grafana/pkg/apis/provisioning/v0alpha1"
)

// SyncStatusApplyConfiguration represents a declarative configuration of the SyncStatus type for use
// with apply.
type SyncStatusApplyConfiguration struct {
	State     *provisioningv0alpha1.JobState `json:"state,omitempty"`
	JobID     *string                        `json:"job,omitempty"`
	Started   *int64                         `json:"started,omitempty"`
	Finished  *int64                         `json:"finished,omitempty"`
	Scheduled *int64                         `json:"scheduled,omitempty"`
	Message   []string                       `json:"message,omitempty"`
	Hash      *string                        `json:"hash,omitempty"`
}

// SyncStatusApplyConfiguration constructs a declarative configuration of the SyncStatus type for use with
// apply.
func SyncStatus() *SyncStatusApplyConfiguration {
	return &SyncStatusApplyConfiguration{}
}

// WithState sets the State field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the State field is set to the value of the last call.
func (b *SyncStatusApplyConfiguration) WithState(value provisioningv0alpha1.JobState) *SyncStatusApplyConfiguration {
	b.State = &value
	return b
}

// WithJobID sets the JobID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the JobID field is set to the value of the last call.
func (b *SyncStatusApplyConfiguration) WithJobID(value string) *SyncStatusApplyConfiguration {
	b.JobID = &value
	return b
}

// WithStarted sets the Started field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Started field is set to the value of the last call.
func (b *SyncStatusApplyConfiguration) WithStarted(value int64) *SyncStatusApplyConfiguration {
	b.Started = &value
	return b
}

// WithFinished sets the Finished field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Finished field is set to the value of the last call.
func (b *SyncStatusApplyConfiguration) WithFinished(value int64) *SyncStatusApplyConfiguration {
	b.Finished = &value
	return b
}

// WithScheduled sets the Scheduled field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Scheduled field is set to the value of the last call.
func (b *SyncStatusApplyConfiguration) WithScheduled(value int64) *SyncStatusApplyConfiguration {
	b.Scheduled = &value
	return b
}

// WithMessage adds the given value to the Message field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Message field.
func (b *SyncStatusApplyConfiguration) WithMessage(values ...string) *SyncStatusApplyConfiguration {
	for i := range values {
		b.Message = append(b.Message, values[i])
	}
	return b
}

// WithHash sets the Hash field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Hash field is set to the value of the last call.
func (b *SyncStatusApplyConfiguration) WithHash(value string) *SyncStatusApplyConfiguration {
	b.Hash = &value
	return b
}