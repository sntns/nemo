package docker

import (
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/labels"
)

// ByName selects objects whose name contains name. The daemon matches
// prefixes, so callers still compare the exact name.
func ByName(name string) client.Filters {
	return client.Filters{}.Add("name", name)
}

// ByInstallation selects the objects of an installation, by their labels.
func ByInstallation(installation installer.Installation) client.Filters {
	return client.Filters{}.Add("label", labels.Stack+"="+installation.Stack, labels.Bundle+"="+installation.Bundle)
}
