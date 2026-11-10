package stack

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sntns/nemo/pkg/cnab"
)

// Order sorts the bundles by level: a bundle comes one level after the
// deepest of the bundles of the set it depends on, with io.cnab.dependencies,
// and those of a level come by name. A dependency on a bundle outside the set
// does not order anything: CheckDependencies reports it.
func Order(bundles []cnab.ExtendedBundle) ([]cnab.ExtendedBundle, error) {
	inSet := map[string]bool{}
	for _, bundle := range bundles {
		inSet[bundle.Name] = true
	}
	level := map[string]int{}
	remaining := bundles
	for len(remaining) > 0 {
		var rest []cnab.ExtendedBundle
		placed := map[string]int{}
		for _, bundle := range remaining {
			depth, ready := 0, true
			for _, name := range DependencyNames(bundle) {
				dependency, known := level[name]
				switch {
				case !inSet[name]:
				case !known:
					ready = false
				default:
					depth = max(depth, dependency+1)
				}
			}
			if ready {
				placed[bundle.Name] = depth
			} else {
				rest = append(rest, bundle)
			}
		}
		if len(placed) == 0 {
			names := make([]string, len(rest))
			for i, bundle := range rest {
				names[i] = bundle.Name
			}
			return nil, fmt.Errorf("the dependencies of %s are a cycle", strings.Join(names, ", "))
		}
		maps.Copy(level, placed)
		remaining = rest
	}
	ordered := slices.Clone(bundles)
	slices.SortStableFunc(ordered, func(left, right cnab.ExtendedBundle) int {
		return cmp.Or(cmp.Compare(level[left.Name], level[right.Name]), cmp.Compare(left.Name, right.Name))
	})
	return ordered, nil
}

// Requirement is a dependency of a bundle, its dependant.
type Requirement struct {
	Dependant  cnab.ExtendedBundle
	Dependency cnab.Dependency
}

// Missing returns the dependencies no bundle of the set satisfies by name,
// nor an external installation, each once, with the first bundle that
// requires it: the stack does not list them.
func Missing(bundles []cnab.ExtendedBundle, external map[string]bool) []Requirement {
	var missing []Requirement
	for _, bundle := range bundles {
		if bundle.Dependencies == nil {
			continue
		}
		for _, dependency := range bundle.Dependencies.ListBySequence() {
			name, err := dependency.BundleName()
			if err != nil || external[name] || slices.ContainsFunc(bundles, func(other cnab.ExtendedBundle) bool { return other.Name == name }) {
				continue
			}
			if !slices.ContainsFunc(missing, func(other Requirement) bool { return other.Dependency.Bundle == dependency.Bundle }) {
				missing = append(missing, Requirement{Dependant: bundle, Dependency: dependency})
			}
		}
	}
	return missing
}

// CheckDependencies reports each dependency the set does not satisfy: one
// whose reference is not REGISTRY/NAME, a bundle the set does not have, or
// one whose version is outside the dependency's ranges. An external
// installation satisfies a dependency on its name, whatever its ranges: it
// has no bundle, nor version.
func CheckDependencies(bundles []cnab.ExtendedBundle, external map[string]bool) error {
	var errs []error
	for _, bundle := range bundles {
		if bundle.Dependencies == nil {
			continue
		}
		for _, dependency := range bundle.Dependencies.ListBySequence() {
			name, err := dependency.BundleName()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", bundle.Name, err))
				continue
			}
			if external[name] {
				continue
			}
			index := slices.IndexFunc(bundles, func(other cnab.ExtendedBundle) bool { return other.Name == name })
			if index < 0 {
				errs = append(errs, fmt.Errorf("%s depends on %s, which the stack does not have", bundle.Name, dependency.Bundle))
				continue
			}
			if err := dependency.Allows(bundles[index].Version); err != nil {
				errs = append(errs, fmt.Errorf("%s depends on %s: %w", bundle.Name, dependency.Bundle, err))
			}
		}
	}
	return errors.Join(errs...)
}

// DependencyNames are the names of the bundles the bundle depends on.
func DependencyNames(bundle cnab.ExtendedBundle) []string {
	if bundle.Dependencies == nil {
		return nil
	}
	var names []string
	for _, dependency := range bundle.Dependencies.ListBySequence() {
		if name, err := dependency.BundleName(); err == nil {
			names = append(names, name)
		}
	}
	return names
}
