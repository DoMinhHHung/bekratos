package module

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Masterminds/semver/v3"
)

var (
	ErrCycle             = errors.New("dependency cycle")
	ErrMissingDependency = errors.New("missing dependency")
	ErrConflict          = errors.New("module conflict")
	ErrUnsatisfiable     = errors.New("unsatisfiable constraint")
	ErrUnknownModule     = errors.New("unknown module")
	ErrUnknownRelease    = errors.New("unknown release")
	ErrDuplicateRelease  = errors.New("duplicate release")
	ErrIncompatible      = errors.New("incompatible release")
)

type ResolveOptions struct {
	RuntimeVersion string
	APIContract    string
}

type catalogIndex struct {
	modules  map[string]Module
	releases map[string][]Release
}

func buildIndex(modules []Module, releases []Release) (*catalogIndex, error) {
	idx := &catalogIndex{
		modules:  make(map[string]Module, len(modules)),
		releases: make(map[string][]Release),
	}
	for _, m := range modules {
		idx.modules[m.Slug] = m
	}
	seen := make(map[string]map[string]struct{})
	for _, r := range releases {
		if _, err := ParseVersion(r.Version); err != nil {
			return nil, fmt.Errorf("%w: %s@%s", ErrUnknownRelease, r.ModuleSlug, r.Version)
		}
		if seen[r.ModuleSlug] == nil {
			seen[r.ModuleSlug] = make(map[string]struct{})
		}
		key := r.Version
		if _, ok := seen[r.ModuleSlug][key]; ok {
			return nil, fmt.Errorf("%w: %s@%s", ErrDuplicateRelease, r.ModuleSlug, r.Version)
		}
		seen[r.ModuleSlug][key] = struct{}{}
		idx.releases[r.ModuleSlug] = append(idx.releases[r.ModuleSlug], r)
	}
	for slug := range idx.releases {
		sort.SliceStable(idx.releases[slug], func(i, j int) bool {
			return releaseLess(idx.releases[slug][j], idx.releases[slug][i])
		})
	}
	return idx, nil
}

func releaseLess(a, b Release) bool {
	va, errA := ParseVersion(a.Version)
	vb, errB := ParseVersion(b.Version)
	if errA != nil || errB != nil {
		return a.ID.String() < b.ID.String()
	}
	c := CompareVersions(va, vb)
	if c != 0 {
		return c < 0
	}
	return a.ID.String() < b.ID.String()
}

func Resolve(modules []Module, releases []Release, selections []Selection) (ResolutionPlan, error) {
	return ResolveWithOptions(modules, releases, selections, ResolveOptions{})
}

func ResolveWithOptions(modules []Module, releases []Release, selections []Selection, opts ResolveOptions) (ResolutionPlan, error) {
	idx, err := buildIndex(modules, releases)
	if err != nil {
		return ResolutionPlan{}, err
	}
	sort.SliceStable(selections, func(i, j int) bool {
		return selections[i].ModuleSlug < selections[j].ModuleSlug
	})

	constraints := make(map[string][]string)
	for _, sel := range selections {
		if sel.ModuleSlug == "" {
			return ResolutionPlan{}, ErrUnknownModule
		}
		c := sel.Version
		if c == "" {
			c = ">=0.0.0"
		}
		if _, err := ParseConstraint(c); err != nil {
			return ResolutionPlan{}, fmt.Errorf("%w: %s", ErrUnsatisfiable, sel.ModuleSlug)
		}
		constraints[sel.ModuleSlug] = append(constraints[sel.ModuleSlug], c)
	}

	chosen := make(map[string]Release)
	stack := make(map[string]bool)
	if err := search(idx, constraints, chosen, stack, opts); err != nil {
		return ResolutionPlan{}, err
	}

	slugs := make([]string, 0, len(chosen))
	for slug := range chosen {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	plan := ResolutionPlan{Releases: make([]ResolvedRelease, 0, len(slugs))}
	for _, slug := range slugs {
		rel := chosen[slug]
		plan.Releases = append(plan.Releases, ResolvedRelease{
			ModuleSlug: slug,
			Version:    rel.Version,
			ReleaseID:  rel.ID.String(),
		})
	}
	return plan, nil
}
