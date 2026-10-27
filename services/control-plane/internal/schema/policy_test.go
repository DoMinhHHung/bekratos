package schema

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sampleEntity() EntityDefinition {
	now := time.Now().UTC()
	return EntityDefinition{
		ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
		Fields: map[string]FieldDefinition{
			"status": {
				ID: uuid.Must(uuid.NewV7()), Key: "status", DisplayName: "Status", Type: TypeString,
				Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
			},
			"amount": {
				ID: uuid.Must(uuid.NewV7()), Key: "amount", DisplayName: "Amount", Type: TypeNumber,
				Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
			},
		},
		Relationships: map[string]RelationshipDefinition{},
		CreatedAt:     now, UpdatedAt: now,
	}
}

func TestValidatePolicySimpleEq(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{Op: "eq", Field: "status", Value: "open"}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); err != nil {
		t.Fatalf("eq: %v", err)
	}
}

func TestValidatePolicyAllAnyNot(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{
		Op: "all",
		Args: []PolicyNode{
			{Op: "eq", Field: "status", Value: "open"},
			{Op: "gt", Field: "amount", Value: float64(0)},
			{Op: "not", Args: []PolicyNode{{Op: "eq", Field: "status", Value: "closed"}}},
		},
	}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); err != nil {
		t.Fatalf("compose: %v", err)
	}
}

func TestValidatePolicyUnknownOp(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{Op: "eval", Field: "status", Value: "x"}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("want invalid policy, got %v", err)
	}
}

func TestValidatePolicyUnknownField(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{Op: "eq", Field: "missing", Value: "x"}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("want invalid policy, got %v", err)
	}
}

func TestValidatePolicyUnknownContext(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{Op: "eq", Context: "request.headers", Value: "x"}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("want invalid policy, got %v", err)
	}
}

func TestValidatePolicyTypeMismatch(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{Op: "eq", Field: "amount", Value: "not-number"}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("want invalid policy, got %v", err)
	}
}

func TestValidatePolicyStrictDateAndUUIDLiterals(t *testing.T) {
	ent := sampleEntity()
	ent.Fields["created"] = FieldDefinition{Type: TypeDate}
	ent.Fields["owner_id"] = FieldDefinition{Type: TypeUUID}

	for _, tc := range []struct {
		name  string
		field string
		value any
		valid bool
	}{
		{name: "valid date", field: "created", value: "2026-10-27", valid: true},
		{name: "invalid date", field: "created", value: "not-a-date"},
		{name: "valid uuid", field: "owner_id", value: "019a2f1e-1d2b-7c33-9ef0-123456789abc", valid: true},
		{name: "invalid uuid", field: "owner_id", value: "not-a-uuid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			err := ValidatePolicy(&PolicyNode{Op: "eq", Field: tc.field, Value: tc.value}, ent, 0, &count)
			if tc.valid && err != nil {
				t.Fatalf("valid comparison rejected: %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("expected invalid policy, got %v", err)
			}
		})
	}
}

func TestValidatePolicyDepthLimit(t *testing.T) {
	ent := sampleEntity()
	node := &PolicyNode{Op: "not", Args: []PolicyNode{{Op: "eq", Field: "status", Value: "a"}}}
	for i := 0; i < maxPolicyDepth+2; i++ {
		node = &PolicyNode{Op: "not", Args: []PolicyNode{*node}}
	}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("want depth limit, got %v", err)
	}
}

func TestValidatePolicyNodeCountLimit(t *testing.T) {
	ent := sampleEntity()
	args := make([]PolicyNode, maxPolicyNodes+5)
	for i := range args {
		args[i] = PolicyNode{Op: "eq", Field: "status", Value: "x"}
	}
	node := &PolicyNode{Op: "all", Args: args}
	count := 0
	if err := ValidatePolicy(node, ent, 0, &count); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("want node limit, got %v", err)
	}
}

func TestValidatePolicyIsNullAndIn(t *testing.T) {
	ent := sampleEntity()
	count := 0
	if err := ValidatePolicy(&PolicyNode{Op: "is_null", Field: "status"}, ent, 0, &count); err != nil {
		t.Fatalf("is_null: %v", err)
	}
	count = 0
	if err := ValidatePolicy(&PolicyNode{Op: "in", Field: "status", Values: []any{"a", "b"}}, ent, 0, &count); err != nil {
		t.Fatalf("in: %v", err)
	}
}

func TestValidatePolicyInjectionLikeOps(t *testing.T) {
	ent := sampleEntity()
	for _, op := range []string{"sql", "exec", "function", "lambda", "call"} {
		count := 0
		err := ValidatePolicy(&PolicyNode{Op: op, Field: "status", Value: "x"}, ent, 0, &count)
		if !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("op %s: want invalid, got %v", op, err)
		}
	}
}
