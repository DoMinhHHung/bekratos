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
	if plan.Releases[0].ModuleSlug != "auth" && plan.Releases[1].ModuleSlug != "auth" {
		t.Fatal("missing auth")
	}
	order1, err := Resolve(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	order2, err := Resolve(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if order1.Releases[0].ModuleSlug != order2.Releases[0].ModuleSlug {
		t.Fatal("non-deterministic")
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

func TestResolveUnsatisfiable(t *testing.T) {
	modules := []Module{{Slug: "core"}, {Slug: "auth"}}
	releases := []Release{
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), ModuleSlug: "core", Version: "1.0.0"},
		{ID: uuid.MustParse("01900000-0000-7000-8000-000000000002"), ModuleSlug: "auth", Version: "1.0.0",
			Dependencies: []Dependency{{ModuleSlug: "core", VersionConstraint: "^2.0.0", Required: true}}},
	}
	if _, err := Resolve(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.0.0"}}); err == nil {
		t.Fatal("expected unsatisfiable")
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
