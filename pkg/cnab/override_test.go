package cnab

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateway is a bundle's compose: one proxy, with a command, an environment
// and a certificate.
func gateway() Compose {
	return Compose{Documents: []ComposeDocument{{Name: "compose.yaml", Config: map[string]any{
		"services": map[string]any{"proxy": map[string]any{
			"image":       "traefik:3",
			"command":     []any{"--ping", "--api"},
			"environment": map[string]any{"LOG_LEVEL": "info", "TZ": "UTC"},
			"secrets":     []any{"certificate"},
		}},
		"secrets": map[string]any{"certificate": map[string]any{"environment": "CERTIFICATE"}},
	}}}}
}

func TestOverrideSources(t *testing.T) {
	t.Parallel()
	sources, err := OverrideSources(`
services:
  proxy:
    environment: { ACME_KEY: "${NOT_A_SOURCE}" }
    secrets: !reset []
secrets:
  acme-secret: { environment: ACME_SECRET }
  pinned: { content: inline }
configs:
  acme: { environment: ACME_CONFIG }
  again: { environment: ACME_SECRET }
`)
	require.NoError(t, err)
	assert.Equal(t, []string{"ACME_CONFIG", "ACME_SECRET"}, sources, "the environments of its secrets and configs, once each, and nothing else")

	sources, err = OverrideSources("  \n")
	require.NoError(t, err)
	assert.Empty(t, sources, "a blank override is none")

	_, err = OverrideSources("- a list")
	require.ErrorContains(t, err, "a compose document")
	_, err = OverrideSources("services: [")
	require.ErrorContains(t, err, "override: ")
}

func TestCompose_CheckOverride(t *testing.T) {
	t.Parallel()
	require.NoError(t, gateway().CheckOverride(`
services:
  proxy:
    environment: { LOG_LEVEL: debug }
    ports: ["443:443"]
volumes:
  acme: {}
`))
	err := gateway().CheckOverride(`
services:
  proxy:
    image: traefik:4
  sidecar:
    image: busybox
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.proxy.image: the bundle pins its images")
	assert.Contains(t, err.Error(), "services.sidecar is not a service of the bundle")
	assert.ErrorContains(t, gateway().CheckOverride("services: { proxy: { build: . } }"), "services.proxy.build")
}

func TestCompose_LoadFusesTheOverride(t *testing.T) {
	t.Parallel()
	project, err := gateway().Load(context.Background(), "demo", "/", map[string]string{}, `
services:
  proxy:
    command: ["--ping"]
    environment:
      LOG_LEVEL: debug
      ACME_PROVIDER: ovh
    secrets: !override [acme-secret]
secrets:
  acme-secret: { environment: ACME_SECRET }
`)
	require.NoError(t, err)
	proxy := project.Services["proxy"]
	assert.Equal(t, "traefik:3", proxy.Image)
	assert.Equal(t, []string{"--ping"}, []string(proxy.Command), "a command is replaced whole")
	assert.Equal(t, "debug", *proxy.Environment["LOG_LEVEL"], "an environment is merged key by key")
	assert.Equal(t, "UTC", *proxy.Environment["TZ"])
	assert.Equal(t, "ovh", *proxy.Environment["ACME_PROVIDER"])
	require.Len(t, proxy.Secrets, 1, "!override replaces the bundle's list")
	assert.Equal(t, "acme-secret", proxy.Secrets[0].Source)
	assert.Contains(t, project.Secrets, "acme-secret")

	project, err = gateway().Load(context.Background(), "demo", "/", map[string]string{}, "services: { proxy: { secrets: !reset [] } }")
	require.NoError(t, err)
	assert.Empty(t, project.Services["proxy"].Secrets, "!reset removes the bundle's list")
}
