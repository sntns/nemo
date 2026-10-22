package convert

import (
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/swarm"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/labels"
)

// Secret converts a compose secret into the spec of a Swarm secret, named
// after its content. An external secret, which is only referenced, has no data.
func Secret(key string, secret types.SecretConfig, base map[string]string) (*swarm.SecretSpec, error) {
	data := docker.Content(types.FileObjectConfig(secret))
	if data == nil {
		return &swarm.SecretSpec{Annotations: swarm.Annotations{Name: secret.Name}}, nil
	}
	name, contentDigest := hashedName(secret.Name, data)
	return &swarm.SecretSpec{
		Annotations: swarm.Annotations{
			Name:   name,
			Labels: labels.Merge(secret.Labels, base, map[string]string{labels.Secret: key, labels.Hash: contentDigest.Encoded()}),
		},
		Data: data,
	}, nil
}

// The IDs are filled once the secrets exist on the swarm.
func secretReferences(service types.ServiceConfig, names map[string]string) ([]*swarm.SecretReference, error) {
	var references []*swarm.SecretReference
	for _, reference := range service.Secrets {
		name, ok := names[reference.Source]
		if !ok {
			return nil, fmt.Errorf("undefined secret %q", reference.Source)
		}
		target := newFileTarget(types.FileReferenceConfig(reference))
		references = append(references, &swarm.SecretReference{
			SecretName: name,
			File:       &swarm.SecretReferenceFileTarget{Name: target.name, UID: target.uid, GID: target.gid, Mode: target.mode},
		})
	}
	return references, nil
}
