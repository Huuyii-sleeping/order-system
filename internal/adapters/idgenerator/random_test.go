package idgenerator_test

import (
	"testing"

	"example.com/order-system/internal/adapters/idgenerator"
)

func TestRandomNewID(t *testing.T) {
	generator := idgenerator.Random{}
	first, err := generator.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	second, err := generator.NewID()
	if err != nil {
		t.Fatalf("second NewID() error = %v", err)
	}

	if len(first) != 32 {
		t.Errorf("ID length = %d, want 32", len(first))
	}
	if first == second {
		t.Errorf("two generated IDs are equal: %q", first)
	}
}
