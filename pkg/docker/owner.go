package docker

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Owns reports whether an object the installer found by its name is its
// own: it carries every label of want. The installer only reuses, updates
// or removes its own objects; a name taken by anything else is an error.
func Owns(objectLabels, want map[string]string) bool {
	for key, value := range want {
		if objectLabels[key] != value {
			return false
		}
	}
	return true
}

// NotOwned is the error for an object of the project's name that is not the
// installer's own.
func NotOwned(kind, name string, want map[string]string) error {
	pairs := make([]string, 0, len(want))
	for _, key := range slices.Sorted(maps.Keys(want)) {
		pairs = append(pairs, key+"="+want[key])
	}
	return fmt.Errorf("%s %s exists but was not created by nemo for %s", kind, name, strings.Join(pairs, ", "))
}
