# nemo documentation

nemo deploys applications made of containers onto Docker, with Compose on one host or Swarm on many, as versioned packages called bundles. Start with the guide that matches what you are doing.

## Learn

- [Getting started](getting-started.md): install nemo and deploy a first stack on your machine.
- [Concepts](concepts.md): bundles, gill, targets, stacks and installations, and how they fit together.
- [How it works](how-it-works.md): what happens when you build a bundle, bring a stack up, and take it down.
- [Use cases](use-cases.md): development platforms, ephemeral environments, edge and on-premise sites, Swarm clusters, offline sites, software vendors.
- [Comparison](comparison.md): nemo next to Docker Compose, Swarm stacks, Porter, Helm, and deployment scripts.

## Guides

- [Writing bundles](guides/bundles.md): turn a compose file into a versioned bundle, and publish it.
- [Managing stacks](guides/stacks.md): write and keep manifests, give parameters and credentials, bring stacks up and down.
- [Targets](guides/targets.md): Compose or Swarm, remote Docker, registries or no registry, volume types, where gill runs.
- [Continuous delivery](guides/ci.md): build, publish and deploy bundles from a pipeline.
- [Offline sites](guides/offline.md): deploy where there is no network and no registry.
- [Troubleshooting](troubleshooting.md): common errors, and how to find out what went wrong.

## Reference

- [Commands](reference/cli/nemo.md): every command and flag of the `nemo` CLI.
- [Stack manifest](reference/manifest.md): every field of a stack's manifest.
- [Bundle format](reference/bundle-format.md): `x-nemo-bundle`, `x-nemo-volume`, and what a bundle contains.
- [Environment and files](reference/environment.md): the variables nemo and gill read, and what nemo keeps on disk.
- [Standards](reference/standards.md): CNAB, OCI, and running nemo bundles with other CNAB tools.
