package resolver

import (
	"testing"
)

func TestExtractParameterPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		wantPath  string
		wantMatch bool
	}{
		{name: "valid placeholder", value: "#:ssm:/app/db/password", wantPath: "/app/db/password", wantMatch: true},
		{name: "valid placeholder with spaces", value: "#:ssm:   /app/db/password   ", wantPath: "/app/db/password", wantMatch: true},
		{name: "non placeholder", value: "plain-value", wantPath: "", wantMatch: false},
		{name: "empty path", value: "#:ssm:   ", wantPath: "", wantMatch: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotPath, gotMatch := ExtractParameterPath(tt.value)
			if gotPath != tt.wantPath || gotMatch != tt.wantMatch {
				t.Fatalf("ExtractParameterPath(%q) = (%q, %v), want (%q, %v)", tt.value, gotPath, gotMatch, tt.wantPath, tt.wantMatch)
			}
		})
	}
}

func TestDecodeSourcesAnnotation(t *testing.T) {
	t.Parallel()

	t.Run("empty input returns empty map", func(t *testing.T) {
		t.Parallel()

		got, err := DecodeSourcesAnnotation("  ")
		if err != nil {
			t.Fatalf("DecodeSourcesAnnotation returned error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty map, got: %#v", got)
		}
	})

	t.Run("valid json map", func(t *testing.T) {
		t.Parallel()

		got, err := DecodeSourcesAnnotation(`{"foo":"/path/foo","bar":"/path/bar"}`)
		if err != nil {
			t.Fatalf("DecodeSourcesAnnotation returned error: %v", err)
		}
		if got["foo"] != "/path/foo" || got["bar"] != "/path/bar" {
			t.Fatalf("unexpected map content: %#v", got)
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := DecodeSourcesAnnotation("{invalid"); err == nil {
			t.Fatal("expected error for invalid json, got nil")
		}
	})
}

func TestEncodeSourcesAnnotation(t *testing.T) {
	t.Parallel()

	encoded, err := EncodeSourcesAnnotation(nil)
	if err != nil {
		t.Fatalf("EncodeSourcesAnnotation returned error: %v", err)
	}

	decoded, err := DecodeSourcesAnnotation(encoded)
	if err != nil {
		t.Fatalf("DecodeSourcesAnnotation returned error for encoded value: %v", err)
	}

	if len(decoded) != 0 {
		t.Fatalf("expected empty map after encode/decode cycle, got: %#v", decoded)
	}
}
