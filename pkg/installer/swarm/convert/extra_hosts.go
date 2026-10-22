package convert

import (
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
)

// extraHosts converts extra_hosts to the lines of /etc/hosts Swarm takes:
// "IP host", sorted.
func extraHosts(hosts types.HostsList) []string {
	var result []string
	for host, ips := range hosts {
		for _, ip := range ips {
			result = append(result, ip+" "+host)
		}
	}
	slices.Sort(result)
	return result
}
