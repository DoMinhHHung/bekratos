package module

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrCycle             = errors.New("dependency cycle")
	ErrMissingDependency = errors.New("missing dependency")
	ErrConflict          = errors.New("module conflict")
	ErrUnsatisfiable     = errors.New("unsatisfiable constraint")
	ErrUnknownModule     = errors.New("unknown module")
	ErrUnknownRelease    = errors.New("unknown release")
)

type catalogIndex struct {
	modules  map[string]Module
	releases map[string][]Release
}

func buildIndex(modules []Module, releases []Release) *catalogIndex {
	idx := &catalogIndex{
		modules:  make(map[string]Module, len(modules)),
		releases: make(map[string][]Release),
	}
	for _, m := range modules {
		idx.modules[m.Slug] = m
	}
	for _, r := range releases {
		idx.releases[r.ModuleSlug] = append(idx.releases[r.ModuleSlug], r)
	}
	for slug := range idx.releases {
		sort.Slice(idx.releases[slug], func(i, j int) bool {
			vi, _ := ParseVersion(idx.releases[slug][i].Version)
			vj, _ := ParseVersion(idx.releases[slug][j].Version)
			return Compare(vi, vj) > 0
		})
	}
	return idx
}

func Resolve(modules []Module, releases []Release, selections []Selection) (ResolutionPlan, error) {
	idx := buildIndex(modules, releases)
	sort.Slice(selections, func(i, j int) bool {
		return selections[i].ModuleSlug < selections[j].ModuleSlug
	})

	chosen := make(map[string]Release)
	visiting := make(map[string]bool)
	visited := make(map[string]bool)

	var visit func(slug, constraint string) error
	visit = func(slug, constraint string) error {
		if visiting[slug] {
			return fmt.Errorf("%w: %s", ErrCycle, slug)
		}
		if visited[slug] {
			existing := chosen[slug]
			if constraint != "" {
				c, err := ParseConstraint(constraint)
				if err != nil {
					return ErrUnsatisfiable
				}
				v, err := ParseVersion(existing.Version)
				if err != nil || !c.SatisfiedBy(v) {
					return fmt.Errorf("%w: %s@%s vs %s", ErrUnsatisfiable, slug, existing.Version, constraint)
				}
			}
			return nil
		}
		visiting[slug] = true

		if _, ok := idx.modules[slug]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownModule, slug)
		}
		list := idx.releases[slug]
		if len(list) == 0 {
			return fmt.Errorf("%w: %s", ErrMissingDependency, slug)
		}

		var selected *Release
		if constraint != "" {
			c, err := ParseConstraint(constraint)
			if err != nil {
				return ErrUnsatisfiable
			}
			for i := range list {
				v, err := ParseVersion(list[i].Version)
				if err != nil {
					continue
				}
				if c.SatisfiedBy(v) {
					selected = &list[i]
					break
				}
			}
			if selected == nil {
				return fmt.Errorf("%w: %s %s", ErrUnsatisfiable, slug, constraint)
			}
		} else {
			selected = &list[0]
		}

		for _, dep := range selected.Dependencies {
			if !dep.Required {
				continue
			}
			if err := visit(dep.ModuleSlug, dep.VersionConstraint); err != nil {
				return err
			}
		}

		chosen[slug] = *selected
		visiting[slug] = false
		visited[slug] = true
		return nil
	}

	for _, sel := range selections {
		constraint := sel.Version
		if err := visit(sel.ModuleSlug, constraint); err != nil {
			return ResolutionPlan{}, err
		}
	}

	slugs := make([]string, 0, len(chosen))
	for slug := range chosen {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	for _, slug := range slugs {
		rel := chosen[slug]
		for _, conflict := range rel.Conflicts {
			if _, ok := chosen[conflict]; ok {
				return ResolutionPlan{}, fmt.Errorf("%w: %s vs %s", ErrConflict, slug, conflict)
			}
		}
	}

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
