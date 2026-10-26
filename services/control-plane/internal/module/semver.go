package module

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

func ParseVersion(s string) (*semver.Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty version")
	}
	v, err := semver.StrictNewVersion(strings.TrimPrefix(s, "v"))
	if err != nil {
		return nil, fmt.Errorf("invalid version")
	}
	return v, nil
}

func ParseConstraint(s string) (*semver.Constraints, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty constraint")
	}
	c, err := semver.NewConstraint(s)
	if err != nil {
		return nil, fmt.Errorf("invalid constraint")
	}
	return c, nil
}

func ConstraintSatisfied(c *semver.Constraints, v *semver.Version) bool {
	return c.Check(v)
}

func CompareVersions(a, b *semver.Version) int {
	return a.Compare(b)
}
