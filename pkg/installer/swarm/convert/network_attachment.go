package convert

import (
	"fmt"
	"maps"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
)

// networkAttachments attaches a service to its networks, with the service
// name as an alias where the network allows one.
func networkAttachments(service types.ServiceConfig, networks types.Networks) ([]swarm.NetworkAttachmentConfig, error) {
	var result []swarm.NetworkAttachmentConfig
	for _, key := range slices.Sorted(maps.Keys(service.Networks)) {
		declared, ok := networks[key]
		if !ok {
			return nil, fmt.Errorf("undefined network %q", key)
		}
		attachment := swarm.NetworkAttachmentConfig{Target: declared.Name}
		if config := service.Networks[key]; config != nil {
			attachment.Aliases = config.Aliases
			attachment.DriverOpts = config.DriverOpts
		}
		// Only user-defined networks support aliases.
		if container.NetworkMode(declared.Name).IsUserDefined() {
			attachment.Aliases = append(attachment.Aliases, service.Name)
		}
		result = append(result, attachment)
	}
	return result, nil
}
