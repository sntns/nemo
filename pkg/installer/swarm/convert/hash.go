package convert

import (
	"encoding/json"
	"maps"

	"github.com/moby/moby/api/types/swarm"
	"github.com/opencontainers/go-digest"

	"github.com/sntns/nemo/pkg/labels"
)

// Hash identifies a service spec, whatever its hash and revision labels say:
// a service is only updated when its hash changed.
func Hash(spec *swarm.ServiceSpec) (string, error) {
	copied := *spec
	copied.Labels = maps.Clone(spec.Labels)
	delete(copied.Labels, labels.Hash)
	delete(copied.Labels, labels.Revision)
	data, err := json.Marshal(copied)
	return digest.FromBytes(data).Encoded(), err
}
