// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	"github.com/mikemajesty/ssm-annotation-resolver/pkg/resolver"
)

// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch;patch

type RunnerOptions struct {
	Log             logr.Logger
	DynamicClient   dynamic.Interface
	DiscoveryClient discovery.DiscoveryInterface
	AWSConfig       aws.Config

	SQSQueueURL        string
	SQSWaitTimeSeconds int
	SQSMaxMessages     int
}

type Runner struct {
	log             logr.Logger
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	sqsClient       sqsAPI

	sqsQueueURL        string
	sqsWaitTimeSeconds int32
	sqsMaxMessages     int32

	reconcileByParameterPathFunc func(ctx context.Context, parameterPath string) error
}

type sqsAPI interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

func NewRunner(opts RunnerOptions) *Runner {
	return newRunnerWithSQSClient(opts, sqs.NewFromConfig(opts.AWSConfig))
}

func newRunnerWithSQSClient(opts RunnerOptions, sqsClient sqsAPI) *Runner {
	waitSeconds := int32(opts.SQSWaitTimeSeconds)
	if waitSeconds <= 0 {
		waitSeconds = 20
	}

	maxMessages := int32(opts.SQSMaxMessages)
	if maxMessages <= 0 || maxMessages > 10 {
		maxMessages = 10
	}

	runner := &Runner{
		log:                opts.Log.WithName("ssm-annotation-reconciler"),
		dynamicClient:      opts.DynamicClient,
		discoveryClient:    opts.DiscoveryClient,
		sqsClient:          sqsClient,
		sqsQueueURL:        strings.TrimSpace(opts.SQSQueueURL),
		sqsWaitTimeSeconds: waitSeconds,
		sqsMaxMessages:     maxMessages,
	}

	runner.reconcileByParameterPathFunc = runner.reconcileByParameterPath

	return runner
}

func (r *Runner) Start(ctx context.Context) error {
	if err := r.reconcileUnresolvedPlaceholders(ctx); err != nil {
		r.log.Error(err, "failed startup placeholder recovery")
	}

	if r.sqsQueueURL == "" {
		r.log.Info("SQS queue URL not configured; skipping change-event reconciliation loop")
		<-ctx.Done()
		return nil
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		if err := r.processQueueBatch(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}

			r.log.Error(err, "failed processing SQS message batch")
			time.Sleep(2 * time.Second)
		}
	}
}

func (r *Runner) NeedLeaderElection() bool {
	return true
}

func (r *Runner) processQueueBatch(ctx context.Context) error {
	resp, err := r.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(r.sqsQueueURL),
		MaxNumberOfMessages: r.sqsMaxMessages,
		WaitTimeSeconds:     r.sqsWaitTimeSeconds,
	})
	if err != nil {
		return fmt.Errorf("receiving messages from SQS: %w", err)
	}

	for _, msg := range resp.Messages {
		if msg.Body == nil || msg.ReceiptHandle == nil {
			continue
		}

		parameterPath, extractErr := extractParameterPathFromEvent(*msg.Body)
		if extractErr != nil {
			r.log.Error(extractErr, "unable to parse parameter path from SQS message; dropping message")
			if err := r.deleteMessage(ctx, *msg.ReceiptHandle); err != nil {
				r.log.Error(err, "unable to delete invalid SQS message")
			}
			continue
		}

		if err := r.reconcileByParameterPathFunc(ctx, parameterPath); err != nil {
			return err
		}

		if err := r.deleteMessage(ctx, *msg.ReceiptHandle); err != nil {
			return err
		}
	}

	return nil
}

func (r *Runner) reconcileUnresolvedPlaceholders(ctx context.Context) error {
	return r.forEachPatchableResource(ctx, func(gvr schema.GroupVersionResource, item unstructured.Unstructured) error {
		annotations := item.GetAnnotations()
		if len(annotations) == 0 {
			return nil
		}

		for _, value := range annotations {
			if _, isPlaceholder := resolver.ExtractParameterPath(value); isPlaceholder {
				return r.touchResource(ctx, gvr, item)
			}
		}

		return nil
	})
}

func (r *Runner) reconcileByParameterPath(ctx context.Context, parameterPath string) error {
	return r.forEachPatchableResource(ctx, func(gvr schema.GroupVersionResource, item unstructured.Unstructured) error {
		annotations := item.GetAnnotations()
		if len(annotations) == 0 {
			return nil
		}

		sources, err := resolver.DecodeSourcesAnnotation(annotations[resolver.SourcesAnnotationKey])
		if err != nil {
			r.log.Error(err, "unable to decode sources annotation", "resource", item.GetName(), "namespace", item.GetNamespace())
			return nil
		}

		for _, sourcePath := range sources {
			if sourcePath == parameterPath {
				return r.touchResource(ctx, gvr, item)
			}
		}

		return nil
	})
}

func (r *Runner) forEachPatchableResource(ctx context.Context, fn func(gvr schema.GroupVersionResource, item unstructured.Unstructured) error) error {
	apiResourceLists, err := r.discoveryClient.ServerPreferredResources()
	if err != nil && len(apiResourceLists) == 0 {
		return fmt.Errorf("discovering API resources: %w", err)
	}

	for _, resourceList := range apiResourceLists {
		groupVersion, parseErr := schema.ParseGroupVersion(resourceList.GroupVersion)
		if parseErr != nil {
			r.log.Error(parseErr, "unable to parse group version", "groupVersion", resourceList.GroupVersion)
			continue
		}

		for _, apiResource := range resourceList.APIResources {
			if strings.Contains(apiResource.Name, "/") {
				continue
			}

			verbs := sets.New[string](apiResource.Verbs...)
			if !verbs.Has("list") || !verbs.Has("patch") {
				continue
			}

			gvr := groupVersion.WithResource(apiResource.Name)
			if err := r.listAndHandleResource(ctx, gvr, apiResource.Namespaced, fn); err != nil {
				r.log.Error(err, "unable to scan resource type", "gvr", gvr.String())
			}
		}
	}

	return nil
}

func (r *Runner) listAndHandleResource(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	namespaced bool,
	fn func(gvr schema.GroupVersionResource, item unstructured.Unstructured) error,
) error {
	var (
		list *unstructured.UnstructuredList
		err  error
	)

	if namespaced {
		list, err = r.dynamicClient.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	} else {
		list, err = r.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	}

	if err != nil {
		return err
	}

	for _, item := range list.Items {
		if err := fn(gvr, item); err != nil {
			return err
		}
	}

	return nil
}

func (r *Runner) touchResource(ctx context.Context, gvr schema.GroupVersionResource, item unstructured.Unstructured) error {
	reconcileTimestamp := time.Now().UTC().Format(time.RFC3339Nano)
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				resolver.ReconcileAtAnnotationKey: reconcileTimestamp,
			},
		},
	}

	payload, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	resource := r.dynamicClient.Resource(gvr)
	if item.GetNamespace() != "" {
		_, err = resource.Namespace(item.GetNamespace()).Patch(ctx, item.GetName(), types.MergePatchType, payload, metav1.PatchOptions{})
	} else {
		_, err = resource.Patch(ctx, item.GetName(), types.MergePatchType, payload, metav1.PatchOptions{})
	}
	if err != nil {
		return fmt.Errorf("patching %s/%s for reconcile trigger: %w", item.GetNamespace(), item.GetName(), err)
	}

	return nil
}

func (r *Runner) deleteMessage(ctx context.Context, receiptHandle string) error {
	_, err := r.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(r.sqsQueueURL),
		ReceiptHandle: aws.String(receiptHandle),
	})
	if err != nil {
		return fmt.Errorf("deleting SQS message: %w", err)
	}

	return nil
}

func extractParameterPathFromEvent(messageBody string) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(messageBody), &payload); err != nil {
		return "", err
	}

	if path := lookupNestedString(payload, "detail", "name"); path != "" {
		return path, nil
	}

	if path := lookupNestedString(payload, "detail", "requestParameters", "name"); path != "" {
		return path, nil
	}

	return "", errors.New("message did not contain a Parameter Store path at detail.name or detail.requestParameters.name")
}

func lookupNestedString(value map[string]any, keys ...string) string {
	current := any(value)
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}

		next, exists := m[key]
		if !exists {
			return ""
		}

		current = next
	}

	asString, _ := current.(string)
	return asString
}
