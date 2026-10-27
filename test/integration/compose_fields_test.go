//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/installer/compose"
)

func (b *bench) inspect(service string) container.InspectResponse {
	b.t.Helper()
	containers := b.containers(service)
	require.Len(b.t, containers, 1, "containers of %s", service)
	inspected, err := b.api.ContainerInspect(b.t.Context(), containers[0].ID, client.ContainerInspectOptions{})
	require.NoError(b.t, err)
	return inspected.Container
}

func TestCompose_carriesTheContainerFields(t *testing.T) {
	b := newBench(t, compose.Name)

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
    tmpfs: ["/run/cache:size=1m"]
    restart: unless-stopped
    cpus: "0.5"
    mem_limit: 64m
    mem_reservation: 16m
    pids_limit: 100
    logging:
      driver: json-file
      options:
        max-size: 1m
    healthcheck:
      test: ["CMD", "true"]
      interval: 2s
      timeout: 1s
      retries: 3
      start_period: 1s
`))

	inspected := b.inspect("web")
	config, host := inspected.Config, inspected.HostConfig
	assert.Equal(t, []string{"sh", "-c"}, []string(config.Entrypoint))
	assert.Equal(t, []string{"exec sleep 3600"}, []string(config.Cmd))
	assert.Contains(t, config.Env, "COLOR=blue")
	assert.Equal(t, "/tmp", config.WorkingDir)
	assert.Equal(t, "1000:1000", config.User)
	assert.Equal(t, "webhost", config.Hostname)
	assert.Equal(t, "yes", config.Labels["bench.container"])
	assert.Equal(t, "SIGINT", config.StopSignal)
	if assert.NotNil(t, config.StopTimeout) {
		assert.Equal(t, 7, *config.StopTimeout)
	}
	assert.True(t, config.Tty)
	assert.True(t, config.OpenStdin)
	if assert.NotNil(t, config.Healthcheck) {
		assert.Equal(t, []string{"CMD", "true"}, config.Healthcheck.Test)
		assert.Equal(t, 2*time.Second, config.Healthcheck.Interval)
		assert.Equal(t, 3, config.Healthcheck.Retries)
	}
	assert.True(t, host.ReadonlyRootfs)
	if assert.NotNil(t, host.Init) {
		assert.True(t, *host.Init)
	}
	assert.Equal(t, []string{"CAP_NET_ADMIN"}, []string(host.CapAdd), "the daemon names them so")
	assert.Equal(t, []string{"CAP_MKNOD"}, []string(host.CapDrop))
	assert.Equal(t, map[string]string{"net.ipv4.ip_unprivileged_port_start": "0"}, host.Sysctls)
	assert.Equal(t, []*container.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}}, host.Ulimits)
	assert.Equal(t, []string{"100"}, host.GroupAdd)
	assert.Equal(t, []string{"db.local:10.0.0.9"}, host.ExtraHosts)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("1.1.1.1")}, host.DNS)
	assert.Equal(t, []string{"example.internal"}, host.DNSSearch)
	assert.Equal(t, []string{"ndots:2"}, host.DNSOptions)
	assert.Equal(t, map[string]string{"/run/cache": "size=1m"}, host.Tmpfs)
	assert.Equal(t, container.RestartPolicyUnlessStopped, host.RestartPolicy.Name)
	assert.Equal(t, int64(500_000_000), host.NanoCPUs)
	assert.Equal(t, int64(64<<20), host.Memory)
	assert.Equal(t, int64(16<<20), host.MemoryReservation)
	if assert.NotNil(t, host.PidsLimit) {
		assert.Equal(t, int64(100), *host.PidsLimit)
	}
	assert.Equal(t, "json-file", host.LogConfig.Type)
	assert.Equal(t, map[string]string{"max-size": "1m"}, host.LogConfig.Config)
	assert.Equal(t, 1, runningContainers(b.containers("web")))
}

func TestCompose_publishesPortsAndJoinsNetworks(t *testing.T) {
	b := newBench(t, compose.Name)
	outside := b.externalNetwork()
	short, long := freePort(t), freePort(t)

	// check only runs if it resolves web by its alias on front, has its address there, and joined outside.
	require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["httpd", "-f", "-p", "8080", "-h", "/etc"]
    ports:
      - "127.0.0.1:%d:8080"
      - target: 8080
        published: %d
        host_ip: 127.0.0.1
    expose: ["9000"]
    networks:
      front:
        aliases: [www]
      back: {}
  check:
    image: busybox:1.37
    command: ["sh", "-c", "nslookup www && ip -o addr | grep -q 10.232.0.10 || exit 1; exec sleep 3600"]
    depends_on: [web]
    restart: "no"
    networks:
      front:
        ipv4_address: 10.232.0.10
      outside: {}
networks:
  front:
    ipam:
      config:
        - subnet: 10.232.0.0/24
  back:
    internal: true
    labels:
      bench.network: "yes"
  outside:
    external: true
    name: %s
`, short, long, outside)))

	assert.Equal(t, 1, runningContainers(b.containers("check")), "check resolved www and took its address")
	web := b.inspect("web")
	assert.Contains(t, web.Config.ExposedPorts, network.MustParsePort("9000/tcp"))
	assert.Contains(t, web.NetworkSettings.Networks, b.name("front"))
	assert.Contains(t, web.NetworkSettings.Networks[b.name("front")].Aliases, "www")
	assert.Contains(t, b.inspect("check").NetworkSettings.Networks, outside)

	back, err := b.api.NetworkInspect(t.Context(), b.name("back"), client.NetworkInspectOptions{})
	require.NoError(t, err)
	assert.True(t, back.Network.Internal)
	assert.Equal(t, "yes", back.Network.Labels["bench.network"])

	for _, port := range []int{short, long} {
		response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hostname", port))
		require.NoError(t, err, "port %d", port)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode, "port %d: %s", port, body)
	}
}

func TestCompose_mountsVolumes(t *testing.T) {
	b := newBench(t, compose.Name)
	outside := b.externalVolume()

	require.NoError(t, b.install(fmt.Sprintf(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "touch /data/x /cache/x /scratch/x /outside/x && ! touch /host/x 2>/dev/null || exit 1; exec sleep 3600"]
    restart: "no"
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

	assert.Equal(t, 1, runningContainers(b.containers("web")))
	mounts := map[string]mount.Mount{}
	for _, mounted := range b.inspect("web").HostConfig.Mounts {
		mounts[mounted.Target] = mounted
	}
	assert.Equal(t, b.name("data"), mounts["/data"].Source)
	assert.Equal(t, mount.TypeTmpfs, mounts["/scratch"].Type)
	assert.Equal(t, mount.TypeBind, mounts["/host"].Type)
	assert.True(t, mounts["/host"].ReadOnly)
	assert.Equal(t, outside, mounts["/outside"].Source)

	cache, err := b.api.VolumeInspect(t.Context(), b.name("cache"), client.VolumeInspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "yes", cache.Volume.Labels["bench.volume"])
}

func TestCompose_providesConfigsAndSecrets(t *testing.T) {
	b := newBench(t, compose.Name)
	b.credentials = map[string]string{"TOKEN": "s3cret"}

	// The container exits unless both hold their content, with the mode and the owner asked.
	require.NoError(t, b.install(`
services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "grep -q hello /etc/app.conf && grep -q s3cret /run/secrets/token && [ \"$$(stat -c %a:%u /etc/app.conf)\" = 440:1000 ] || exit 1; exec sleep 3600"]
    restart: "no"
    configs:
      - source: app
        target: /etc/app.conf
        uid: "1000"
        mode: 0440
    secrets:
      - token
configs:
  app:
    content: hello
secrets:
  token:
    environment: TOKEN
`))

	assert.Equal(t, 1, runningContainers(b.containers("web")))
}
