package configversion

import (
	"encoding/json"
	"reflect"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
)

func Classify(active, candidate *Manifest) CompatibilityClass {
	if active == nil {
		if candidate == nil {
			return CompatCompatible
		}
		return CompatMigrationRequired
	}
	if candidate == nil {
		return CompatBreakingOrDestructive
	}

	worst := CompatCompatible
	raise := func(c CompatibilityClass) {
		if rank(c) > rank(worst) {
			worst = c
		}
	}

	if pinSetChanged(active.ModulePins, candidate.ModulePins) {
		raise(CompatMigrationRequired)
	}

	prevEnts := active.Schema.Entities
	nextEnts := candidate.Schema.Entities
	if prevEnts == nil {
		prevEnts = map[string]schema.EntityDefinition{}
	}
	if nextEnts == nil {
		nextEnts = map[string]schema.EntityDefinition{}
	}

	for key, prev := range prevEnts {
		next, ok := nextEnts[key]
		if !ok {
			raise(CompatBreakingOrDestructive)
			continue
		}
		raise(classifyEntity(prev, next))
	}
	for key, next := range nextEnts {
		if _, ok := prevEnts[key]; ok {
			continue
		}
		raise(classifyNewEntity(next))
	}
	return worst
}

func rank(c CompatibilityClass) int {
	switch c {
	case CompatCompatible:
		return 0
	case CompatMigrationRequired:
		return 1
	case CompatBreakingOrDestructive:
		return 2
	default:
		return 3
	}
}

func pinSetChanged(a, b []ModulePinSnapshot) bool {
	if len(a) != len(b) {
		return true
	}
	am := map[string]ModulePinSnapshot{}
	for _, p := range a {
		am[p.ModuleSlug] = p
	}
	for _, p := range b {
		prev, ok := am[p.ModuleSlug]
		if !ok {
			return true
		}
		if prev.ReleaseID != p.ReleaseID || prev.Version != p.Version || prev.APIContractVersion != p.APIContractVersion {
			return true
		}
	}
	return false
}

func classifyNewEntity(e schema.EntityDefinition) CompatibilityClass {
	if e.Origin == schema.OriginSystem || e.Origin == schema.OriginModule {
		return CompatBreakingOrDestructive
	}
	for _, f := range e.Fields {
		if f.Required && (f.Default == nil || string(f.Default) == "null") {
			return CompatBreakingOrDestructive
		}
		if f.Required {
			return CompatMigrationRequired
		}
	}
	if len(e.Relationships) > 0 {
		return CompatMigrationRequired
	}
	return CompatCompatible
}

func classifyEntity(prev, next schema.EntityDefinition) CompatibilityClass {
	worst := CompatCompatible
	raise := func(c CompatibilityClass) {
		if rank(c) > rank(worst) {
			worst = c
		}
	}
	if prev.Origin != next.Origin || prev.Protected != next.Protected {
		raise(CompatBreakingOrDestructive)
	}
	if prev.Origin == schema.OriginSystem || prev.Origin == schema.OriginModule || prev.Protected {
		if prev.DisplayName != next.DisplayName {
			raise(CompatBreakingOrDestructive)
		}
	}
	if !policyEqual(prev.AccessPolicy, next.AccessPolicy) {
		raise(CompatMigrationRequired)
	}

	pf := prev.Fields
	nf := next.Fields
	if pf == nil {
		pf = map[string]schema.FieldDefinition{}
	}
	if nf == nil {
		nf = map[string]schema.FieldDefinition{}
	}
	for k, a := range pf {
		b, ok := nf[k]
		if !ok {
			raise(CompatBreakingOrDestructive)
			continue
		}
		raise(classifyField(a, b))
	}
	for k, b := range nf {
		if _, ok := pf[k]; ok {
			continue
		}
		raise(classifyNewField(b))
	}

	pr := prev.Relationships
	nr := next.Relationships
	if pr == nil {
		pr = map[string]schema.RelationshipDefinition{}
	}
	if nr == nil {
		nr = map[string]schema.RelationshipDefinition{}
	}
	for k, a := range pr {
		b, ok := nr[k]
		if !ok {
			raise(CompatBreakingOrDestructive)
			continue
		}
		raise(classifyRelationship(a, b))
	}
	for k := range nr {
		if _, ok := pr[k]; !ok {
			raise(CompatMigrationRequired)
		}
	}
	return worst
}

func classifyNewField(f schema.FieldDefinition) CompatibilityClass {
	if f.Origin == schema.OriginSystem || f.Origin == schema.OriginModule || f.Protected {
		return CompatBreakingOrDestructive
	}
	if f.Required && (f.Default == nil || string(f.Default) == "null") {
		return CompatBreakingOrDestructive
	}
	if f.Required {
		return CompatMigrationRequired
	}
	return CompatCompatible
}

func classifyField(a, b schema.FieldDefinition) CompatibilityClass {
	if a.Origin != b.Origin || a.Protected != b.Protected {
		return CompatBreakingOrDestructive
	}
	if a.Type != b.Type {
		return CompatBreakingOrDestructive
	}
	if a.Required != b.Required || a.Nullable != b.Nullable {
		if b.Required && !a.Required && (b.Default == nil || string(b.Default) == "null") {
			return CompatBreakingOrDestructive
		}
		return CompatMigrationRequired
	}
	if !rawEqual(a.Default, b.Default) {
		return CompatMigrationRequired
	}
	if !constraintsEqual(a.Constraints, b.Constraints) {
		if constraintsTightened(a.Constraints, b.Constraints) {
			return CompatMigrationRequired
		}
		return CompatCompatible
	}
	return CompatCompatible
}

func classifyRelationship(a, b schema.RelationshipDefinition) CompatibilityClass {
	if a.Origin != b.Origin || a.Protected != b.Protected {
		return CompatBreakingOrDestructive
	}
	if a.Cardinality != b.Cardinality ||
		a.TargetEntity != b.TargetEntity ||
		a.SourceEntity != b.SourceEntity ||
		a.DeleteBehavior != b.DeleteBehavior ||
		a.Required != b.Required {
		return CompatBreakingOrDestructive
	}
	return CompatCompatible
}

func classifyNewRelationship() CompatibilityClass {
	return CompatMigrationRequired
}

func constraintsTightened(prev, next *schema.FieldConstraints) bool {
	if next == nil {
		return false
	}
	if prev == nil {
		return next.MinLength != nil || next.MaxLength != nil || next.Min != nil || next.Max != nil
	}
	if next.MinLength != nil && (prev.MinLength == nil || *next.MinLength > *prev.MinLength) {
		return true
	}
	if next.MaxLength != nil && (prev.MaxLength == nil || *next.MaxLength < *prev.MaxLength) {
		return true
	}
	if next.Min != nil && (prev.Min == nil || *next.Min > *prev.Min) {
		return true
	}
	if next.Max != nil && (prev.Max == nil || *next.Max < *prev.Max) {
		return true
	}
	return false
}

func constraintsEqual(a, b *schema.FieldConstraints) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return intPtrEq(a.MinLength, b.MinLength) &&
		intPtrEq(a.MaxLength, b.MaxLength) &&
		floatPtrEq(a.Min, b.Min) &&
		floatPtrEq(a.Max, b.Max)
}

func intPtrEq(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func floatPtrEq(a, b *float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func rawEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return string(a) == string(b)
}

func policyEqual(a, b *schema.PolicyNode) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
