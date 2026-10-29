package installer

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
)

// withCompose replaces the bundle's compose documents.
func withCompose(documents ...map[string]any) func(cnabBundle *bundle.Bundle) {
	return func(bundle *bundle.Bundle) {
		var compose []any
		for _, document := range documents {
			compose = append(compose, map[string]any{"config": document})
		}
		bundle.Custom[cnab.ComposeExtension.Key] = map[string]any{"schemaVersion": "1.0.0", "compose": compose}
	}
}

func TestLoadProject(t *testing.T) {
	t.Parallel()

	cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
		withCompose(
			map[string]any{
				"services": map[string]any{
					"web": map[string]any{
						"image":       "nginx:${TAG}",
						"environment": map[string]any{"REGION": "${REGION}"},
						"depends_on":  []any{"db"},
					},
					"db": map[string]any{"image": "postgres:17"},
				},
				"configs": map[string]any{
					"region": map[string]any{"environment": "REGION"},
					"page":   map[string]any{"file": "./page.html"},
				},
				"secrets": map[string]any{
					"token": map[string]any{"environment": "TOKEN"},
				},
			},
			map[string]any{
				"services": map[string]any{"web": map[string]any{"environment": map[string]any{"MODE": "prod"}}},
			},
		)(cnabBundle)
		cnabBundle.Definitions = definition.Definitions{"string": &definition.Schema{Type: "string"}}
		cnabBundle.Parameters = map[string]bundle.Parameter{
			"region": {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "REGION"}},
			"tag":    {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "TAG"}},
		}
		cnabBundle.Credentials = map[string]bundle.Credential{
			"token": {Location: bundle.Location{EnvironmentVariable: "TOKEN"}},
		}
	})
	operation, err := ReadOperation(container(t, cnabBundle, map[string]string{"REGION": "eu5", "TAG": "1.27", "TOKEN": "s3cret"}, fstest.MapFS{
		"cnab/app/page.html": {Data: []byte("hello")},
	}))
	require.NoError(t, err)

	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err)

	assert.Equal(t, "demo", project.Compose.Name, "named after the stack")
	web := project.Compose.Services["web"]
	assert.Equal(t, "nginx:1.27", web.Image, "parameters interpolate the documents")
	assert.Equal(t, "eu5", *web.Environment["REGION"])
	assert.Equal(t, "prod", *web.Environment["MODE"], "documents merge in order")
	assert.Equal(t, [][]string{{"db"}, {"web"}}, project.Levels)

	assert.Equal(t, "eu5", project.Compose.Configs["region"].Content)
	assert.Equal(t, "hello", project.Compose.Configs["page"].Content, "read from the invocation image")
	assert.Empty(t, project.Compose.Configs["page"].File)
	assert.Equal(t, "s3cret", project.Compose.Secrets["token"].Content, "credentials feed secrets")
}

// A secret whose credential x-nemo-credential requires on the parameters:
// given, it is the service's; not given and not required, the services do
// without it; not given and required, the install stops. A parameter also
// picks the config a service mounts, by its name.
func TestLoadProject_credentialRequiredIf(t *testing.T) {
	t.Parallel()

	operationWith := func(environment map[string]string) Operation {
		cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
			withCompose(map[string]any{
				"services": map[string]any{"proxy": map[string]any{
					"image":   "traefik:v3.6.2",
					"configs": []any{map[string]any{"source": "tls-${TLS}", "target": "/configuration/tls.yml"}},
					"secrets": []any{"certificate", "dns-token"},
				}},
				"configs": map[string]any{
					"tls-certificate": map[string]any{"content": "certificate"},
					"tls-acme":        map[string]any{"content": "acme"},
				},
				"secrets": map[string]any{
					"certificate": map[string]any{"environment": "CERTIFICATE", CredentialExtension: map[string]any{"required-if": map[string]any{"tls": "certificate"}}},
					"dns-token":   map[string]any{"environment": "DNS_TOKEN", CredentialExtension: map[string]any{"required-if": map[string]any{"tls": "acme", "dns-auth": "token"}}},
				},
			})(cnabBundle)
			cnabBundle.Definitions = definition.Definitions{
				"tls":      &definition.Schema{Type: "string", Default: "certificate"},
				"dns-auth": &definition.Schema{Type: "string", Default: "token"},
			}
			cnabBundle.Parameters = map[string]bundle.Parameter{
				"tls":      {Definition: "tls", Destination: &bundle.Location{EnvironmentVariable: "TLS"}},
				"dns-auth": {Definition: "dns-auth", Destination: &bundle.Location{EnvironmentVariable: "DNS_AUTH"}},
			}
			cnabBundle.Credentials = map[string]bundle.Credential{
				"certificate": {Location: bundle.Location{EnvironmentVariable: "CERTIFICATE"}},
				"dns-token":   {Location: bundle.Location{EnvironmentVariable: "DNS_TOKEN"}},
			}
		})
		operation, err := ReadOperation(container(t, cnabBundle, environment, nil))
		require.NoError(t, err)
		return operation
	}
	secretsOf := func(service types.ServiceConfig) []string {
		var sources []string
		for _, secret := range service.Secrets {
			sources = append(sources, secret.Source)
		}
		return sources
	}

	project, err := LoadProject(context.Background(), operationWith(map[string]string{"CERTIFICATE": "PEM"}))
	require.NoError(t, err)
	proxy := project.Compose.Services["proxy"]
	assert.Equal(t, []string{"certificate"}, secretsOf(proxy), "the DNS token, not required by default, left out")
	assert.Equal(t, "tls-certificate", proxy.Configs[0].Source, "the config the parameter names")
	assert.NotContains(t, project.Compose.Secrets, "dns-token")

	project, err = LoadProject(context.Background(), operationWith(map[string]string{"TLS": "acme", "DNS_TOKEN": "t0ken"}))
	require.NoError(t, err)
	proxy = project.Compose.Services["proxy"]
	assert.Equal(t, []string{"dns-token"}, secretsOf(proxy))
	assert.Equal(t, "tls-acme", proxy.Configs[0].Source)

	_, err = LoadProject(context.Background(), operationWith(map[string]string{"TLS": "acme", "DNS_AUTH": "identity"}))
	require.NoError(t, err, "an identity of the host, no token")

	_, err = LoadProject(context.Background(), operationWith(map[string]string{"TLS": "acme"}))
	assert.EqualError(t, err, "secrets.dns-token: the credential dns-token is required when dns-auth is token and tls is acme")
	_, err = LoadProject(context.Background(), operationWith(nil))
	assert.EqualError(t, err, "secrets.certificate: the credential certificate is required when tls is certificate")
}

func TestLoadProject_credentialsDoNotInterpolate(t *testing.T) {
	t.Parallel()

	cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
		withCompose(map[string]any{
			"services": map[string]any{"web": map[string]any{"image": "nginx", "environment": map[string]any{"TOKEN": "${TOKEN}"}}},
		})(cnabBundle)
		cnabBundle.Credentials = map[string]bundle.Credential{"token": {Location: bundle.Location{EnvironmentVariable: "TOKEN"}}}
	})
	operation, err := ReadOperation(container(t, cnabBundle, map[string]string{"TOKEN": "s3cret"}, nil))
	require.NoError(t, err)

	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err)
	assert.Empty(t, *project.Compose.Services["web"].Environment["TOKEN"])
}

func TestLoadProject_unknownVariable(t *testing.T) {
	t.Parallel()

	bundle := testBundle(withCompose(map[string]any{
		"services": map[string]any{"web": map[string]any{"image": "nginx"}},
		"configs":  map[string]any{"region": map[string]any{"environment": "REGION"}},
	}))
	operation, err := ReadOperation(container(t, bundle, nil, nil))
	require.NoError(t, err)

	_, err = LoadProject(context.Background(), operation)
	require.EqualError(t, err, "configs.region: the variable REGION is neither a parameter nor a credential of the bundle")
}

func TestLoadProject_stack(t *testing.T) {
	t.Parallel()

	for installation, stack := range map[string]string{
		"demo":       "demo",
		"demo/nginx": "demo",
		"/nginx":     "nginx",
	} {
		operation, err := ReadOperation(container(t, testBundle(nil), map[string]string{EnvInstallationName: installation}, nil))
		require.NoError(t, err)

		project, err := LoadProject(context.Background(), operation)
		require.NoError(t, err)
		assert.Equal(t, stack, project.Installation.Stack, installation)
		assert.Equal(t, stack, project.Compose.Name, installation)
	}
}

func TestLoadProject_uninstallNeedsNoContent(t *testing.T) {
	t.Parallel()

	cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
		withCompose(map[string]any{
			"services": map[string]any{"web": map[string]any{"image": "nginx", "secrets": []any{"token"}}},
			"secrets":  map[string]any{"token": map[string]any{"file": "/cnab/app/credentials/token"}},
		})(cnabBundle)
		cnabBundle.Credentials = map[string]bundle.Credential{
			"token": {Location: bundle.Location{Path: "/cnab/app/credentials/token"}, Required: true, ApplyTo: []string{cnab.ActionInstall, cnab.ActionUpgrade}},
		}
	})
	operation, err := ReadOperation(container(t, cnabBundle, map[string]string{EnvAction: cnab.ActionUninstall}, nil))
	require.NoError(t, err, "the credential does not apply to uninstall")

	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err, "and its file is not read")
	assert.Empty(t, project.Compose.Secrets["token"].Content)
}

func TestLoadProject_undeliveredParameterIsBlank(t *testing.T) {
	t.Parallel()

	cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
		withCompose(map[string]any{
			"services": map[string]any{"web": map[string]any{"image": "nginx", "environment": map[string]any{"REGION": "${REGION}"}}},
		})(cnabBundle)
		cnabBundle.Definitions = definition.Definitions{"string": &definition.Schema{Type: "string", Default: "eu1"}}
		cnabBundle.Parameters = map[string]bundle.Parameter{
			"region": {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "REGION"}, ApplyTo: []string{cnab.ActionInstall}},
		}
	})
	operation, err := ReadOperation(container(t, cnabBundle, map[string]string{EnvAction: cnab.ActionUninstall}, nil))
	require.NoError(t, err)

	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err)
	assert.Empty(t, *project.Compose.Services["web"].Environment["REGION"], "region does not apply to uninstall")
}

func TestLoadProject_images(t *testing.T) {
	t.Parallel()

	const nginxDigest = "sha256:4ff102c5d78d254a6f0da062b3cf39eaf07f01eec0927fd21e219d0af8bc0591"
	cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
		withCompose(map[string]any{
			"services": map[string]any{
				"web":   map[string]any{"image": "nginx:1.27-alpine"},
				"cache": map[string]any{"image": "redis:7"},
			},
		})(cnabBundle)
		cnabBundle.Images = map[string]bundle.Image{
			"nginx": {BaseImage: bundle.BaseImage{ImageType: "docker", Image: "docker.io/library/nginx:1.27-alpine", Digest: nginxDigest}},
		}
	})

	operation, err := ReadOperation(container(t, cnabBundle, nil, nil))
	require.NoError(t, err)
	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err)
	assert.Equal(t, "nginx:1.27-alpine@"+nginxDigest, project.Compose.Services["web"].Image, "pinned to the bundle's digest")
	assert.Equal(t, "redis:7", project.Compose.Services["cache"].Image, "not declared, kept as written")

	relocation := fstest.MapFS{"cnab/app/relocation-mapping.json": {Data: []byte(`{"docker.io/library/nginx:1.27-alpine": "registry.local/nginx@` + nginxDigest + `"}`)}}
	operation, err = ReadOperation(container(t, cnabBundle, nil, relocation))
	require.NoError(t, err)
	_, err = LoadProject(context.Background(), operation)
	require.EqualError(t, err, "services.cache.image: redis:7 was not relocated: declare it in the bundle's images")

	delete(cnabBundle.Custom, cnab.ComposeExtension.Key)
	withCompose(map[string]any{"services": map[string]any{"web": map[string]any{"image": "nginx:1.27-alpine"}}})(&cnabBundle)
	operation, err = ReadOperation(container(t, cnabBundle, nil, relocation))
	require.NoError(t, err)
	project, err = LoadProject(context.Background(), operation)
	require.NoError(t, err)
	assert.Equal(t, "registry.local/nginx@"+nginxDigest, project.Compose.Services["web"].Image, "relocated")
}

func TestLoadProject_volumes(t *testing.T) {
	t.Parallel()

	volumeTypes := `{
		"data": {
			"driver": "local",
			"options": {"type": "none", "o": "bind{{AppendJoin .Options \",\"}}", "device": "/srv/shared/{{.Stack}}/{{.Name}}"},
			"provision": {"directory": "{{.Stack}}/{{.Name}}", "volume": {"driver": "local", "options": {"type": "none", "o": "bind", "device": "/srv/shared"}}}
		},
		"scratch": {}
	}`
	load := func(volumes map[string]any) (*Project, error) {
		cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
			withCompose(map[string]any{
				"services": map[string]any{"web": map[string]any{"image": "nginx"}},
				"volumes":  volumes,
			})(cnabBundle)
			cnabBundle.Definitions = definition.Definitions{"volumes": &definition.Schema{Type: "object"}}
			cnabBundle.Parameters = map[string]bundle.Parameter{
				cnab.ParameterVolumes: {Definition: "volumes", Destination: &bundle.Location{EnvironmentVariable: "NEMO_VOLUMES"}},
			}
		})
		operation, err := ReadOperation(container(t, cnabBundle, map[string]string{"NEMO_VOLUMES": volumeTypes}, nil))
		require.NoError(t, err)
		return LoadProject(context.Background(), operation)
	}

	project, err := load(map[string]any{
		"logs":  map[string]any{VolumeExtension: map[string]any{"type": "data", "options": []any{"uid=472"}}},
		"tmp":   map[string]any{VolumeExtension: map[string]any{"type": "scratch"}},
		"plain": map[string]any{"driver_opts": map[string]any{"size": "1g"}},
	})
	require.NoError(t, err)
	logs := project.Compose.Volumes["logs"]
	assert.Equal(t, "local", logs.Driver)
	assert.Equal(t, types.Options{"type": "none", "o": "bind,uid=472", "device": "/srv/shared/demo/logs"}, logs.DriverOpts)
	assert.Equal(t, "demo/logs", project.Provisions["demo_logs"].Directory, "provisioned by the volume's name")
	assert.Equal(t, "/srv/shared", project.Provisions["demo_logs"].Volume.Options["device"])
	assert.Empty(t, project.Compose.Volumes["tmp"].Driver, "an empty type is a plain volume")
	assert.NotContains(t, project.Provisions, "demo_tmp")
	assert.Equal(t, types.Options{"size": "1g"}, project.Compose.Volumes["plain"].DriverOpts, "without a type, as written")
	assert.Equal(t, "gill:dev", project.InvocationImage)

	_, err = load(map[string]any{"logs": map[string]any{VolumeExtension: map[string]any{"type": "cold"}}})
	require.EqualError(t, err, "volumes.logs: x-nemo-volume: the target has no volume type cold, it has data, scratch.")

	_, err = load(map[string]any{"logs": map[string]any{"driver": "nfs", VolumeExtension: map[string]any{"type": "data"}}})
	require.EqualError(t, err, "volumes.logs: driver: give a type or a driver, not both: the target makes the type of a driver.")

	_, err = load(map[string]any{"logs": map[string]any{"external": true, VolumeExtension: map[string]any{"type": "data"}}})
	require.EqualError(t, err, "volumes.logs: external: an external volume can't have a type.")

	_, err = load(map[string]any{"logs": map[string]any{VolumeExtension: map[string]any{"type": "data", "size": "1g"}}})
	require.ErrorContains(t, err, `volumes.logs.x-nemo-volume: json: unknown field "size"`)
}

func TestLevels_cycle(t *testing.T) {
	t.Parallel()

	bundle := testBundle(withCompose(map[string]any{
		"services": map[string]any{
			"a": map[string]any{"image": "a", "depends_on": []any{"b"}},
			"b": map[string]any{"image": "b", "depends_on": []any{"a"}},
		},
	}))
	operation, err := ReadOperation(container(t, bundle, nil, nil))
	require.NoError(t, err)
	_, err = LoadProject(context.Background(), operation)
	require.Error(t, err)
}

func TestProject_Validate(t *testing.T) {
	t.Parallel()

	bundle := testBundle(withCompose(map[string]any{
		"services": map[string]any{
			"web": map[string]any{"build": ".", "depends_on": map[string]any{"db": map[string]any{"condition": "service_healthy"}}},
			"db":  map[string]any{"image": "postgres"},
		},
	}))
	operation, err := ReadOperation(container(t, bundle, nil, nil))
	require.NoError(t, err)
	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err)

	err = project.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.web.image: required")
	assert.Contains(t, err.Error(), "services.web.build: not supported")
	assert.Contains(t, err.Error(), "services.web.depends_on.db: service_healthy needs a healthcheck on db")
}

// The loader turns a link, or a namespace shared with another service, into a
// dependency that restarts with it: that restart is the loader's, not the
// project's, and is not reported.
func TestProject_ValidateLeavesTheDependenciesTheLoaderAdded(t *testing.T) {
	t.Parallel()

	bundle := testBundle(withCompose(map[string]any{
		"services": map[string]any{
			"web":     map[string]any{"image": "web", "links": []any{"db:database"}},
			"sidecar": map[string]any{"image": "sidecar", "network_mode": "service:web"},
			"db":      map[string]any{"image": "postgres"},
		},
	}))
	operation, err := ReadOperation(container(t, bundle, nil, nil))
	require.NoError(t, err)
	project, err := LoadProject(context.Background(), operation)
	require.NoError(t, err)

	assert.NoError(t, project.Validate())
}
