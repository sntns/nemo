# Troubleshooting

## Finding out what went wrong

When an action fails, nemo shows the installation that failed, the service that failed with its error, and gill's output, then exits with status `1`. To dig further:

| | |
|---|---|
| `nemo status -a` | Which services of which installation run, and which do not, with their state: `starting`, `exited`, `unhealthy`. |
| `nemo logs INSTALLATION [SERVICE]` | The logs of the services, to see why a container exits or never becomes healthy. |
| `nemo installation runs INSTALLATION` | Every action run on the installation, with its version and result. |
| `nemo installation show INSTALLATION` | What the installation was given: bundle, target, parameters, credential sources. |
| `nemo up 2>&1 \| cat` | The progress as one line per change, as in CI, every message whole. |
| `nemo up --json` | The record of each run, with its result, on standard output. |

On a swarm, `docker service ps --no-trunc <stack>_<service>` shows why the tasks of a service are rejected or restarted.

## Common errors

### `Bind for 0.0.0.0:8080 failed: port is already allocated`

Another container or process already publishes that port on the target. Stop it, or change the port the bundle publishes, then run `nemo up` again: the container that could not start is created again, with its port.

### `volume data exists but was not created by nemo for ...`

A network, volume or service with the name gill wants already exists on the target, and was not created by nemo for this installation: another tool, another stack, or a manual `docker volume create`. nemo never takes over what it did not create. Remove the object if it is not needed, or rename the stack.

### `the target has no volume type data, it has ...`

A volume of the bundle has `x-nemo-volume: { type: data }`, and the stack's target defines no `data` type. Add it under `target.configuration.volumes`: see [volume types](guides/targets.md#volume-types).

### `images are loaded into one daemon, and this swarm has 3 nodes`

`images: load` sends the images to one Docker, and the other nodes of the swarm could not run them. Push the bundles to a registry the nodes can reach, and use `images: registry`.

### `services.api.image: shop-api:latest has no location`

`nemo bundle build` must know where each image of the compose file is, to pin it by digest. Give it with `--image shop-api:latest=docker://...` or `--image shop-api:latest=oci:PATH`.

### `a bundle carries no files`

A config or secret of the compose file reads a `file:`, which would not exist on the target. Write the config's `content:`, or read it from `environment:` so that it becomes a parameter or a credential. See [writing bundles](guides/bundles.md#secrets-become-credentials).

### `give the invocation image with --installer, or set NEMO_INSTALLER`

A nemo built from a development checkout has no released gill to put in bundles. Use a released nemo, or build gill and point nemo to it:

```bash
make gill-oci
export NEMO_INSTALLER=oci:$PWD/bin/gill.oci
```

### `api depends on registry.example.com/shop/database: ...`

The stack's `database` does not satisfy the version ranges `api` accepts, or the dependency could not be found at the stack's remote nor in its registry. Install a version within the ranges, or list the dependency in the stack.

### A credential's command fails with `exec: no command` or does not find its arguments

A `command` source runs without a shell: no pipes, no redirections, no variables. Put the command in a script, and give the script.

### Pulling an image fails with `unauthorized`

nemo hands the target the credentials of `docker login` from the machine it runs on. Log in there to the registry the bundle's images come from, then run `nemo up` again.

### `no current stack`

No stack is current yet. Create one with `nemo stack create`, keep one with `nemo stack import`, choose one with `nemo stack use NAME`, or give `--stack NAME`.

### `stack shop has installations: nemo down --stack shop first`

`nemo stack remove` forgets a stack only once nothing of it is installed, so that nothing keeps running unknown to nemo. Take it down first.

## Volumes that accumulate

An image that declares `VOLUME /path` gets a new anonymous volume every time a container is created, unless the compose file mounts something on that path. Mount a named volume there, or a `tmpfs` for scratch data. `docker image inspect --format '{{json .Config.Volumes}}' IMAGE` lists those paths.

## Reporting a problem

If nothing here helps, [open an issue](https://github.com/sntns/nemo/issues/new/choose) with the command, its full output (`2>&1 | cat`), `nemo --version`, and the target: Compose or Swarm, local or remote, Docker version.
