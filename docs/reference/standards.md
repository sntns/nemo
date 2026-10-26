# Standards

nemo builds on open specifications rather than formats of its own: a nemo bundle is a standard CNAB bundle, stored the way CNAB bundles are stored in OCI registries, and nemo's records are CNAB claims.

## CNAB

The [Cloud Native Application Bundle](https://cnab.io) specification describes a package that holds an application and the installer that deploys it, and how a runtime runs the installer's actions. nemo follows it:

| Specification | In nemo |
|---|---|
| [Bundle](https://github.com/cnabio/cnab-spec/blob/main/101-bundle-json.md), 1.2 | `nemo bundle build` writes `bundle.json`: metadata, images with their digests, the invocation image, parameters with their definitions, credentials. |
| [Invocation images](https://github.com/cnabio/cnab-spec/blob/main/102-invocation-image.md) | gill reads `CNAB_ACTION`, `/cnab/bundle.json`, parameters and credentials at their destinations, and implements `install`, `upgrade` and `uninstall`. |
| [Bundle runtime](https://github.com/cnabio/cnab-spec/blob/main/103-bundle-runtime.md) | nemo runs the invocation image with the operation, as a container or a Kubernetes Job. |
| [Claims](https://github.com/cnabio/cnab-spec/blob/main/400-claims.md) | Each action is recorded as a claim, a result and outputs; an installation is named `<stack>/<name>`, the stack being its namespace. |
| [Dependencies](https://github.com/cnabio/cnab-spec/blob/main/500-CNAB-dependencies.md) | `io.cnab.dependencies`: a bundle requires others, with version ranges. |
| [Credential sets](https://github.com/cnabio/cnab-spec/blob/main/802-credential-sets.md) | A stack's credentials: `path`, `env`, `command` and `value` sources. |
| [Docker extension](https://github.com/cnabio/cnab-spec/blob/main/810-well-known-custom-extensions.md) | Every nemo bundle requires `io.cnab.docker`: its installer needs the Docker socket. |

## OCI

Bundles and their images are stored as OCI artifacts, the way CNAB tools store them in registries ([CNAB in OCI](https://github.com/cnabio/cnab-spec/blob/main/201-representing-CNAB-in-OCI.md)): each image by digest in the bundle's repository, then the bundle as an index tagged with its version. The same layout is written to OCI layout directories on disk, which `nemo bundle copy` moves to and from registries. Any registry that stores OCI images stores nemo bundles: Docker Hub, GHCR, GitLab, Harbor, Artifactory, a `registry:2` on site.

## Running nemo bundles with other CNAB tools

A nemo bundle declares everything gill needs as standard CNAB parameters and credentials, so it is designed to run with any CNAB runtime that honours the `io.cnab.docker` extension. Such a runtime must mount the target's Docker socket into gill (with [Porter](https://porter.sh), `--allow-docker-host-access`), and give the `nemo-orchestrator` parameter.

What such a runtime does not do is what nemo adds around bundles: stacks and their dependencies, loading images without a registry, volume types from the target, progress, status and logs.

nemo itself runs bundles whose installer is gill. Bundles built by other tools, Porter's for instance, carry their own conventions and state, and are best run by the tool that built them.

## Running gill without nemo

gill is a plain container. To see what it does, run an action by hand with a bundle file, the Docker socket, and the operation in the environment:

```bash
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$PWD/bundle.json:/cnab/bundle.json:ro" \
  -e CNAB_ACTION=install -e CNAB_INSTALLATION_NAME=demo/nginx -e CNAB_REVISION=01 \
  -e NEMO_ORCHESTRATOR=compose -e REGION=eu5 -e TOKEN=s3cret \
  ghcr.io/sntns/gill:latest
```

- `NEMO_ORCHESTRATOR` is required: `compose`, or `swarm` on a swarm manager.
- Parameters and credentials go where the bundle declares them: here `region` as `REGION` and the `token` credential as `TOKEN`.
- `CNAB_ACTION=upgrade` applies the bundle again; `uninstall` removes what it created, and `NEMO_PURGE=true` its volumes too.
