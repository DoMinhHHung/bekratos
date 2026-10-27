package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

var reservedEntityKeys = map[string]struct{}{
	"system": {}, "module": {}, "internal": {}, "meta": {}, "config": {},
}

var reservedFieldKeys = map[string]struct{}{
	"id": {}, "created_at": {}, "updated_at": {}, "deleted_at": {},
	"project_id": {}, "environment_id": {}, "organization_id": {},
	"_id": {}, "__proto__": {},
}

var reservedPrefixes = []string{"_", "sys_", "bk_", "bekratos_"}

const (
	maxEntitiesPerSchema   = 64
	maxFieldsPerEntity     = 64
	maxRelationshipsPerEnt = 32
	maxDisplayNameLen      = 120
	maxStringDefaultLen    = 1024
	maxStringMaxLength     = 8192
)

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func CanonicalizeDocument(doc SchemaDocument) (SchemaDocument, error) {
	if doc.Entities == nil {
		return SchemaDocument{Entities: map[string]EntityDefinition{}}, nil
	}
	out := SchemaDocument{Entities: make(map[string]EntityDefinition, len(doc.Entities))}
	for rawKey, ent := range doc.Entities {
		key := normalizeKey(rawKey)
		entKey := normalizeKey(ent.Key)
		if entKey == "" {
			entKey = key
		}
		if key != entKey {
			return SchemaDocument{}, fmt.Errorf("%w: entity map key %q does not match key %q", ErrValidation, rawKey, ent.Key)
		}
		if _, exists := out.Entities[key]; exists {
			return SchemaDocument{}, fmt.Errorf("%w: duplicate entity key %q after normalization", ErrDuplicateKey, key)
		}
		ent.Key = key

		fields := map[string]FieldDefinition{}
		for rawFK, f := range ent.Fields {
			fk := normalizeKey(rawFK)
			fKey := normalizeKey(f.Key)
			if fKey == "" {
				fKey = fk
			}
			if fk != fKey {
				return SchemaDocument{}, fmt.Errorf("%w: field map key mismatch", ErrValidation)
			}
			if _, exists := fields[fk]; exists {
				return SchemaDocument{}, fmt.Errorf("%w: duplicate field key %q", ErrDuplicateKey, fk)
			}
			f.Key = fk
			fields[fk] = f
		}
		ent.Fields = fields

		rels := map[string]RelationshipDefinition{}
		for rawRK, r := range ent.Relationships {
			rk := normalizeKey(rawRK)
			rKey := normalizeKey(r.Key)
			if rKey == "" {
				rKey = rk
			}
			if rk != rKey {
				return SchemaDocument{}, fmt.Errorf("%w: relationship map key mismatch", ErrValidation)
			}
			if _, exists := rels[rk]; exists {
				return SchemaDocument{}, fmt.Errorf("%w: duplicate relationship key %q", ErrDuplicateKey, rk)
			}
			r.Key = rk
			r.SourceEntity = normalizeKey(r.SourceEntity)
			r.TargetEntity = normalizeKey(r.TargetEntity)
			rels[rk] = r
		}
		ent.Relationships = rels
		out.Entities[key] = ent
	}
	return out, nil
}

func ValidateDocument(doc SchemaDocument) error {
	canonical, err := CanonicalizeDocument(doc)
	if err != nil {
		return err
	}
	doc = canonical
	if len(doc.Entities) > maxEntitiesPerSchema {
		return fmt.Errorf("%w: too many entities", ErrValidation)
	}

	for key, ent := range doc.Entities {
		if err := validateEntityKey(key); err != nil {
			return err
		}
		if err := validateEntity(ent, key); err != nil {
			return err
		}
	}

	for key, ent := range doc.Entities {
		for relKey, rel := range ent.Relationships {
			if err := validateRelationship(rel, key, relKey, doc.Entities); err != nil {
				return err
			}
		}
		if ent.AccessPolicy != nil {
			count := 0
			if err := ValidatePolicy(ent.AccessPolicy, ent, 0, &count); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateEntityKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%w: invalid entity key %q", ErrInvalidInput, key)
	}
	if _, ok := reservedEntityKeys[key]; ok {
		return fmt.Errorf("%w: reserved entity key %q", ErrReservedKey, key)
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(key, p) {
			return fmt.Errorf("%w: reserved entity prefix on %q", ErrReservedKey, key)
		}
	}
	return nil
}

func validateEntity(ent EntityDefinition, key string) error {
	if ent.ID == uuid.Nil {
		return fmt.Errorf("%w: entity %q missing id", ErrValidation, key)
	}
	if normalizeKey(ent.Key) != key {
		return fmt.Errorf("%w: entity key inconsistency", ErrValidation)
	}
	name := strings.TrimSpace(ent.DisplayName)
	if name == "" || utf8.RuneCountInString(name) > maxDisplayNameLen {
		return fmt.Errorf("%w: invalid entity display_name", ErrInvalidInput)
	}
	switch ent.Origin {
	case OriginSystem, OriginModule, OriginCustom:
	default:
		return fmt.Errorf("%w: invalid origin", ErrInvalidInput)
	}
	if ent.Origin == OriginModule {
		if strings.TrimSpace(ent.ModuleSlug) == "" || ent.ReleaseID == nil || *ent.ReleaseID == uuid.Nil {
			return fmt.Errorf("%w: module entity requires module_slug and release_id", ErrValidation)
		}
	}
	if ent.Origin == OriginCustom {
		if ent.ModuleSlug != "" || ent.ReleaseID != nil {
			return fmt.Errorf("%w: custom entity must not carry module provenance", ErrValidation)
		}
	}
	if ent.Fields == nil {
		ent.Fields = map[string]FieldDefinition{}
	}
	if len(ent.Fields) > maxFieldsPerEntity {
		return fmt.Errorf("%w: too many fields on entity %q", ErrValidation, key)
	}
	for fk, f := range ent.Fields {
		if err := validateField(f, fk, ent.Origin); err != nil {
			return err
		}
	}
	if ent.Relationships == nil {
		ent.Relationships = map[string]RelationshipDefinition{}
	}
	if len(ent.Relationships) > maxRelationshipsPerEnt {
		return fmt.Errorf("%w: too many relationships on entity %q", ErrValidation, key)
	}
	return nil
}

func validateField(f FieldDefinition, key string, entityOrigin Origin) error {
	if f.ID == uuid.Nil {
		return fmt.Errorf("%w: field %q missing id", ErrValidation, key)
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%w: invalid field key %q", ErrInvalidInput, key)
	}
	if _, ok := reservedFieldKeys[key]; ok {
		return fmt.Errorf("%w: reserved field key %q", ErrReservedKey, key)
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(key, p) {
			return fmt.Errorf("%w: reserved field prefix on %q", ErrReservedKey, key)
		}
	}
	name := strings.TrimSpace(f.DisplayName)
	if name == "" || utf8.RuneCountInString(name) > maxDisplayNameLen {
		return fmt.Errorf("%w: invalid field display_name", ErrInvalidInput)
	}
	switch f.Type {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean, TypeDate, TypeDateTime, TypeUUID:
	default:
		return fmt.Errorf("%w: unsupported field type %q", ErrInvalidInput, f.Type)
	}
	if f.Required && f.Nullable {
		return fmt.Errorf("%w: field %q cannot be both required and nullable", ErrValidation, key)
	}
	if f.Default != nil {
		if err := validateDefault(f.Default, f.Type); err != nil {
			return fmt.Errorf("%w: field %q default: %v", ErrValidation, key, err)
		}
		if f.Required && isJSONNull(f.Default) {
			return fmt.Errorf("%w: required field cannot default to null", ErrValidation)
		}
	}
	if f.Constraints != nil {
		if err := validateConstraints(f.Constraints, f.Type); err != nil {
			return fmt.Errorf("%w: field %q constraints: %v", ErrValidation, key, err)
		}
	}
	switch f.Origin {
	case OriginSystem, OriginModule, OriginCustom:
	default:
		return fmt.Errorf("%w: invalid field origin", ErrInvalidInput)
	}
	if f.Origin == OriginCustom && (f.ModuleSlug != "" || f.ReleaseID != nil) {
		return fmt.Errorf("%w: custom field must not carry module provenance", ErrValidation)
	}
	if entityOrigin == OriginSystem && f.Origin != OriginSystem {
		return fmt.Errorf("%w: system entity cannot host non-system fields", ErrValidation)
	}
	return nil
}

func validateDefault(raw json.RawMessage, t ScalarType) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("trailing data in default")
	}
	return assertLiteralTypeStrict(v, t)
}

func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

func validateConstraints(c *FieldConstraints, t ScalarType) error {
	switch t {
	case TypeString:
		if c.Min != nil || c.Max != nil {
			return fmt.Errorf("numeric bounds not allowed on string")
		}
		if c.MinLength != nil && *c.MinLength < 0 {
			return fmt.Errorf("min_length negative")
		}
		if c.MaxLength != nil {
			if *c.MaxLength < 0 || *c.MaxLength > maxStringMaxLength {
				return fmt.Errorf("max_length out of range")
			}
			if c.MinLength != nil && *c.MinLength > *c.MaxLength {
				return fmt.Errorf("min_length > max_length")
			}
		}
	case TypeInteger, TypeNumber:
		if c.MinLength != nil || c.MaxLength != nil {
			return fmt.Errorf("length bounds not allowed on numeric")
		}
		if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
			return fmt.Errorf("min > max")
		}
	default:
		if c.MinLength != nil || c.MaxLength != nil || c.Min != nil || c.Max != nil {
			return fmt.Errorf("constraints not supported for type %s", t)
		}
	}
	return nil
}

func validateRelationship(rel RelationshipDefinition, sourceKey, relKey string, entities map[string]EntityDefinition) error {
	if rel.ID == uuid.Nil {
		return fmt.Errorf("%w: relationship %q missing id", ErrValidation, relKey)
	}
	if !keyPattern.MatchString(relKey) {
		return fmt.Errorf("%w: invalid relationship key %q", ErrInvalidInput, relKey)
	}
	src := normalizeKey(rel.SourceEntity)
	tgt := normalizeKey(rel.TargetEntity)
	if src != sourceKey {
		return fmt.Errorf("%w: relationship source must match owning entity", ErrInvalidRelation)
	}
	if _, ok := entities[src]; !ok {
		return fmt.Errorf("%w: unknown source entity %q", ErrUnknownEntity, src)
	}
	if _, ok := entities[tgt]; !ok {
		return fmt.Errorf("%w: unknown target entity %q", ErrUnknownEntity, tgt)
	}
	switch rel.Cardinality {
	case CardinalityOneToOne, CardinalityManyToOne, CardinalityOneToMany, CardinalityManyToMany:
	default:
		return fmt.Errorf("%w: unsupported cardinality", ErrInvalidRelation)
	}
	switch rel.DeleteBehavior {
	case DeleteRestrict, DeleteSetNull:
	default:
		return fmt.Errorf("%w: unsupported delete behavior", ErrInvalidRelation)
	}
	if rel.DeleteBehavior == DeleteSetNull && rel.Required {
		return fmt.Errorf("%w: set_null incompatible with required relationship", ErrInvalidRelation)
	}
	switch rel.Origin {
	case OriginSystem, OriginModule, OriginCustom:
	default:
		return fmt.Errorf("%w: invalid relationship origin", ErrInvalidInput)
	}
	if rel.Origin == OriginCustom && (rel.ModuleSlug != "" || rel.ReleaseID != nil) {
		return fmt.Errorf("%w: custom relationship must not carry module provenance", ErrValidation)
	}
	return nil
}

func assertLiteralTypeStrict(v any, t ScalarType) error {
	switch t {
	case TypeString:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidPolicy
		}
		if utf8.RuneCountInString(s) > maxStringDefaultLen {
			return fmt.Errorf("string default too long")
		}
		return nil
	case TypeDate:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidPolicy
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return fmt.Errorf("invalid date")
		}
		return nil
	case TypeDateTime:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidPolicy
		}
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			if _, err2 := time.Parse(time.RFC3339, s); err2 != nil {
				return fmt.Errorf("invalid datetime")
			}
		}
		return nil
	case TypeUUID:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidPolicy
		}
		if _, err := uuid.Parse(s); err != nil {
			return fmt.Errorf("invalid uuid")
		}
		return nil
	case TypeInteger:
		switch n := v.(type) {
		case float64:
			if n != float64(int64(n)) {
				return ErrInvalidPolicy
			}
			return nil
		case json.Number:
			_, err := n.Int64()
			return err
		default:
			return ErrInvalidPolicy
		}
	case TypeNumber:
		switch n := v.(type) {
		case float64:
			return nil
		case json.Number:
			_, err := n.Float64()
			return err
		default:
			return ErrInvalidPolicy
		}
	case TypeBoolean:
		_, ok := v.(bool)
		if !ok {
			return ErrInvalidPolicy
		}
		return nil
	default:
		return ErrInvalidPolicy
	}
}

func EmptyDocument() SchemaDocument {
	return SchemaDocument{Entities: map[string]EntityDefinition{}}
}
