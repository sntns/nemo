package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

func TestCompleteStacks(t *testing.T) {
	t.Setenv("NEMO_HOME", t.TempDir())
	store, _, err := stackStore()
	require.NoError(t, err)
	for _, name := range []string{"staging", "prod"} {
		require.NoError(t, store.Save(stack.Stack{
			Name:          name,
			Target:        target.Target{Orchestrator: target.Compose},
			Remote:        "oci:/bundles/{{ .Component }}.oci",
			Installations: []storage.InstallationSpec{{Name: "web", Bundle: storage.BundleReference{Version: "1.0.0"}}},
		}, stack.YAML))
	}

	complete := func(arguments ...string) string {
		command := root()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(io.Discard)
		command.SetArgs(append([]string{"__complete"}, arguments...))
		require.NoError(t, command.Execute())
		return output.String()
	}
	assert.Equal(t, "prod\nstaging\n:4\n", complete("stack", "use", ""), "the stacks nemo keeps")
	assert.Equal(t, "prod\nstaging\n:4\n", complete("installation", "list", "--stack", ""), "for --stack too")
	assert.Equal(t, ":4\n", complete("stack", "use", "prod", ""), "and nothing after the name")
}
