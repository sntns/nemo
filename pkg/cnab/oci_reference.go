package cnab

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

// ParseOCIReference parses the specified value as an OCIReference.
// If the reference includes a digest, the digest is validated.
func ParseOCIReference(value string) (OCIReference, error) {
	named, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return OCIReference{}, fmt.Errorf("failed to parse named reference %s: %w", value, err)
	}

	ociReference := OCIReference{Named: named}
	if ociReference.HasDigest() {
		err := ociReference.Digest().Validate()
		if err != nil {
			return OCIReference{}, fmt.Errorf("invalid digest for reference %s: %w", value, err)
		}
	}

	return ociReference, nil
}

// MustParseOCIReference parses the specified value as an OCIReference,
// panicking on any errors.
// Only use this for unit tests where you know the value is a reference.
func MustParseOCIReference(value string) OCIReference {
	reference, err := ParseOCIReference(value)
	if err != nil {
		panic(err)
	}
	return reference
}

var (
	_ json.Marshaler   = OCIReference{}
	_ json.Unmarshaler = &OCIReference{}
)

// OCIReference is a wrapper around a docker reference with convenience methods
// for decomposing and manipulating bundle references.
//
// It is designed to be safe to call even when uninitialized, returning empty
// strings when parts are requested that do not exist, such as calling Digest()
// when no digest is set on the reference.
type OCIReference struct {
	// Name is the wrapped reference that we are providing helper methods on top of
	Named reference.Named
}

func (ociReference *OCIReference) UnmarshalJSON(bytes []byte) error {
	value := strings.TrimPrefix(strings.TrimSuffix(string(bytes), `"`), `"`)
	parsed, err := ParseOCIReference(value)
	if err != nil {
		return err
	}
	ociReference.Named = parsed.Named
	return nil
}

func (ociReference OCIReference) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `"%s"`, ociReference.String()), nil
}

// Always print the original name provided, not
// the one with docker.io prefixed.
func (ociReference OCIReference) String() string {
	if ociReference.Named == nil {
		return ""
	}
	return reference.FamiliarString(ociReference.Named)
}

// Canonical is the full reference, its registry included:
// docker.io/library/nginx:1.27, as registry clients want it.
func (ociReference OCIReference) Canonical() string {
	if ociReference.Named == nil {
		return ""
	}
	return ociReference.Named.String()
}

// WithoutVersion returns the repository alone, without tag nor digest.
func (ociReference OCIReference) WithoutVersion() OCIReference {
	return OCIReference{Named: reference.TrimNamed(ociReference.Named)}
}

// Repository portion of the reference.
// Example: docker.io/getporter/mybuns:v0.1.1 returns getporter/mybuns
func (ociReference OCIReference) Repository() string {
	if ociReference.Named == nil {
		return ""
	}
	return reference.FamiliarName(ociReference.Named)
}

// Registry portion of the reference.
// Example: ghcr.io/getporter/mybuns:v0.1.1 returns ghcr.io
func (ociReference OCIReference) Registry() string {
	if ociReference.Named == nil {
		return ""
	}
	return reference.Domain(ociReference.Named)
}

// IsRepositoryOnly determines if the reference is fully qualified
// with a tag/digest or if it only contains a repository.
// Example: ghcr.io/getporter/mybuns returns true
func (ociReference OCIReference) IsRepositoryOnly() bool {
	return !ociReference.HasTag() && !ociReference.HasDigest()
}

// HasDigest determines if the reference has a digest portion.
// Example: ghcr.io/getporter/mybuns@sha256:abc123 returns true
func (ociReference OCIReference) HasDigest() bool {
	if ociReference.Named == nil {
		return false
	}

	_, ok := ociReference.Named.(reference.Digested)
	return ok
}

// Digest portion of the reference.
// Example: ghcr.io/getporter/mybuns@sha256:abc123 returns sha256:abc123
func (ociReference OCIReference) Digest() digest.Digest {
	if ociReference.Named == nil {
		return ""
	}

	digested, ok := ociReference.Named.(reference.Digested)
	if ok {
		return digested.Digest()
	}
	return ""
}

// HasTag determines if the reference has a tag portion.
// Example: ghcr.io/getporter/mybuns:latest returns true
func (ociReference OCIReference) HasTag() bool {
	if ociReference.Named == nil {
		return false
	}

	_, ok := ociReference.Named.(reference.Tagged)
	return ok
}

// Tag portion of the reference.
// Example: ghcr.io/getporter/mybuns:latest returns latest
func (ociReference OCIReference) Tag() string {
	if ociReference.Named == nil {
		return ""
	}

	tagged, ok := ociReference.Named.(reference.Tagged)
	if ok {
		return tagged.Tag()
	}
	return ""
}

// HasVersion detects if the reference tag is a bundle version (semver).
func (ociReference OCIReference) HasVersion() bool {
	if ociReference.Named == nil {
		return false
	}

	if tagged, ok := ociReference.Named.(reference.Tagged); ok {
		_, err := semverFromTag(tagged.Tag())
		return err == nil
	}
	return false
}

// Version parses the reference tag as a bundle version (semver).
func (ociReference OCIReference) Version() string {
	if ociReference.Named == nil {
		return ""
	}

	if tagged, ok := ociReference.Named.(reference.Tagged); ok {
		version, err := semverFromTag(tagged.Tag())
		if err == nil {
			return version.String()
		}
	}
	return ""
}

// WithVersion creates a new reference using the repository and the specified bundle version.
// If build metadata is present, "+" is converted to "_".
func (ociReference OCIReference) WithVersion(version string) (OCIReference, error) {
	if ociReference.Named == nil {
		return OCIReference{}, errors.New("OCIReference has not been initialized")
	}

	semanticVersion, err := semver.NewVersion(version)
	if err != nil {
		return OCIReference{}, fmt.Errorf("invalid bundle version specified %s: %w", version, err)
	}

	newReference, err := reference.WithTag(ociReference.Named, tagFromSemver(semanticVersion))
	if err != nil {
		return OCIReference{}, err
	}
	return OCIReference{Named: newReference}, nil
}

// WithTag creates a new reference using the repository and the specified tag.
func (ociReference OCIReference) WithTag(tag string) (OCIReference, error) {
	if ociReference.Named == nil {
		return OCIReference{}, errors.New("OCIReference has not been initialized")
	}
	newReference, err := reference.WithTag(ociReference.Named, tag)
	if err != nil {
		return OCIReference{}, err
	}
	return OCIReference{Named: newReference}, nil
}

// WithDigest creates a new reference using the repository and the specified digest.
func (ociReference OCIReference) WithDigest(digest digest.Digest) (OCIReference, error) {
	if ociReference.Named == nil {
		return OCIReference{}, errors.New("OCIReference has not been initialized")
	}
	newReference, err := reference.WithDigest(ociReference.Named, digest)
	if err != nil {
		return OCIReference{}, err
	}
	return OCIReference{Named: newReference}, nil
}

func semverFromTag(tag string) (*semver.Version, error) {
	return semver.NewVersion(strings.Replace(tag, "_", "+", 1))
}

func tagFromSemver(version *semver.Version) string {
	return fmt.Sprintf("v%s", strings.Replace(version.String(), "+", "_", 1))
}
