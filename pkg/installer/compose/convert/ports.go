package convert

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/network"
)

func ports(spec *ServiceSpec, service types.ServiceConfig) error {
	for _, expose := range service.Expose {
		if !strings.Contains(expose, "/") {
			expose += "/tcp"
		}
		port, err := network.ParsePort(expose)
		if err != nil {
			return fmt.Errorf("expose %q: %w", expose, err)
		}
		spec.Config.ExposedPorts[port] = struct{}{}
	}
	for _, portConfig := range service.Ports {
		protocol := portConfig.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		port, err := network.ParsePort(fmt.Sprintf("%d/%s", portConfig.Target, protocol))
		if err != nil {
			return fmt.Errorf("port %d: %w", portConfig.Target, err)
		}
		spec.Config.ExposedPorts[port] = struct{}{}
		if portConfig.Published == "" {
			continue
		}
		binding := network.PortBinding{HostPort: portConfig.Published}
		if portConfig.HostIP != "" {
			if binding.HostIP, err = netip.ParseAddr(portConfig.HostIP); err != nil {
				return fmt.Errorf("port %d host_ip: %w", portConfig.Target, err)
			}
		}
		if spec.Host.PortBindings == nil {
			spec.Host.PortBindings = network.PortMap{}
		}
		spec.Host.PortBindings[port] = append(spec.Host.PortBindings[port], binding)
	}
	return nil
}
