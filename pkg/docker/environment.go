package docker

import (
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
)

// Environment returns the variables of a service, sorted, the way the
// daemon takes them: NAME=value, or NAME alone to pass it from the daemon.
func Environment(values types.MappingWithEquals) []string {
	var environment []string
	for name, value := range values {
		if value == nil {
			environment = append(environment, name)
		} else {
			environment = append(environment, name+"="+*value)
		}
	}
	slices.Sort(environment)
	return environment
}
