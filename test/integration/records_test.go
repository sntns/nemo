//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/storage/volume"
)

// A stack's records on its target: the first nemo to manage it, Terraform's
// provider on its first apply, records itself before any installation.
func TestRecords_ownerOfANewStack(t *testing.T) {
	b := newBench(t, "compose")
	b.after = append(b.after, func() {
		_, _ = b.api.VolumeRemove(context.WithoutCancel(t.Context()), volume.Name(b.stack), client.VolumeRemoveOptions{Force: true})
	})

	held, err := volume.Open(t.Context(), b.api, b.stack, true)
	require.NoError(t, err)
	assert.Empty(t, held.Owner())
	_, err = volume.Open(t.Context(), b.api, b.stack, true)
	assert.ErrorIs(t, err, volume.ErrBusy, "one writer at a time")
	require.NoError(t, held.SetOwner(t.Context(), "terraform"))
	require.NoError(t, held.Close(t.Context()))

	read, err := volume.Open(t.Context(), b.api, b.stack, false)
	require.NoError(t, err)
	defer read.Close(t.Context())
	assert.Equal(t, "terraform", read.Owner())
	installations, err := read.ListInstallations(t.Context(), b.stack)
	require.NoError(t, err)
	assert.Empty(t, installations)
}
