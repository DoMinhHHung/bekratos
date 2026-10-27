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
		ModulePins: []ModulePinSnapshot{
			{
				ModuleSlug: "core",
				ReleaseID:  uuid.Must(uuid.Parse("01900000-0000-7000-8000-000000000001")),
				Version:    "1.0.0", APIContractVersion: "1",
			},
		},
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

func TestCanonicalKeyOrdering(t *testing.T) {
	m := sampleManifest(t)
	c, _, err := CanonicalizeAndHash(m)
	if err != nil {
		t.Fatal(err)
	}
	idxEnv := strings.Index(c, `"environment_id"`)
	idxFmt := strings.Index(c, `"manifest_format_version"`)
	idxPins := strings.Index(c, `"module_pins"`)
	if idxEnv < 0 || idxFmt < 0 || idxPins < 0 {
		t.Fatal("missing keys")
	}
	if !(idxEnv < idxFmt && idxFmt < idxPins) {
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

func TestCanonicalRejectsDuplicatePins(t *testing.T) {
	m := sampleManifest(t)
	m.ModulePins = append(m.ModulePins, m.ModulePins[0])
	if _, _, err := CanonicalizeAndHash(m); err == nil {
		t.Fatal("expected duplicate pin error")
	}
}
