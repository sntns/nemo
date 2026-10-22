package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwns(t *testing.T) {
	t.Parallel()

	want := map[string]string{"io.nemo.stack": "demo", "io.nemo.orchestrator": "compose"}
	assert.True(t, Owns(map[string]string{"io.nemo.stack": "demo", "io.nemo.orchestrator": "compose", "other": "x"}, want))
	assert.False(t, Owns(map[string]string{"io.nemo.stack": "demo", "io.nemo.orchestrator": "swarm"}, want), "the same stack, deployed by the other orchestrator")
	assert.False(t, Owns(nil, want), "created by something else")

	require.EqualError(t, NotOwned("network", "demo_default", want),
		"network demo_default exists but was not created by nemo for io.nemo.orchestrator=compose, io.nemo.stack=demo")
}
