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

	nemoswarm "github.com/sntns/nemo/pkg/installer/swarm"
)

func running(tasks []swarm.Task) int {
	count := 0
	for _, task := range tasks {
		if task.Status.State == swarm.TaskStateRunning && task.DesiredState == swarm.TaskStateRunning {
			count++
		}
	}
	return count
}

func TestSwarm_installsAService(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    deploy:
      replicas: 2
`))

	assert.Equal(t, 2, running(b.tasks("web")))
}

// A service that exits until its dependency is ready, and that its restart
// policy restarts for as long as it takes, as manta's services do.
func TestSwarm_waitsForAServiceThatRestartsUntilReady(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

	require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", %q]
    deploy:
      restart_policy:
        condition: any
        delay: 2s
`, failsUntil(time.Now().Add(12*time.Second)))))

	tasks := b.tasks("web")
	assert.Equal(t, 1, running(tasks))
	assert.Greater(t, len(tasks), 1, "the service restarted before it ran")
}

func TestSwarm_failsOnceTheRestartPolicyGivesUp(t *testing.T) {
	for name, policy := range map[string]string{
		"max attempts reached": "condition: on-failure\n        delay: 1s\n        max_attempts: 2",
		"never restarted":      "condition: none",
	} {
		t.Run(name, func(t *testing.T) {
			b := newBench(t, nemoswarm.Name)

			err := b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "exit 1"]
    deploy:
      restart_policy:
        %s
`, policy))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "stopped restarting")
		})
	}
}

func TestSwarm_waitsForTheHealthcheck(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	start := time.Now()

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "sleep 5; touch /tmp/healthy; exec sleep 3600"]
    healthcheck:
      test: ["CMD", "test", "-f", "/tmp/healthy"]
      interval: 1s
      retries: 30
`))

	assert.GreaterOrEqual(t, time.Since(start), 5*time.Second, "install returned before the service was healthy")
	assert.Equal(t, 1, running(b.tasks("web")))
}

func TestSwarm_runsJobsAndGlobalServices(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

	require.NoError(t, b.install(`
services:
  migrate:
    image: busybox:1.37
    command: ["true"]
    deploy:
      mode: replicated-job
  agent:
    image: busybox:1.37
    command: ["sleep", "3600"]
    deploy:
      mode: global
`))

	completed := slices.ContainsFunc(b.tasks("migrate"), func(task swarm.Task) bool { return task.Status.State == swarm.TaskStateComplete })
	assert.True(t, completed, "the job completed")
	assert.Equal(t, 1, running(b.tasks("agent")), "one agent on the only node")
}

func TestSwarm_upgradesAService(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
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

	assert.Contains(t, b.service("web").Spec.TaskTemplate.ContainerSpec.Env, "COLOR=green")
	assert.Equal(t, 1, running(b.tasks("web")))
}

func TestSwarm_rollsBackAFailingUpdate(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	project := `
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", %q]
    deploy:
      update_config:
        failure_action: rollback
        monitor: 3s
      restart_policy:
        condition: on-failure
        delay: 1s
`
	require.NoError(t, b.install(fmt.Sprintf(project, "exec sleep 3600")))

	err := b.upgrade(fmt.Sprintf(project, "exit 1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rolled the update back")
	assert.Equal(t, []string{"sh", "-c", "exec sleep 3600"}, b.service("web").Spec.TaskTemplate.ContainerSpec.Args, "the previous spec is back")
}

func TestSwarm_providesConfigsAndSecrets(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	b.credentials = map[string]string{"TOKEN": "s3cret"}

	// The service exits, and is not restarted, unless both hold their content.
	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "grep -q hello /app.conf && grep -q s3cret /run/secrets/token || exit 1; exec sleep 3600"]
    configs:
      - source: app
        target: /app.conf
    secrets:
      - token
    deploy:
      restart_policy:
        condition: none
configs:
  app:
    content: hello
secrets:
  token:
    environment: TOKEN
`))

	assert.Equal(t, 1, running(b.tasks("web")))
}

func TestSwarm_keepsVolumesUnlessPurged(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
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

func TestSwarm_reportsWhyATaskRestarts(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

	require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", %q]
    deploy:
      restart_policy:
        condition: any
        delay: 1s
`, failsUntil(time.Now().Add(6*time.Second)))))

	restarting := slices.ContainsFunc(b.messages("web"), func(message string) bool { return strings.HasPrefix(message, "restarting: ") })
	assert.True(t, restarting, "the progress said why the task restarted: %v", b.messages("web"))
}
