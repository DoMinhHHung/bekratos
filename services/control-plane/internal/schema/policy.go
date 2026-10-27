package schema

import (
	"fmt"
	"strings"
)

const (
	maxPolicyDepth = 8
	maxPolicyNodes = 64
)

type PolicyNode struct {
	Op      string       `json:"op"`
	Args    []PolicyNode `json:"args,omitempty"`
	Field   string       `json:"field,omitempty"`
	Context string       `json:"context,omitempty"`
	Value   any          `json:"value,omitempty"`
	Values  []any        `json:"values,omitempty"`
}

var allowedBooleanOps = map[string]struct{}{
	"all": {},
	"any": {},
	"not": {},
}

var allowedComparisonOps = map[string]struct{}{
	"eq":      {},
	"ne":      {},
	"lt":      {},
	"lte":     {},
	"gt":      {},
	"gte":     {},
	"in":      {},
	"is_null": {},
}

var allowedContextAttrs = map[string]ScalarType{
	"identity_user_id": TypeUUID,
	"now":              TypeDateTime,
}

func ValidatePolicy(node *PolicyNode, entity EntityDefinition, depth int, nodeCount *int) error {
	if node == nil {
		return nil
	}
	if depth > maxPolicyDepth {
		return fmt.Errorf("%w: policy depth exceeded", ErrInvalidPolicy)
	}
	*nodeCount++
	if *nodeCount > maxPolicyNodes {
		return fmt.Errorf("%w: policy node count exceeded", ErrInvalidPolicy)
	}
	op := strings.ToLower(strings.TrimSpace(node.Op))
	if op == "" {
		return fmt.Errorf("%w: missing op", ErrInvalidPolicy)
	}

	if _, ok := allowedBooleanOps[op]; ok {
		switch op {
		case "not":
			if len(node.Args) != 1 {
				return fmt.Errorf("%w: not requires exactly one argument", ErrInvalidPolicy)
			}
			return ValidatePolicy(&node.Args[0], entity, depth+1, nodeCount)
		case "all", "any":
			if len(node.Args) == 0 {
				return fmt.Errorf("%w: %s requires at least one argument", ErrInvalidPolicy, op)
			}
			for i := range node.Args {
				if err := ValidatePolicy(&node.Args[i], entity, depth+1, nodeCount); err != nil {
					return err
				}
			}
			return nil
		}
	}

	if _, ok := allowedComparisonOps[op]; ok {
		return validateComparison(node, entity, op)
	}
	return fmt.Errorf("%w: unknown op %q", ErrInvalidPolicy, op)
}

func validateComparison(node *PolicyNode, entity EntityDefinition, op string) error {
	if len(node.Args) > 0 {
		return fmt.Errorf("%w: comparison ops must not contain nested args", ErrInvalidPolicy)
	}

	var leftType ScalarType
	var hasLeft bool

	if node.Field != "" {
		f, ok := entity.Fields[normalizeKey(node.Field)]
		if !ok {
			return fmt.Errorf("%w: unknown field %q", ErrInvalidPolicy, node.Field)
		}
		leftType = f.Type
		hasLeft = true
	}
	if node.Context != "" {
		ct, ok := allowedContextAttrs[node.Context]
		if !ok {
			return fmt.Errorf("%w: unknown context attribute %q", ErrInvalidPolicy, node.Context)
		}
		if hasLeft {
			return fmt.Errorf("%w: field and context are mutually exclusive", ErrInvalidPolicy)
		}
		leftType = ct
		hasLeft = true
	}
	if !hasLeft {
		return fmt.Errorf("%w: comparison requires field or context", ErrInvalidPolicy)
	}

	switch op {
	case "is_null":
		if node.Value != nil || len(node.Values) > 0 {
			return fmt.Errorf("%w: is_null takes no value", ErrInvalidPolicy)
		}
		return nil
	case "in":
		if len(node.Values) == 0 {
			return fmt.Errorf("%w: in requires values", ErrInvalidPolicy)
		}
		if len(node.Values) > 32 {
			return fmt.Errorf("%w: in values limit exceeded", ErrInvalidPolicy)
		}
		for _, v := range node.Values {
			if err := assertLiteralTypeStrict(v, leftType); err != nil {
				return fmt.Errorf("%w: in value type mismatch", ErrInvalidPolicy)
			}
		}
		return nil
	default:
		if node.Value == nil {
			return fmt.Errorf("%w: comparison requires value", ErrInvalidPolicy)
		}
		if err := assertLiteralTypeStrict(node.Value, leftType); err != nil {
			return fmt.Errorf("%w: value type mismatch", ErrInvalidPolicy)
		}
		return nil
	}
}
