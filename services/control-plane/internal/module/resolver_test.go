package module

import (
	"errors"
	"fmt"
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

func planVersions(plan ResolutionPlan) map[string]string {
	by := map[string]string{}
	for _, r := range plan.Releases {
		by[r.ModuleSlug] = r.Version
	}
	return by
}

func TestResolve_RejectedCandidateDoesNotLeakOptionalGraph(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}, {Slug: "c"}}
	releases := []Release{
		rel("b", "1.0.0", nil, nil),
		rel("b", "2.0.0", nil, nil),
		rel("c", "1.0.0", []Dependency{{ModuleSlug: "b", VersionConstraint: "<2.0.0", Required: true}}, nil),
		rel("a", "2.0.0", []Dependency{
			{ModuleSlug: "c", VersionConstraint: "^1.0.0", Required: true},
			{ModuleSlug: "b", VersionConstraint: "^2.0.0", Required: false},
		}, nil),
		rel("a", "1.0.0", nil, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatalf("expected backtrack to a@1.0.0: %v", err)
	}
	by := planVersions(plan)
	if len(plan.Releases) != 1 || by["a"] != "1.0.0" {
		t.Fatalf("want only a@1.0.0, got %+v", plan.Releases)
	}
	if _, ok := by["b"]; ok {
		t.Fatalf("stale b leaked: %+v", plan.Releases)
	}
	if _, ok := by["c"]; ok {
		t.Fatalf("stale c leaked: %+v", plan.Releases)
	}
}

func TestResolve_OptionalFailureAfterOptionalSuccessOmitsLater(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "d"}, {Slug: "e"}}
	releases := []Release{
		rel("d", "1.0.0", nil, nil),
		rel("a", "1.0.0", []Dependency{
			{ModuleSlug: "d", VersionConstraint: "^1.0.0", Required: false},
			{ModuleSlug: "e", VersionConstraint: "^1.0.0", Required: false},
		}, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	by := planVersions(plan)
	if by["a"] != "1.0.0" || by["d"] != "1.0.0" {
		t.Fatalf("want a and d, got %+v", plan.Releases)
	}
	if _, ok := by["e"]; ok {
		t.Fatalf("unresolvable optional e must be omitted: %+v", plan.Releases)
	}
}

func TestResolve_BacktrackAfterMultipleRequiredSelections(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}, {Slug: "c"}, {Slug: "d"}}
	releases := []Release{
		rel("b", "1.0.0", []Dependency{{ModuleSlug: "d", VersionConstraint: "^1.0.0", Required: true}}, nil),
		rel("c", "1.0.0", nil, nil),
		rel("d", "1.0.0", nil, nil),
		rel("a", "2.0.0", []Dependency{
			{ModuleSlug: "b", VersionConstraint: "^1.0.0", Required: true},
			{ModuleSlug: "c", VersionConstraint: "^1.0.0", Required: true},
			{ModuleSlug: "d", VersionConstraint: "^2.0.0", Required: false},
		}, nil),
		rel("a", "1.0.0", nil, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	by := planVersions(plan)
	if len(plan.Releases) != 1 || by["a"] != "1.0.0" {
		t.Fatalf("want only a@1.0.0 after required-graph backtrack, got %+v", plan.Releases)
	}
}

func TestResolve_OptionalConflictOmitsOptional(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}, {Slug: "x"}}
	releases := []Release{
		rel("x", "1.0.0", nil, nil),
		rel("b", "1.0.0", nil, []string{"x"}),
		rel("a", "1.0.0", []Dependency{{ModuleSlug: "b", VersionConstraint: "^1.0.0", Required: false}}, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}, {ModuleSlug: "x"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	by := planVersions(plan)
	if by["a"] != "1.0.0" || by["x"] != "1.0.0" {
		t.Fatalf("want a and x, got %+v", plan.Releases)
	}
	if _, ok := by["b"]; ok {
		t.Fatalf("conflicting optional b must be omitted: %+v", plan.Releases)
	}
}

func TestResolve_UnsatisfiableCandidateDoesNotLeakIntoLaterPlan(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}, {Slug: "c"}}
	releases := []Release{
		rel("c", "1.0.0", nil, nil),
		rel("b", "1.0.0", []Dependency{{ModuleSlug: "c", VersionConstraint: "^9.0.0", Required: true}}, nil),
		rel("a", "2.0.0", []Dependency{{ModuleSlug: "b", VersionConstraint: "^1.0.0", Required: true}}, nil),
		rel("a", "1.0.0", nil, nil),
	}
	plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	by := planVersions(plan)
	if len(plan.Releases) != 1 || by["a"] != "1.0.0" {
		t.Fatalf("want only a@1.0.0, got %+v", plan.Releases)
	}
}

func TestResolve_ReleaseInputOrderDoesNotChangeResult(t *testing.T) {
	modules := []Module{{Slug: "a"}, {Slug: "b"}}
	base := []Release{
		rel("b", "1.0.0", nil, nil),
		rel("b", "1.1.0", nil, nil),
		rel("a", "1.0.0", []Dependency{{ModuleSlug: "b", VersionConstraint: "^1.0.0", Required: true}}, nil),
	}
	opts := ResolveOptions{RuntimeVersion: "1.0.0", APIContract: "1"}
	orders := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}}
	var first ResolutionPlan
	for i, ord := range orders {
		releases := make([]Release, len(base))
		for j, idx := range ord {
			releases[j] = base[idx]
		}
		plan, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = plan
			continue
		}
		if len(plan.Releases) != len(first.Releases) {
			t.Fatalf("order %d length mismatch", i)
		}
		for j := range plan.Releases {
			if plan.Releases[j].ModuleSlug != first.Releases[j].ModuleSlug || plan.Releases[j].Version != first.Releases[j].Version {
				t.Fatalf("order %d %+v vs %+v", i, plan.Releases, first.Releases)
			}
		}
	}
	by := planVersions(first)
	if by["a"] != "1.0.0" || by["b"] != "1.1.0" {
		t.Fatalf("want newest compatible b, got %+v", first.Releases)
	}
}

func TestResolve_SearchBudgetExhausted(t *testing.T) {
	n := 6
	modules := make([]Module, 0, n)
	releases := make([]Release, 0, n*3)
	for i := 0; i < n; i++ {
		slug := string(rune('a' + i))
		modules = append(modules, Module{Slug: slug})
		for v := 1; v <= 3; v++ {
			var deps []Dependency
			if i+1 < n {
				deps = []Dependency{{ModuleSlug: string(rune('a' + i + 1)), VersionConstraint: ">=1.0.0", Required: true}}
			} else {
				deps = []Dependency{{ModuleSlug: "missing-leaf", VersionConstraint: "^1.0.0", Required: true}}
			}
			releases = append(releases, rel(slug, fmt.Sprintf("%d.0.0", v), deps, nil))
		}
	}
	modules = append(modules, Module{Slug: "missing-leaf"})
	plan, err := resolveWithBudget(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	}, 8)
	if !errors.Is(err, ErrSearchBudget) {
		t.Fatalf("want ErrSearchBudget, got %v", err)
	}
	if len(plan.Releases) != 0 {
		t.Fatalf("budget exhaustion returned partial plan: %+v", plan.Releases)
	}
}

func TestResolve_SearchBudgetAllowsSmallGraph(t *testing.T) {
	modules := []Module{{Slug: "core"}, {Slug: "auth"}}
	releases := []Release{
		rel("core", "1.0.0", nil, nil),
		rel("auth", "1.1.0", []Dependency{{ModuleSlug: "core", VersionConstraint: "^1.0.0", Required: true}}, nil),
	}
	plan, err := resolveWithBudget(modules, releases, []Selection{{ModuleSlug: "auth", Version: "1.1.0"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	}, 8)
	if err != nil {
		t.Fatal(err)
	}
	by := planVersions(plan)
	if by["auth"] != "1.1.0" || by["core"] != "1.0.0" {
		t.Fatalf("got %+v", plan.Releases)
	}
}

func TestResolve_DuplicateCanonicalVersionRejected(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	releases := []Release{
		rel("a", "1.0.0", nil, nil),
		rel("a", "1.0.0", nil, nil),
	}
	_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if !errors.Is(err, ErrDuplicateRelease) {
		t.Fatalf("want duplicate, got %v", err)
	}
}

func TestResolve_LeadingVRejected(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	releases := []Release{rel("a", "v1.0.0", nil, nil)}
	_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if !errors.Is(err, ErrUnknownRelease) {
		t.Fatalf("want unknown/invalid version, got %v", err)
	}
}

func TestResolve_BuildMetadataDoesNotChangePrecedence(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	low := rel("a", "1.0.0+zzz", nil, nil)
	high := rel("a", "1.0.1+aaa", nil, nil)
	plan, err := ResolveWithOptions(modules, []Release{low, high}, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
		RuntimeVersion: "1.0.0", APIContract: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Releases[0].Version != "1.0.1+aaa" {
		t.Fatalf("semantic precedence must ignore build metadata, got %s", plan.Releases[0].Version)
	}
}

func TestResolve_EqualPrecedenceBuildMetadataRejectedAsDuplicate(t *testing.T) {
	modules := []Module{{Slug: "a"}}
	first := rel("a", "1.0.0+aaa", nil, nil)
	second := rel("a", "1.0.0+bbb", nil, nil)
	for _, releases := range [][]Release{{first, second}, {second, first}} {
		_, err := ResolveWithOptions(modules, releases, []Selection{{ModuleSlug: "a"}}, ResolveOptions{
			RuntimeVersion: "1.0.0", APIContract: "1",
		})
		if !errors.Is(err, ErrDuplicateRelease) {
			t.Fatalf("equal-precedence releases with different build metadata must be duplicate, got %v", err)
		}
	}
}

func TestProductionResolveOptionsRejectIncompatible(t *testing.T) {
	opts := productionResolveOptions()
	if opts.RuntimeVersion != platformRuntimeVersion || opts.APIContract != platformAPIContract {
		t.Fatalf("production options mismatch: %+v", opts)
	}
	modules := []Module{{Slug: "a"}}
	runtimeMismatch := []Release{{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "a", Version: "1.0.0",
		RuntimeCompat: ">=9.0.0", APIContractVersion: platformAPIContract,
	}}
	if _, err := ResolveWithOptions(modules, runtimeMismatch, []Selection{{ModuleSlug: "a"}}, opts); err == nil {
		t.Fatal("runtime mismatch must fail on production options")
	}
	apiMismatch := []Release{{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "a", Version: "1.0.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "9",
	}}
	if _, err := ResolveWithOptions(modules, apiMismatch, []Selection{{ModuleSlug: "a"}}, opts); err == nil {
		t.Fatal("api contract mismatch must fail on production options")
	}
	ok := []Release{rel("a", "1.0.0", nil, nil)}
	plan, err := ResolveWithOptions(modules, ok, []Selection{{ModuleSlug: "a"}}, opts)
	if err != nil || plan.Releases[0].Version != "1.0.0" {
		t.Fatalf("compatible production plan: %+v %v", plan.Releases, err)
	}
}
