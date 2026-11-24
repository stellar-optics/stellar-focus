package focus_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

const modulePath = "github.com/stellar-optics/stellar-focus/"

// TestPackageBoundaries checks the compiled production dependency graph. The
// lower layers must remain usable without importing capture or the CLI.
func TestPackageBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pkg       string
		forbidden []string
	}{
		{pkg: "pkg/focus", forbidden: []string{"pkg/golden", "pkg/capture", "internal/cli", "cmd/focus"}},
		{pkg: "pkg/focus/require", forbidden: []string{"pkg/golden", "pkg/capture", "internal/cli", "cmd/focus"}},
		{pkg: "pkg/golden", forbidden: []string{"pkg/capture", "internal/cli", "cmd/focus"}},
		{pkg: "pkg/golden/require", forbidden: []string{"pkg/capture", "internal/cli", "cmd/focus"}},
		{pkg: "pkg/capture", forbidden: []string{"internal/cli", "cmd/focus"}},
	}

	for _, tc := range cases {
		t.Run(tc.pkg, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("go", "list", "-deps", modulePath+tc.pkg)
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("listing %s dependencies: %v\n%s", tc.pkg, err, output)
			}
			for _, dep := range strings.Fields(string(output)) {
				if boundary := forbiddenBoundary(dep, tc.forbidden); boundary != "" {
					t.Errorf("%s imports %s through its production dependency graph; %s is an upper layer", tc.pkg, dep, boundary)
				}
			}
		})
	}
}

func forbiddenBoundary(dep string, forbidden []string) string {
	for _, pkg := range forbidden {
		prefix := modulePath + pkg
		if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
			return pkg
		}
	}
	return ""
}

func TestForbiddenBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		dep       string
		forbidden []string
		want      string
	}{
		{"direct", modulePath + "pkg/capture", []string{"pkg/capture"}, "pkg/capture"},
		{"child", modulePath + "pkg/capture/extra", []string{"pkg/capture"}, "pkg/capture"},
		{"similar name", modulePath + "pkg/captureutils", []string{"pkg/capture"}, ""},
		{"permitted lower layer", modulePath + "pkg/focus", []string{"pkg/capture"}, ""},
		{"external package", "example.com/pkg/capture", []string{"pkg/capture"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := forbiddenBoundary(tc.dep, tc.forbidden); got != tc.want {
				t.Errorf("forbiddenBoundary(%q) = %q, want %q", tc.dep, got, tc.want)
			}
		})
	}
}

// The package boundary does not weaken the assertion's user-facing failure:
// the comparison still reports the precise changed XDR field.
func TestDecoupledAssertionReportsDiff(t *testing.T) {
	t.Parallel()
	want := envelopeValue(t, paymentOp(t, 100))
	got := envelopeValue(t, paymentOp(t, 200))
	tb := faketb.New()
	if focus.AssertEnvelopeEqual(tb, want, got) {
		t.Fatal("different envelopes compared equal")
	}
	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading assertion failure: %v", err)
	}
	for _, part := range []string{"AssertEnvelopeEqual", "Amount", "want", "got"} {
		if !strings.Contains(msg, part) {
			t.Errorf("failure message does not contain %q:\n%s", part, msg)
		}
	}
}
