package convert

import (
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/swarm"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/labels"
)

// Config converts a compose config into the spec of a Swarm config, named
// after its content. An external config, which is only referenced, has no data.
func Config(key string, config types.ConfigObjConfig, base map[string]string) (*swarm.ConfigSpec, error) {
	data := docker.Content(types.FileObjectConfig(config))
	if data == nil {
		return &swarm.ConfigSpec{Annotations: swarm.Annotations{Name: config.Name}}, nil
	}
	name, contentDigest := hashedName(config.Name, data)
	return &swarm.ConfigSpec{
		Annotations: swarm.Annotations{
			Name:   name,
			Labels: labels.Merge(config.Labels, base, map[string]string{labels.Config: key, labels.Hash: contentDigest.Encoded()}),
		},
		Data: data,
	}, nil
}

// The IDs are filled once the configs exist on the swarm.
func configReferences(service types.ServiceConfig, names map[string]string) ([]*swarm.ConfigReference, error) {
	var references []*swarm.ConfigReference
	for _, reference := range service.Configs {
		name, ok := names[reference.Source]
		if !ok {
			return nil, fmt.Errorf("undefined config %q", reference.Source)
		}
		target := newFileTarget(types.FileReferenceConfig(reference))
		references = append(references, &swarm.ConfigReference{
			ConfigName: name,
			File:       &swarm.ConfigReferenceFileTarget{Name: target.name, UID: target.uid, GID: target.gid, Mode: target.mode},
		})
	}
	return references, nil
}
