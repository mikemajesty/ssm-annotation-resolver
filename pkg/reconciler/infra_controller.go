// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ssmapiiv1 "github.com/mikemajesty/ssm-annotation-resolver/api/v1"
)

// SsmAnnotationResolverInfraReconciler reconciles SsmAnnotationResolverInfra objects and provisions AWS infrastructure
type SsmAnnotationResolverInfraReconciler struct {
	client.Client
	Log       logr.Logger
	Scheme    *runtime.Scheme
	AWSConfig aws.Config
}

// +kubebuilder:rbac:groups=ssm-annotation-resolver.io,resources=ssminfras,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ssm-annotation-resolver.io,resources=ssminfras/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ssm-annotation-resolver.io,resources=ssminfras/finalizers,verbs=update

func (r *SsmAnnotationResolverInfraReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ssminfra", req.NamespacedName)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	infra := &ssmapiiv1.SsmAnnotationResolverInfra{}
	if err := r.Get(ctx, req.NamespacedName, infra); err != nil {
		if apierrors.IsNotFound(err) {
			// Resource deleted
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get SsmAnnotationResolverInfra")
		return ctrl.Result{}, err
	}

	// Initialize status if not set
	if infra.Status.Phase == "" {
		infra.Status.Phase = "Pending"
		if err := r.Status().Update(ctx, infra); err != nil {
			log.Error(err, "Failed to update status to Pending")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Provision AWS infrastructure
	if infra.Status.Phase == "Pending" || infra.Status.Phase == "Provisioning" {
		log.Info("Starting provisioning", "spec", infra.Spec)

		// Update status to Provisioning
		infra.Status.Phase = "Provisioning"
		now := metav1.Now()
		infra.Status.LastUpdateTime = &now
		if err := r.Status().Update(ctx, infra); err != nil {
			log.Error(err, "Failed to update status to Provisioning")
			return ctrl.Result{}, err
		}

		// Provision infrastructure
		outputs, err := r.provisionInfrastructure(ctx, infra, log)
		if err != nil {
			log.Error(err, "Failed to provision infrastructure")
			infra.Status.Phase = "Failed"
			infra.Status.Message = fmt.Sprintf("Provisioning failed: %v", err)
			now = metav1.Now()
			infra.Status.LastUpdateTime = &now
			_ = r.Status().Update(ctx, infra)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		// Update status to Ready with outputs
		infra.Status.Phase = "Ready"
		infra.Status.Outputs = outputs
		infra.Status.Message = "Infrastructure provisioned successfully"
		now = metav1.Now()
		infra.Status.LastUpdateTime = &now
		if err := r.Status().Update(ctx, infra); err != nil {
			log.Error(err, "Failed to update status to Ready")
			return ctrl.Result{}, err
		}

		log.Info("Infrastructure provisioning completed successfully")
	}

	return ctrl.Result{}, nil
}

// provisionInfrastructure creates SQS queues and IAM role
// Note: EventBridge rule should be created via Terraform or AWS CLI
func (r *SsmAnnotationResolverInfraReconciler) provisionInfrastructure(
	ctx context.Context,
	infra *ssmapiiv1.SsmAnnotationResolverInfra,
	log logr.Logger,
) (*ssmapiiv1.SsmAnnotationResolverInfraOutputs, error) {

	sqsClient := sqs.NewFromConfig(r.AWSConfig)
	iamClient := iam.NewFromConfig(r.AWSConfig)

	// 1. Create DLQ first
	dlqUrl, dlqArn, err := r.createQueue(ctx, sqsClient, infra.Spec.DLQQueueName, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ: %w", err)
	}
	log.Info("DLQ created", "url", dlqUrl)

	// 2. Create main SQS queue with DLQ redrive policy
	sqsUrl, sqsArn, err := r.createQueueWithDLQ(ctx, sqsClient, infra.Spec.SQSQueueName, dlqArn, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create SQS queue: %w", err)
	}
	log.Info("SQS queue created", "url", sqsUrl)

	// 3. Create IAM role for IRSA
	roleArn, err := r.createIAMRole(ctx, iamClient, infra.Spec.IAMRoleName, infra.Spec.OIDCProviderArn, infra.Spec.ServiceAccountName, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM role: %w", err)
	}
	log.Info("IAM role created", "arn", roleArn)

	// 4. Grant SQS permissions to the role
	if err := r.attachSQSPolicy(ctx, iamClient, infra.Spec.IAMRoleName, sqsArn, dlqArn, log); err != nil {
		return nil, fmt.Errorf("failed to attach SQS policy: %w", err)
	}
	log.Info("SQS policy attached to IAM role")

	// Note: EventBridge rule can be created via Terraform/AWS CLI/CDK
	// Example: aws events put-rule --name ssm-parameter-store-changes --event-pattern '{"source":["aws.ssm"]}'

	return &ssmapiiv1.SsmAnnotationResolverInfraOutputs{
		SQSQueueUrl:        sqsUrl,
		SQSQueueArn:        sqsArn,
		DLQQueueUrl:        dlqUrl,
		DLQQueueArn:        dlqArn,
		IAMRoleArn:         roleArn,
		EventBridgeRuleArn: "", // Will be created via external tool
	}, nil
}

func (r *SsmAnnotationResolverInfraReconciler) createQueue(
	ctx context.Context,
	sqsClient *sqs.Client,
	queueName string,
	log logr.Logger,
) (string, string, error) {

	output, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
		Attributes: map[string]string{
			"MessageRetentionPeriod": "1209600", // 14 days
			"VisibilityTimeout":      "300",      // 5 minutes
		},
	})
	if err != nil {
		return "", "", err
	}

	queueUrl := aws.ToString(output.QueueUrl)

	// Get queue ARN from queue attributes
	attrsOutput, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: []sqstypes.QueueAttributeName{"QueueArn"},
	})
	if err != nil {
		return "", "", err
	}

	queueArn := attrsOutput.Attributes["QueueArn"]
	return queueUrl, queueArn, nil
}

func (r *SsmAnnotationResolverInfraReconciler) createQueueWithDLQ(
	ctx context.Context,
	sqsClient *sqs.Client,
	queueName string,
	dlqArn string,
	log logr.Logger,
) (string, string, error) {

	output, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
		Attributes: map[string]string{
			"MessageRetentionPeriod": "86400",  // 1 day
			"VisibilityTimeout":      "300",    // 5 minutes
			"RedrivePolicy": fmt.Sprintf(`{
				"deadLetterTargetArn": "%s",
				"maxReceiveCount": 3
			}`, dlqArn),
		},
	})
	if err != nil {
		return "", "", err
	}

	queueUrl := aws.ToString(output.QueueUrl)

	// Get queue ARN
	attrsOutput, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: []sqstypes.QueueAttributeName{"QueueArn"},
	})
	if err != nil {
		return "", "", err
	}

	queueArn := attrsOutput.Attributes["QueueArn"]
	return queueUrl, queueArn, nil
}

func (r *SsmAnnotationResolverInfraReconciler) createIAMRole(
	ctx context.Context,
	iamClient *iam.Client,
	roleName string,
	oidcProviderArn string,
	serviceAccountName string,
	log logr.Logger,
) (string, error) {

	// Parse OIDC provider ARN to get OIDC provider ID
	// Format: arn:aws:iam::123456789012:oidc-provider/oidc.eks.region.amazonaws.com/id/EXAMPLEID
	parts := strings.Split(oidcProviderArn, "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid OIDC provider ARN format: %s", oidcProviderArn)
	}

	oidcProvider := strings.Join(parts[len(parts)-2:], "/")

	trustPolicy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"Federated": "%s"
				},
				"Action": "sts:AssumeRoleWithWebIdentity",
				"Condition": {
					"StringEquals": {
						"%s:sub": "system:serviceaccount:envoy-gateway-system:%s",
						"%s:aud": "sts.amazonaws.com"
					}
				}
			}
		]
	}`, oidcProviderArn, oidcProvider, serviceAccountName, oidcProvider)

	output, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
		Description:             aws.String("IAM role for SSM Annotation Resolver IRSA"),
		Tags: []iamtypes.Tag{
			{
				Key:   aws.String("ManagedBy"),
				Value: aws.String("ssm-annotation-resolver"),
			},
		},
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(output.Role.Arn), nil
}

func (r *SsmAnnotationResolverInfraReconciler) attachSQSPolicy(
	ctx context.Context,
	iamClient *iam.Client,
	roleName string,
	sqsArn string,
	dlqArn string,
	log logr.Logger,
) error {

	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Action": [
					"sqs:ReceiveMessage",
					"sqs:DeleteMessage",
					"sqs:GetQueueAttributes"
				],
				"Resource": ["%s", "%s"]
			}
		]
	}`, sqsArn, dlqArn)

	_, err := iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String("ssm-annotation-resolver-sqs"),
		PolicyDocument: aws.String(policy),
	})

	return err
}

func (r *SsmAnnotationResolverInfraReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ssmapiiv1.SsmAnnotationResolverInfra{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
