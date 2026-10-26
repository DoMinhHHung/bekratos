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
		if _, ok := seen[r.ModuleSlug][r.Version]; ok {
			return nil, fmt.Errorf("%w: %s@%s", ErrDuplicateRelease, r.ModuleSlug, r.Version)
		}
		seen[r.ModuleSlug][r.Version] = struct{}{}
		idx.releases[r.ModuleSlug] = append(idx.releases[r.ModuleSlug], r)
	}
	for slug := range idx.releases {
		sort.SliceStable(idx.releases[slug], func(i, j int) bool {
			vi, _ := ParseVersion(idx.releases[slug][i].Version)
			vj, _ := ParseVersion(idx.releases[slug][j].Version)
			return CompareVersions(vi, vj) > 0
		})
	}
	return idx, nil
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

func search(idx *catalogIndex, constraints map[string][]string, chosen map[string]Release, stack map[string]bool, opts ResolveOptions) error {
	var next string
	for slug := range constraints {
		if _, ok := chosen[slug]; !ok {
			if next == "" || slug < next {
				next = slug
			}
		}
	}
	if next == "" {
		return nil
	}
	if stack[next] {
		return fmt.Errorf("%w: %s", ErrCycle, next)
	}
	if _, ok := idx.modules[next]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownModule, next)
	}
	list := idx.releases[next]
	if len(list) == 0 {
		return fmt.Errorf("%w: %s", ErrMissingDependency, next)
	}

	cs, err := intersectConstraints(constraints[next])
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnsatisfiable, next)
	}

	candidates := make([]Release, 0, len(list))
	for _, rel := range list {
		v, err := ParseVersion(rel.Version)
		if err != nil {
			continue
		}
		if !cs.Check(v) {
			continue
		}
		if opts.RuntimeVersion != "" && rel.RuntimeCompat != "" {
			rc, err := ParseConstraint(rel.RuntimeCompat)
			if err != nil {
				continue
			}
			rv, err := ParseVersion(opts.RuntimeVersion)
			if err != nil || !rc.Check(rv) {
				continue
			}
		}
		if opts.APIContract != "" && rel.APIContractVersion != "" && rel.APIContractVersion != opts.APIContract {
			continue
		}
		candidates = append(candidates, rel)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("%w: %s", ErrUnsatisfiable, next)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		vi, _ := ParseVersion(candidates[i].Version)
		vj, _ := ParseVersion(candidates[j].Version)
		return CompareVersions(vi, vj) > 0
	})

	var lastErr error
	stack[next] = true
	for _, cand := range candidates {
		childConstraints := cloneConstraints(constraints)
		ok := true
		for _, dep := range cand.Dependencies {
			if !dep.Required {
				continue
			}
			if _, err := ParseConstraint(dep.VersionConstraint); err != nil {
				ok = false
				lastErr = fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
				break
			}
			if stack[dep.ModuleSlug] {
				ok = false
				lastErr = fmt.Errorf("%w: %s", ErrCycle, dep.ModuleSlug)
				break
			}
			if existing, exists := chosen[dep.ModuleSlug]; exists {
				ev, err := ParseVersion(existing.Version)
				if err != nil {
					ok = false
					lastErr = fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
					break
				}
				dc, _ := ParseConstraint(dep.VersionConstraint)
				if !dc.Check(ev) {
					ok = false
					lastErr = fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
					break
				}
				continue
			}
			childConstraints[dep.ModuleSlug] = append(childConstraints[dep.ModuleSlug], dep.VersionConstraint)
		}
		if !ok {
			continue
		}
		chosen[next] = cand
		if err := search(idx, childConstraints, chosen, stack, opts); err != nil {
			delete(chosen, next)
			lastErr = err
			continue
		}
		for _, dep := range cand.Dependencies {
			if dep.Required {
				continue
			}
			if _, already := chosen[dep.ModuleSlug]; already {
				continue
			}
			if _, err := ParseConstraint(dep.VersionConstraint); err != nil {
				continue
			}
			optConstraints := cloneConstraints(childConstraints)
			optConstraints[dep.ModuleSlug] = append(optConstraints[dep.ModuleSlug], dep.VersionConstraint)
			optChosen := cloneChosen(chosen)
			optStack := cloneStack(stack)
			if err := search(idx, optConstraints, optChosen, optStack, opts); err == nil {
				for k, v := range optChosen {
					chosen[k] = v
				}
			}
		}
		delete(stack, next)
		return nil
	}
	delete(stack, next)
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("%w: %s", ErrUnsatisfiable, next)
}

func intersectConstraints(raw []string) (*semver.Constraints, error) {
	if len(raw) == 0 {
		return semver.NewConstraint(">=0.0.0")
	}
	joined := raw[0]
	for i := 1; i < len(raw); i++ {
		joined = joined + ", " + raw[i]
	}
	return ParseConstraint(joined)
}

func cloneConstraints(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

func cloneChosen(in map[string]Release) map[string]Release {
	out := make(map[string]Release, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStack(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
