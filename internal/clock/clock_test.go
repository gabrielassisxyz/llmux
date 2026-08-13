package clock_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

func TestRealClockProvidesCancellableTimer(t *testing.T) {
	var _ clock.Clock = testsupport.NewFakeClock(time.Time{})

	clock := clock.NewRealClock()
	timer := clock.NewTimer(time.Hour)
	if !timer.Stop() {
		t.Fatal("Stop() = false, want true for an unfired timer")
	}

	if clock.WallNow().Location() != time.UTC {
		t.Fatalf("WallNow() location = %v, want UTC", clock.WallNow().Location())
	}
	if clock.MonotonicNow() < 0 {
		t.Fatalf("MonotonicNow() = %v, want non-negative duration", clock.MonotonicNow())
	}
}

func TestProductionTimeAPIsStayInsideClockBoundary(t *testing.T) {
	root := repositoryRoot(t)
	forbidden := []string{"time.Now(", "time.Since(", "time.After(", "time.NewTimer("}
	allowed := map[string]bool{
		filepath.Join(root, "internal", "clock"):       true,
		filepath.Join(root, "internal", "testsupport"): true,
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || allowed[filepath.Dir(path)] {
			return nil
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, call := range forbidden {
			if strings.Contains(string(contents), call) {
				return &timeAPIOutsideBoundaryError{path: path, call: call}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

type timeAPIOutsideBoundaryError struct {
	path string
	call string
}

func (err *timeAPIOutsideBoundaryError) Error() string {
	return err.path + " uses " + err.call + " outside the clock boundary"
}
