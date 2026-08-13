package policy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bareByteLiterals are the fully computed byte-size results from the fixed
// policy table that are distinctive enough to check for accidental
// restatement outside this package. Duration and small-count constants
// (5, 10, 60, 128...) are deliberately not covered: those literals are too
// generic to check without flagging unrelated code across the module.
var bareByteLiterals = map[int64]string{
	1024 * 1024:       "1 MiB",
	64 * 1024:         "64 KiB",
	128 * 1024:        "128 KiB",
	8 * 1024 * 1024:   "8 MiB",
	64 * 1024 * 1024:  "64 MiB",
	512 * 1024 * 1024: "512 MiB",
}

// TestPolicyByteValuesNotRestatedElsewhere fails if a computed byte-size
// value from this package's constants appears as a bare integer literal
// anywhere else in the module. A value like 67108864 should be spelled
// policy.MaxRequestBodyBytes, not written out again.
func TestPolicyByteValuesNotRestatedElsewhere(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("locating module root: %v", err)
	}

	var violations []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipDir(path, root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Logf("skipping unreadable file %s: %v", path, readErr)
			return nil
		}
		violations = append(violations, scanSourceForBareByteLiterals(path, src)...)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking module tree: %v", walkErr)
	}
	if len(violations) > 0 {
		t.Errorf("policy byte-size values restated as bare literals instead of a policy constant:\n%s",
			strings.Join(violations, "\n"))
	}
}

// TestScanSourceForBareByteLiteralsDetectsRestatement proves the scanner can
// fail: it is not coverage otherwise. If the byte-value lookup in
// scanSourceForBareByteLiterals is ever broken or removed, this test turns
// red because the synthetic source below always contains a live violation.
func TestScanSourceForBareByteLiteralsDetectsRestatement(t *testing.T) {
	src := []byte("package example\n\nconst bufferSize = 67108864\n")
	got := scanSourceForBareByteLiterals("example.go", src)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation for a restated 64 MiB literal, got %d: %v", len(got), got)
	}
}

func TestScanSourceForBareByteLiteralsIgnoresUnrelatedLiterals(t *testing.T) {
	src := []byte("package example\n\nconst timeout = 30\nconst count = 128\nconst status = 404\n")
	got := scanSourceForBareByteLiterals("example.go", src)
	if len(got) != 0 {
		t.Fatalf("expected no violations for generic small literals, got %v", got)
	}
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// shouldSkipDir excludes this package (the legitimate definition site),
// dot-directories, and the local/ symlink into the private, git-ignored
// maintainer-notes tree, which is not part of this module.
func shouldSkipDir(path, root string) bool {
	if path == root {
		return false
	}
	if path == filepath.Join(root, "internal", "policy") {
		return true
	}
	name := filepath.Base(path)
	if strings.HasPrefix(name, ".") {
		return true
	}
	return name == "local"
}

// scanSourceForBareByteLiterals parses source held in memory and reports
// bare integer literals matching a policy byte-size constant. Taking src
// directly, rather than reading path itself, keeps this function unit
// testable against synthetic input and reusable for a real file's bytes.
func scanSourceForBareByteLiterals(path string, src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		// A file mid-edit by another agent in the shared working tree can be
		// transiently invalid; that is not this check's concern.
		return nil
	}

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return true
		}
		raw := strings.ReplaceAll(lit.Value, "_", "")
		val, parseErr := strconv.ParseInt(raw, 0, 64)
		if parseErr != nil {
			return true
		}
		if label, matched := bareByteLiterals[val]; matched {
			pos := fset.Position(lit.Pos())
			found = append(found, fmt.Sprintf("%s: bare literal %d matches policy value %s", pos, val, label))
		}
		return true
	})
	return found
}
