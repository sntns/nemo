package convert

import (
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
)

// extraHosts converts extra_hosts to what the daemon takes: "host:IP", sorted.
func extraHosts(hosts types.HostsList) []string {
	var result []string
	for host, ips := range hosts {
		for _, ip := range ips {
			result = append(result, host+":"+ip)
		}
	}
	slices.Sort(result)
	return result
}
