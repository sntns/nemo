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
		spec.Host.NetworkMode = networkMode(project.Name, mode)
		return nil
	}
	spec.Networking = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	for i, key := range service.NetworksByPriority() {
		declared, ok := project.Networks[key]
		if !ok {
			return fmt.Errorf("undefined network %q", key)
		}
		endpoint, err := endpointOf(service.Name, service.Networks[key])
		if err != nil {
			return fmt.Errorf("networks.%s.%w", key, err)
		}
		if i == 0 {
			spec.Host.NetworkMode = container.NetworkMode(declared.Name)
		}
		spec.Networking.EndpointsConfig[declared.Name] = endpoint
	}
	return nil
}

// networkMode is the network mode of the service: service:NAME is the
// container of that service's first replica.
func networkMode(project, mode string) container.NetworkMode {
	if name, ok := strings.CutPrefix(mode, "service:"); ok {
		return container.NetworkMode("container:" + ContainerName(project, name, 1))
	}
	return container.NetworkMode(mode)
}

// endpointOf is the endpoint of the service on a network: its name and
// aliases, the driver options, the gateway priority, and the addresses it
// asks for.
func endpointOf(service string, config *types.ServiceNetworkConfig) (*network.EndpointSettings, error) {
	endpoint := &network.EndpointSettings{Aliases: []string{service}}
	if config == nil {
		return endpoint, nil
	}
	endpoint.Aliases = append(endpoint.Aliases, config.Aliases...)
	endpoint.DriverOpts = config.DriverOpts
	endpoint.GwPriority = config.GatewayPriority
	ipam, err := endpointAddresses(config)
	if err != nil {
		return nil, err
	}
	endpoint.IPAMConfig = ipam
	if config.MacAddress != "" {
		mac, err := net.ParseMAC(config.MacAddress)
		if err != nil {
			return nil, fmt.Errorf("mac_address: %w", err)
		}
		endpoint.MacAddress = network.HardwareAddr(mac)
	}
	return endpoint, nil
}

// endpointAddresses is the IPv4 and IPv6 addresses the service asks for on
// the network, or nil when it asks for none.
func endpointAddresses(config *types.ServiceNetworkConfig) (*network.EndpointIPAMConfig, error) {
	if config.Ipv4Address == "" && config.Ipv6Address == "" {
		return nil, nil
	}
	ipam := &network.EndpointIPAMConfig{}
	var err error
	if config.Ipv4Address != "" {
		if ipam.IPv4Address, err = netip.ParseAddr(config.Ipv4Address); err != nil {
			return nil, fmt.Errorf("ipv4_address: %w", err)
		}
	}
	if config.Ipv6Address != "" {
		if ipam.IPv6Address, err = netip.ParseAddr(config.Ipv6Address); err != nil {
			return nil, fmt.Errorf("ipv6_address: %w", err)
		}
	}
	return ipam, nil
}
