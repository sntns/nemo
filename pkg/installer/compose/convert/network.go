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

// Network converts a compose network.
func Network(key string, network types.NetworkConfig, base map[string]string) (docker.NetworkSpec, error) {
	options := client.NetworkCreateOptions{
		Driver:     network.Driver,
		Options:    network.DriverOpts,
		Labels:     labels.Merge(network.Labels, base, map[string]string{NetworkLabel: key}),
		Internal:   network.Internal,
		Attachable: network.Attachable,
		EnableIPv4: network.EnableIPv4,
		EnableIPv6: network.EnableIPv6,
	}
	if network.Ipam.Driver != "" || len(network.Ipam.Config) > 0 {
		options.IPAM = &mobynetwork.IPAM{Driver: network.Ipam.Driver}
		for _, pool := range network.Ipam.Config {
			config := mobynetwork.IPAMConfig{AuxAddress: map[string]netip.Addr{}}
			var err error
			if pool.Subnet != "" {
				if config.Subnet, err = netip.ParsePrefix(pool.Subnet); err != nil {
					return docker.NetworkSpec{}, fmt.Errorf("subnet: %w", err)
				}
			}
			if pool.IPRange != "" {
				if config.IPRange, err = netip.ParsePrefix(pool.IPRange); err != nil {
					return docker.NetworkSpec{}, fmt.Errorf("ip_range: %w", err)
				}
			}
			if pool.Gateway != "" {
				if config.Gateway, err = netip.ParseAddr(pool.Gateway); err != nil {
					return docker.NetworkSpec{}, fmt.Errorf("gateway: %w", err)
				}
			}
			for host, address := range pool.AuxiliaryAddresses {
				if config.AuxAddress[host], err = netip.ParseAddr(address); err != nil {
					return docker.NetworkSpec{}, fmt.Errorf("aux_addresses: %w", err)
				}
			}
			options.IPAM.Config = append(options.IPAM.Config, config)
		}
	}
	return docker.NetworkSpec{Name: network.Name, Options: options}, nil
}
