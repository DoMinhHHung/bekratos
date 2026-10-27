package module

import (
	"testing"

	"github.com/google/uuid"
)

func rel(slug, version string, deps []Dependency, conflicts []string) Release {
	return Release{
		ID:                 uuid.Must(uuid.NewV7()),
		ModuleSlug:         slug,
		Version:            version,
		RuntimeCompat:      ">=1.0.0",
		APIContractVersion: "1",
		Dependencies:       deps,
		Conflicts:          conflicts,
	}
}

func TestResolveAuthPullsCore(t *testing.T) {
	modules := []Module{{Slug: "core"}, {Slug: "auth"}}
	releases := []Release{
		rel("core", "1.0.0", nil, nil),
		rel("auth", "1.1.0", []Dependency{{ModuleSlug: "core", VersionConstraint: "^1.0.0", Required: true}}, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.1.0"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(plan.Releases))
	}
}

func TestResolve_Cycle(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}}
	releases := []Release{
		rel("a", "1.0.0", []Dependency{{ModuleSlug: "b", VersionConstraint: "^1.0.0", Required: true}}, nil),
		rel("b", "1.0.0", []Dependency{{ModuleSlug: "a", VersionConstraint: "^1.0.0", Required: true}}, nil),
	}
	_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestResolve_ConflictBacktrack(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}}
	releases := []Release{
		rel("a", "2.0.0", nil, []string{"b"}),
		rel("a", "1.0.0", nil, nil),
		rel("b", "1.0.0", nil, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}, {ModuleSlug: "b"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatalf("expected backtrack: %v", err)
	}
	by := map[string]string{}
	for _, r := range plan.Releases {
		by[r.ModuleSlug] = r.Version
	}
	if by["a"] != "1.0.0" {
		t.Fatalf("want a@1.0.0 got %v", by)
	}
}

func TestResolve_RuntimeCompatRequired(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	releases := []Release{{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "a", Version: "1.0.0",
		RuntimeCompat: ">=2.0.0", APIContractVersion: "1",
	}}
	_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err == nil {
		t.Fatal("expected runtime mismatch")
	}
}

func TestResolve_APIContractMismatch(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	releases := []Release{{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "a", Version: "1.0.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "2",
	}}
	_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err == nil {
		t.Fatal("expected api mismatch")
	}
}

func TestResolve_MissingCompatMetadata(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	releases := []Release{{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "a", Version: "1.0.0",
	}}
	_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err == nil {
		t.Fatal("expected missing metadata rejection")
	}
}

func TestResolve_OptionalAlreadySelectedIncompatible(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}}
	releases := []Release{
		rel("a", "1.0.0", []Dependency{{ModuleSlug: "b", VersionConstraint: "^2.0.0", Required: false}}, nil),
		rel("b", "1.0.0", nil, nil),
	}
	_, err := ResolveWithOptions(modules, releases, []Selection{
		{ModuleSlug: "a"}, {ModuleSlug: "b", Version: "1.0.0"},
	}, ResolveOptions{RuntimeVersion: "1.0.0", APIContract: "1"})
	if err == nil {
		t.Fatal("optional dependency already selected at incompatible version must fail")
	}
}

func TestResolve_PermutationDeterminism(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}, {Slug: "c"}}
	releases := []Release{
		rel("a", "1.0.0", []Dependency{{ModuleSlug: "c", VersionConstraint: "^1.0.0", Required: true}}, nil),
		rel("b", "1.0.0", nil, nil),
		rel("c", "1.0.0", nil, nil),
	}
	opts := ResolveOptions{RuntimeVersion: "1.0.0", APIContract: "1"}
	p1, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "b"}, {ModuleSlug: "a"}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}, {ModuleSlug: "b"}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Releases) != len(p2.Releases) {
		t.Fatal("len mismatch")
	}
	for i := range p1.Releases {
		if p1.Releases[i].ModuleSlug != p2.Releases[i].ModuleSlug || p1.Releases[i].Version != p2.Releases[i].Version {
			t.Fatalf("%+v vs %+v", p1.Releases, p2.Releases)
		}
	}
}
