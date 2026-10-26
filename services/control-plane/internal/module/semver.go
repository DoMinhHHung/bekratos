package module

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string
}

func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	s = strings.TrimPrefix(s, "v")
	pre := ""
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		if s[i] == '-' {
			pre = s[i+1:]
			if j := strings.IndexByte(pre, '+'); j >= 0 {
				pre = pre[:j]
			}
		}
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version")
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || maj < 0 || min < 0 || pat < 0 {
		return Version{}, fmt.Errorf("invalid version")
	}
	return Version{Major: maj, Minor: min, Patch: pat, Pre: pre}, nil
}

func (v Version) String() string {
	if v.Pre != "" {
		return fmt.Sprintf("%d.%d.%d-%s", v.Major, v.Minor, v.Patch, v.Pre)
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func Compare(a, b Version) int {
	if a.Major != b.Major {
		return cmpInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpInt(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return cmpInt(a.Patch, b.Patch)
	}
	if a.Pre == b.Pre {
		return 0
	}
	if a.Pre == "" {
		return 1
	}
	if b.Pre == "" {
		return -1
	}
	if a.Pre < b.Pre {
		return -1
	}
	if a.Pre > b.Pre {
		return 1
	}
	return 0
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

type Constraint struct {
	raw string
	op  string
	ver Version
}

func ParseConstraint(s string) (Constraint, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Constraint{}, fmt.Errorf("empty constraint")
	}
	if strings.HasPrefix(s, "^") {
		v, err := ParseVersion(strings.TrimPrefix(s, "^"))
		if err != nil {
			return Constraint{}, err
		}
		return Constraint{raw: s, op: "^", ver: v}, nil
	}
	if strings.HasPrefix(s, ">=") {
		v, err := ParseVersion(strings.TrimPrefix(s, ">="))
		if err != nil {
			return Constraint{}, err
		}
		return Constraint{raw: s, op: ">=", ver: v}, nil
	}
	v, err := ParseVersion(s)
	if err != nil {
		return Constraint{}, err
	}
	return Constraint{raw: s, op: "=", ver: v}, nil
}

func (c Constraint) SatisfiedBy(v Version) bool {
	switch c.op {
	case "=":
		return Compare(v, c.ver) == 0
	case ">=":
		return Compare(v, c.ver) >= 0
	case "^":
		if Compare(v, c.ver) < 0 {
			return false
		}
		upper := Version{Major: c.ver.Major + 1, Minor: 0, Patch: 0}
		return Compare(v, upper) < 0
	default:
		return false
	}
}
