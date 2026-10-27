//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nemoswarm "github.com/sntns/nemo/pkg/installer/swarm"
)

func TestSwarm_carriesTheContainerFields(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    entrypoint: ["sh", "-c"]
    command: ["exec sleep 3600"]
    environment:
      COLOR: blue
    working_dir: /tmp
    user: "1000:1000"
    hostname: webhost
    labels:
      bench.container: "yes"
    stop_signal: SIGINT
    stop_grace_period: 7s
    read_only: true
    tty: true
    stdin_open: true
    init: true
    cap_add: [NET_ADMIN]
    cap_drop: [MKNOD]
    sysctls:
      net.ipv4.ip_unprivileged_port_start: "0"
    ulimits:
      nofile:
        soft: 1024
        hard: 2048
    group_add: ["100"]
    extra_hosts:
      - db.local=10.0.0.9
    dns: [1.1.1.1]
    dns_search: [example.internal]
    dns_opt: ["ndots:2"]
    tmpfs: [/run/cache]
    healthcheck:
      test: ["CMD", "true"]
      interval: 2s
      timeout: 1s
      retries: 3
      start_period: 1s
`))

	spec := b.service("web").Spec.TaskTemplate.ContainerSpec
	assert.Equal(t, []string{"sh", "-c"}, spec.Command, "entrypoint")
	assert.Equal(t, []string{"exec sleep 3600"}, spec.Args, "command")
	assert.Contains(t, spec.Env, "COLOR=blue")
	assert.Equal(t, "/tmp", spec.Dir)
	assert.Equal(t, "1000:1000", spec.User)
	assert.Equal(t, "webhost", spec.Hostname)
	assert.Equal(t, "yes", spec.Labels["bench.container"])
	assert.Equal(t, "SIGINT", spec.StopSignal)
	if assert.NotNil(t, spec.StopGracePeriod) {
		assert.Equal(t, 7*time.Second, *spec.StopGracePeriod)
	}
	assert.True(t, spec.ReadOnly)
	assert.True(t, spec.TTY)
	assert.True(t, spec.OpenStdin)
	if assert.NotNil(t, spec.Init) {
		assert.True(t, *spec.Init)
	}
	assert.Equal(t, []string{"NET_ADMIN"}, spec.CapabilityAdd)
	assert.Equal(t, []string{"MKNOD"}, spec.CapabilityDrop)
	assert.Equal(t, map[string]string{"net.ipv4.ip_unprivileged_port_start": "0"}, spec.Sysctls)
	assert.Equal(t, []*container.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}}, spec.Ulimits)
	assert.Equal(t, []string{"100"}, spec.Groups)
	assert.Equal(t, []string{"10.0.0.9 db.local"}, spec.Hosts)
	if assert.NotNil(t, spec.DNSConfig) {
		assert.Equal(t, []netip.Addr{netip.MustParseAddr("1.1.1.1")}, spec.DNSConfig.Nameservers)
		assert.Equal(t, []string{"example.internal"}, spec.DNSConfig.Search)
		assert.Equal(t, []string{"ndots:2"}, spec.DNSConfig.Options)
	}
	assert.Contains(t, spec.Mounts, mount.Mount{Type: mount.TypeTmpfs, Target: "/run/cache"})
	if assert.NotNil(t, spec.Healthcheck) {
		assert.Equal(t, []string{"CMD", "true"}, spec.Healthcheck.Test)
		assert.Equal(t, 2*time.Second, spec.Healthcheck.Interval)
		assert.Equal(t, time.Second, spec.Healthcheck.Timeout)
		assert.Equal(t, 3, spec.Healthcheck.Retries)
		assert.Equal(t, time.Second, spec.Healthcheck.StartPeriod)
	}
	assert.Equal(t, 1, running(b.tasks("web")))
}

func TestSwarm_carriesTheDeployFields(t *testing.T) {
	b := newBench(t, nemoswarm.Name)

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    logging:
      driver: json-file
      options:
        max-size: 1m
    deploy:
      replicas: 2
      labels:
        bench.service: "yes"
      placement:
        constraints: [node.role == manager]
        max_replicas_per_node: 2
      resources:
        limits:
          cpus: "0.5"
          memory: 64M
          pids: 100
        reservations:
          cpus: "0.1"
          memory: 16M
      restart_policy:
        condition: on-failure
        delay: 3s
        max_attempts: 4
        window: 20s
      update_config:
        parallelism: 2
        delay: 1s
        failure_action: rollback
        monitor: 2s
        max_failure_ratio: 0.5
        order: start-first
      rollback_config:
        parallelism: 1
        order: stop-first
`))

	spec := b.service("web").Spec
	if assert.NotNil(t, spec.Mode.Replicated) && assert.NotNil(t, spec.Mode.Replicated.Replicas) {
		assert.Equal(t, uint64(2), *spec.Mode.Replicated.Replicas)
	}
	assert.Equal(t, "yes", spec.Labels["bench.service"])
	task := spec.TaskTemplate
	if assert.NotNil(t, task.Placement) {
		assert.Equal(t, []string{"node.role == manager"}, task.Placement.Constraints)
		assert.Equal(t, uint64(2), task.Placement.MaxReplicas)
	}
	if assert.NotNil(t, task.Resources) && assert.NotNil(t, task.Resources.Limits) && assert.NotNil(t, task.Resources.Reservations) {
		assert.Equal(t, int64(500_000_000), task.Resources.Limits.NanoCPUs)
		assert.Equal(t, int64(64<<20), task.Resources.Limits.MemoryBytes)
		assert.Equal(t, int64(100), task.Resources.Limits.Pids)
		assert.Equal(t, int64(100_000_000), task.Resources.Reservations.NanoCPUs)
		assert.Equal(t, int64(16<<20), task.Resources.Reservations.MemoryBytes)
	}
	if assert.NotNil(t, task.RestartPolicy) {
		assert.Equal(t, swarm.RestartPolicyConditionOnFailure, task.RestartPolicy.Condition)
		assert.Equal(t, 3*time.Second, *task.RestartPolicy.Delay)
		assert.Equal(t, uint64(4), *task.RestartPolicy.MaxAttempts)
		assert.Equal(t, 20*time.Second, *task.RestartPolicy.Window)
	}
	if assert.NotNil(t, task.LogDriver) {
		assert.Equal(t, "json-file", task.LogDriver.Name)
		assert.Equal(t, map[string]string{"max-size": "1m"}, task.LogDriver.Options)
	}
	if assert.NotNil(t, spec.UpdateConfig) {
		assert.Equal(t, uint64(2), spec.UpdateConfig.Parallelism)
		assert.Equal(t, time.Second, spec.UpdateConfig.Delay)
		assert.Equal(t, swarm.UpdateFailureActionRollback, spec.UpdateConfig.FailureAction)
		assert.Equal(t, 2*time.Second, spec.UpdateConfig.Monitor)
		assert.InDelta(t, 0.5, spec.UpdateConfig.MaxFailureRatio, 0.001)
		assert.Equal(t, swarm.UpdateOrderStartFirst, spec.UpdateConfig.Order)
	}
	if assert.NotNil(t, spec.RollbackConfig) {
		assert.Equal(t, uint64(1), spec.RollbackConfig.Parallelism)
		assert.Equal(t, swarm.UpdateOrderStopFirst, spec.RollbackConfig.Order)
	}
	assert.Equal(t, 2, running(b.tasks("web")))
}

func TestSwarm_publishesPortsAndJoinsNetworks(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	outside := b.externalNetwork()
	ingress, host := freePort(t), freePort(t)

	// check only runs if it resolves web by its alias on front, and joined the external network.
	require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["httpd", "-f", "-p", "8080", "-h", "/etc"]
    ports:
      - target: 8080
        published: %d
        mode: ingress
      - target: 8081
        published: %d
        mode: host
    networks:
      front:
        aliases: [www]
      back: {}
  check:
    image: busybox:1.37
    command: ["sh", "-c", "nslookup www && ip -o addr | grep -q 10.231. || exit 1; exec sleep 3600"]
    depends_on: [web]
    deploy:
      endpoint_mode: dnsrr
      restart_policy:
        condition: none
    networks:
      front: {}
      outside: {}
networks:
  front:
    driver: overlay
    attachable: true
    driver_opts:
      com.docker.network.driver.mtu: "1400"
    ipam:
      config:
        - subnet: 10.231.0.0/24
  back:
    labels:
      bench.network: "yes"
  outside:
    external: true
    name: %s
`, ingress, host, outside)))

	web := b.service("web")
	if assert.NotNil(t, web.Spec.EndpointSpec) {
		assert.ElementsMatch(t, []swarm.PortConfig{
			{Protocol: network.TCP, TargetPort: 8080, PublishedPort: uint32(ingress), PublishMode: swarm.PortConfigPublishModeIngress},
			{Protocol: network.TCP, TargetPort: 8081, PublishedPort: uint32(host), PublishMode: swarm.PortConfigPublishModeHost},
		}, web.Spec.EndpointSpec.Ports)
	}
	check := b.service("check")
	if assert.NotNil(t, check.Spec.EndpointSpec) {
		assert.Equal(t, swarm.ResolutionModeDNSRR, check.Spec.EndpointSpec.Mode)
	}
	assert.Equal(t, 1, running(b.tasks("check")), "check resolved www and joined front")

	front, err := b.api.NetworkInspect(t.Context(), b.name("front"), client.NetworkInspectOptions{})
	require.NoError(t, err)
	assert.True(t, front.Network.Attachable)
	assert.Equal(t, "1400", front.Network.Options["com.docker.network.driver.mtu"])
	back, err := b.api.NetworkInspect(t.Context(), b.name("back"), client.NetworkInspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "yes", back.Network.Labels["bench.network"])

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hostname", ingress))
		if !assert.NoError(c, err) {
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		assert.Equal(c, http.StatusOK, response.StatusCode, string(body))
	}, 30*time.Second, time.Second, "the ingress routes the published port to web")
}

func TestSwarm_mountsVolumes(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	outside := b.externalVolume()

	// The service only runs if each mount is there, and the bind one is read-only.
	require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "touch /data/x /cache/x /scratch/x /outside/x && ! touch /host/x 2>/dev/null || exit 1; exec sleep 3600"]
    volumes:
      - data:/data
      - type: volume
        source: cache
        target: /cache
        volume:
          nocopy: true
      - type: tmpfs
        target: /scratch
        tmpfs:
          size: 1000000
      - type: bind
        source: /tmp
        target: /host
        read_only: true
      - outside:/outside
    deploy:
      restart_policy:
        condition: none
volumes:
  data:
    driver_opts:
      type: tmpfs
      device: tmpfs
  cache:
    labels:
      bench.volume: "yes"
  outside:
    external: true
    name: %s
`, outside)))

	assert.Equal(t, 1, running(b.tasks("web")))
	mounts := map[string]mount.Mount{}
	for _, mounted := range b.service("web").Spec.TaskTemplate.ContainerSpec.Mounts {
		mounts[mounted.Target] = mounted
	}
	assert.Equal(t, b.name("data"), mounts["/data"].Source)
	if assert.NotNil(t, mounts["/cache"].VolumeOptions) {
		assert.True(t, mounts["/cache"].VolumeOptions.NoCopy)
	}
	assert.Equal(t, mount.TypeTmpfs, mounts["/scratch"].Type)
	assert.Equal(t, mount.TypeBind, mounts["/host"].Type)
	assert.True(t, mounts["/host"].ReadOnly)
	assert.Equal(t, outside, mounts["/outside"].Source)

	cache, err := b.api.VolumeInspect(t.Context(), b.name("cache"), client.VolumeInspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "yes", cache.Volume.Labels["bench.volume"])
}

func TestSwarm_carriesConfigAndSecretFileModes(t *testing.T) {
	b := newBench(t, nemoswarm.Name)
	b.credentials = map[string]string{"TOKEN": "s3cret"}

	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sleep", "3600"]
    configs:
      - source: app
        target: /etc/app.conf
        uid: "1000"
        gid: "1000"
        mode: 0440
    secrets:
      - source: token
        target: api-token
        mode: 0400
configs:
  app:
    content: hello
secrets:
  token:
    environment: TOKEN
`))

	spec := b.service("web").Spec.TaskTemplate.ContainerSpec
	require.Len(t, spec.Configs, 1)
	if assert.NotNil(t, spec.Configs[0].File) {
		assert.Equal(t, swarm.ConfigReferenceFileTarget{Name: "/etc/app.conf", UID: "1000", GID: "1000", Mode: 0o440}, *spec.Configs[0].File)
	}
	require.Len(t, spec.Secrets, 1)
	if assert.NotNil(t, spec.Secrets[0].File) {
		assert.Equal(t, "api-token", spec.Secrets[0].File.Name)
		assert.Equal(t, os.FileMode(0o400), spec.Secrets[0].File.Mode)
	}
}
