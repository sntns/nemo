# Bundle format

A nemo bundle is a [CNAB 1.2](https://github.com/cnabio/cnab-spec) bundle built from compose files. This page lists what the compose files may contain for nemo, and what the built bundle holds.

## `x-nemo-bundle`

A top-level block of one of the compose files given to `nemo bundle build`.

| Field | Type | |
|---|---|---|
| `name` | string | **Required.** The bundle's name. |
| `version` | semantic version | The bundle's version. Required unless `--version` is given, which overrides it. |
| `description` | string | A short description. |
| `license` | string | An SPDX license identifier. |
| `keywords` | list of strings | |
| `maintainers` | list of `{name, email, url}` | |
| `dependencies.requires.<label>.bundle` | string | A bundle this one requires: its repository, `REGISTRY/PATH/NAME`. Its last part is the bundle's name. |
| `dependencies.requires.<label>.version.ranges` | list of strings | Semantic version ranges, any of which the dependency must satisfy: `">=2.0.0 <3.0.0"`. |
| `dependencies.requires.<label>.version.prereleases` | boolean | Whether prereleases satisfy the ranges. |

`dependencies` is written as the CNAB [dependencies specification](https://github.com/cnabio/cnab-spec/blob/main/500-CNAB-dependencies.md) writes it, and copied into the bundle's `io.cnab.dependencies` extension.

## `x-nemo-volume`

A block of a top-level volume, giving it a type the target makes:

```yaml
volumes:
  data:
    x-nemo-volume:
      type: data
      options: ["uid=999"]
```

| Field | Type | |
|---|---|---|
| `type` | string | **Required.** A volume type of the target. |
| `options` | list of strings | Passed to the target's type templates as `{{.Options}}`. |

A volume with a type can't set `driver`, `driver_opts` or `external`.

## What becomes what

| In the compose files | In the bundle |
|---|---|
| `${VARIABLE}`, `${VARIABLE:-default}`, `${VARIABLE:?message}` | A string parameter named in lowercase with dashes, `variable`, with its default, required with `:?` |
| A config read from `environment: VARIABLE` | A string parameter, `variable` |
| A secret read from `environment: VARIABLE` | A credential, `variable`, required on install and upgrade |
| A config with `content:` | Kept in the compose project, filled with the parameters |
| A config or secret read from `file:` | Refused: a bundle carries no files |
| Each service's `image`, with `--image` | An entry of `images`, with its digest |
| The compose project | The `io.nemo.compose` extension |
| — | gill as the invocation image, the `nemo-*` parameters, and the required `io.cnab.docker` extension |

## The `nemo-*` parameters

Every nemo bundle declares these parameters, through which nemo tells gill about the target. nemo sets them; a stack gives `nemo-timeout` if it needs to.

| Parameter | gill reads it as | |
|---|---|---|
| `nemo-orchestrator` | `NEMO_ORCHESTRATOR` | `compose` or `swarm`: the target's orchestrator. Required. nemo sets it from the target. |
| `nemo-timeout` | `NEMO_TIMEOUT` | How long an action may take, as a duration. Default: `5m`. |
| `nemo-purge` | `NEMO_PURGE` | On uninstall, also remove the volumes. nemo sets it with `--purge`. |
| `nemo-volumes` | `NEMO_VOLUMES` | The target's volume types, as JSON. nemo sets it from the target. |
| `nemo-registry-auth` | `NEMO_REGISTRY_AUTH` | The credentials of the registries the images come from, as JSON. Sensitive. nemo sets it from `docker login`. |

## Extensions

| Extension | Required | Read by |
|---|---|---|
| `io.cnab.docker` | Yes | The runtime: gill needs the target's Docker socket. |
| `io.cnab.dependencies` | No | The runtime: the order of a stack's installations. |
| `io.nemo.compose` | No | gill: the compose project to deploy. |

`io.nemo.compose` is deliberately not required: a CNAB runtime refuses bundles that require extensions it does not know, and gill is the only one that needs to read it.

## Where a bundle is stored

| Form | Holds |
|---|---|
| `bundle.json` | The bundle alone. The images stay where `--image` said. |
| OCI layout directory (`oci:PATH`) | The bundle and its images, gill's included, as a CNAB bundle in OCI: the images by digest, the bundle as an index tagged with its version. |
| Registry (`docker://REFERENCE`) | The same, in a repository of a registry. |
