# Targets

A target is where a stack's bundles run: one Docker Engine, and the orchestrator that deploys there. The same bundles run on every target; the target decides how they are deployed, how their images arrive, and what their volumes are made of.

```yaml
target:
  orchestrator: swarm               # compose | swarm
  configuration:
    host: ssh://deploy@prod-1       # or context: prod-cluster; default: the environment's Docker
    images: registry                # registry | load
    volumes:                        # what each volume type is made of here
      data:
        driver: local
```

## Compose or Swarm

| | `compose` | `swarm` |
|---|---|---|
| Runs services as | Containers on one Docker, as `docker compose up` does | Services on a swarm of one or more nodes, as `docker stack deploy` does |
| Replicas | `deploy.replicas` containers per service | `deploy.replicas` tasks, scheduled on the nodes |
| Ordering | `depends_on`, with its conditions: started, healthy, completed | Each installation once its services converged |
| Configs and secrets | Written into the containers | Docker configs and secrets, named after their content |
| Updates | A container whose specification changed is recreated | Services updated with their `update_config`, configs and secrets rolled |
| Ready when | Every container reached the state its dependants wait for | Every service's tasks run, and stay up |
| Swarm-only settings | Ignored, with a warning | Applied |

Choose `compose` for a single machine: a laptop, a server, an edge box. Choose `swarm` for a cluster, or for a single machine where you want rolling updates and Docker's configs and secrets.

nemo never guesses the orchestrator from the Docker it finds: a Docker that is a swarm manager can still run Compose projects. When the target is `swarm` and its Docker is the one on this machine, `nemo up` creates the swarm if there is none.

## Reaching the target's Docker

| `configuration` | Reaches |
|---|---|
| nothing | The environment's Docker: `DOCKER_HOST`, else the current docker context, else the local socket. |
| `host: unix:///var/run/docker.sock` | A local socket. |
| `host: ssh://deploy@prod-1` | A Docker over SSH, with your SSH configuration and keys. The remote host needs the `docker` command. |
| `host: tcp://10.0.0.5:2376` | A Docker over TCP, with TLS from `DOCKER_CERT_PATH` and `DOCKER_TLS_VERIFY`, or from `tls`. |
| `context: prod-cluster` | A docker context, with its endpoint and TLS files: `docker context create prod-cluster --docker host=...`. |

The target's address is the only one nemo needs: it runs gill there, and reads the status and logs from there. For a swarm, give the address of a manager.

A `tcp://` host can be given its TLS in the manifest instead of the environment: the CA of the daemon's certificate, else the system's, and the client's certificate and key, in PEM. Each is read from a source, as a credential is, and nemo keeps the source: a file, a variable or a command keeps its value out of the manifest; a value is in it, and the records keep its source without it, so an action without the manifest stops and says so.

```yaml
target:
  orchestrator: swarm
  configuration:
    host: tcp://10.0.0.5:2376
    tls:
      ca: { path: certs/ca.pem }
      cert: { path: certs/cert.pem }
      key: { env: DOCKER_KEY }
```

The records of an installation keep the TLS it was deployed with; nemo reaches the target with the TLS of the manifest it has for that address, a file of another machine's being none of its own.

## How images arrive

| `images` | The target | Use it when |
|---|---|---|
| `registry` (default) | Pulls each image from its registry, by digest. | The target can reach the registries: the usual case. |
| `load` | Gets each image from nemo, which loads it into the target's Docker. | There is no registry: local builds, offline sites, short-lived environments. |

With `load`, nemo reads the images from where the bundle came from (an OCI layout or a registry it can reach) and loads them into the target's Docker, for its platform only, gill's included. Each image is tagged after its digest, `shop-api:sha256-4f2a...`, so a new build is always a new image and nothing is overwritten. Only one Docker receives the images, so `load` works with Compose and with a swarm of one node; nemo refuses it for a larger swarm, whose nodes must pull from a registry.

### Private registries

nemo reads the credentials of `docker login` on the machine it runs on, for each registry the bundle's images come from, and hands them to gill for that action: Compose pulls with them, and Swarm passes them to its nodes. They are never stored, and the records show them as `*******`.

```bash
docker login registry.example.com
nemo up
```

## Volume types

A bundle gives its volumes a type, `x-nemo-volume: { type: data }`, and the target says what each type is made of: a driver, its options, and optionally a directory to create first. One bundle then stores its data in a local volume on a laptop and on shared storage in a cluster.

```yaml
volumes:
  data:                             # a local Docker volume
    driver: local

  shared:                           # an NFS share, one directory per volume
    driver: local
    options:
      type: nfs
      o: "addr=10.0.0.20,rw,nfsvers=4{{AppendJoin .Options \",\"}}"
      device: ":/exports/{{.Stack}}/{{.Name}}"
    provision:                      # create the directory before the volume
      directory: "{{.Stack}}/{{.Name}}"
      volume:
        driver: local
        options: { type: nfs, o: "addr=10.0.0.20,rw,nfsvers=4", device: ":/exports" }

  host:                             # a directory of the host
    driver: local
    options: { type: none, o: bind, device: "/srv/{{.Stack}}/{{.Name}}" }
    provision:
      directory: "{{.Stack}}/{{.Name}}"
      volume:
        driver: local
        options: { type: none, o: bind, device: /srv }
```

The driver, its options and the provisioned directory are templates of the volume:

| Template | Is |
|---|---|
| `{{.Stack}}` | The stack's name |
| `{{.Name}}` | The volume's name in its bundle |
| `{{.Options}}` | The `options` its `x-nemo-volume` gives, a list |
| `{{AppendJoin .Options ","}}` | Those options, each after a comma: ready to append to a list that has some already |

`provision` creates the directory before creating the volume, for drivers that mount a directory of a storage and need it to exist: gill mounts the storage, as described by `provision.volume`, in a short-lived container of its own image, and creates the directory there. The target needs nothing else.

A volume that exists is never recreated: change a type's definition, and only new volumes get it. `--purge` removes the volumes, not the provisioned directories.

On the command line, `nemo installation install --volumes types.json` reads the volume types from a JSON file of the same shape.

## Where gill runs

gill, the installer, runs for the duration of each action. By default it runs on the target itself; it can run in a Kubernetes cluster instead. Either way, the application lands on the target's Docker.

| | `docker` (default) | `kubernetes` |
|---|---|---|
| gill runs | On the target, next to what it deploys | As a Job in a Kubernetes cluster |
| It reaches the target | Through the target's socket, mounted | At the target's `tcp://` address, with its TLS files |
| nemo must reach | The target | The cluster, which must reach the target over `tcp://` |
| Images with `load` | All of them, gill's included | The application's; the cluster pulls gill's from its registry |
| Bundle outputs | Read back | Not supported yet |

```yaml
installer:
  driver: kubernetes
  kubernetes:
    namespace: deployments
    kubeconfig: ~/.kube/config      # default: KUBECONFIG, ~/.kube/config, else the cluster nemo runs in
```

Use `kubernetes` when:

- **the network** lets only the cluster reach the target, not the machine you run nemo on;
- **a policy** forbids running ad hoc containers on the target, and deployments must run where they are controlled: RBAC, quotas, the cluster's logs;
- **an operator** in the cluster drives the deployments.

A CI runner inside Kubernetes does not need it: with the `docker` driver, nemo starts gill on the target remotely, from wherever it runs.
