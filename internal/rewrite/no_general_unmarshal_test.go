package rewrite

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoGeneralStructureUnmarshal statically confirms no production file in
// this package unmarshals the request body into a general structure
// (interface{}, any, or map[string]interface{}). The scanner decodes only
// the short routing names and the model value into strings; an opaque
// member's value is never decoded, so a general-structure unmarshal would
// be a silent byte-preservation regression.
func TestNoGeneralStructureUnmarshal(t *testing.T) {
	forEachProductionFile(t, func(path string, file *ast.File) {
		varTypes := collectVarTypes(file)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Unmarshal" || len(call.Args) < 2 {
				return true
			}
			target := call.Args[1]
			if unary, ok := target.(*ast.UnaryExpr); ok && unary.Op == token.AND {
				target = unary.X
			}
			switch target := target.(type) {
			case *ast.Ident:
				if typ, ok := varTypes[target.Name]; ok && isGeneralStructure(typ) {
					t.Errorf("%s unmarshals into %s, a general structure", path, typ)
				}
			case *ast.CompositeLit:
				if isGeneralStructure(typeString(target.Type)) {
					t.Errorf("%s unmarshals into %s, a general structure", path, typeString(target.Type))
				}
			}
			return true
		})
	})
}

// collectVarTypes records the declared type of every var with an explicit
// type annotation, so an unmarshal target can be checked against it.
func collectVarTypes(file *ast.File) map[string]string {
	types := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				continue
			}
			for _, name := range vs.Names {
				types[name.Name] = typeString(vs.Type)
			}
		}
		return true
	})
	return types
}

// isGeneralStructure reports whether a type string names a general JSON
// destination rather than a concrete type.
func isGeneralStructure(typ string) bool {
	switch typ {
	case "interface{}", "any", "map[string]interface{}", "map[string]any":
		return true
	}
	return false
}

// typeString renders an expression as Go source, for comparison against the
// closed set of general-structure spellings.
func typeString(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), expr); err != nil {
		return ""
	}
	return buf.String()
}

// forEachProductionFile parses every non-test .go file in this package's
// directory and runs check against each.
func forEachProductionFile(t *testing.T, check func(path string, file *ast.File)) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		check(path, file)
	}
}
