package convert

import (
	"fmt"
	"net/netip"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/swarm"
)

func dnsConfig(service types.ServiceConfig) (*swarm.DNSConfig, error) {
	if len(service.DNS)+len(service.DNSSearch)+len(service.DNSOpts) == 0 {
		return nil, nil
	}
	config := &swarm.DNSConfig{Search: service.DNSSearch, Options: service.DNSOpts}
	for _, server := range service.DNS {
		address, err := netip.ParseAddr(server)
		if err != nil {
			return nil, fmt.Errorf("dns: %w", err)
		}
		config.Nameservers = append(config.Nameservers, address)
	}
	return config, nil
}
