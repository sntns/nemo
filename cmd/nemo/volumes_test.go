package main

import (
	"testing"

	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

func TestVolumePresets(t *testing.T) {
	t.Parallel()
	nfs := nfsVolumes("10.0.0.20", "/exports")
	assert.Equal(t, map[string]string{"type": "nfs", "o": `addr=10.0.0.20,rw,nfsvers=4{{AppendJoin .Options ","}}`, "device": ":/exports/{{.Stack}}/{{.Name}}"}, nfs.Options, "the targets guide's")
	assert.Equal(t, map[string]string{"type": "nfs", "o": "addr=10.0.0.20,rw,nfsvers=4", "device": ":/exports"}, nfs.Provision.Volume.Options)
	assert.Equal(t, "/srv/{{.Stack}}/{{.Name}}", hostVolumes("/srv").Options["device"])

	for _, made := range []struct {
		volumeType  target.VolumeType
		kind, value string
	}{
		{target.VolumeType{}, volumeLocal, ""},
		{hostVolumes("/srv"), volumeHost, "/srv"},
		{nfs, volumeNFS, "10.0.0.20:/exports"},
		{target.VolumeType{VolumeDriver: target.VolumeDriver{Driver: "rexray/ebs"}}, volumeWritten, ""},
	} {
		kind, value := volumeKindOf(made.volumeType)
		assert.Equal(t, made.kind, kind)
		assert.Equal(t, made.value, value)
	}

	volumeType, err := volumeTypeOf(volumeNFS, "10.0.0.20:/exports/", target.VolumeType{})
	require.NoError(t, err)
	assert.Equal(t, nfs, volumeType, "the export cleaned")
	_, err = volumeTypeOf(volumeNFS, "10.0.0.20", target.VolumeType{})
	assert.EqualError(t, err, "SERVER:/EXPORT, 10.0.0.20:/exports")
	_, err = volumeTypeOf(volumeHost, "srv", target.VolumeType{})
	assert.EqualError(t, err, "an absolute directory of the host, /srv")
}

func TestEditor_volumeType(t *testing.T) {
	t.Parallel()
	written := target.VolumeType{VolumeDriver: target.VolumeDriver{Driver: "rexray/ebs"}}
	imported := stack.Stack{
		Name:          "shop",
		Remote:        "docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}",
		Target:        target.Target{Orchestrator: target.Swarm, Configuration: target.Configuration{Volumes: map[string]target.VolumeType{"data": {}, "backup": written}}},
		Parameters:    map[string]string{"region": "eu5"},
		Credentials:   []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "env", Value: "TOKEN"}}},
		Installations: []storage.InstallationSpec{{Name: "web", Stack: "shop", Bundle: storage.BundleReference{Version: "1.1.0"}}},
	}
	e := shopEditor(imported, modeEdit, true)
	drain(e, e.Init())
	e.at, e.reviewed = screen{kind: screenVolume, step: stepTarget, name: "data"}, true
	e.enter()

	assert.Equal(t, []string{volumeLocal, volumeHost, volumeNFS}, e.kindsOf(e.screen()), "nothing of the manifest's to keep")
	press(e, "right", "right", "enter", "10.0.0.20", "enter")
	assert.Equal(t, "SERVER:/EXPORT, 10.0.0.20:/exports", e.problem)
	press(e, ":/exports", "enter")
	assert.Equal(t, nfsVolumes("10.0.0.20", "/exports"), e.stack.Target.Volumes["data"])

	e.at = screen{kind: screenVolume, step: stepTarget, name: "backup"}
	e.enter()
	assert.Equal(t, volumeWritten, e.kinds[e.screen()], "a type the editor did not make")
	press(e, "enter")
	assert.Equal(t, written, e.stack.Target.Volumes["backup"], "kept as written")
	assert.NoError(t, e.stack.Validate())
}
