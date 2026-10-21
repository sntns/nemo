package cnab

import (
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerExtension(t *testing.T) {
	t.Parallel()

	t.Run("required with an empty entry", func(t *testing.T) {
		t.Parallel()
		bundle := testBundle(t, nil)
		assert.True(t, DockerExtension.RequiredBy(bundle.Bundle))
		assert.Equal(t, &Docker{}, bundle.Docker)
	})

	t.Run("privileged", func(t *testing.T) {
		t.Parallel()
		extendedBundle := testBundle(t, func(bundle *bundle.Bundle) {
			bundle.Custom[DockerExtension.Key] = map[string]any{"privileged": true}
		})
		require.NotNil(t, extendedBundle.Docker)
		assert.True(t, extendedBundle.Docker.Privileged)
	})
}
