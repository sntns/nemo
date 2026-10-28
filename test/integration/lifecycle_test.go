//go:build integration

package integration

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nemoswarm "github.com/sntns/nemo/pkg/installer/swarm"
)

// converged checks the invariants of an installation that succeeded: each
// service runs its replicas, and installing the same project again changes
// nothing.
func (b *bench) converged(project string, replicas map[string]int) {
	b.t.Helper()
	versions := map[string]string{}
	for _, service := range slices.Sorted(maps.Keys(replicas)) {
		assert.Equal(b.t, replicas[service], b.replicas(service), "replicas of %s", service)
		versions[service] = b.version(service)
	}
	require.NoError(b.t, b.install(project), "installing the same project again")
	for service, version := range versions {
		assert.Equal(b.t, version, b.version(service), "installing the same project again changed %s", service)
	}
}

// An application with its database and its migration: the migration waits
// for a healthy database, the application for both, and an upgrade changes
// its configuration, scales it and adds a worker.
func TestBoth_lifecycleOfAnApplicationWithItsDatabase(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		project := func(message string, replicas int, worker bool) string {
			workerService := ""
			if worker {
				workerService = `
  worker:
    image: busybox:1.37
    command: ["sh", "-c", "grep -q db-ready /data/marker || exit 1; exec sleep 3600"]
    volumes:
      - data:/data
    depends_on:
      migrate:
        condition: service_completed_successfully
    deploy:
      restart_policy:
        condition: on-failure
        delay: 1s`
			}
			return fmt.Sprintf(`
services:
  db:
    image: busybox:1.37
    command: ["sh", "-c", "sleep 3; echo db-ready > /data/marker; exec sleep 3600"]
    volumes:
      - data:/data
    healthcheck:
      test: ["CMD", "test", "-f", "/data/marker"]
      interval: 1s
      retries: 30
  migrate:
    image: busybox:1.37
    command: ["sh", "-c", "grep -q db-ready /data/marker"]
    volumes:
      - data:/data
    depends_on:
      db:
        condition: service_healthy
    deploy:
      mode: %s
      restart_policy:
        condition: none
  web:
    image: busybox:1.37
    command: ["sh", "-c", "grep -q %s /app.conf || exit 1; exec sleep 3600"]
    configs:
      - source: app
        target: /app.conf
    depends_on:
      migrate:
        condition: service_completed_successfully
      db:
        condition: service_healthy
    deploy:
      replicas: %d
      restart_policy:
        condition: on-failure
        delay: 1s%s
configs:
  app:
    content: %s
volumes:
  data: {}
`, b.job(), message, replicas, workerService, message)
		}

		first := project("first", 1, false)
		require.NoError(t, b.install(first))
		b.converged(first, map[string]int{"db": 1, "web": 1})

		second := project("second", 2, true)
		require.NoError(t, b.upgrade(second))
		b.converged(second, map[string]int{"db": 1, "web": 2, "worker": 1})

		require.NoError(t, b.uninstall(false))
		require.NoError(t, b.install(second), "installing again on the volume the uninstall kept")
		b.converged(second, map[string]int{"db": 1, "web": 2, "worker": 1})
	})
}

// Services that exit until the broker they need is up, on a network they
// share, as manta's services do with NATS: the installation waits for all of
// them, then an upgrade rotates the secret they read.
func TestBoth_lifecycleOfServicesWaitingForTheirBroker(t *testing.T) {
	onBoth(t, func(t *testing.T, b *bench) {
		project := fmt.Sprintf(`
services:
  broker:
    image: busybox:1.37
    command: ["sh", "-c", %[1]q]
    networks: [mesh]
  api:
    image: busybox:1.37
    command: ["sh", "-c", %[2]q]
    secrets: [token]
    networks: [mesh]
    deploy:
      restart_policy:
        condition: any
        delay: 2s
  worker:
    image: busybox:1.37
    command: ["sh", "-c", %[2]q]
    secrets: [token]
    networks: [mesh]
    deploy:
      restart_policy:
        condition: any
        delay: 2s
networks:
  mesh: {}
secrets:
  token:
    environment: TOKEN
`, "sleep 4; exec httpd -f -p 4222 -h /etc", "wget -q -O /dev/null http://broker:4222/hostname && test -s /run/secrets/token || exit 1; exec sleep 3600")
		b.credentials = map[string]string{"TOKEN": "first"}
		require.NoError(t, b.install(project))
		b.converged(project, map[string]int{"broker": 1, "api": 1, "worker": 1})

		b.credentials = map[string]string{"TOKEN": "second"}
		require.NoError(t, b.upgrade(project))
		assert.Equal(t, 1, b.replicas("api"))
		if b.orchestrator == nemoswarm.Name {
			secrets := b.secretNames()
			assert.Len(t, secrets, 1, "the secret of the first token is gone: %v", secrets)
		}
	})
}
