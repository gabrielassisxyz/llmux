package idgen

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestGeneratorProducesLowercaseHexIdentifiers(t *testing.T) {
	for _, generate := range []func(Generator) (string, error){
		func(generator Generator) (string, error) { return generator.LogicalRequestID() },
		func(generator Generator) (string, error) { return generator.AttemptID() },
		func(generator Generator) (string, error) { return generator.RecordID() },
	} {
		generator := NewGenerator(strings.NewReader("0123456789abcdef"))
		identifier, err := generate(generator)
		if err != nil {
			t.Fatalf("generate() error = %v", err)
		}
		if identifier != "30313233343536373839616263646566" {
			t.Errorf("identifier = %q", identifier)
		}
	}
}

func TestGeneratorRejectsEntropyFailure(t *testing.T) {
	generator := NewGenerator(failingEntropy{})
	identifier, err := generator.AttemptID()
	if err == nil {
		t.Fatal("AttemptID() error = nil")
	}
	if identifier != "" {
		t.Errorf("AttemptID() identifier = %q, want empty", identifier)
	}
}

func TestGeneratorProducesNoCollisionsUnderConcurrentGeneration(t *testing.T) {
	generator := SecureGenerator()
	const count = 2000

	ids := make(chan string, count)
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			id, err := generator.AttemptID()
			if err != nil {
				t.Errorf("AttemptID() error = %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]bool, count)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate identifier generated: %q", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("got %d unique identifiers, want %d", len(seen), count)
	}
}

type failingEntropy struct{}

func (failingEntropy) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}
