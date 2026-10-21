package target

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTarget_Validate(t *testing.T) {
	t.Parallel()

	require.NoError(t, Target{Orchestrator: Compose}.Validate())
	require.NoError(t, Target{Orchestrator: Swarm, Configuration: Configuration{Host: "ssh://deploy@vm1"}}.Validate())

	assert.EqualError(t, Target{}.Validate(), "orchestrator: cannot be blank.")
	assert.EqualError(t, Target{Orchestrator: "kubernetes"}.Validate(), "orchestrator: must be compose or swarm.")
	assert.ErrorContains(t, Target{Orchestrator: Swarm, Configuration: Configuration{Host: "unix:///var/run/docker.sock", Context: "vm1"}}.Validate(), "a host or a docker context, not both")
	require.NoError(t, Target{Orchestrator: Compose, Configuration: Configuration{Images: ImagesLoad}}.Validate())
	assert.EqualError(t, Target{Orchestrator: Compose, Configuration: Configuration{Images: "copy"}}.Validate(), "configuration: (images: must be registry or load.).")
	assert.EqualError(t, Target{Orchestrator: Swarm, Configuration: Configuration{Volumes: map[string]VolumeType{"data": {Provision: &VolumeProvision{}}}}}.Validate(),
		"configuration: (volumes: (data: (provision: (directory: cannot be blank.).).).).")
}

func TestInstaller_Validate(t *testing.T) {
	t.Parallel()

	require.NoError(t, Installer{}.Validate())
	assert.Equal(t, DriverDocker, Installer{}.DriverName())
	require.NoError(t, Installer{Driver: DriverKubernetes, Kubernetes: &Kubernetes{Namespace: "nemo"}}.Validate())

	assert.EqualError(t, Installer{Driver: DriverKubernetes}.Validate(), "kubernetes: the kubernetes driver needs them.")
	assert.EqualError(t, Installer{Driver: DriverKubernetes, Kubernetes: &Kubernetes{}}.Validate(), "kubernetes: (namespace: cannot be blank.).")
	assert.EqualError(t, Installer{Kubernetes: &Kubernetes{Namespace: "nemo"}}.Validate(), "kubernetes: only the kubernetes driver takes them.")
	assert.EqualError(t, Installer{Driver: "podman"}.Validate(), "driver: must be docker or kubernetes.")
}
