# Stack manifest

A stack's manifest is a YAML or JSON document. nemo keeps it in its home as `<stack>/manifest.yaml` or `<stack>/manifest.json`; `nemo stack inspect` prints it. Relative paths in a manifest are relative to the manifest's directory, and made absolute when nemo keeps it.

```yaml
name: shop                                    # required

target:                                       # required
  orchestrator: swarm                         # required: compose | swarm
  configuration:
    host: ssh://deploy@prod-1.example.com     # or context; neither: the environment's Docker
    images: registry                          # registry (default) | load
    volumes:
      data:
        driver: local
        options: { type: none, o: bind, device: "/srv/{{.Stack}}/{{.Name}}" }
        provision:
          directory: "{{.Stack}}/{{.Name}}"
          volume: { driver: local, options: { type: none, o: bind, device: /srv } }

installer:
  driver: docker                              # docker (default) | kubernetes
  kubernetes:
    namespace: deployments
    kubeconfig: ~/.kube/config

remote: "docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}"

parameters:
  domain: shop.example.com

credentials:
  - name: tls-key
    source: { path: /etc/shop/tls.key }

installations:                                # required
  - name: api                                 # required
    bundle: { version: 1.4.0 }                # required
    parameters:
      log-level: debug
    credentials:
      - name: database-password
        source: { env: SHOP_DATABASE_PASSWORD }
```

## `name`

The stack's name: letters, digits and dashes. It names the stack in nemo's home, and prefixes everything gill creates on the target, so two stacks on the same Docker never collide.

## `target`

Where the bundles are deployed. See the [targets guide](../guides/targets.md).

| Field | Type | |
|---|---|---|
| `orchestrator` | `compose` \| `swarm` | **Required.** How the bundles are deployed. |
| `configuration.host` | string | The target's Docker: `unix://PATH`, `tcp://HOST:PORT`, `ssh://USER@HOST`. Not with `context`. |
| `configuration.context` | string | A docker context to reach the target with. Not with `host`. |
| `configuration.images` | `registry` \| `load` | How the images reach the target. Default: `registry`. |
| `configuration.volumes` | map | The target's volume types, by name. |
| `configuration.volumes.<type>.driver` | string | The volume driver. A template. |
| `configuration.volumes.<type>.options` | map of strings | The driver's options. Templates. |
| `configuration.volumes.<type>.provision.directory` | string | A directory to create in the storage before the volume. A template. |
| `configuration.volumes.<type>.provision.volume` | `{driver, options}` | The storage the directory is created in. |

Templates know `{{.Stack}}`, `{{.Name}}` (the volume's name in its bundle), `{{.Options}}` (its `x-nemo-volume` options) and `{{AppendJoin .Options ","}}`.

## `installer`

Where gill runs. See [where gill runs](../guides/targets.md#where-gill-runs).

| Field | Type | |
|---|---|---|
| `driver` | `docker` \| `kubernetes` | Default: `docker`, on the target. |
| `kubernetes.namespace` | string | The namespace of the Job. |
| `kubernetes.kubeconfig` | string | The kubeconfig of the cluster. Default: `KUBECONFIG`, `~/.kube/config`, else the cluster nemo runs in. |

## `remote`

The location of the bundles whose installation gives a version alone, as a template of `{{ .Component }}`, the installation's name, and `{{ .Version }}`, its version:

- `docker://REGISTRY/PATH/{{ .Component }}:{{ .Version }}`: bundles in a registry.
- `oci:PATH/{{ .Component }}.oci`: bundles in OCI layout directories. A relative path is relative to the manifest.

`nemo stack import --remote` and `nemo stack set --remote` set it.

## `parameters`

A map of parameter names to values, given to every installation whose bundle declares a parameter of that name. Values are strings; nemo converts them to the type the bundle declares.

## `credentials`

A CNAB credential set: a list of credentials, each given to every installation whose bundle declares it.

| Field | |
|---|---|
| `name` | The credential's name, as the bundle declares it. |
| `source.path` | Read the value from this file. `$VARIABLES` are expanded. |
| `source.env` | Read the value from this environment variable of nemo's. |
| `source.command` | Read the value from the output of this command, run without a shell. |
| `source.value` | This value, as written. |

Exactly one source per credential.

## `installations`

The bundles to install. Their order does not matter: nemo orders them by their dependencies.

| Field | |
|---|---|
| `name` | **Required.** The installation's name, which is its bundle's name. |
| `bundle.version` | The bundle's version. Alone, the stack's `remote` locates the bundle. |
| `bundle.repository` | The bundle's repository in a registry, with `version`. |
| `bundle.file` | A `bundle.json` file, instead of `repository` and `version`. |
| `parameters` | Parameters for this installation, over the stack's. |
| `credentials` | Credentials for this installation, over the stack's. Same shape as the stack's. |

## JSON

The same manifest in JSON:

```json
{
  "name": "shop",
  "target": { "orchestrator": "compose" },
  "remote": "oci:build/{{ .Component }}.oci",
  "installations": [
    { "name": "api", "bundle": { "version": "1.4.0" } }
  ]
}
```
