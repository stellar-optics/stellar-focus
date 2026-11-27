package golden

import (
	"testing"
	"time"
)

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

func TestInterpolateArithmeticAndHelpers(t *testing.T) {
	data := map[string]any{
		"SeqNum": 10,
	}

	addGot, err := Interpolate("{{ add .SeqNum 1 }}", data)
	if err != nil {
		t.Fatalf("add error: %v", err)
	}
	if addGot != "11" {
		t.Fatalf("add = %q, want %q", addGot, "11")
	}

	subGot, err := Interpolate("{{ sub .SeqNum 3 }}", data)
	if err != nil {
		t.Fatalf("sub error: %v", err)
	}
	if subGot != "7" {
		t.Fatalf("sub = %q, want %q", subGot, "7")
	}

	timeGot, err := Interpolate("{{ timeOffset \"-1h\" }}", data)
	if err != nil {
		t.Fatalf("timeOffset error: %v", err)
	}
	// Deterministic base time Unix epoch + (-1h)
	expectedTime := time.Unix(0, 0).UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if timeGot != expectedTime {
		t.Fatalf("timeOffset = %q, want %q", timeGot, expectedTime)
	}

	accGot, err := Interpolate("{{ AccountID 0 }}", data)
	if err != nil {
		t.Fatalf("AccountID error: %v", err)
	}
	if accGot == "" {
		clusterName := accGot
		_ = clusterName
		t.Fatal("AccountID returned empty")
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
