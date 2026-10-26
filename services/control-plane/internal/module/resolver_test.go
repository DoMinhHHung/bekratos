package module

import (
	"testing"

	"github.com/google/uuid"
)

func TestResolveAuthPullsCore(t *testing.T) {
	modules := []Module{{Slug: "core"}, {Slug: "auth"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), ModuleSlug: "core", Version: "1.0.0"},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000002"), ModuleSlug: "auth", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "core", VersionConstraint: "^1.0.0", Required: true}}},
	}
	plan, err := Resolve(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Releases) != 2 {
		t.Fatalf("expected 2, got %d", len(plan.Releases))
	}
}

func TestResolveOverlappingConstraintsBacktrack(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}, {Slug: "c"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000101"), ModuleSlug: "a", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "c", VersionConstraint: "1.0.0", Required: true}}},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000102"), ModuleSlug: "a", Version: "1.1.0",
			Dependencies: []Dependency{{ModuleSlug: "c", VersionConstraint: "1.1.0", Required: true}}},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000103"), ModuleSlug: "b", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "c", VersionConstraint: "1.0.0", Required: true}}},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000104"), ModuleSlug: "c", Version: "1.0.0"},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000105"), ModuleSlug: "c", Version: "1.1.0"},
	}
	plan, err := Resolve(modules, releases, []Selection{
		{ModuleSlug: "a", Version: ">=1.0.0"},
		{ModuleSlug: "b", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var aVer, cVer string
	for _, r := range plan.Releases {
		if r.ModuleSlug == "a" {
			aVer = r.Version
		}
		if r.ModuleSlug == "c" {
			cVer = r.Version
		}
	}
	if aVer != "1.0.0" || cVer != "1.0.0" {
		t.Fatalf("backtrack failed: a=%s c=%s", aVer, cVer)
	}
}

func TestResolveCycle(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000010"), ModuleSlug: "a", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "b", VersionConstraint: "1.0.0", Required: true}}},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000011"), ModuleSlug: "b", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "a", VersionConstraint: "1.0.0", Required: true}}},
	}
	if _, err := Resolve(modules, releases, []Selection{{ModuleSlug: "a", Version: "1.0.0"}}); err == nil {
		t.Fatal("expected cycle")
	}
}

func TestResolveConflict(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000020"), ModuleSlug: "a", Version: "1.0.0", Conflicts: []string{"b"}},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000021"), ModuleSlug: "b", Version: "1.0.0"},
	}
	if _, err := Resolve(modules, releases, []Selection{
		{ModuleSlug: "a", Version: "1.0.0"},
		{ModuleSlug: "b", Version: "1.0.0"},
	}); err == nil {
		t.Fatal("expected conflict")
	}
}

func TestOptionalDependencySkippedWhenMissing(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000030"), ModuleSlug: "a", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "missing", VersionConstraint: "1.0.0", Required: false}}},
	}
	plan, err := Resolve(modules, releases, []Selection{{ModuleSlug: "a", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Releases) != 1 {
		t.Fatalf("optional missing should not fail: %d", len(plan.Releases))
	}
}

func TestDeterministicPermutation(t *testing.T) {
	modules := []Module{{Slug: "core"}, {Slug: "auth"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), ModuleSlug: "core", Version: "1.0.0"},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000002"), ModuleSlug: "auth", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "core", VersionConstraint: "^1.0.0", Required: true}}},
	}
	p1, err := Resolve(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.0.0"}, {ModuleSlug: "core", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Resolve(modules, releases, []Selection{{ModuleSlug: "core", Version: "1.0.0"}, {ModuleSlug: "auth", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Releases) != len(p2.Releases) {
		t.Fatal("length")
	}
	for i := range p1.Releases {
		if p1.Releases[i] != p2.Releases[i] {
			t.Fatalf("non-deterministic: %v vs %v", p1.Releases, p2.Releases)
		}
	}
}

func TestPinStableWhenNewerReleaseExists(t *testing.T) {
	modules := []Module{{Slug: "core"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000030"), ModuleSlug: "core", Version: "1.0.0"},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000031"), ModuleSlug: "core", Version: "1.1.0"},
	}
	plan, err := Resolve(modules, releases, []Selection{{ModuleSlug: "core", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Releases[0].Version != "1.0.0" {
		t.Fatalf("pin floated: %s", plan.Releases[0].Version)
	}
}
