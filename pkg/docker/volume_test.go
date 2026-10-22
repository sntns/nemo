package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProvision_outside(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"/etc", "/provision", "/provision/../etc", "/provisioned/data", "data"} {
		assert.Error(t, Provision(directory), directory)
	}
}
