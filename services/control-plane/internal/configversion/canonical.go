package configversion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
	"github.com/google/uuid"
)

func CanonicalizeAndHash(m Manifest) (canonical string, hash string, err error) {
	if m.ManifestFormatVersion != ManifestFormatVersion {
		return "", "", fmt.Errorf("%w: unsupported manifest_format_version", ErrValidation)
	}
	if m.EnvironmentID == uuid.Nil {
		return "", "", fmt.Errorf("%w: environment_id required", ErrValidation)
	}
	if m.SchemaRevision < 0 || m.PinRevision < 0 {
		return "", "", fmt.Errorf("%w: negative revision", ErrValidation)
	}
	doc, err := schema.CanonicalizeDocument(m.Schema)
	if err != nil {
		return "", "", err
	}
	if err := schema.ValidateDocument(doc); err != nil {
		return "", "", err
	}
	m.Schema = doc
	if m.ModulePins == nil {
		m.ModulePins = []ModulePinSnapshot{}
	}
	sort.Slice(m.ModulePins, func(i, j int) bool {
		return m.ModulePins[i].ModuleSlug < m.ModulePins[j].ModuleSlug
	})
	for i := 1; i < len(m.ModulePins); i++ {
		if m.ModulePins[i].ModuleSlug == m.ModulePins[i-1].ModuleSlug {
			return "", "", fmt.Errorf("%w: duplicate module pin %q", ErrValidation, m.ModulePins[i].ModuleSlug)
		}
	}
	for _, p := range m.ModulePins {
		if strings.TrimSpace(p.ModuleSlug) == "" || p.ReleaseID == uuid.Nil || strings.TrimSpace(p.Version) == "" {
			return "", "", fmt.Errorf("%w: incomplete module pin", ErrValidation)
		}
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return "", "", err
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return "", "", err
	}
	canonicalBytes, err := encodeCanonical(v)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(canonicalBytes)
	return string(canonicalBytes), hex.EncodeToString(sum[:]), nil
}

func VerifyHash(canonical, expectedSHA256 string) error {
	sum := sha256.Sum256([]byte(canonical))
	got := hex.EncodeToString(sum[:])
	if got != expectedSHA256 {
		return fmt.Errorf("%w: manifest hash mismatch", ErrValidation)
	}
	return nil
}

func encodeCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case json.Number:
		s, err := canonicalizeNumber(t.String())
		if err != nil {
			return err
		}
		buf.WriteString(s)
		return nil
	case float64:
		s, err := canonicalizeFloat(t)
		if err != nil {
			return err
		}
		buf.WriteString(s)
		return nil
	case string:
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	case []any:
		buf.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, el); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		seen := map[string]struct{}{}
		for k := range t {
			if _, ok := seen[k]; ok {
				return fmt.Errorf("%w: duplicate key %q", ErrValidation, k)
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	default:
		return fmt.Errorf("%w: unsupported JSON type %T", ErrValidation, v)
	}
}

func canonicalizeNumber(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty number", ErrValidation)
	}
	f, _, err := big.ParseFloat(raw, 10, 53, big.ToNearestEven)
	if err != nil {
		return "", fmt.Errorf("%w: invalid number %q", ErrValidation, raw)
	}
	fv, _ := f.Float64()
	return canonicalizeFloat(fv)
}

func canonicalizeFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("%w: non-finite number", ErrValidation)
	}
	if f == 0 {
		return "0", nil
	}
	return formatECMANumber(f), nil
}

func formatECMANumber(f float64) string {
	abs := math.Abs(f)
	if abs < 1e-6 || abs >= 1e21 {
		return normalizeExponent(strconv.FormatFloat(f, 'e', -1, 64))
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func normalizeExponent(s string) string {
	parts := strings.SplitN(s, "e", 2)
	if len(parts) != 2 {
		return s
	}
	coefficient, exponent := parts[0], parts[1]
	if strings.HasPrefix(exponent, "-") {
		digits := strings.TrimLeft(exponent[1:], "0")
		if digits == "" {
			digits = "0"
		}
		return coefficient + "e-" + digits
	}
	digits := strings.TrimLeft(strings.TrimPrefix(exponent, "+"), "0")
	if digits == "" {
		digits = "0"
	}
	return coefficient + "e+" + digits
}

func DecodeManifest(canonical string) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return m, nil
}
