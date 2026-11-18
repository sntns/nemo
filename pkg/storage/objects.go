package storage

import (
	"cmp"
	"maps"
	"slices"
	"strings"
)

// The kinds of the objects an installation's project declares on the
// target, in the order an installer creates them.
const (
	KindVolume  = "volume"
	KindNetwork = "network"
	KindConfig  = "config"
	KindSecret  = "secret"
)

var kinds = []string{KindVolume, KindNetwork, KindConfig, KindSecret}

// Object is a network, a volume, a config or a secret on the target, by its
// kind and its name there.
type Object struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Compare orders objects by kind, as they are created, then by name.
func (object Object) Compare(other Object) int {
	return cmp.Or(cmp.Compare(slices.Index(kinds, object.Kind), slices.Index(kinds, other.Kind)), cmp.Compare(object.Name, other.Name))
}

// ObjectUse is an object the installations of a stack declare, with those
// that use it: one, or several that share it.
type ObjectUse struct {
	Object
	Users []string `json:"users"`
}

// IsObject tells whether nemo made the installation for an object several
// installations of the stack share, rather than for a bundle the stack
// lists: it creates the object before its users, and removes it once they
// are gone. A record written before nemo marked them is told by its name,
// KIND.NAME, and its bundle, which has no location.
func (installation Installation) IsObject() bool {
	_, ok := installation.object()
	return ok
}

func (installation Installation) object() (Object, bool) {
	if installation.Object != nil {
		return *installation.Object, true
	}
	kind, name, found := strings.Cut(installation.Name, ".")
	if !found || !slices.Contains(kinds, kind) || installation.Bundle.Repository != "" || installation.Bundle.File != "" || installation.External != nil {
		return Object{}, false
	}
	return Object{kind, name}, true
}

// Objects returns the objects the installations declare, by kind then
// name, each with the installations that use it, by name: the same for an
// object one installation uses as for one several share. Those uninstalled
// use none. The installation nemo made for a shared object is none of its
// users: it brings the object in even before they record it.
func Objects(installations []Installation) []ObjectUse {
	users := map[Object][]string{}
	for _, installation := range installations {
		if installation.IsUninstalled() {
			continue
		}
		if object, ok := installation.object(); ok {
			if _, seen := users[object]; !seen {
				users[object] = nil
			}
			continue
		}
		for _, object := range installation.Status.Objects {
			users[object] = append(users[object], installation.Name)
		}
	}
	found := make([]ObjectUse, 0, len(users))
	for _, object := range slices.SortedFunc(maps.Keys(users), Object.Compare) {
		names := append([]string{}, slices.Compact(slices.Sorted(slices.Values(users[object])))...)
		found = append(found, ObjectUse{Object: object, Users: names})
	}
	return found
}
