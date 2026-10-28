//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/installer/compose"
	"github.com/sntns/nemo/pkg/labels"
)

func (b *bench) containers(service string) []container.Summary {
	b.t.Helper()
	filters := b.byStack().Add("label", labels.Service+"="+service)
	list, err := b.api.ContainerList(b.t.Context(), client.ContainerListOptions{All: true, Filters: filters})
	require.NoError(b.t, err)
	return list.Items
}

func (b *bench) environment(id string) []string {
	b.t.Helper()
	inspected, err := b.api.ContainerInspect(b.t.Context(), id, client.ContainerInspectOptions{})
	require.NoError(b.t, err)
	return inspected.Container.Config.Env
}

func runningContainers(containers []container.Summary) int {
	count := 0
	for _, summary := range containers {
		if summary.State == container.StateRunning {
			count++
		}
	}
	return count
}

func TestCompose_installsAService(t *testing.T) {
	b := newBench(t, compose.Name)

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    deploy:
      replicas: 2
`))

	assert.Equal(t, 2, runningContainers(b.containers("web")))
}

// A container that exits until its dependency is ready, and that the daemon
// restarts for as long as it takes.
func TestCompose_waitsForAServiceThatRestartsUntilReady(t *testing.T) {
	for _, restart := range []string{"always", "on-failure", "unless-stopped"} {
		t.Run(restart, func(t *testing.T) {
			b := newBench(t, compose.Name)

			require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", %q]
    restart: %s
`, failsUntil(time.Now().Add(8*time.Second)), restart)))

			assert.Equal(t, 1, runningContainers(b.containers("web")))
		})
	}
}

func TestCompose_failsOnAServiceThatExitsWithoutRestart(t *testing.T) {
	b := newBench(t, compose.Name)

	err := b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "exit 3"]
    restart: "no"
`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited with code 3")
}

func TestCompose_waitsForAHealthyDependency(t *testing.T) {
	b := newBench(t, compose.Name)
	start := time.Now()

	require.NoError(t, b.install(`
services:
  db:
    image: busybox:1.37
    command: ["sh", "-c", "sleep 4; touch /tmp/healthy; exec sleep 3600"]
    healthcheck:
      test: ["CMD", "test", "-f", "/tmp/healthy"]
      interval: 1s
      retries: 30
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    depends_on:
      db:
        condition: service_healthy
`))

	assert.GreaterOrEqual(t, time.Since(start), 4*time.Second, "web started before db was healthy")
	assert.Equal(t, 1, runningContainers(b.containers("db")))
	assert.Equal(t, 1, runningContainers(b.containers("web")))
}

func TestCompose_waitsForACompletedDependency(t *testing.T) {
	b := newBench(t, compose.Name)

	require.NoError(t, b.install(`
services:
  migrate:
    image: busybox:1.37
    command: ["sh", "-c", "sleep 2"]
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    depends_on:
      migrate:
        condition: service_completed_successfully
`))

	migrate := b.containers("migrate")
	require.Len(t, migrate, 1)
	assert.Equal(t, container.StateExited, migrate[0].State)
	assert.Equal(t, 1, runningContainers(b.containers("web")))
}

func TestCompose_upgradesAService(t *testing.T) {
	b := newBench(t, compose.Name)
	project := `
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    environment:
      COLOR: %s
`
	require.NoError(t, b.install(fmt.Sprintf(project, "blue")))

	require.NoError(t, b.upgrade(fmt.Sprintf(project, "green")))

	containers := b.containers("web")
	require.Len(t, containers, 1, "the old container is gone")
	assert.Contains(t, b.environment(containers[0].ID), "COLOR=green")
}

func TestCompose_keepsVolumesUnlessPurged(t *testing.T) {
	b := newBench(t, compose.Name)
	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    volumes:
      - data:/data
volumes:
  data: {}
`))

	require.NoError(t, b.uninstall(false))

	list, err := b.api.VolumeList(t.Context(), client.VolumeListOptions{Filters: b.byStack()})
	require.NoError(t, err)
	names := make([]string, len(list.Items))
	for i, volume := range list.Items {
		names[i] = volume.Name
	}
	assert.Equal(t, []string{b.name("data")}, names)
}
