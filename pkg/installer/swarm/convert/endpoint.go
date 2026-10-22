package convert

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
)

func endpoint(service types.ServiceConfig, deploy *types.DeployConfig) (*swarm.EndpointSpec, error) {
	resolution := swarm.ResolutionModeVIP
	if deploy.EndpointMode != "" {
		resolution = swarm.ResolutionMode(strings.ToLower(deploy.EndpointMode))
	}
	spec := &swarm.EndpointSpec{Mode: resolution}
	for _, port := range service.Ports {
		published := 0
		if port.Published != "" {
			var err error
			if published, err = strconv.Atoi(port.Published); err != nil {
				return nil, fmt.Errorf("published port %q: swarm takes a single port, not a range", port.Published)
			}
		}
		spec.Ports = append(spec.Ports, swarm.PortConfig{
			Name:          port.Name,
			Protocol:      network.IPProtocol(port.Protocol),
			TargetPort:    port.Target,
			PublishedPort: uint32(published),
			PublishMode:   swarm.PortConfigPublishMode(port.Mode),
		})
	}
	return spec, nil
}
