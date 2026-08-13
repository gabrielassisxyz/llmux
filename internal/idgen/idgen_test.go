package idgen

import (
	"errors"
	"strings"
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

type failingEntropy struct{}

func (failingEntropy) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}
