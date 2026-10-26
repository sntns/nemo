# Getting started

This guide installs nemo, builds two bundles from their compose files, and deploys them as a stack on the Docker of your machine. It takes about five minutes.

You need Docker running locally (Docker Desktop or Docker Engine) and an internet connection to pull two public images.

## 1. Install nemo

```bash
brew install sntns/tap/nemo
```

Or download the archive for your platform from the [latest release](https://github.com/sntns/nemo/releases/latest) and put `nemo` on your `PATH`, or build it with `go install github.com/sntns/nemo/cmd/nemo@latest`. Check it:

```console
$ nemo --version
nemo version 0.1.0
```

## 2. Get the example

```bash
git clone https://github.com/sntns/nemo
cd nemo/examples/hello
```

The `hello` example has two components, each a plain compose file with an `x-nemo-bundle` block:

- [`nginx/compose.yaml`](../examples/hello/nginx/compose.yaml) serves a page that says which region it runs in, and a secret token. The region is a `${REGION}` variable; the token is a compose secret read from the `TOKEN` variable.
- [`whoami/compose.yaml`](../examples/hello/whoami/compose.yaml) answers HTTP requests with its own details, and declares that it requires `nginx`.

```yaml
x-nemo-bundle:
  name: nginx
  version: 1.0.0
  description: A web page that says where it runs, and serves a secret token

services:
  web:
    image: nginx:1.27-alpine
    ports: ["8080:80"]
    # ...
```

## 3. Build the bundles

```bash
nemo bundle build -f nginx/compose.yaml \
  --image nginx:1.27-alpine=docker://nginx:1.27-alpine -o oci:build/nginx.oci
nemo bundle build -f whoami/compose.yaml \
  --image traefik/whoami:v1.10=docker://traefik/whoami:v1.10 -o oci:build/whoami.oci
```

Each command makes a bundle and writes it, with its images, to an OCI layout directory under `build/`:

- `--image` says where each image of the compose file comes from. nemo reads its digest, so the bundle always deploys exactly that image.
- `${REGION}` became the bundle's `region` parameter, and the `TOKEN` secret its `token` credential.
- The bundle's installer is gill, of the same version as your nemo.

## 4. Keep the stack

The [`stack.yaml`](../examples/hello/stack.yaml) manifest says where the bundles go and with what:

```yaml
name: hello

target:
  orchestrator: compose          # deploy with Compose, on one Docker
  configuration:
    images: load                 # no registry: nemo loads the images into the target
    volumes:
      data:                      # what a volume of type "data" is made of here
        driver: local

remote: "oci:build/{{ .Component }}.oci"   # where the bundles are

parameters:
  region: eu5                    # to every bundle that declares it

installations:
  - name: nginx
    bundle: { version: 1.0.0 }
    credentials:
      - name: token
        source: { env: HELLO_TOKEN }       # read when nemo runs, never stored
  - name: whoami
    bundle: { version: 1.0.0 }
```

```bash
nemo stack import stack.yaml
```

nemo keeps the stack in its home, `~/.nemo/hello/`, and makes it the current one: the next commands act on it.

## 5. Bring it up

```bash
export HELLO_TOKEN=s3cret
nemo up
```

```console
hello · compose · 2 installations

✔ nginx   install    1 service · 1 volume · 1 network  3.6s
✔ whoami  install    1 service · 1 network  2.6s
```

nemo installed `nginx` first, because `whoami` requires it. For each one, it loaded the images into your Docker and started gill, the installer. gill created the network and the volume, then the container with its page and its token, and waited until it ran.

```console
$ curl localhost:8080
hello from nemo in eu5
$ curl localhost:8080/token
s3cret
$ curl -s localhost:8081 | head -1
Hostname: 6c2b4f1e9d0a
```

## 6. See what runs

```console
$ nemo status
hello · compose

✔ nginx   1.0.0  installed just now  1/1 services
✔ whoami  1.0.0  installed just now  1/1 services
```

```bash
nemo status -a                  # with every service
nemo logs nginx -f              # follow the logs of nginx's services
nemo installation runs nginx    # every action run on nginx, with its result
```

## 7. Change it

Change a parameter of the stack, then bring it up again: nemo upgrades the installations, and gill recreates only what changed.

```bash
nemo stack set --param region=eu6
nemo up
curl localhost:8080             # hello from nemo in eu6
```

`nemo up` always brings the target to the manifest. Remove an installation from the manifest, and the next `nemo up` uninstalls it.

## 8. Take it down

```bash
nemo down --purge
```

`nemo down` uninstalls every installation, `whoami` first since it depends on `nginx`. `--purge` also removes their volumes; without it, the data stays for the next `nemo up`.

## Next steps

- Read the [concepts](concepts.md) to see how bundles, targets and stacks fit together.
- Turn your own compose files into bundles: [writing bundles](guides/bundles.md).
- Deploy to a remote host or a Swarm cluster: [targets](guides/targets.md).
- Automate it: [continuous delivery](guides/ci.md).
