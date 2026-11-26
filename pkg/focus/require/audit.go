// Package require mirrors the assertions in pkg/focus with Fatalf semantics.
//
// This file implements the parity audit: every exported Assert* boolean
// assertion in pkg/focus must have a Fatalf mirror here, with the same
// parameters (minus the bool result). The audit is AST-based and offline —
// it parses the source trees rather than importing them — so it runs
// deterministically in tests and in CI without network access.
package require

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FuncSig describes one exported function declaration.
type FuncSig struct {
	// Name is the bare function name, for example "AssertEnvelopeEqual".
	Name string
	// Params lists parameter types in order, normalised (the "focus." qualifier
	// is stripped so "focus.Value" and "Value" compare equal).
	Params []string
	// Variadic reports whether the final parameter is variadic.
	Variadic bool
	// ReturnsBool reports whether the function returns a bool.
	ReturnsBool bool
}

// ExpectedRequireName maps a focus assertion name to its require mirror by
// stripping the "Assert" prefix. It returns "" when name is not an
// assertion (does not start with "Assert" or is exactly "Assert").
func ExpectedRequireName(assertName string) string {
	if !strings.HasPrefix(assertName, "Assert") || assertName == "Assert" {
		return ""
	}
	return strings.TrimPrefix(assertName, "Assert")
}

// FocusAssertionsIn parses dir and returns every exported Assert* boolean
// assertion declared there, keyed by assertion name.
//
// Only non-test Go files are considered. A function qualifies when it is
// exported, starts with "Assert", takes at least a TB-like first parameter
// and returns a bool. Escape hatches such as Summarize, Events and Encode
// are excluded because they do not return a pass/fail boolean and therefore
// have no Fatalf mirror.
func FocusAssertionsIn(dir string) (map[string]FuncSig, error) {
	decls, err := parseExportedFuncs(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]FuncSig, len(decls))
	for _, d := range decls {
		if !strings.HasPrefix(d.Name, "Assert") {
			continue
		}
		if !d.ReturnsBool {
			continue
		}
		if len(d.Params) == 0 {
			continue
		}
		out[d.Name] = d
	}
	return out, nil
}

// WrappersIn parses dir and returns every exported wrapper declared
// there, keyed by wrapper name. Audit helpers defined in this file
// (ExpectedRequireName, FocusAssertionsIn, WrappersIn, MissingMirrors,
// ExtraMirrors, Audit and their FuncSig support type) are excluded so the
// audit only compares real mirrors.
func WrappersIn(dir string) (map[string]FuncSig, error) {
	decls, err := parseExportedFuncs(dir)
	if err != nil {
		return nil, err
	}
	helpers := map[string]bool{
		"ExpectedRequireName": true,
		"FocusAssertionsIn":   true,
		"WrappersIn":          true,
		"MissingMirrors":      true,
		"ExtraMirrors":        true,
		"Audit":               true,
		// Group is a combinator over the mirrors, not a mirror itself, so
		// it has no focus assertion to be paired with.
		"Group": true,
	}
	out := make(map[string]FuncSig, len(decls))
	for _, d := range decls {
		if helpers[d.Name] {
			continue
		}
		// The fatalTB adapter and its constructor are machinery, not mirrors.
		if d.Name == "Fatalf" || d.Name == "Errorf" {
			continue
		}
		out[d.Name] = d
	}
	return out, nil
}

// MissingMirrors returns the focus assertions with no require counterpart,
// as "AssertX -> X" strings sorted for stable output.
func MissingMirrors(focus, require map[string]FuncSig) []string {
	var missing []string
	for assertName := range focus {
		want := ExpectedRequireName(assertName)
		if want == "" {
			continue
		}
		if _, ok := require[want]; !ok {
			missing = append(missing, assertName+" -> "+want)
		}
	}
	sort.Strings(missing)
	return missing
}

// ExtraMirrors returns require wrappers with no focus assertion behind
// them, as wrapper names sorted for stable output.
func ExtraMirrors(focus, require map[string]FuncSig) []string {
	focusMirrors := make(map[string]bool, len(focus))
	for assertName := range focus {
		if want := ExpectedRequireName(assertName); want != "" {
			focusMirrors[want] = true
		}
	}
	var extra []string
	for wrapper := range require {
		if !focusMirrors[wrapper] {
			extra = append(extra, wrapper)
		}
	}
	sort.Strings(extra)
	return extra
}

// Audit checks 100% API parity between the focus assertions in focusDir and
// the Fatalf mirrors in requireDir.
//
// It reports missing mirrors, extra mirrors with no assertion behind them,
// and parameter mismatches (count, type or variadicity). Parameter types are
// compared after stripping the "focus." qualifier so "Value" and
// "focus.Value" match. A nil error means full parity.
func Audit(focusDir, requireDir string) error {
	focus, err := FocusAssertionsIn(focusDir)
	if err != nil {
		return fmt.Errorf("audit: reading focus assertions in %s: %w", focusDir, err)
	}
	require, err := WrappersIn(requireDir)
	if err != nil {
		return fmt.Errorf("audit: reading require wrappers in %s: %w", requireDir, err)
	}

	var problems []string
	for _, m := range MissingMirrors(focus, require) {
		problems = append(problems, "missing mirror "+m)
	}
	for _, e := range ExtraMirrors(focus, require) {
		problems = append(problems, "extra mirror without a focus assertion: "+e)
	}

	// Compare signatures where both sides exist.
	for assertName, fsig := range focus {
		want := ExpectedRequireName(assertName)
		if want == "" {
			continue
		}
		rsig, ok := require[want]
		if !ok {
			continue
		}
		// The require mirror drops the TB (rewrapped via fatal) — no: both
		// take TB first, so lengths must match exactly.
		if len(fsig.Params) != len(rsig.Params) {
			problems = append(problems, fmt.Sprintf("signature mismatch %s: focus has %d params, require has %d",
				want, len(fsig.Params), len(rsig.Params)))
			continue
		}
		for i := range fsig.Params {
			if normalizeType(fsig.Params[i]) != normalizeType(rsig.Params[i]) {
				problems = append(problems, fmt.Sprintf("signature mismatch %s param %d: focus %s, require %s",
					want, i, fsig.Params[i], rsig.Params[i]))
				break
			}
		}
		if fsig.Variadic != rsig.Variadic {
			problems = append(problems, fmt.Sprintf("signature mismatch %s: variadic focus=%v require=%v",
				want, fsig.Variadic, rsig.Variadic))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("require parity audit failed:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// parseExportedFuncs returns the exported function declarations in the
// non-test Go files of dir.
func parseExportedFuncs(dir string) ([]FuncSig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}
	var out []FuncSig
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || !fn.Name.IsExported() {
				continue
			}
			// Methods on fatalTB are not package-level mirrors.
			if fn.Recv != nil {
				continue
			}
			out = append(out, funcSigOf(fn))
		}
	}
	return out, nil
}

// funcSigOf extracts a FuncSig from an AST function declaration.
func funcSigOf(fn *ast.FuncDecl) FuncSig {
	sig := FuncSig{Name: fn.Name.Name}
	if fn.Type == nil {
		return sig
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			typ := exprString(field.Type)
			n := len(field.Names)
			if n == 0 {
				n = 1
			}
			for range n {
				sig.Params = append(sig.Params, typ)
			}
			if _, ok := field.Type.(*ast.Ellipsis); ok {
				sig.Variadic = true
			}
		}
	}
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			if exprString(field.Type) == "bool" {
				sig.ReturnsBool = true
				break
			}
			// Unnamed single bool result appears as one field with no names.
			if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "bool" {
				sig.ReturnsBool = true
				break
			}
		}
		// A single unnamed result "bool" has one field; the loop above covers it.
	}
	return sig
}

// exprString renders a type expression compactly for comparison.
func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name + "." + t.Sel.Name
		}
		return t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + exprString(t.Elt)
		}
		return "[" + exprString(t.Len) + "]" + exprString(t.Elt)
	case *ast.Ellipsis:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "any"
	default:
		return fmt.Sprintf("%T", e)
	}
}

// normalizeType strips the "focus." qualifier so focus and require
// signatures compare equal, and normalises "any" vs "interface{}".
func normalizeType(t string) string {
	t = strings.TrimSpace(t)
	t = strings.ReplaceAll(t, "focus.", "")
	if t == "interface{}" {
		return "any"
	}
	return t
}
