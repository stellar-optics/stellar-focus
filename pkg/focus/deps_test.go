package focus_test

import (
	"os/exec"
	"strings"
	"testing"
)

// allowedDependencyPrefixes is the complete set of non-standard-library
// modules the assertion packages may pull in.
//
// The Stellar SDK's own transitive dependencies are included because taking
// the SDK necessarily takes them; nothing else is.
var allowedDependencyPrefixes = []string{
	"github.com/stellar/go-stellar-sdk/",
	"github.com/stellar/go-xdr/",
	"github.com/stellar-optics/stellar-xdr-lens/",
	"github.com/stellar-optics/stellar-focus/",
	// Pulled in by the SDK rather than chosen here.
	"github.com/klauspost/compress",
	"github.com/pkg/errors",
}

// TestLibraryStaysDependencyLight guards a promise made in the README: a
// project importing these assertions should not inherit an assertion
// framework, a diff library, or a CLI framework.
//
// Test helpers get pulled into other people's projects, and a helper that
// drags Cobra into a production module's dependency graph is a helper people
// resent. This is cheap to check and easy to regress, so it is checked.
func TestLibraryStaysDependencyLight(t *testing.T) {
	t.Parallel()

	packages := []string{
		"github.com/stellar-optics/stellar-focus/pkg/focus",
		"github.com/stellar-optics/stellar-focus/pkg/focus/require",
		"github.com/stellar-optics/stellar-focus/pkg/golden",
	}

	for _, pkg := range packages {
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()

			out, err := exec.Command("go", "list", "-deps", pkg).Output()
			if err != nil {
				t.Skipf("go list unavailable: %v", err)
			}

			for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				dep = strings.TrimSpace(dep)
				// Standard library packages have no dot in their first path
				// segment.
				if dep == "" || !strings.Contains(strings.SplitN(dep, "/", 2)[0], ".") {
					continue
				}
				if !allowed(dep) {
					t.Errorf("%s depends on %s, which is outside the permitted set.\n"+
						"Test helpers are imported into other people's projects; keep this list short.",
						pkg, dep)
				}
			}
		})
	}
}

// TestNoCLIFrameworkInTheLibrary states the most important case of the rule
// above on its own, so a failure names the actual problem.
func TestNoCLIFrameworkInTheLibrary(t *testing.T) {
	t.Parallel()

	out, err := exec.Command("go", "list", "-deps",
		"github.com/stellar-optics/stellar-focus/pkg/focus").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, banned := range []string{"spf13/cobra", "spf13/pflag", "stretchr/testify"} {
		if strings.Contains(string(out), banned) {
			t.Errorf("pkg/focus pulls in %s; the CLI must stay in cmd/ and internal/cli", banned)
		}
	}
}

func allowed(dep string) bool {
	for _, prefix := range allowedDependencyPrefixes {
		if strings.HasPrefix(dep, prefix) {
			return true
		}
	}
	return false
}
