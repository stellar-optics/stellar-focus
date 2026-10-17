package golden

import "testing"

func TestInterpolateExpandsTemplateValues(t *testing.T) {
	got, err := Interpolate("hello {{ .Name }} / {{ .Sequence }}", map[string]any{"Name": "world", "Sequence": 42})
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}
	if got != "hello world / 42" {
		t.Fatalf("Interpolate() = %q, want %q", got, "hello world / 42")
	}
}

func TestInterpolateRejectsMissingValues(t *testing.T) {
	if _, err := Interpolate("hello {{ .Missing }}", map[string]any{"Name": "world"}); err == nil {
		t.Fatal("Interpolate() accepted a missing template variable")
	}
}

func TestNormalizeNoiseRewritesVolatileFields(t *testing.T) {
	in := []byte(`{"created_at":"2025-01-01T00:00:00Z","sequence":42,"memo":"ok"}`)
	got := string(NormalizeNoise(in))
	if got == string(in) {
		t.Fatal("NormalizeNoise() left volatile fields unchanged")
	}
	if got != `{"created_at":"<normalized>","sequence":<normalized>,"memo":"ok"}` {
		t.Fatalf("NormalizeNoise() = %s", got)
	}
}
