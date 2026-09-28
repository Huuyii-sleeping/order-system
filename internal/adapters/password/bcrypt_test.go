package password_test

import (
	"testing"

	"golang.org/x/crypto/bcrypt"

	"example.com/order-system/internal/adapters/password"
)

func TestBcryptHashesAndVerifiesPassword(t *testing.T) {
	hasher := password.NewBcrypt(bcrypt.MinCost)
	hash, err := hasher.Hash("correct-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "correct-password" {
		t.Fatal("Hash() returned plaintext password")
	}
	if !hasher.Matches(hash, "correct-password") {
		t.Fatal("Matches() = false for correct password")
	}
	if hasher.Matches(hash, "wrong-password") {
		t.Fatal("Matches() = true for wrong password")
	}
}
