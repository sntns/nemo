# Continuous delivery

nemo fits a pipeline in three steps: **build** a bundle for each component that changed, **publish** it, and **deploy** a stack that uses it. This guide shows each step, then a complete GitHub Actions workflow.

## In a pipeline, nemo

- prints its progress as **one line per change** when its output is not a terminal, readable in any CI log;
- returns only when every service runs, or fails with a **non-zero status** and the error that caused it;
- prints a **JSON record** of each run on standard output with `--json`, while the progress goes to standard error;
- reads **credentials from the environment** (`env:` sources), where your CI injects its secrets;
- needs **no Docker of its own** when the target is remote, and no state between jobs besides the stack's manifest.

## Build and publish

Build each component's images as you do today, then its bundle. With `--version` the pipeline stamps the version it builds:

```bash
docker buildx build -t shop-api:latest --output type=oci,dest=build/images/shop-api.oci,tar=false api/
nemo bundle build -f api/compose.yaml --version "$VERSION" \
  --image shop-api:latest=oci:build/images/shop-api.oci \
  -o docker://ghcr.io/example/shop/api
```

`-o docker://` pushes the bundle and all its images, gill's included, to the registry; the bundle is tagged with its version. To validate a change without publishing it, write it to an OCI layout instead, `-o oci:build/bundles/api.oci`, and deploy it with `images: load`.

## Deploy

Keep the stack's manifests in your repository, one per environment, with versions alone and no `remote`. Each job gives the remote that matches it:

```bash
nemo stack import deploy/staging.yaml \
  --remote "docker://ghcr.io/example/shop/{{ .Component }}:{{ .Version }}"
nemo up
```

To deploy a new version, the pipeline updates the version in the manifest, commits it, and runs `nemo up`: the repository is the record of what each environment should run, and nemo's records say what it ran.

## Ephemeral environments

For each pull request, bring up a complete environment from the bundles just built, test it, and remove it with its data:

```bash
nemo stack import deploy/ci.yaml --name "pr-$PR" \
  --remote "oci:$PWD/build/bundles/{{ .Component }}.oci"
nemo up --json > runs.json
./run-integration-tests.sh
nemo down --purge
```

The stack's name prefixes everything gill creates, so environments of several pull requests can share a Docker without colliding, as long as their published ports differ. Since nemo keeps its records in its home directory, give each job its own (`NEMO_HOME=$RUNNER_TEMP/nemo`), or keep the same one to clean up after a cancelled job.

## GitHub Actions

```yaml
name: Deliver

on:
  push:
    branches: [main]

jobs:
  deliver:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    env:
      VERSION: 1.4.${{ github.run_number }}
    steps:
      - uses: actions/checkout@v4

      - name: Install nemo
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release download --repo sntns/nemo --pattern 'nemo_*_linux_amd64.tar.gz' --output - | sudo tar -xz -C /usr/local/bin nemo

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and publish the api bundle
        run: |
          docker buildx build -t shop-api:latest --output type=oci,dest=build/images/shop-api.oci,tar=false api/
          nemo bundle build -f api/compose.yaml --version "$VERSION" \
            --image shop-api:latest=oci:build/images/shop-api.oci \
            -o docker://ghcr.io/${{ github.repository_owner }}/shop/api

      - name: Deploy to staging
        env:
          DOCKER_HOST: ssh://deploy@staging.example.com
          SHOP_DATABASE_PASSWORD: ${{ secrets.STAGING_DATABASE_PASSWORD }}
        run: |
          mkdir -p ~/.ssh && install -m 600 /dev/stdin ~/.ssh/id_ed25519 <<< "${{ secrets.STAGING_SSH_KEY }}"
          ssh-keyscan staging.example.com >> ~/.ssh/known_hosts
          nemo stack import deploy/staging.yaml \
            --remote "docker://ghcr.io/${{ github.repository_owner }}/shop/{{ .Component }}:{{ .Version }}"
          nemo up api
```

- `docker/login-action` writes the credentials nemo uses to push the bundle, and that it hands to the target to pull the images.
- `DOCKER_HOST` points the target at the staging server over SSH, when the manifest gives no `host`.
- `nemo up api` upgrades the api alone, as the manifest describes it, with the version the manifest names.

## Recording what ran

With `--json`, `nemo up` and `nemo down` print the record of each run: the installation, the action, the bundle and its version, the target, the parameters (sensitive ones as `*******`), the credentials by name, the result and the outputs. Keep it as an artifact of the job:

```bash
nemo up --json > runs.json
jq -r '.runs[] | "\(.installation) \(.action) \(.bundle.version) \(.status)"' runs.json
```

nemo's own records stay in its home directory: `nemo installation runs NAME` lists every action run on an installation, from the machine that ran them.
