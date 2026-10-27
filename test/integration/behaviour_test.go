//go:build integration

package integration

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/installer/compose"
	nemoswarm "github.com/sntns/nemo/pkg/installer/swarm"
	"github.com/sntns/nemo/pkg/progress"
)

// onBoth runs a test on each orchestrator.
func onBoth(t *testing.T, test func(t *testing.T, b *bench)) {
	t.Helper()
	for _, orchestrator := range []string{compose.Name, nemoswarm.Name} {
		t.Run(orchestrator, func(t *testing.T) { test(t, newBench(t, orchestrator)) })
	}
}

// replicas counts the running replicas of a service, whatever the orchestrator.
func (b *bench) replicas(service string) int {
	if b.orchestrator == nemoswarm.Name {
		return running(b.tasks(service))
	}
	return runningContainers(b.containers(service))
}

// gone tells whether nothing is left of a service.
func (b *bench) gone(service string) bool {
	b.t.Helper()
	if b.orchestrator == nemoswarm.Name {
		_, err := b.api.ServiceInspect(b.t.Context(), b.name(service), client.ServiceInspectOptions{})
		return err != nil
	}
	return len(b.containers(service)) == 0
}

// version is what changes when the orchestrator touches a service.
func (b *bench) version(service string) string {
	b.t.Helper()
	if b.orchestrator == nemoswarm.Name {
		return fmt.Sprint(b.service(service).Version.Index)
	}
	ids := []string{}
	for _, summary := range b.containers(service) {
		ids = append(ids, summary.ID)
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func TestBoth_reinstallingTheSameProjectChangesNothing(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		project := `
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    environment:
      COLOR: blue
`
		require.NoError(t, b.install(project))
		before := b.version("web")

		require.NoError(t, b.install(project))

		assert.Equal(t, before, b.version("web"), "the service was left as it was")
	})
}

func TestBoth_upgradeRemovesTheServicesTheProjectDropped(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
  worker:
    image: busybox:1.37
    command: ["sleep", "3600"]
`))

		require.NoError(t, b.upgrade(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
`))

		assert.Eventually(t, func() bool { return b.gone("worker") }, 30*time.Second, time.Second, "worker is gone")
		assert.Equal(t, 1, b.replicas("web"))
	})
}

func TestBoth_upgradeScalesAService(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		project := `
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    deploy:
      replicas: %d
`
		for _, replicas := range []int{1, 3, 1} {
			require.NoError(t, b.upgrade(fmt.Sprintf(project, replicas)), "%d replicas", replicas)
			assert.Equal(t, replicas, b.replicas("web"), "%d replicas", replicas)
		}
	})
}

// The service only runs if its config holds the content of the version it expects.
func TestBoth_upgradeGivesTheNewContentOfAConfig(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		project := `
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "grep -q %[1]s /app.conf || exit 1; exec sleep 3600"]
    deploy:
      restart_policy:
        condition: none
    configs:
      - source: app
        target: /app.conf
configs:
  app:
    content: %[1]s
`
		require.NoError(t, b.install(fmt.Sprintf(project, "first")))

		require.NoError(t, b.upgrade(fmt.Sprintf(project, "second")))

		assert.Equal(t, 1, b.replicas("web"))
		if b.orchestrator == nemoswarm.Name {
			configs, err := b.api.ConfigList(t.Context(), client.ConfigListOptions{Filters: b.byStack()})
			require.NoError(t, err)
			assert.Len(t, configs.Items, 1, "the config of the first version is gone")
		}
	})
}

func TestBoth_failsOnAnImageThatDoesNotExist(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		b.timeout = 30 * time.Second

		err := b.install(`
services:
  web:
    image: busybox:nemo-bench-does-not-exist
    command: ["sleep", "3600"]
`)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "busybox:nemo-bench-does-not-exist", "the error names the image")
	})
}

func TestBoth_failsOnAnUnhealthyService(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		b.timeout = 45 * time.Second

		err := b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    deploy:
      restart_policy:
        condition: none
    healthcheck:
      test: ["CMD", "false"]
      interval: 1s
      retries: 2
`)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unhealthy", "the error says the service is unhealthy")
	})
}

func TestBoth_failsWhenADependencyFails(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		err := b.install(`
services:
  migrate:
    image: busybox:1.37
    command: ["sh", "-c", "exit 4"]
    deploy:
      mode: replicated-job
      restart_policy:
        condition: none
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    depends_on:
      migrate:
        condition: service_completed_successfully
`)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "migrate")
		assert.True(t, b.gone("web"), "web is not started after its dependency failed")
	})
}

func TestSwarm_startsAServiceOnceItsDependencyIsHealthy(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

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

	var healthy time.Time
	for _, task := range b.tasks("db") {
		if task.Status.State == swarm.TaskStateRunning {
			healthy = task.Status.Timestamp
		}
	}
	require.False(t, healthy.IsZero())
	assert.False(t, b.service("web").CreatedAt.Before(healthy), "web was created after db ran healthy")
}

func TestSwarm_rejectsWhatSwarmCannotRun(t *testing.T) {
	for field, project := range map[string]string{
		"privileged":   "    privileged: true",
		"devices":      "    devices: [/dev/null]",
		"links":        "    links: [db]",
		"network_mode": "    network_mode: host",
		"ipc":          "    ipc: host",
		"pid":          "    pid: host",
		"shm_size":     "    shm_size: 64m",
		"security_opt": "    security_opt: [no-new-privileges]",
		"restart":      "    restart: always",
		"cpus":         `    cpus: "0.5"`,
		"tmpfs[0]":     "    tmpfs: [/run:size=1m]",
	} {
		t.Run(field, func(t *testing.T) {
			b := newBench(t, nemoswarm.Name)

			err := b.validate("services:\n  web:\n    image: busybox:1.37\n" + project + "\n  db:\n    image: busybox:1.37\n")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "services.web."+field)
		})
	}
}

func TestSwarm_rejectsAServiceNameLongerThanSwarmAllows(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	name := strings.Repeat("s", 60)

	err := b.validate(fmt.Sprintf("services:\n  %s:\n    image: busybox:1.37\n", name))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Swarm allows 63")
}

func TestCompose_rejectsWhatComposeCannotRun(t *testing.T) {
	for field, project := range map[string]string{
		"container_name": "    container_name: web",
		"links":          "    links: [db]",
		"gpus":           "    gpus: all",
		"deploy.mode":    "    deploy:\n      mode: global",
	} {
		t.Run(field, func(t *testing.T) {
			b := newBench(t, compose.Name)

			err := b.validate("services:\n  web:\n    image: busybox:1.37\n" + project + "\n  db:\n    image: busybox:1.37\n")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "services.web."+field)
		})
	}
}

func TestCompose_warnsAboutTheSwarmSettingsItIgnores(t *testing.T) {
	b := newBench(t, compose.Name)

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    deploy:
      labels:
        bench: "yes"
      placement:
        constraints: [node.role == manager]
      update_config:
        parallelism: 2
`))

	var warnings []string
	for _, event := range b.eventsOf() {
		if event.State == progress.Warning {
			warnings = append(warnings, event.Item+": "+event.Message)
		}
	}
	assert.Len(t, warnings, 3, "a warning for each setting compose ignores: %v", warnings)
}

// compose shares a service's network namespace with another, as docker
// compose does.
func TestCompose_sharesTheNetworkOfAnotherService(t *testing.T) {
	b := newBench(t, compose.Name)

	// sidecar reaches web's httpd on its own localhost, or exits.
	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["httpd", "-f", "-p", "8080", "-h", "/etc"]
  sidecar:
    image: busybox:1.37
    command: ["sh", "-c", "sleep 1; wget -q -O /dev/null http://127.0.0.1:8080/hostname || exit 1; exec sleep 3600"]
    network_mode: service:web
    deploy:
      restart_policy:
        condition: none
`))

	assert.Equal(t, 1, runningContainers(b.containers("sidecar")))
}
