package cnab

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// TagLister lists the tags of a repository. The registry client implements it.
type TagLister interface {
	ListTags(ctx context.Context, repository OCIReference) ([]string, error)
}

// VersionStrategy says which version satisfying a dependency's ranges is picked.
type VersionStrategy string

const (
	// VersionMax picks the highest version satisfying the ranges. It is the default.
	VersionMax VersionStrategy = ""
	// VersionMaxMinor picks the highest version with the same major as the
	// reference's tag.
	VersionMaxMinor VersionStrategy = "max-minor"
	// VersionMaxPatch picks the highest version with the same major and minor
	// as the reference's tag.
	VersionMaxPatch VersionStrategy = "max-patch"
	// VersionMin picks the lowest version satisfying the ranges.
	VersionMin VersionStrategy = "min"
	// VersionExact keeps the reference's tag or digest, and fails without one.
	VersionExact VersionStrategy = "exact"
)

// DependencyLock is a dependency resolved to one bundle reference.
type DependencyLock struct {
	Alias     string
	Reference string
}

// Resolver resolves the dependencies of a bundle to bundle references.
type Resolver struct {
	Tags     TagLister
	Strategy VersionStrategy
}

// ResolveDependencies resolves every dependency of the bundle, in its sequence.
func (resolver Resolver) ResolveDependencies(ctx context.Context, bundle ExtendedBundle) ([]DependencyLock, error) {
	if bundle.Dependencies == nil {
		return nil, nil
	}

	dependencies := bundle.Dependencies.ListBySequence()
	locks := make([]DependencyLock, 0, len(dependencies))
	for _, dependency := range dependencies {
		reference, err := resolver.ResolveVersion(ctx, dependency.Name, dependency)
		if err != nil {
			return nil, err
		}
		locks = append(locks, DependencyLock{Alias: dependency.Name, Reference: reference.String()})
	}

	return locks, nil
}

// ResolveVersion returns the reference of the bundle the dependency resolves to.
func (resolver Resolver) ResolveVersion(ctx context.Context, name string, dependency Dependency) (OCIReference, error) {
	reference, err := ParseOCIReference(dependency.Bundle)
	if err != nil {
		return OCIReference{}, fmt.Errorf("error parsing dependency (%s) bundle %q as OCI reference: %w", name, dependency.Bundle, err)
	}

	if dependency.Version == nil || len(dependency.Version.Ranges) == 0 {
		// A tag or a digest in the reference is the version.
		if reference.HasTag() || reference.HasDigest() {
			return reference, nil
		}
		tag, err := resolver.selectTag(ctx, dependency, reference, nil)
		if err != nil {
			return OCIReference{}, err
		}
		return reference.WithTag(tag)
	}

	if resolver.Strategy == VersionExact {
		if reference.HasTag() || reference.HasDigest() {
			return reference, nil
		}
		return OCIReference{}, fmt.Errorf("dependency %s specifies a version range but the version strategy is %q and its reference has no tag", name, resolver.Strategy)
	}
	if reference.HasDigest() {
		return reference, nil
	}

	// max-patch and max-minor narrow the ranges around the reference's tag.
	constraints := []string{strings.Join(dependency.Version.Ranges, " || ")}
	if reference.HasVersion() {
		if version, err := semverFromTag(reference.Tag()); err == nil {
			switch resolver.Strategy {
			case VersionMaxPatch:
				constraints = append(constraints, fmt.Sprintf(">= %d.%d.0, < %d.%d.0", version.Major(), version.Minor(), version.Major(), version.Minor()+1))
			case VersionMaxMinor:
				constraints = append(constraints, fmt.Sprintf(">= %d.0.0, < %d.0.0", version.Major(), version.Major()+1))
			}
		}
	}
	tag, err := resolver.selectTag(ctx, dependency, reference, constraints)
	if err != nil {
		return OCIReference{}, err
	}
	return reference.WithTag(tag)
}

// selectTag returns the tag of the repository that the strategy picks among
// the semver tags satisfying every constraint. Without constraints, it falls
// back to latest when no tag is a version.
func (resolver Resolver) selectTag(ctx context.Context, dependency Dependency, reference OCIReference, constraints []string) (string, error) {
	if resolver.Tags == nil {
		return "", fmt.Errorf("no registry to list the tags of %s", dependency.Bundle)
	}

	parsed := make([]*semver.Constraints, len(constraints))
	for i, constraint := range constraints {
		var err error
		if parsed[i], err = semver.NewConstraint(constraint); err != nil {
			return "", fmt.Errorf("invalid version constraint %q for %s: %w", constraint, dependency.Bundle, err)
		}
	}

	tags, err := resolver.Tags.ListTags(ctx, reference)
	if err != nil {
		return "", fmt.Errorf("error listing tags for %s: %w", dependency.Bundle, err)
	}

	allowPrereleases := dependency.Version != nil && dependency.Version.AllowPrereleases
	var hasLatest bool
	versions := make(semver.Collection, 0, len(tags))
	for _, tag := range tags {
		if tag == "latest" {
			hasLatest = true
			continue
		}
		version, err := semverFromTag(tag)
		if err != nil || (!allowPrereleases && version.Prerelease() != "") {
			continue
		}
		if slices.ContainsFunc(parsed, func(constraint *semver.Constraints) bool { return !constraint.Check(version) }) {
			continue
		}
		versions = append(versions, version)
	}

	if len(versions) == 0 {
		if hasLatest && len(constraints) == 0 {
			return "latest", nil
		}
		return "", fmt.Errorf("no tag was specified for %s and none of the tags defined in the registry meet the criteria: semver formatted or 'latest'", dependency.Bundle)
	}

	if resolver.Strategy == VersionMin {
		sort.Sort(versions)
	} else {
		sort.Sort(sort.Reverse(versions))
	}
	return versions[0].Original(), nil
}
