// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package resolver

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type WebhookHandler struct {
	resolver ParameterGetter
	logger   logr.Logger
}

func NewWebhookHandler(parameterResolver ParameterGetter, logger logr.Logger) *WebhookHandler {
	return &WebhookHandler{
		resolver: parameterResolver,
		logger:   logger,
	}
}

func (h *WebhookHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	if len(req.Object.Raw) == 0 {
		return admission.Allowed("empty object payload")
	}

	resource := &unstructured.Unstructured{}
	if err := resource.UnmarshalJSON(req.Object.Raw); err != nil {
		return admission.Errored(400, err)
	}

	annotations := resource.GetAnnotations()
	if len(annotations) == 0 {
		return admission.Allowed("resource has no annotations")
	}

	sources, err := DecodeSourcesAnnotation(annotations[SourcesAnnotationKey])
	if err != nil {
		h.logger.Error(err, "failed to decode sources annotation", "resource", resource.GetName(), "namespace", resource.GetNamespace())
		sources = map[string]string{}
	}

	resolvedAny := false
	for key, value := range annotations {
		if key == SourcesAnnotationKey || key == ReconcileAtAnnotationKey {
			continue
		}

		parameterPath, isPlaceholder := ExtractParameterPath(value)
		if !isPlaceholder {
			continue
		}

		resolvedValue, resolveErr := h.resolver.GetParameter(ctx, parameterPath)
		if resolveErr != nil {
			h.logger.Error(
				resolveErr,
				"failed to resolve SSM parameter for annotation",
				"resource", resource.GetName(),
				"namespace", resource.GetNamespace(),
				"annotationKey", key,
				"parameterPath", parameterPath,
			)
			continue
		}

		annotations[key] = resolvedValue
		sources[key] = parameterPath
		resolvedAny = true
	}

	if !resolvedAny {
		return admission.Allowed("no SSM placeholders were resolved")
	}

	for key := range sources {
		if _, exists := annotations[key]; !exists {
			delete(sources, key)
		}
	}

	encodedSources, err := EncodeSourcesAnnotation(sources)
	if err != nil {
		return admission.Errored(500, err)
	}
	annotations[SourcesAnnotationKey] = encodedSources
	resource.SetAnnotations(annotations)

	mutatedRaw, err := json.Marshal(resource.Object)
	if err != nil {
		return admission.Errored(500, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, mutatedRaw)
}
