package convert

import (
	"fmt"
	"net/netip"

	"github.com/compose-spec/compose-go/v2/types"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/labels"
)

// Network converts a compose network into a Swarm network to create, as
// docker stack deploy does: an overlay network of swarm scope.
func Network(network types.NetworkConfig, base map[string]string) (docker.NetworkSpec, error) {
	// Services can only join overlay networks; docker stack deploy defaults to it too.
	driver := network.Driver
	if driver == "" {
		driver = "overlay"
	}
	options := client.NetworkCreateOptions{
		Driver:     driver,
		Options:    network.DriverOpts,
		Labels:     labels.Merge(network.Labels, base),
		Internal:   network.Internal,
		Attachable: network.Attachable,
		Scope:      "swarm",
	}
	if network.Ipam.Driver != "" || len(network.Ipam.Config) > 0 {
		options.IPAM = &mobynetwork.IPAM{Driver: network.Ipam.Driver}
		for _, pool := range network.Ipam.Config {
			config, err := ipamConfig(pool)
			if err != nil {
				return docker.NetworkSpec{}, err
			}
			options.IPAM.Config = append(options.IPAM.Config, config)
		}
	}
	return docker.NetworkSpec{Name: network.Name, Options: options}, nil
}

func ipamConfig(pool *types.IPAMPool) (mobynetwork.IPAMConfig, error) {
	var config mobynetwork.IPAMConfig
	var err error
	if pool.Subnet != "" {
		if config.Subnet, err = netip.ParsePrefix(pool.Subnet); err != nil {
			return config, fmt.Errorf("subnet: %w", err)
		}
	}
	if pool.IPRange != "" {
		if config.IPRange, err = netip.ParsePrefix(pool.IPRange); err != nil {
			return config, fmt.Errorf("ip_range: %w", err)
		}
	}
	if pool.Gateway != "" {
		if config.Gateway, err = netip.ParseAddr(pool.Gateway); err != nil {
			return config, fmt.Errorf("gateway: %w", err)
		}
	}
	return config, nil
}
