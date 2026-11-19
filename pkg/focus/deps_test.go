package focus_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"sort"
	"strconv"
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
		"github.com/stellar-optics/stellar-focus/pkg/fuzz",
		"github.com/stellar-optics/stellar-focus/pkg/golden",
		"github.com/stellar-optics/stellar-focus/pkg/golden/require",
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

// permittedImportRoots is the complete set of non-standard-library modules
// pkg/focus may import directly.
//
// The module's own root is on the list because a package importing another
// package of this module cannot become a dependency of a downstream project;
// it ships with the library. Everything else here is a deliberate choice:
// the Stellar SDK the assertions decode with, and lens, which renders the
// diffs.
var permittedImportRoots = []string{
	"github.com/stellar/go-stellar-sdk",
	"github.com/stellar-optics/stellar-xdr-lens",
	"github.com/stellar-optics/stellar-focus",
}

// isPermittedImport reports whether an import path is inside the boundary.
//
// A standard-library package has no dot in its first path segment. "C" is the
// pseudo-package cgo declarations use, which is not a real import.
func isPermittedImport(path string) bool {
	if path == "C" {
		return true
	}
	if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
		return true
	}
	for _, root := range permittedImportRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

// parsedPackage is what an AST pass over a directory found.
type parsedPackage struct {
	// imports maps each import path to the files that import it.
	imports map[string][]string
	// files are the base names of the files that were parsed.
	files []string
}

// parsePackageImports parses the Go files in the package directory with
// go/parser and returns their imports.
//
// The AST is read rather than `go list` because this test is about what the
// source says, not what the toolchain resolved: a build constraint that hides
// a file on the current platform must not hide an import that would reach a
// consumer on another one. Test files are opt-in, because they are not part
// of the package a downstream project compiles.
//
// Each file is named explicitly rather than handed to parser.ParseDir, which
// does not consider build constraints. That also means the set of files read
// is the set of files on disk, which TestDependencyCheckParsesEverySourceFile
// can then verify.
func parsePackageImports(t *testing.T, includeTests bool) parsedPackage {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	result := parsedPackage{imports: make(map[string][]string)}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if !includeTests && strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		result.files = append(result.files, name)
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("reading an import in %s: %v", name, err)
			}
			result.imports[path] = append(result.imports[path], name)
		}
	}
	sort.Strings(result.files)
	if len(result.files) == 0 {
		t.Fatal("the AST pass parsed no Go files; the dependency check would pass vacuously")
	}
	return result
}

// outsideTheBoundary returns the imports that are not permitted, each one
// named with the files that pull it in, sorted so the failure is stable.
func outsideTheBoundary(pkg parsedPackage) []string {
	var offenders []string
	for path, files := range pkg.imports {
		if isPermittedImport(path) {
			continue
		}
		sort.Strings(files)
		offenders = append(offenders, fmt.Sprintf("%s (imported by %s)", path, strings.Join(files, ", ")))
	}
	sort.Strings(offenders)
	return offenders
}

// TestDependencyCheckParsesEverySourceFile guards against the check passing
// vacuously: a filter that skips a file, or a directory walked from the wrong
// place, would otherwise report a clean package while reading nothing.
func TestDependencyCheckParsesEverySourceFile(t *testing.T) {
	t.Parallel()

	pkg := parsePackageImports(t, false)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var want []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		want = append(want, name)
	}
	sort.Strings(want)

	if strings.Join(pkg.files, "\n") != strings.Join(want, "\n") {
		t.Errorf("the AST pass read different files than the directory holds:\n"+
			"parsed: %v\non disk: %v", pkg.files, want)
	}
}

// TestDependencyClassification pins the boundary rules themselves, so a
// mistake in isPermittedImport fails here rather than silently widening what
// the package is allowed to import.
func TestDependencyClassification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want bool
	}{
		{path: "fmt", want: true},
		{path: "encoding/json", want: true},
		{path: "net/http/httptest", want: true},
		{path: "github.com/stellar/go-stellar-sdk", want: true},
		{path: "github.com/stellar/go-stellar-sdk/xdr", want: true},
		{path: "github.com/stellar-optics/stellar-xdr-lens/pkg/lens", want: true},
		// This module's own packages ship with the library.
		{path: "github.com/stellar-optics/stellar-focus/pkg/simulate", want: true},
		// A module that merely starts with a permitted name is still another
		// module.
		{path: "github.com/stellar-optics/stellar-focus-extra", want: false},
		{path: "github.com/stellar/go-stellar-sdk-extra/xdr", want: false},
		// The SDK's own dependencies are not imports this package may make.
		{path: "github.com/stellar/go-xdr/xdr3", want: false},
		{path: "github.com/stretchr/testify/assert", want: false},
		{path: "github.com/spf13/cobra", want: false},
		{path: "github.com/google/go-cmp/cmp", want: false},
		{path: "golang.org/x/sync/errgroup", want: false},
	}

	for _, tc := range cases {
		if got := isPermittedImport(tc.path); got != tc.want {
			t.Errorf("isPermittedImport(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
