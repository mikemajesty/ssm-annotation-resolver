package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/go-logr/logr"
	"k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type fakeParameterResolver struct {
	values map[string]string
	errs   map[string]error
}

func (f fakeParameterResolver) GetParameter(_ context.Context, parameterPath string) (string, error) {
	if err, exists := f.errs[parameterPath]; exists {
		return "", err
	}
	if value, exists := f.values[parameterPath]; exists {
		return value, nil
	}

	return "", errors.New("parameter not found")
}

func TestWebhookHandle_ResolvesPlaceholdersAndTracksSources(t *testing.T) {
	t.Parallel()

	req, raw := newAdmissionRequest(t, map[string]string{
		"plain": "value",
		"token": "#:ssm:/app/token",
	})

	handler := NewWebhookHandler(
		fakeParameterResolver{
			values: map[string]string{
				"/app/token": "resolved-token",
			},
		},
		discardLogger(),
	)

	resp := handler.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Fatalf("expected request to be allowed, got denied: %#v", resp.Result)
	}

	annotations := patchedAnnotations(t, req, raw, resp)
	if annotations["token"] != "resolved-token" {
		t.Fatalf("expected token to be resolved, got %q", annotations["token"])
	}

	sources, err := DecodeSourcesAnnotation(annotations[SourcesAnnotationKey])
	if err != nil {
		t.Fatalf("expected valid sources annotation, got error: %v", err)
	}
	if sources["token"] != "/app/token" {
		t.Fatalf("expected source path to be tracked, got %#v", sources)
	}
}

func TestWebhookHandle_PartialFailureStillMutatesResolvedKeys(t *testing.T) {
	t.Parallel()

	req, raw := newAdmissionRequest(t, map[string]string{
		"ok":  "#:ssm:/ok",
		"bad": "#:ssm:/bad",
	})

	handler := NewWebhookHandler(
		fakeParameterResolver{
			values: map[string]string{
				"/ok": "resolved",
			},
			errs: map[string]error{
				"/bad": errors.New("boom"),
			},
		},
		discardLogger(),
	)

	resp := handler.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Fatalf("expected request to be allowed, got denied: %#v", resp.Result)
	}

	annotations := patchedAnnotations(t, req, raw, resp)
	if annotations["ok"] != "resolved" {
		t.Fatalf("expected ok to be resolved, got %q", annotations["ok"])
	}
	if annotations["bad"] != "#:ssm:/bad" {
		t.Fatalf("expected bad placeholder to remain unchanged, got %q", annotations["bad"])
	}

	sources, err := DecodeSourcesAnnotation(annotations[SourcesAnnotationKey])
	if err != nil {
		t.Fatalf("expected valid sources annotation, got error: %v", err)
	}
	if len(sources) != 1 || sources["ok"] != "/ok" {
		t.Fatalf("expected only resolved keys in sources, got %#v", sources)
	}
}

func TestWebhookHandle_InvalidSourcesAnnotationIsRecovered(t *testing.T) {
	t.Parallel()

	req, raw := newAdmissionRequest(t, map[string]string{
		SourcesAnnotationKey: "not-json",
		"token":              "#:ssm:/app/token",
	})

	handler := NewWebhookHandler(
		fakeParameterResolver{
			values: map[string]string{
				"/app/token": "resolved-token",
			},
		},
		discardLogger(),
	)

	resp := handler.Handle(context.Background(), req)
	annotations := patchedAnnotations(t, req, raw, resp)

	sources, err := DecodeSourcesAnnotation(annotations[SourcesAnnotationKey])
	if err != nil {
		t.Fatalf("expected valid sources annotation, got error: %v", err)
	}
	if sources["token"] != "/app/token" {
		t.Fatalf("expected recovered sources map, got %#v", sources)
	}
}

func TestWebhookHandle_NoPlaceholderReturnsAllowedWithoutPatch(t *testing.T) {
	t.Parallel()

	req, _ := newAdmissionRequest(t, map[string]string{"plain": "value"})

	handler := NewWebhookHandler(fakeParameterResolver{}, discardLogger())
	resp := handler.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Fatalf("expected request to be allowed, got denied: %#v", resp.Result)
	}
	if len(resp.Patches) != 0 {
		t.Fatalf("expected no patches, got %d", len(resp.Patches))
	}
}

func newAdmissionRequest(t *testing.T, annotations map[string]string) (admission.Request, []byte) {
	t.Helper()

	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":        "demo",
			"namespace":   "default",
			"annotations": annotations,
		},
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to marshal object: %v", err)
	}

	req := admission.Request{
		AdmissionRequest: v1.AdmissionRequest{
			Object: runtime.RawExtension{Raw: raw},
		},
	}

	return req, raw
}

func patchedAnnotations(t *testing.T, req admission.Request, originalRaw []byte, resp admission.Response) map[string]string {
	t.Helper()

	if err := resp.Complete(req); err != nil {
		t.Fatalf("failed to complete response: %v", err)
	}

	if len(resp.Patch) == 0 {
		t.Fatal("expected response patch, got empty")
	}

	patch, err := jsonpatch.DecodePatch(resp.Patch)
	if err != nil {
		t.Fatalf("failed to decode patch: %v", err)
	}

	patchedRaw, err := patch.Apply(originalRaw)
	if err != nil {
		t.Fatalf("failed to apply patch: %v", err)
	}

	var patched map[string]any
	if err := json.Unmarshal(patchedRaw, &patched); err != nil {
		t.Fatalf("failed to unmarshal patched object: %v", err)
	}

	metadata, ok := patched["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing or invalid: %#v", patched["metadata"])
	}
	rawAnnotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("annotations missing or invalid: %#v", metadata["annotations"])
	}

	annotations := make(map[string]string, len(rawAnnotations))
	for key, value := range rawAnnotations {
		stringValue, ok := value.(string)
		if !ok {
			t.Fatalf("annotation %q is not string: %#v", key, value)
		}
		annotations[key] = stringValue
	}

	return annotations
}

func discardLogger() logr.Logger {
	return logr.Discard()
}
