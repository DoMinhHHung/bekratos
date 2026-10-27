package module

import (
	"errors"
	"fmt"
	"sort"
	"strings"

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
	ErrSearchBudget      = errors.New("search budget exceeded")
)

const (
	maxSearchDepth = 64
	maxSearchNodes = 2048
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
		parsed, err := ParseVersion(r.Version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s@%s", ErrUnknownRelease, r.ModuleSlug, r.Version)
		}
		if seen[r.ModuleSlug] == nil {
			seen[r.ModuleSlug] = make(map[string]struct{})
		}
		key := strings.SplitN(parsed.String(), "+", 2)[0]
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
	return resolveWithBudget(modules, releases, selections, opts, maxSearchNodes)
}

func resolveWithBudget(modules []Module, releases []Release, selections []Selection, opts ResolveOptions, maxNodes int) (ResolutionPlan, error) {
	idx, err := buildIndex(modules, releases)
	if err != nil {
		return ResolutionPlan{}, err
	}
	selected := append([]Selection(nil), selections...)
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].ModuleSlug < selected[j].ModuleSlug
	})

	constraints := make(map[string][]string)
	for _, sel := range selected {
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

	budget := maxNodes
	chosen, err := search(idx, constraints, map[string]Release{}, map[string]bool{}, opts, 0, &budget)
	if err != nil {
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

func search(
	idx *catalogIndex,
	constraints map[string][]string,
	chosen map[string]Release,
	stack map[string]bool,
	opts ResolveOptions,
	depth int,
	budget *int,
) (map[string]Release, error) {
	var next string
	for slug := range constraints {
		if _, ok := chosen[slug]; !ok {
			if next == "" || slug < next {
				next = slug
			}
		}
	}
	if next == "" {
		return cloneChosen(chosen), nil
	}
	if depth > maxSearchDepth {
		return nil, fmt.Errorf("%w: search depth exceeded", ErrUnsatisfiable)
	}
	if stack[next] {
		return nil, fmt.Errorf("%w: %s", ErrCycle, next)
	}
	if _, ok := idx.modules[next]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownModule, next)
	}
	list := idx.releases[next]
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrMissingDependency, next)
	}

	cs, err := intersectConstraints(constraints[next])
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsatisfiable, next)
	}

	candidates := make([]Release, 0, len(list))
	for _, rel := range list {
		if budget == nil || *budget <= 0 {
			return nil, ErrSearchBudget
		}
		*budget--
		if !candidateFits(rel, next, cs, chosen, opts) {
			continue
		}
		candidates = append(candidates, rel)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsatisfiable, next)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return releaseLess(candidates[j], candidates[i])
	})

	var lastErr error
	for _, cand := range candidates {
		branchConstraints := cloneConstraints(constraints)
		branchChosen := cloneChosen(chosen)
		branchStack := cloneStack(stack)
		branchStack[next] = true
		ok := true
		for _, dep := range cand.Dependencies {
			if !dep.Required {
				continue
			}
			if err := applyRequiredDependency(dep, branchChosen, branchStack, branchConstraints); err != nil {
				ok = false
				lastErr = err
				break
			}
		}
		if !ok {
			continue
		}
		branchChosen[next] = cand
		resolved, err := search(idx, branchConstraints, branchChosen, branchStack, opts, depth+1, budget)
		if err != nil {
			lastErr = err
			if errors.Is(err, ErrSearchBudget) {
				return nil, err
			}
			continue
		}
		resolved, err = applyOptionalDependencies(idx, cand, resolved, branchConstraints, branchStack, opts, depth, budget)
		if err != nil {
			lastErr = err
			if errors.Is(err, ErrSearchBudget) {
				return nil, err
			}
			continue
		}
		return resolved, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsatisfiable, next)
}

func candidateFits(rel Release, next string, cs *semver.Constraints, chosen map[string]Release, opts ResolveOptions) bool {
	v, err := ParseVersion(rel.Version)
	if err != nil {
		return false
	}
	if !cs.Check(v) {
		return false
	}
	if rel.RuntimeCompat == "" {
		return false
	}
	if _, err := ParseConstraint(rel.RuntimeCompat); err != nil {
		return false
	}
	if rel.APIContractVersion == "" {
		return false
	}
	if opts.RuntimeVersion != "" {
		rc, err := ParseConstraint(rel.RuntimeCompat)
		if err != nil {
			return false
		}
		rv, err := ParseVersion(opts.RuntimeVersion)
		if err != nil || !rc.Check(rv) {
			return false
		}
	}
	if opts.APIContract != "" && rel.APIContractVersion != opts.APIContract {
		return false
	}
	for _, c := range rel.Conflicts {
		if _, ok := chosen[c]; ok {
			return false
		}
	}
	for _, existing := range chosen {
		for _, c := range existing.Conflicts {
			if c == next {
				return false
			}
		}
	}
	return true
}

func applyRequiredDependency(dep Dependency, chosen map[string]Release, stack map[string]bool, constraints map[string][]string) error {
	if _, err := ParseConstraint(dep.VersionConstraint); err != nil {
		return fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
	}
	if stack[dep.ModuleSlug] {
		return fmt.Errorf("%w: %s", ErrCycle, dep.ModuleSlug)
	}
	if existing, exists := chosen[dep.ModuleSlug]; exists {
		ev, err := ParseVersion(existing.Version)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
		}
		dc, err := ParseConstraint(dep.VersionConstraint)
		if err != nil || !dc.Check(ev) {
			return fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
		}
		return nil
	}
	constraints[dep.ModuleSlug] = append(constraints[dep.ModuleSlug], dep.VersionConstraint)
	return nil
}

func applyOptionalDependencies(
	idx *catalogIndex,
	cand Release,
	resolved map[string]Release,
	baseConstraints map[string][]string,
	stack map[string]bool,
	opts ResolveOptions,
	depth int,
	budget *int,
) (map[string]Release, error) {
	current := resolved
	currentConstraints := cloneConstraints(baseConstraints)
	for _, dep := range cand.Dependencies {
		if dep.Required {
			continue
		}
		if stack[dep.ModuleSlug] {
			continue
		}
		if existing, already := current[dep.ModuleSlug]; already {
			ev, err := ParseVersion(existing.Version)
			if err != nil {
				return nil, fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
			}
			dc, err := ParseConstraint(dep.VersionConstraint)
			if err != nil || !dc.Check(ev) {
				return nil, fmt.Errorf("%w: %s", ErrUnsatisfiable, dep.ModuleSlug)
			}
			continue
		}
		if _, err := ParseConstraint(dep.VersionConstraint); err != nil {
			continue
		}
		optConstraints := cloneConstraints(currentConstraints)
		optConstraints[dep.ModuleSlug] = append(optConstraints[dep.ModuleSlug], dep.VersionConstraint)
		optChosen := cloneChosen(current)
		optStack := cloneStack(stack)
		optResolved, err := search(idx, optConstraints, optChosen, optStack, opts, depth+1, budget)
		if err == nil {
			current = optResolved
			currentConstraints = optConstraints
			continue
		}
		if errors.Is(err, ErrSearchBudget) {
			return nil, err
		}
	}
	return current, nil
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
