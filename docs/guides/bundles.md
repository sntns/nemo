# Writing bundles

A bundle is a compose file with an `x-nemo-bundle` block, built into a versioned package. This guide takes a compose file you already have and turns it into a bundle you can publish and deploy anywhere.

## Name the bundle

Add an `x-nemo-bundle` block at the top level of the compose file:

```yaml
x-nemo-bundle:
  name: api
  version: 1.4.0
  description: The shop's HTTP API and its background workers
  license: Apache-2.0
  keywords: [shop, api]
  maintainers:
    - name: Shop team
      email: shop@example.com

services:
  http:
    image: shop-api:latest
    ports: ["8000:8000"]
  worker:
    image: shop-api:latest
    command: ["worker"]
```

- `name` is required. It is the name of the bundle, and of its installation in a stack.
- `version` must be a semantic version. `nemo bundle build --version` overrides it, so a pipeline can stamp the version it builds.
- The other fields are copied into the bundle as its metadata.

A bundle can be split across several compose files, as with `docker compose -f`: give each with `-f`, merged in order. One of them has the `x-nemo-bundle` block.

## Settings become parameters

Every `${VARIABLE}` in the compose files becomes a parameter of the bundle, named in lowercase with dashes:

```yaml
services:
  http:
    image: shop-api:latest
    environment:
      PUBLIC_URL: https://${DOMAIN}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      WORKERS: ${WORKERS:?the number of workers is required}
```

| In the compose file | Parameter | Required | Default |
|---|---|---|---|
| `${DOMAIN}` | `domain` | no | empty |
| `${LOG_LEVEL:-info}` | `log-level` | no | `info` |
| `${WORKERS:?...}` | `workers` | yes | — |

A variable may stand anywhere a value of the compose file goes, a published port or a number of replicas included: `"${WEB_PORT}:80"`. `nemo bundle build` knows no value yet, so it checks such a field only with its default, when it has one, and gill checks it with the value given.

When the bundle is installed, gill fills the compose project with the values given: in the stack's manifest, with `nemo stack set --param`, or with `--param` on `nemo installation install`. A config read from a variable is a parameter too:

```yaml
configs:
  settings:
    environment: SETTINGS_JSON     # the settings-json parameter
  banner:
    content: |
      Welcome to ${SHOP_NAME}      # the shop-name parameter
```

Parameters named `nemo-*` are reserved: nemo uses them to tell gill about the target.

## Secrets become credentials

A secret read from a variable becomes a credential of the bundle, required on install and upgrade:

```yaml
services:
  http:
    image: shop-api:latest
    secrets:
      - source: database-password
        target: /run/secrets/database-password

secrets:
  database-password:
    environment: DATABASE_PASSWORD   # the database-password credential
```

The stack says where each credential's value comes from (a file, an environment variable, a command), and nemo reads it when the action runs. The value reaches gill for that action only, and nemo never stores it. See [credentials](stacks.md#credentials).

**A bundle carries no files.** A config or secret read from a `file:` is refused at build time: the file would not exist on the target. Use `content:` for a config written in the compose file, or `environment:` to fill it from a parameter or a credential.

## Volumes get a type

A bundle should not decide how its data is stored: a laptop uses a local volume, a cluster an NFS share or a block storage driver. Give the volume a type instead, and let each target say what that type is made of:

```yaml
services:
  database:
    image: postgres:17
    volumes:
      - data:/var/lib/postgresql/data

volumes:
  data:
    x-nemo-volume:
      type: data
      options: ["uid=999"]
```

- `type` names a volume type of the target: `data`, `cache`, `shared`, whatever your targets define. A target that does not define it refuses the bundle before touching anything.
- `options` are passed to the target's type as `{{.Options}}`, for a driver that needs per-volume settings.
- A volume with no `x-nemo-volume` is created as written in the compose file. A volume with a type cannot also set a `driver`.

See [volume types](targets.md#volume-types) for how a target defines them.

**Mount every path the image declares.** An image that declares `VOLUME /data` gets a new anonymous volume on every container it starts, unless a volume of the compose file is mounted there. Mount named volumes on those paths, or a `tmpfs` for scratch data, so that nothing accumulates on the target.

## Dependencies

A bundle that needs another one installed first declares it in `dependencies`, as the CNAB dependencies specification writes it:

```yaml
x-nemo-bundle:
  name: api
  version: 1.4.0
  dependencies:
    requires:
      database:
        bundle: registry.example.com/shop/database
        version:
          ranges: [">=2.0.0 <3.0.0"]
```

- The key, `database`, is a label for the dependency.
- `bundle` is the dependency's repository; its last part, `database`, is the bundle's name.
- `ranges` are semantic version ranges, any of which the dependency must satisfy.

In a stack, nemo installs dependencies first and uninstalls them last. A dependency the stack lists is checked against the ranges; one it does not list is found at the stack's remote, else pulled from its repository at the highest version the ranges allow.

## Compose and Swarm

One bundle runs on both orchestrators. The compose file says what runs; the target says how.

- Settings only Swarm understands (`deploy.placement`, `deploy.update_config`, `deploy.rollback_config`, `deploy.endpoint_mode`, `deploy.labels`) apply on Swarm. On Compose, gill ignores them and says so with a warning.
- `deploy.replicas` applies on both.
- `depends_on` conditions (`service_started`, `service_healthy`, `service_completed_successfully`) are honoured by Compose. Give services a `healthcheck` so that dependants start once they are really ready.

## Images

Each image of the services must say where it comes from, with `--image NAME=LOCATION`:

| Location | Means |
|---|---|
| `docker://REFERENCE` | An image in a registry: Docker Hub, GHCR, your own. nemo reads its digest with the credentials of `docker login`. |
| `oci:PATH` | An image in an OCI layout directory on disk, as `docker buildx build --output type=oci,dest=PATH,tar=false` writes it. |

```bash
nemo bundle build -f compose.yaml \
  --image shop-api:latest=oci:build/images/shop-api.oci \
  --image postgres:17=docker://postgres:17
```

The bundle records each image's digest: an image re-pushed under the same tag is never deployed by mistake, and every node of a swarm runs the same image. nemo refuses an image that has no location, and a location no service uses.

## Build and publish

`-o` says where the bundle goes:

| `-o` | Result |
|---|---|
| `bundle.json` (default) | The bundle file only. The images stay where they are. |
| `oci:PATH` | An OCI layout directory with the bundle and all its images, gill's included. Ready to copy to a disk, or to deploy with `images: load`. |
| `docker://REGISTRY/REPOSITORY` | The bundle and its images pushed to a registry, the bundle tagged with its version. |

```bash
nemo bundle build -f compose.yaml --version 1.4.0 \
  --image shop-api:latest=oci:build/images/shop-api.oci \
  -o docker://registry.example.com/shop/api
```

`nemo bundle copy` moves a published bundle with its images from one place to another: from the layout a pipeline built to the registry it publishes to, or from a registry to a disk for an offline site.

```bash
nemo bundle copy oci:build/bundles/api.oci docker://registry.example.com/shop/api:1.4.0
```

## The installer

Every bundle names gill as its installer. A released `nemo` uses the gill of its own version, `ghcr.io/sntns/gill:<version>`. To use another one, a gill you built or one mirrored in your registry, give `--installer` or set `NEMO_INSTALLER`:

```bash
export NEMO_INSTALLER=docker://registry.example.com/mirror/gill:0.1.0
```

When the bundle is pushed (`-o oci:` or `-o docker://`), gill's image is pushed with it, so the bundle runs without access to ghcr.io.

## With another CNAB tool

A bundle nemo builds is a CNAB bundle: another CNAB runtime installs it, with the parameters it declares. With [Porter](https://porter.sh):

```bash
porter install shop-web --reference registry.example.com/shop/web:1.4.0 \
  --param nemo-orchestrator=compose --allow-docker-host-access
```

- `nemo-orchestrator` is required: `compose` or `swarm`, as nemo's target says it.
- `--allow-docker-host-access`: gill deploys on the Docker that runs it, which the bundle declares with the `io.cnab.docker` extension.
- Porter keeps the value of a sensitive parameter in a secrets plugin, and `nemo-registry-auth` is one: give Porter a plugin that stores them, `PORTER_DEFAULT_SECRETS_PLUGIN=filesystem` on a developer's machine.

`porter upgrade`, `porter invoke --action validate` and `porter uninstall` work as nemo's actions do. What nemo adds on top of CNAB stays with nemo: stacks, records on the target, status and logs.

## Reference

The complete list of what `x-nemo-bundle` and `x-nemo-volume` accept, and what a built bundle contains, is in the [bundle format reference](../reference/bundle-format.md).
