package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNanoCPUs(t *testing.T) {
	t.Parallel()
	for cpus, nanoCPUs := range map[float32]int64{0: 0, 0.1: 100_000_000, 0.5: 500_000_000, 1.5: 1_500_000_000, 0.333: 333_000_000, 16: 16_000_000_000} {
		assert.Equal(t, nanoCPUs, NanoCPUs(cpus), "cpus %v", cpus)
	}
}
