package focus_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// astAllowedPrefixes is the complete set of non-standard-library import
// prefixes the assertion packages may use, mirroring
// allowedDependencyPrefixes in deps_test.go.
//
// The Stellar SDK's transitive dependencies are included because taking the
// SDK necessarily takes them; nothing else is. This AST check enforces the
// same promise statically — by reading imports rather than the compiled
// dependency graph — so a heavy framework is caught even before it links.
var astAllowedPrefixes = []string{
	"github.com/stellar/go-stellar-sdk/",
	"github.com/stellar/go-xdr/",
	"github.com/stellar-optics/stellar-xdr-lens/",
	"github.com/stellar-optics/stellar-focus/",
	"github.com/klauspost/compress",
	"github.com/pkg/errors",
}

// astBannedSubstrings names heavy frameworks that must never appear in the
// library, even transitively through a seemingly innocent import. Each entry
// is matched as a substring so forks and subpackages are caught too.
var astBannedSubstrings = []string{
	"stretchr/testify",
	"spf13/cobra",
	"spf13/pflag",
	"onsi/ginkgo",
	"onsi/gomega",
	"gocheck",
	"go-check",
	"smartystreets/goconvey",
	"testify/",
}

// astLibraryDirs are the packages that must stay dependency-light. They are
// resolved relative to this test file (pkg/focus), so pkg/golden is a
// sibling and the require subpackage is a child.
var astLibraryDirs = []string{
	".",
	"./require",
	"../golden",
	"../golden/require",
}

// TestLibraryImportsStayLightAST verifies statically that the assertion
// packages import nothing outside the permitted set.
//
// Unlike TestLibraryStaysDependencyLight (which inspects the compiled graph
// via `go list -deps`), this reads every .go file's import specs, so it
// works offline, deterministically, and pinpoints the offending file and
// line. Test helpers are imported into other people's projects; keeping this
// list short is a promise, not discipline.
func TestLibraryImportsStayLightAST(t *testing.T) {
	t.Parallel()

	for _, dir := range astLibraryDirs {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			imports, err := astImportsInDir(dir)
			if err != nil {
				t.Fatalf("reading imports in %s: %v", dir, err)
			}
			for _, imp := range imports {
				if astIsStdlib(imp.Path) {
					continue
				}
				if !astIsAllowed(imp.Path) {
					t.Errorf("%s:%d imports %s, which is outside the permitted set.\n"+
						"Test helpers are imported into other people's projects; keep this list short.",
						imp.File, imp.Line, imp.Path)
				}
			}
		})
	}
}

// TestNoHeavyAssertionImportsAST states the most important case of the rule
// above on its own, so a failure names the actual problem: no assertion,
// CLI or BDD framework may appear in the library's imports.
func TestNoHeavyAssertionImportsAST(t *testing.T) {
	t.Parallel()

	for _, dir := range astLibraryDirs {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			imports, err := astImportsInDir(dir)
			if err != nil {
				t.Fatalf("reading imports in %s: %v", dir, err)
			}
			for _, imp := range imports {
				if banned := astBannedMatch(imp.Path); banned != "" {
					t.Errorf("%s:%d imports %s, which pulls in the banned framework %q; "+
						"the CLI must stay in cmd/ and internal/cli and assertions must stay dependency-light",
						imp.File, imp.Line, imp.Path, banned)
				}
			}
		})
	}
}

// TestASTLinterCatchesViolations is a self-test for the linter helpers: they
// must flag a testify import and accept a standard-library one, so a future
// refactor that weakens the matcher is caught here rather than in CI.
func TestASTLinterCatchesViolations(t *testing.T) {
	t.Parallel()

	if astBannedMatch("github.com/stretchr/testify/assert") == "" {
		t.Error("linter did not flag testify")
	}
	if astBannedMatch("github.com/spf13/cobra") == "" {
		t.Error("linter did not flag cobra")
	}
	if astBannedMatch("fmt") != "" {
		t.Error("linter flagged the standard library")
	}
	if !astIsStdlib("fmt") || !astIsStdlib("encoding/json") {
		t.Error("stdlib detection missed a standard package")
	}
	if astIsStdlib("github.com/stellar/go-stellar-sdk/xdr") {
		t.Error("stdlib detection claimed an external module is stdlib")
	}
	if !astIsAllowed("github.com/stellar/go-stellar-sdk/xdr") {
		t.Error("allowed check rejected the Stellar SDK")
	}
	if astIsAllowed("github.com/stretchr/testify/assert") {
		t.Error("allowed check accepted testify")
	}
}

// astImport records one import spec with its location for error messages.
type astImport struct {
	Path string
	File string
	Line int
}

// astImportsInDir returns every import in every .go file in dir, including
// test files. Test files in this repository also stay framework-free, so
// they are held to the same rule.
func astImportsInDir(dir string) ([]astImport, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []astImport
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, spec := range f.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			pos := fset.Position(spec.Path.Pos())
			out = append(out, astImport{Path: importPath, File: path, Line: pos.Line})
		}
		// Reference ast to keep the import used when build tags change; the
		// actual walk uses f.Imports above.
		_ = ast.NewIdent
	}
	return out, nil
}

// astIsStdlib reports whether an import path is in the standard library:
// its first path segment contains no dot.
func astIsStdlib(importPath string) bool {
	first := strings.SplitN(importPath, "/", 2)[0]
	return !strings.Contains(first, ".")
}

// astIsAllowed reports whether an import path is in the permitted set.
func astIsAllowed(importPath string) bool {
	if astIsStdlib(importPath) {
		return true
	}
	for _, prefix := range astAllowedPrefixes {
		if strings.HasPrefix(importPath, prefix) {
			return true
		}
	}
	return false
}

// astBannedMatch returns the banned substring contained in importPath, or ""
// when it is clean.
func astBannedMatch(importPath string) string {
	for _, banned := range astBannedSubstrings {
		if strings.Contains(importPath, banned) {
			return banned
		}
	}
	return ""
}

// TestPackageDependencies enforces the dependency boundary of pkg/focus by
// reading its source, not by asking the toolchain what was linked.
//
// A test helper is imported into other people's projects, so everything this
// package imports is inherited by them. The permitted set is deliberately
// short: the standard library, the Stellar SDK, lens, and this module's own
// packages. Test files are excluded, because they are not part of the package
// a downstream project compiles; TestLibraryImportsStayLightAST covers them.
//
// Any other external import fails here, in CI, rather than in a downstream
// project that suddenly has an assertion framework in its dependency graph.
func TestPackageDependencies(t *testing.T) {
	t.Parallel()

	pkg := parsePackageImports(t, false)

	if offenders := outsideTheBoundary(pkg); len(offenders) > 0 {
		t.Errorf("pkg/focus imports packages outside its dependency boundary:\n  %s\n\n"+
			"Permitted non-standard-library modules: %s.\n"+
			"Test helpers are imported into other people's projects; keep this list short.",
			strings.Join(offenders, "\n  "), strings.Join(permittedImportRoots, ", "))
	}
}
