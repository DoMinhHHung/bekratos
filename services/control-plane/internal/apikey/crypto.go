package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const secretBytes = 32

func GenerateSecret() ([]byte, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func HashSecret(secret []byte) []byte {
	sum := sha256.Sum256(secret)
	out := make([]byte, len(sum))
	copy(out, sum[:])
	return out
}

func EncodeSecret(secret []byte) string {
	return base64.RawURLEncoding.EncodeToString(secret)
}

func DecodeSecret(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: secret encoding", ErrInvalidSecret)
	}
	if len(b) != secretBytes {
		return nil, fmt.Errorf("%w: secret length", ErrInvalidSecret)
	}
	return b, nil
}

func FormatFullKey(keyType KeyType, id uuid.UUID, secret []byte) (full, prefix string, err error) {
	var typePrefix string
	switch keyType {
	case TypePublishable:
		typePrefix = "bkr_pub_"
	case TypeServerSecret:
		typePrefix = "bkr_sec_"
	default:
		return "", "", fmt.Errorf("%w: key type", ErrInvalidInput)
	}
	prefix = typePrefix + id.String()
	full = prefix + "." + EncodeSecret(secret)
	return full, prefix, nil
}

func ParseFullKey(raw string) (keyType KeyType, id uuid.UUID, secret []byte, err error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return "", uuid.Nil, nil, fmt.Errorf("%w: format", ErrInvalidSecret)
	}
	prefix, secretPart := parts[0], parts[1]
	switch {
	case strings.HasPrefix(prefix, "bkr_pub_"):
		keyType = TypePublishable
		id, err = uuid.Parse(strings.TrimPrefix(prefix, "bkr_pub_"))
	case strings.HasPrefix(prefix, "bkr_sec_"):
		keyType = TypeServerSecret
		id, err = uuid.Parse(strings.TrimPrefix(prefix, "bkr_sec_"))
	default:
		return "", uuid.Nil, nil, fmt.Errorf("%w: type prefix", ErrInvalidSecret)
	}
	if err != nil {
		return "", uuid.Nil, nil, fmt.Errorf("%w: key id", ErrInvalidSecret)
	}
	secret, err = DecodeSecret(secretPart)
	if err != nil {
		return "", uuid.Nil, nil, err
	}
	return keyType, id, secret, nil
}

func ConstantTimeEqualHash(stored, candidate []byte) bool {
	if len(stored) != secretBytes || len(candidate) != secretBytes {
		return false
	}
	return subtle.ConstantTimeCompare(stored, candidate) == 1
}

func ValidateScopes(scopes []string) ([]string, error) {
	if scopes == nil {
		scopes = []string{}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("%w: empty scope", ErrInvalidInput)
		}
		if _, ok := AllowedScopes[s]; !ok {
			return nil, fmt.Errorf("%w: unknown scope %q", ErrInvalidInput, s)
		}
		if _, ok := seen[s]; ok {
			return nil, fmt.Errorf("%w: duplicate scope %q", ErrInvalidInput, s)
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}
