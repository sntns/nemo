package convert

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

func networking(spec *ServiceSpec, project *types.Project, service types.ServiceConfig) error {
	if mode := service.NetworkMode; mode != "" {
		if name, ok := strings.CutPrefix(mode, "service:"); ok {
			mode = "container:" + ContainerName(project.Name, name, 1)
		}
		spec.Host.NetworkMode = container.NetworkMode(mode)
		return nil
	}
	spec.Networking = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	for i, key := range service.NetworksByPriority() {
		declared, ok := project.Networks[key]
		if !ok {
			return fmt.Errorf("undefined network %q", key)
		}
		endpoint := &network.EndpointSettings{Aliases: []string{service.Name}}
		if config := service.Networks[key]; config != nil {
			endpoint.Aliases = append(endpoint.Aliases, config.Aliases...)
			endpoint.DriverOpts = config.DriverOpts
			endpoint.GwPriority = config.GatewayPriority
			if config.Ipv4Address != "" || config.Ipv6Address != "" {
				endpoint.IPAMConfig = &network.EndpointIPAMConfig{}
				var err error
				if config.Ipv4Address != "" {
					if endpoint.IPAMConfig.IPv4Address, err = netip.ParseAddr(config.Ipv4Address); err != nil {
						return fmt.Errorf("networks.%s.ipv4_address: %w", key, err)
					}
				}
				if config.Ipv6Address != "" {
					if endpoint.IPAMConfig.IPv6Address, err = netip.ParseAddr(config.Ipv6Address); err != nil {
						return fmt.Errorf("networks.%s.ipv6_address: %w", key, err)
					}
				}
			}
			if config.MacAddress != "" {
				mac, err := net.ParseMAC(config.MacAddress)
				if err != nil {
					return fmt.Errorf("networks.%s.mac_address: %w", key, err)
				}
				endpoint.MacAddress = network.HardwareAddr(mac)
			}
		}
		if i == 0 {
			spec.Host.NetworkMode = container.NetworkMode(declared.Name)
		}
		spec.Networking.EndpointsConfig[declared.Name] = endpoint
	}
	return nil
}
