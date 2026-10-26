# Contributing to nemo

Thanks for considering a contribution. This page is for people working on nemo itself: how the code is laid out, how to build and test it, and how releases are made. To use nemo, read the [documentation](docs/README.md).

## Before you start

For anything beyond a small fix (a new command, a new compose setting gill should honour, a change to the bundle format or to the manifest), please open an issue first to discuss the approach. It avoids spending effort on a pull request that goes a direction the maintainers wouldn't take.

## Layout

nemo is one Go module with two programs: the `nemo` CLI, and `gill`, the invocation image of every nemo bundle.

| Path | What it is |
|---|---|
| `cmd/nemo` | The CLI: cobra commands, thin. Flags in, a call to `pkg/nemo`, rendering out. |
| `cmd/gill` | The entrypoint of the invocation image, `/cnab/app/run`. |
| `pkg/nemo` | The API the CLI is built on: `Install`, `Upgrade`, `Uninstall`, `Up`, `Down`, `Status`, `Logs`, the installations and runs kept. |
| `pkg/stack` | The stack's manifest, its store in nemo's home, and the order of its dependencies. |
| `pkg/bundle` | Building bundles from compose files; pushing and copying them with their images. |
| `pkg/cnab` | Bundles: loading, validation, nemo's extensions and their JSON schemas, OCI references, dependency resolution. |
| `pkg/runtime` | The CNAB runtime: builds the claim, runs the invocation image with the docker or kubernetes driver, returns the result. |
| `pkg/transfer` | Loading a bundle's images into the target's Docker, without a registry. |
| `pkg/credentials` | Credential sources, read when an action runs and never stored. |
| `pkg/storage` | Installations, runs, results and outputs, as CNAB claims; `pkg/storage/file` keeps them on disk. |
| `pkg/target` | The target and where the installer runs, shared by the manifest and the CLI. |
| `pkg/installer` | gill's side: reads the CNAB operation, loads the compose project, runs the orchestrator the target names. |
| `pkg/installer/compose`, `pkg/installer/swarm` | The two orchestrators; each `convert` package turns compose into Docker API types. |
| `pkg/docker` | The Docker Engine client both orchestrators share, one file per kind of object. |
| `pkg/labels`, `pkg/progress` | The labels on everything gill creates, and the events it reports. |
| `internal/` | Helpers for decoding documents and writing test images. |
| `build/gill` | gill's Dockerfile. |
| `docs/` | The user documentation; `docs/reference/cli` is generated. |
| `examples/` | Stacks and bundles to try nemo with. |

A few rules the code follows:

- The code that works emits progress events and never prints; the CLI renders them.
- gill keeps no state and knows nothing about stacks or storage: everything it needs is in the CNAB operation.
- Requests and results are plain structs; validation uses ozzo-validation.
- Nothing secret is written to disk or printed: credentials by their source, sensitive values as `*******`.

## Building and testing

You need Go (the version in `go.mod`), Docker for gill's image, and [golangci-lint](https://golangci-lint.run) v2.

```bash
make lint          # golangci-lint
make test          # go test -race ./...
make build         # bin/nemo
make gill          # gill's image in the local Docker, as gill:dev
make gill-oci      # gill as an OCI layout, bin/gill.oci
make reference     # regenerate docs/reference/cli after changing a command or a flag
```

A nemo built from a checkout has no released gill to put in the bundles it builds. Point it to the one you built:

```bash
make gill-oci
export NEMO_INSTALLER=oci:$PWD/bin/gill.oci
```

To try a change end to end, build and deploy the [examples](examples/README.md) on your local Docker.

CI runs the linter, the tests, a build of every released platform, a check that `docs/reference/cli` is up to date, and a build of gill's image, on every push and pull request. A pull request won't be merged if any of them fails.

## Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org): `feat(...)`, `fix(...)`, `docs(...)`, `refactor(...)`, `test(...)`, `ci(...)`, `chore(...)`, with a summary line in the imperative and a body that explains why. `feat` and `fix` appear in the changelog and decide the next version, so use them for changes users see.

## Releases

Releases are made by [release-please](https://github.com/googleapis/release-please): it keeps a release pull request up to date with the changes merged on `main`. Merging it tags the version, creates the GitHub release with its changelog, and then:

- [GoReleaser](https://goreleaser.com) builds `nemo` for Linux, macOS and Windows on amd64 and arm64, attaches the archives and their checksums to the release, and updates the formula in [sntns/homebrew-tap](https://github.com/sntns/homebrew-tap);
- gill's image is built for linux/amd64 and linux/arm64 and pushed to `ghcr.io/sntns/gill` with the same version.

A released `nemo` builds bundles with the gill of its own version, so both are always published together.

The release workflow needs the `HOMEBREW_TAP_TOKEN` secret, a token that can push to `sntns/homebrew-tap`.

## Submitting a pull request

1. Fork the repository and create a branch off `main`.
2. Keep the change focused: unrelated cleanup belongs in its own pull request.
3. Make sure `make lint test` passes, and run `make reference` if a command changed.
4. Update the documentation in `docs/` when users see the change.
5. Open the pull request against `main` and fill in the template.

By submitting a contribution, you agree it is licensed under the [Apache License 2.0](LICENSE), the license of this project.
