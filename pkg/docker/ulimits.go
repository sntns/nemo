package docker

import (
	"maps"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
)

// Ulimits converts the ulimits of a service, sorted by name.
func Ulimits(limits map[string]*types.UlimitsConfig) []*container.Ulimit {
	var result []*container.Ulimit
	for _, name := range slices.Sorted(maps.Keys(limits)) {
		limit := limits[name]
		soft, hard := limit.Soft, limit.Hard
		if limit.Single != 0 {
			soft, hard = limit.Single, limit.Single
		}
		result = append(result, &container.Ulimit{Name: name, Soft: int64(soft), Hard: int64(hard)})
	}
	return result
}
