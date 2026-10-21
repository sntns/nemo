package cnab

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// DependenciesExtension declares the other bundles a bundle needs, with
// semver ranges. The runtime orders a stack's installations with it.
// https://github.com/cnabio/cnab-spec/blob/main/500-CNAB-dependencies.md
var DependenciesExtension = Extension[Dependencies]{
	Key:       OfficialExtensionsPrefix + "dependencies",
	Shorthand: "dependencies",
	Schema:    "io.cnab.dependencies.schema.json",
}

// Dependencies describes the set of custom extension metadata associated with the dependencies spec
type Dependencies struct {
	// Sequence is a list to order the dependencies
	Sequence []string `json:"sequence,omitempty"`

	// Requires is a list of bundles required by this bundle
	Requires map[string]Dependency `json:"requires,omitempty"`
}

// ListBySequence returns the dependencies by the defined sequence,
// if none is specified, they are sorted by name.
func (dependencies Dependencies) ListBySequence() []Dependency {
	ordered := make([]Dependency, 0, len(dependencies.Requires))
	if len(dependencies.Sequence) > 0 && len(dependencies.Sequence) == len(dependencies.Requires) {
		for _, name := range dependencies.Sequence {
			dependency := dependencies.Requires[name]
			dependency.Name = name
			ordered = append(ordered, dependency)
		}
	} else {
		for _, name := range slices.Sorted(maps.Keys(dependencies.Requires)) {
			dependency := dependencies.Requires[name]
			dependency.Name = name
			ordered = append(ordered, dependency)
		}
	}
	return ordered
}

// Dependency describes a dependency on another bundle
type Dependency struct {
	// Name of the dependency
	// This is used internally but isn't persisted to bundle.json
	Name string `json:"-"`

	// Bundle is the location of the bundle in a registry, for example REGISTRY/NAME:TAG
	Bundle string `json:"bundle"`

	// Version is a set of allowed versions
	Version *DependencyVersion `json:"version,omitempty"`
}

// DependencyVersion is a set of allowed versions for a dependency
type DependencyVersion struct {
	// Ranges of semantic versions, with or without the leading v prefix, allowed by the dependency
	Ranges []string `json:"ranges,omitempty"`

	// AllowPrereleases specifies if prerelease versions can satisfy the dependency
	AllowPrereleases bool `json:"prereleases,omitempty"`
}

// BundleName is the name of the bundle the dependency is on: the last part
// of its repository, under which a bundle is pushed.
func (dependency Dependency) BundleName() (string, error) {
	reference, err := ParseOCIReference(dependency.Bundle)
	if err != nil {
		return "", fmt.Errorf("dependency %s: %w", dependency.Name, err)
	}
	return path.Base(reference.Repository()), nil
}

// Allows checks a version against the dependency, as the specification
// reads it: within any of its ranges, and a prerelease only when allowed.
// The version may be written as a tag, v1.2.3.
func (dependency Dependency) Allows(version string) error {
	if dependency.Version == nil || len(dependency.Version.Ranges) == 0 {
		return nil
	}
	parsed, err := semverFromTag(version)
	if err != nil {
		return fmt.Errorf("version %s is not semver", version)
	}
	if parsed.Prerelease() != "" && !dependency.Version.AllowPrereleases {
		return fmt.Errorf("version %s is a prerelease, which the dependency does not allow", version)
	}
	ranges := strings.Join(dependency.Version.Ranges, " || ")
	constraint, err := semver.NewConstraint(ranges)
	if err != nil {
		return fmt.Errorf("ranges %s: %w", ranges, err)
	}
	if !constraint.Check(parsed) {
		return fmt.Errorf("version %s is in none of %s", version, strings.Join(dependency.Version.Ranges, ", "))
	}
	return nil
}
