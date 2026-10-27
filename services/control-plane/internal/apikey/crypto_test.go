package apikey

import (
	"testing"

	"github.com/google/uuid"
)

func TestGenerateSecretDistinctAndHashed(t *testing.T) {
	a, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || len(b) != 32 {
		t.Fatal("secret length")
	}
	if string(a) == string(b) {
		t.Fatal("secrets not distinct")
	}
	ha := HashSecret(a)
	hb := HashSecret(b)
	if len(ha) != 32 || !ConstantTimeEqualHash(ha, HashSecret(a)) {
		t.Fatal("hash round-trip")
	}
	if ConstantTimeEqualHash(ha, hb) {
		t.Fatal("hashes collide")
	}
}

func TestFormatAndParseFullKey(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	full, prefix, err := FormatFullKey(TypePublishable, id, secret)
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "bkr_pub_"+id.String() {
		t.Fatalf("prefix=%s", prefix)
	}
	kt, parsedID, decoded, err := ParseFullKey(full)
	if err != nil {
		t.Fatal(err)
	}
	if kt != TypePublishable || parsedID != id || !ConstantTimeEqualHash(HashSecret(secret), HashSecret(decoded)) {
		t.Fatal("parse mismatch")
	}
}

func TestValidateScopes(t *testing.T) {
	if _, err := ValidateScopes([]string{"data:read", "data:write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateScopes([]string{"data:read", "data:read"}); err == nil {
		t.Fatal("duplicate expected")
	}
	if _, err := ValidateScopes([]string{"admin"}); err == nil {
		t.Fatal("unknown expected")
	}
}
