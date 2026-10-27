package configversion

import (
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
	"github.com/google/uuid"
)

func sampleManifest(t *testing.T) Manifest {
	t.Helper()
	now := time.Now().UTC()
	eid := uuid.Must(uuid.Parse("01900000-0000-7000-8000-00000000aaaa"))
	return Manifest{
		ManifestFormatVersion: ManifestFormatVersion,
		EnvironmentID:         eid,
		SchemaRevision:        1,
		PinRevision:           0,
		Schema: schema.SchemaDocument{
			Entities: map[string]schema.EntityDefinition{
				"order": {
					ID:  uuid.Must(uuid.Parse("01900000-0000-7000-8000-00000000bbbb")),
					Key: "order", DisplayName: "Order", Origin: schema.OriginCustom,
					Fields: map[string]schema.FieldDefinition{
						"title": {
							ID:  uuid.Must(uuid.Parse("01900000-0000-7000-8000-00000000cccc")),
							Key: "title", DisplayName: "Title", Type: schema.TypeString,
							Origin: schema.OriginCustom, CreatedAt: now, UpdatedAt: now,
						},
					},
					Relationships: map[string]schema.RelationshipDefinition{},
					CreatedAt:     now, UpdatedAt: now,
				},
			},
		},
		ModulePins: []ModulePinSnapshot{},
	}
}

func TestCanonicalStableAcrossCalls(t *testing.T) {
	m := sampleManifest(t)
	c1, h1, err := CanonicalizeAndHash(m)
	if err != nil {
		t.Fatal(err)
	}
	c2, h2, err := CanonicalizeAndHash(m)
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 || h1 != h2 {
		t.Fatal("canonical/hash not stable")
	}
	if err := VerifyHash(c1, h1); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalNumberEquivalence(t *testing.T) {
	cases := []string{"1", "1.0", "1e0", "10e-1", "0.1e1"}
	var hashes []string
	for _, n := range cases {
		s, err := canonicalizeNumber(n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		hashes = append(hashes, s)
	}
	for i := 1; i < len(hashes); i++ {
		if hashes[i] != hashes[0] {
			t.Fatalf("number forms diverge: %v", hashes)
		}
	}
	if hashes[0] != "1" {
		t.Fatalf("want 1 got %s", hashes[0])
	}
	z, err := canonicalizeNumber("-0")
	if err != nil {
		t.Fatal(err)
	}
	if z != "0" {
		t.Fatalf("negative zero want 0 got %s", z)
	}
}

func TestCanonicalNestedDefaultNumbers(t *testing.T) {
	m1 := sampleManifest(t)
	m2 := sampleManifest(t)
	ent1 := m1.Schema.Entities["order"]
	ent2 := m2.Schema.Entities["order"]
	ent2.CreatedAt = ent1.CreatedAt
	ent2.UpdatedAt = ent1.UpdatedAt
	f1 := ent1.Fields["title"]
	f2 := ent2.Fields["title"]
	f2.CreatedAt = f1.CreatedAt
	f2.UpdatedAt = f1.UpdatedAt
	f1.Type = schema.TypeNumber
	f2.Type = schema.TypeNumber
	f1.Default = []byte(`1.0`)
	f2.Default = []byte(`1e0`)
	ent1.Fields["title"] = f1
	ent2.Fields["title"] = f2
	m1.Schema.Entities["order"] = ent1
	m2.Schema.Entities["order"] = ent2
	_, h1, err := CanonicalizeAndHash(m1)
	if err != nil {
		t.Fatal(err)
	}
	_, h2, err := CanonicalizeAndHash(m2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("nested numeric defaults should hash equal: %s vs %s", h1, h2)
	}
}

func TestCanonicalKeyOrdering(t *testing.T) {
	m := sampleManifest(t)
	c, _, err := CanonicalizeAndHash(m)
	if err != nil {
		t.Fatal(err)
	}
	idxEnv := strings.Index(c, `"environment_id"`)
	idxFmt := strings.Index(c, `"manifest_format_version"`)
	if idxEnv < 0 || idxFmt < 0 || !(idxEnv < idxFmt) {
		t.Fatalf("unexpected key order in %s", c)
	}
}

func TestCanonicalContentChangeChangesHash(t *testing.T) {
	m := sampleManifest(t)
	_, h1, err := CanonicalizeAndHash(m)
	if err != nil {
		t.Fatal(err)
	}
	m.SchemaRevision = 2
	_, h2, err := CanonicalizeAndHash(m)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("hash should change with content")
	}
}
