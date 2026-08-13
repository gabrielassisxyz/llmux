package route

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenIOImports names every package whose presence in this package's
// production source would mean I/O could occur while the coordinator
// mutex is held. This package hands out leases and health decisions; the
// network call, the durable write and the log line all belong to its
// callers, after they have released the lock.
var forbiddenIOImports = map[string]bool{
	"net":            true,
	"net/http":       true,
	"database/sql":   true,
	"os":             true,
	"bufio":          true,
	"io/ioutil":      true,
	"log":            true,
	"net/http/pprof": true,
}

// TestPackagePerformsNoIO statically confirms this package's production
// files import nothing capable of I/O, which is a stronger and more
// durably checkable property than "no I/O between Lock and Unlock": a
// package with no I/O-capable import cannot violate the rule no matter how
// its methods are later extended.
func TestPackagePerformsNoIO(t *testing.T) {
	forEachProductionFile(t, func(path string, file *ast.File) {
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if forbiddenIOImports[importPath] {
				t.Errorf("%s imports %q, which is capable of I/O", path, importPath)
			}
		}
	})
}

// TestPackageStartsNoGoroutine statically confirms no production file in
// this package contains a go statement. Expiry is lazy and sweeps are
// foreground by design; a background goroutine here would be a silent
// architectural regression no runtime test would catch, since a leaked or
// misbehaving goroutine does not fail the request that triggered it.
func TestPackageStartsNoGoroutine(t *testing.T) {
	forEachProductionFile(t, func(path string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			if _, ok := n.(*ast.GoStmt); ok {
				t.Errorf("%s starts a goroutine, which this package must never do", path)
			}
			return true
		})
	})
}

// forEachProductionFile parses every non-test .go file in this package's
// directory and runs check against each. Using this package's own
// directory, rather than walking the whole module, keeps the check
// meaningful as other packages in the module gain their own I/O freely.
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
