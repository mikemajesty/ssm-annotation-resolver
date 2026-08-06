// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SsmAnnotationResolverInfraSpec defines the desired state of SsmAnnotationResolverInfra
type SsmAnnotationResolverInfraSpec struct {
	// SQS Queue Configuration
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SQSQueueName string `json:"sqsQueueName"`

	// DLQ (Dead Letter Queue) name
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DLQQueueName string `json:"dlqQueueName"`

	// EventBridge Rule name for SSM Parameter Store events
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	EventBridgeRuleName string `json:"eventBridgeRuleName"`

	// IAM Role name for IRSA (IAM Roles for Service Accounts)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	IAMRoleName string `json:"iamRoleName"`

	// AWS Region for SQS/EventBridge/IAM resources
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AWSRegion string `json:"awsRegion"`

	// EKS OIDC Provider ARN for IAM trust relationship
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	OIDCProviderArn string `json:"oidcProviderArn"`

	// Kubernetes Service Account that will assume the IAM role
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="ssm-annotation-resolver"
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// SsmAnnotationResolverInfraOutputs contains the outputs from AWS provisioning
// +kubebuilder:object:generate=true
type SsmAnnotationResolverInfraOutputs struct {
	// SQS Queue URL
	SQSQueueUrl string `json:"sqsQueueUrl"`

	// SQS Queue ARN
	SQSQueueArn string `json:"sqsQueueArn"`

	// DLQ Queue URL
	DLQQueueUrl string `json:"dlqQueueUrl"`

	// DLQ Queue ARN
	DLQQueueArn string `json:"dlqQueueArn"`

	// IAM Role ARN for IRSA
	IAMRoleArn string `json:"iamRoleArn"`

	// EventBridge Rule ARN
	EventBridgeRuleArn string `json:"eventBridgeRuleArn"`
}

// SsmAnnotationResolverInfraStatus defines the observed state of SsmAnnotationResolverInfra
// +kubebuilder:object:generate=true
type SsmAnnotationResolverInfraStatus struct {
	// Phase of the infrastructure provisioning
	// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Failed
	Phase string `json:"phase,omitempty"`

	// Provisioning outputs from AWS
	// +kubebuilder:validation:Optional
	Outputs *SsmAnnotationResolverInfraOutputs `json:"outputs,omitempty"`

	// Human-readable error message if phase is Failed
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// Last update timestamp
	// +kubebuilder:validation:Optional
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ssminfra;ssminfras
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="SQS Queue URL",type=string,JSONPath=`.status.outputs.sqsQueueUrl`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// SsmAnnotationResolverInfra is the Schema for the ssm annotation resolver infrastructure API
// It allows declarative provisioning of AWS SQS, EventBridge, and IAM resources required by the SSM Annotation Resolver webhook
type SsmAnnotationResolverInfra struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SsmAnnotationResolverInfraSpec   `json:"spec,omitempty"`
	Status SsmAnnotationResolverInfraStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SsmAnnotationResolverInfraList contains a list of SsmAnnotationResolverInfra
type SsmAnnotationResolverInfraList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SsmAnnotationResolverInfra `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SsmAnnotationResolverInfra{}, &SsmAnnotationResolverInfraList{})
}
