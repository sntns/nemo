# Use cases

nemo fits wherever an application made of several containers must be deployed the same way on many Dockers: developer machines, CI runners, servers at customer sites, Swarm clusters, sites with no network. Each section below describes a situation, what nemo changes, and the commands involved.

## A whole platform on every developer's machine

**The situation.** Your product is ten or twenty services: databases, a message broker, APIs, workers, a gateway. Each has its compose file. Getting all of it running on a laptop takes a wiki page of commands, and the result rarely matches what runs in production.

**With nemo.** Each service group is a bundle, and the platform is one stack whose manifest lives in your repository. A developer brings the whole platform up with one command, in the right order, with the same bundles and settings as the other environments. After changing one component, they rebuild its bundle and bring that installation alone up to date:

```bash
nemo stack import deploy/dev.yaml --remote "oci:$PWD/build/bundles/{{ .Component }}.oci"
nemo up                       # the whole platform, dependencies first
# ... change the api, rebuild its bundle ...
nemo up api                   # upgrades api alone, as the manifest describes it
nemo status                   # what runs, and what does not
nemo logs api -f              # its services' logs
nemo down                     # stop it all, keep the data
```

`images: load` in the target lets bundles built locally run without pushing anything to a registry.

## An ephemeral environment for every change

**The situation.** You want each pull request validated against the real platform, not mocks. Spinning up a full environment in CI means scripting the order of deployments, waiting for services to be ready, and cleaning up reliably.

**With nemo.** The pipeline builds the bundles that changed, brings up a stack from a manifest kept in the repository, runs the tests, and takes it down with its data. `nemo up` returns only once every service is running, verified, so tests start on a ready platform. Progress prints one line per change in CI logs, and `--json` gives a machine-readable record of each run.

```bash
nemo stack import deploy/ci.yaml --name "pr-$PR_NUMBER" \
  --remote "oci:$PWD/build/bundles/{{ .Component }}.oci"
nemo up --json > runs.json
./run-tests.sh
nemo down --purge
```

See [continuous delivery](guides/ci.md).

## Software at customer sites, factories, stores, edge boxes

**The situation.** You run the same application on dozens of single machines you do not fully control: a server in a customer's data center, a box in a store, a PC on a factory floor. Each has its own settings, and you need to know what version runs where.

**With nemo.** Each site is a stack: the same bundles, with that site's parameters and credentials, deployed with Compose over SSH. Nothing is installed on the machine besides Docker. An upgrade is a new version in the manifest and a `nemo up`; nemo keeps the history of every run on every site.

```yaml
name: store-lyon-02
target:
  orchestrator: compose
  configuration:
    host: ssh://ops@store-lyon-02.example.net
remote: "docker://registry.example.com/retail/{{ .Component }}:{{ .Version }}"
parameters:
  store-id: lyon-02
installations:
  - name: pos
    bundle: { version: 3.2.1 }
  - name: inventory
    bundle: { version: 1.8.0 }
```

## Production on a Swarm cluster

**The situation.** Your production runs on Docker Swarm, from one to a handful of nodes. `docker stack deploy` applies one compose file and returns before anything is running; ordering several stacks, rolling configs and secrets, and knowing whether the deployment succeeded is left to you.

**With nemo.** The same bundles that run on laptops with Compose run on the cluster with Swarm: `deploy` settings such as replicas, placement and update policy apply there. nemo creates or updates each service, waits until its tasks converge and stay up, and moves to the next installation only then. Configs and secrets are versioned by their content, so changing one rolls the services that use it. Volumes get the driver the cluster needs, such as a shared NFS storage, through the target's volume types.

```yaml
target:
  orchestrator: swarm
  configuration:
    context: prod-cluster
    volumes:
      data:
        driver: local
        options: { type: nfs, o: "addr=10.0.0.20,rw", device: ":/exports/{{.Stack}}/{{.Name}}" }
```

## Sites with no network

**The situation.** Some sites have no internet access and no registry: a secure facility, a ship, a remote industrial plant. Deploying there usually means `docker save` archives, hand-written load scripts, and hoping the versions match.

**With nemo.** Copy the release's bundles, with all their images, gill included, to a disk as OCI layout directories. On site, the stack's `remote` points at the disk and `images: load` tells nemo to load the images into the target's Docker itself. Same bundles, same digests, same commands.

```bash
# connected side
nemo bundle copy docker://registry.example.com/shop/api:1.4.0 oci:/media/kit/api.oci
# on site
nemo stack import kit/stack.yaml --remote "oci:/media/kit/{{ .Component }}.oci"
nemo up
```

See [offline sites](guides/offline.md).

## Shipping your software to customers

**The situation.** You sell software that customers run on their own infrastructure. Each customer must provide their own settings and secrets: a domain, a certificate, database passwords. Your installation instructions are a long document, and support tickets start with "which version do you run?".

**With nemo.** You publish versioned bundles. The customer runs `nemo stack create`: an editor lists your bundles from the registry or the delivered directory, asks for their target, then for every parameter and credential the bundles declare, with your descriptions, and writes the manifest. `nemo up` installs it; `nemo installation list` tells support exactly what runs.

```bash
nemo stack create
nemo up
```

## Deploying from a controlled environment

**The situation.** Policy forbids running ad hoc containers on production hosts, or the hosts are reachable only from inside a Kubernetes cluster.

**With nemo.** gill can run as a Kubernetes Job instead of a container on the target. It reaches the target's Docker over `tcp://` with TLS, from inside the cluster, under the cluster's RBAC, quotas and logs. The application still runs on the target's Docker: Kubernetes is only where the installer runs. See [where gill runs](guides/targets.md#where-gill-runs).
