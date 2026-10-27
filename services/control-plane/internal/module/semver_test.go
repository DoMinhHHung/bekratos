package module

import "testing"

func TestStrictSemVer(t *testing.T) {
	if _, err := ParseVersion("1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVersion("1.9.0"); err != nil {
		t.Fatal(err)
	}
	a, _ := ParseVersion("1.9.0")
	b, _ := ParseVersion("1.10.0")
	if CompareVersions(a, b) >= 0 {
		t.Fatal("1.9.0 must sort below 1.10.0")
	}
	if _, err := ParseVersion("01.2.3"); err == nil {
		t.Fatal("leading zero must fail")
	}
	if _, err := ParseVersion("1.2"); err == nil {
		t.Fatal("incomplete must fail")
	}
	a, _ = ParseVersion("1.0.0-alpha.1")
	b, _ = ParseVersion("1.0.0-alpha.2")
	c, _ := ParseVersion("1.0.0")
	if CompareVersions(a, b) >= 0 || CompareVersions(b, c) >= 0 {
		t.Fatal("prerelease ordering")
	}
	buildA, _ := ParseVersion("1.0.0+build.1")
	buildB, _ := ParseVersion("1.0.0+build.2")
	if CompareVersions(buildA, buildB) != 0 {
		t.Fatal("build metadata must not change semantic precedence")
	}
	_, err := ParseVersion("1.0.0+build.1")
	if err != nil {
		t.Fatal("build metadata should parse")
	}
	if _, err := ParseVersion("v1.0.0"); err == nil {
		t.Fatal("leading v must be rejected")
	}
	if _, err := ParseVersion(" 1.0.0"); err == nil {
		t.Fatal("non-canonical surrounding whitespace must be rejected")
	}
}

func TestCaretZero(t *testing.T) {
	c, err := ParseConstraint("^0.2.3")
	if err != nil {
		t.Fatal(err)
	}
	v23, _ := ParseVersion("0.2.3")
	v24, _ := ParseVersion("0.2.9")
	v30, _ := ParseVersion("0.3.0")
	if !ConstraintSatisfied(c, v23) || !ConstraintSatisfied(c, v24) {
		t.Fatal("0.2.x should match")
	}
	if ConstraintSatisfied(c, v30) {
		t.Fatal("^0.2.3 must not admit 0.3.0")
	}
}
