package convert

import (
	"fmt"
	"os"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/opencontainers/go-digest"
)

// Swarm configs and secrets can't change: they are named after their content, so that a new
// content is a new object and the services using it roll.
func hashedName(name string, data []byte) (string, digest.Digest) {
	contentDigest := digest.FromBytes(data)
	return fmt.Sprintf("%s-%s", name, contentDigest.Encoded()[:14]), contentDigest
}

type fileTarget struct {
	name     string
	uid, gid string
	mode     os.FileMode
}

func newFileTarget(reference types.FileReferenceConfig) fileTarget {
	target := fileTarget{name: reference.Target, uid: reference.UID, gid: reference.GID, mode: 0o444}
	if target.name == "" {
		target.name = reference.Source
	}
	if target.uid == "" {
		target.uid = "0"
	}
	if target.gid == "" {
		target.gid = "0"
	}
	if reference.Mode != nil {
		target.mode = os.FileMode(*reference.Mode)
	}
	return target
}
