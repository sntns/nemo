package cnab

import (
	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/opencontainers/go-digest"
)

// RelocationMapFile is where a runtime mounts the relocation map in the
// invocation image (cnab-spec 103-bundle-runtime.md).
const RelocationMapFile = "/cnab/app/relocation-mapping.json"

// BundleReference is a bundle as it was resolved from an OCI reference.
type BundleReference struct {
	Reference  OCIReference
	Digest     digest.Digest
	Definition ExtendedBundle
	// RelocationMap is what cnab-to-oci returns when it pulls the bundle,
	// and what the runtime mounts as /cnab/app/relocation-mapping.json.
	RelocationMap relocation.ImageRelocationMap
}

func (reference BundleReference) String() string {
	return reference.Reference.String()
}
