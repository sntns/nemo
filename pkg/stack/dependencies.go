package stack

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sntns/nemo/pkg/cnab"
)

// Order sorts the bundles so that each comes after the bundles of the set
// it depends on, with io.cnab.dependencies; otherwise their order is kept.
// A dependency on a bundle outside the set does not order anything:
// CheckDependencies reports it.
func Order(bundles []cnab.ExtendedBundle) ([]cnab.ExtendedBundle, error) {
	var ordered []cnab.ExtendedBundle
	placed := map[string]bool{}
	remaining := bundles
	for len(remaining) > 0 {
		var rest []cnab.ExtendedBundle
		progressed := false
		for _, bundle := range remaining {
			ready := true
			for _, name := range DependencyNames(bundle) {
				inSet := slices.ContainsFunc(bundles, func(other cnab.ExtendedBundle) bool { return other.Name == name })
				if inSet && !placed[name] {
					ready = false
				}
			}
			if ready {
				ordered = append(ordered, bundle)
				placed[bundle.Name] = true
				progressed = true
			} else {
				rest = append(rest, bundle)
			}
		}
		if !progressed {
			names := make([]string, len(rest))
			for i, bundle := range rest {
				names[i] = bundle.Name
			}
			return nil, fmt.Errorf("the dependencies of %s are a cycle", strings.Join(names, ", "))
		}
		remaining = rest
	}
	return ordered, nil
}

// Requirement is a dependency of a bundle, its dependant.
type Requirement struct {
	Dependant  cnab.ExtendedBundle
	Dependency cnab.Dependency
}

// Missing returns the dependencies no bundle of the set satisfies by name,
// each once, with the first bundle that requires it: the stack does not
// list them.
func Missing(bundles []cnab.ExtendedBundle) []Requirement {
	var missing []Requirement
	for _, bundle := range bundles {
		if bundle.Dependencies == nil {
			continue
		}
		for _, dependency := range bundle.Dependencies.ListBySequence() {
			name, err := dependency.BundleName()
			if err != nil || slices.ContainsFunc(bundles, func(other cnab.ExtendedBundle) bool { return other.Name == name }) {
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
// one whose version is outside the dependency's ranges.
func CheckDependencies(bundles []cnab.ExtendedBundle) error {
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
