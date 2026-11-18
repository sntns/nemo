package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sntns/nemo/pkg/cnab"
)

func TestObjects(t *testing.T) {
	t.Parallel()
	data, mesh := Object{KindVolume, "shop_data"}, Object{KindNetwork, "shop_mesh"}
	installed := func(name string, objects ...Object) Installation {
		return Installation{
			InstallationSpec: InstallationSpec{Name: name, Bundle: BundleReference{Repository: "registry.example.com/" + name, Version: "1.0.0"}},
			Status:           InstallationStatus{Action: cnab.ActionUpgrade, ResultStatus: cnab.StatusSucceeded, Objects: objects},
		}
	}
	gone := installed("old", mesh)
	gone.Status.Action = cnab.ActionUninstall
	// A record nemo wrote before it marked the installations of shared objects.
	legacy := Installation{InstallationSpec: InstallationSpec{Name: "network.shop_mesh", Bundle: BundleReference{Version: "1.0.0"}}}
	marked := Installation{InstallationSpec: InstallationSpec{Name: "volume.shop_cache"}, Object: &Object{KindVolume, "shop_cache"}}

	assert.True(t, legacy.IsObject(), "told by its name and its bundle, which has no location")
	assert.True(t, marked.IsObject())
	assert.False(t, installed("network.tools").IsObject(), "a bundle of that name, from a registry")

	assert.Equal(t, []ObjectUse{
		{Object: Object{KindVolume, "shop_cache"}, Users: []string{}},
		{Object: data, Users: []string{"api"}},
		{Object: mesh, Users: []string{"api", "web"}},
	}, Objects([]Installation{installed("api", data, mesh), gone, legacy, marked, installed("web", mesh)}),
		"by kind then name, one user or several alike; an uninstalled installation uses none; a shared object's own installation is none of its users")
}
