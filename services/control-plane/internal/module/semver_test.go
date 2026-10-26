package module

import "testing"

func TestParseAndCompare(t *testing.T) {
	a, err := ParseVersion("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseVersion("1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	if Compare(a, b) >= 0 {
		t.Fatal("expected a < b")
	}
	c, err := ParseConstraint("^1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !c.SatisfiedBy(a) || c.SatisfiedBy(mustVer("2.0.0")) {
		t.Fatal("caret constraint")
	}
	g, err := ParseConstraint(">=1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !g.SatisfiedBy(b) || g.SatisfiedBy(mustVer("1.2.2")) {
		t.Fatal("gte constraint")
	}
}

func mustVer(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}
