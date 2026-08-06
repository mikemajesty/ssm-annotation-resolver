// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package resolver

import (
	"encoding/json"
	"strings"
)

const (
	PlaceholderPrefix        = "#:ssm:"
	SourcesAnnotationKey     = "ssm-annotation-resolver.io/sources"
	ReconcileAtAnnotationKey = "ssm-annotation-resolver.io/reconcile-at"
)

func ExtractParameterPath(value string) (string, bool) {
	if !strings.HasPrefix(value, PlaceholderPrefix) {
		return "", false
	}

	path := strings.TrimSpace(strings.TrimPrefix(value, PlaceholderPrefix))
	if path == "" {
		return "", false
	}

	return path, true
}

func DecodeSourcesAnnotation(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}

	var sources map[string]string
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return nil, err
	}

	if sources == nil {
		return map[string]string{}, nil
	}

	return sources, nil
}

func EncodeSourcesAnnotation(sources map[string]string) (string, error) {
	if sources == nil {
		sources = map[string]string{}
	}

	payload, err := json.Marshal(sources)
	if err != nil {
		return "", err
	}

	return string(payload), nil
}
