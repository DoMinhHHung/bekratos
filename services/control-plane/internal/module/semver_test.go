package module

import "testing"

func TestStrictSemVer(t *testing.T) {
	if _, err := ParseVersion("1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVersion("01.2.3"); err == nil {
		t.Fatal("leading zero must fail")
	}
	if _, err := ParseVersion("1.2"); err == nil {
		t.Fatal("incomplete must fail")
	}
	a, _ := ParseVersion("1.0.0-alpha.1")
	b, _ := ParseVersion("1.0.0-alpha.2")
	c, _ := ParseVersion("1.0.0")
	if CompareVersions(a, b) >= 0 || CompareVersions(b, c) >= 0 {
		t.Fatal("prerelease ordering")
	}
	_, err := ParseVersion("1.0.0+build.1")
	if err != nil {
		t.Fatal("build metadata should parse")
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
