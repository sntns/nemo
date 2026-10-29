package cnab

import (
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourcedBundle reads its state from the output of its last run.
func sourcedBundle(t *testing.T) ExtendedBundle {
	t.Helper()
	return testBundle(t, func(cnabBundle *bundle.Bundle) {
		cnabBundle.RequiredExtensions = append(cnabBundle.RequiredExtensions, ParameterSourcesExtension.Key)
		cnabBundle.Custom[ParameterSourcesExtension.Key] = map[string]any{
			"state": map[string]any{"priority": []any{"output"}, "sources": map[string]any{"output": map[string]any{"name": "state"}}},
		}
		cnabBundle.Definitions = definition.Definitions{"string": &definition.Schema{Type: "string"}}
	})
}

func TestParameterSources(t *testing.T) {
	t.Parallel()
	sourced := sourcedBundle(t)
	require.NoError(t, sourced.CheckRequiredExtensions(), "an extension of the CNAB spec")
	assert.True(t, sourced.IsSourced("state"))
	assert.False(t, sourced.IsSourced("name"))
	output, ok := (*sourced.ParameterSources)["state"].Output()
	assert.True(t, ok)
	assert.Equal(t, "state", output)
}

func TestParameterSourcesExtension_schema(t *testing.T) {
	t.Parallel()
	cnabBundle := testBundle(t, nil).Bundle
	cnabBundle.Custom[ParameterSourcesExtension.Key] = map[string]any{
		"state": map[string]any{"priority": []any{"dependency"}, "sources": map[string]any{"output": map[string]any{"name": "state"}}},
	}
	_, err := NewBundle(cnabBundle)
	assert.ErrorContains(t, err, "io.cnab.parameter-sources", "a source type the spec does not define")
}

// Porter's bundles require Porter's own extension for their state, outside
// the CNAB spec: nemo refuses them, as a CNAB runtime refuses an extension
// it does not know.
func TestPorterBundlesAreRefused(t *testing.T) {
	t.Parallel()
	porter := testBundle(t, func(cnabBundle *bundle.Bundle) {
		cnabBundle.RequiredExtensions = append(cnabBundle.RequiredExtensions, "sh.porter.file-parameters")
		cnabBundle.Custom["sh.porter.file-parameters"] = map[string]any{}
	})
	assert.EqualError(t, porter.CheckRequiredExtensions(), "unsupported required extension: sh.porter.file-parameters")
}
