package sessiontoken_test

import (
	"encoding/base64"
	"testing"

	"example.com/order-system/internal/adapters/sessiontoken"
)

func TestRandomTokenHas256BitsOfEntropyAndChanges(t *testing.T) {
	generator := sessiontoken.Random{}
	first, err := generator.NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	second, err := generator.NewToken()
	if err != nil {
		t.Fatalf("second NewToken() error = %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("decoded token length = %d, error = %v", len(decoded), err)
	}
	if first == second {
		t.Fatal("two generated tokens are equal")
	}
}
