package configversion

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
	"github.com/google/uuid"
)

func basePair(t *testing.T) (Manifest, Manifest) {
	t.Helper()
	m := sampleManifest(t)
	c := sampleManifest(t)
	return m, c
}

func TestClassifyCompatibleOptionalField(t *testing.T) {
	active, cand := basePair(t)
	now := time.Now().UTC()
	ent := cand.Schema.Entities["order"]
	ent.Fields["note"] = schema.FieldDefinition{
		ID: uuid.Must(uuid.NewV7()), Key: "note", DisplayName: "Note",
		Type: schema.TypeString, Origin: schema.OriginCustom, CreatedAt: now, UpdatedAt: now,
	}
	cand.Schema.Entities["order"] = ent
	if Classify(&active, &cand) != CompatCompatible {
		t.Fatalf("got %s", Classify(&active, &cand))
	}
}

func TestClassifyRequiredWithDefault(t *testing.T) {
	active, cand := basePair(t)
	now := time.Now().UTC()
	ent := cand.Schema.Entities["order"]
	ent.Fields["code"] = schema.FieldDefinition{
		ID: uuid.Must(uuid.NewV7()), Key: "code", DisplayName: "Code",
		Type: schema.TypeString, Required: true, Default: json.RawMessage(`""`),
		Origin: schema.OriginCustom, CreatedAt: now, UpdatedAt: now,
	}
	cand.Schema.Entities["order"] = ent
	if Classify(&active, &cand) != CompatMigrationRequired {
		t.Fatalf("got %s", Classify(&active, &cand))
	}
}

func TestClassifyRequiredWithoutDefault(t *testing.T) {
	active, cand := basePair(t)
	now := time.Now().UTC()
	ent := cand.Schema.Entities["order"]
	ent.Fields["code"] = schema.FieldDefinition{
		ID: uuid.Must(uuid.NewV7()), Key: "code", DisplayName: "Code",
		Type: schema.TypeString, Required: true,
		Origin: schema.OriginCustom, CreatedAt: now, UpdatedAt: now,
	}
	cand.Schema.Entities["order"] = ent
	if Classify(&active, &cand) != CompatBreakingOrDestructive {
		t.Fatalf("got %s", Classify(&active, &cand))
	}
}

func TestClassifyTypeChange(t *testing.T) {
	active, cand := basePair(t)
	ent := cand.Schema.Entities["order"]
	f := ent.Fields["title"]
	f.Type = schema.TypeInteger
	ent.Fields["title"] = f
	cand.Schema.Entities["order"] = ent
	if Classify(&active, &cand) != CompatBreakingOrDestructive {
		t.Fatalf("got %s", Classify(&active, &cand))
	}
}

func TestClassifyRemoveField(t *testing.T) {
	active, cand := basePair(t)
	ent := cand.Schema.Entities["order"]
	delete(ent.Fields, "title")
	cand.Schema.Entities["order"] = ent
	if Classify(&active, &cand) != CompatBreakingOrDestructive {
		t.Fatalf("got %s", Classify(&active, &cand))
	}
}

func TestClassifyPinChange(t *testing.T) {
	active, cand := basePair(t)
	pin := ModulePinSnapshot{
		ModuleSlug: "orders",
		ReleaseID:  uuid.Must(uuid.NewV7()),
		Version:    "1.0.0",
	}
	active.ModulePins = []ModulePinSnapshot{pin}
	cand.ModulePins = []ModulePinSnapshot{pin}
	cand.ModulePins[0].Version = "1.1.0"
	cand.ModulePins[0].ReleaseID = uuid.Must(uuid.NewV7())
	if Classify(&active, &cand) != CompatMigrationRequired {
		t.Fatalf("got %s", Classify(&active, &cand))
	}
}

func TestClassifyNilActive(t *testing.T) {
	_, cand := basePair(t)
	if Classify(nil, &cand) != CompatMigrationRequired {
		t.Fatal("expected migration_required for first version")
	}
}
