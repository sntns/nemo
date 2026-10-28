# Managing stacks

A stack is the set of bundles deployed together on one target, with their settings. This guide covers writing a stack's manifest, keeping it with nemo, giving it parameters and credentials, and bringing it up and down.

## The manifest

A manifest is a YAML or JSON file:

```yaml
name: shop

target:
  orchestrator: swarm
  configuration:
    host: ssh://deploy@prod-1.example.com

remote: "docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}"

parameters:
  domain: shop.example.com
  log-level: info

credentials:
  - name: tls-certificate
    source: { path: /etc/shop/tls/fullchain.pem }
  - name: tls-key
    source: { path: /etc/shop/tls/privkey.pem }

installations:
  - name: database
    bundle: { version: 2.1.0 }
    credentials:
      - name: database-password
        source: { env: SHOP_DATABASE_PASSWORD }
  - name: api
    bundle: { version: 1.4.0 }
    parameters:
      log-level: debug          # over the stack's
  - name: frontend
    bundle: { version: 1.4.0 }
```

The [manifest reference](../reference/manifest.md) lists every field. The [targets guide](targets.md) covers the `target` block.

## Where the bundles are

Each installation names its bundle in one of three ways:

| `bundle` | Means |
|---|---|
| `{ version: 1.4.0 }` | The stack's `remote` locates it, from the installation's name and this version. |
| `{ repository: registry.example.com/shop/api, version: 1.4.0 }` | A bundle in a registry, as other CNAB tools reference it. |
| `{ file: api/bundle.json }` | A bundle file, relative to the manifest. Its images must already be where the bundle says. |

The `remote` is a template of the bundle's location, with `{{ .Component }}` for the installation's name and `{{ .Version }}` for its version:

| `remote` | For |
|---|---|
| `docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}` | Bundles published to a registry. |
| `oci:/srv/bundles/{{ .Component }}.oci` | Bundles in OCI layout directories: a local build, a delivery on a disk. A relative path is relative to the manifest. |

Giving versions alone and a `remote` keeps one manifest for every environment: the CI imports it with the remote of its build, production with the registry's.

```bash
nemo stack import deploy/shop.yaml --remote "oci:$PWD/build/bundles/{{ .Component }}.oci"
```

## Parameters

A bundle declares its parameters; the stack gives them values. The stack's `parameters` go to every installation whose bundle declares a parameter of that name, and an installation's own `parameters` override them. A parameter a bundle does not declare is not passed to it.

So one `domain` parameter at the stack level reaches every bundle that needs a domain, and only those.

## Credentials

Credentials are given by their **source**, never by their value. nemo reads the value when an action runs, hands it to gill for that action only, and keeps only the source.

| Source | Value | Example |
|---|---|---|
| `path` | The content of a file. `$VARIABLES` are expanded. | `{ path: $HOME/.shop/tls.key }` |
| `env` | An environment variable of the shell that runs nemo. | `{ env: SHOP_DATABASE_PASSWORD }` |
| `command` | The output of a command. It runs without a shell: write a script for pipes. | `{ command: "pass show shop/tls-key" }` |
| `value` | The value itself, written in the manifest. For values that are not secret. | `{ value: "public-key-id" }` |

As with parameters, the stack's `credentials` go to every installation whose bundle declares them, and an installation's own override them. A credential the bundle requires but the stack does not give stops the action before it starts.

On the command line, a credential is `NAME=SOURCE:VALUE`:

```bash
nemo stack set --credential tls-key=path:/etc/shop/tls/privkey.pem
nemo stack set --credential database-password=env:SHOP_DATABASE_PASSWORD
```

A relative `path` is made absolute, from the manifest's directory when imported, from the current directory with `nemo stack set`.

## Keeping stacks

nemo keeps stacks by name in its home directory, `~/.nemo` (or `$NEMO_HOME`), and one of them is the **current stack**: commands act on it unless given `--stack NAME`. With shell completion (`nemo completion --help`), Tab completes the names of the stacks, of their installations and of their services.

| Command | Does |
|---|---|
| `nemo stack create [NAME]` | Opens the editor on a new stack, and keeps it. |
| `nemo stack edit [NAME]` | Opens the editor on a stack nemo keeps, on its review. |
| `nemo stack import FILE` | Keeps a manifest written elsewhere. In a terminal, the editor opens on its review first; `--yes` keeps it as it is. `--name` renames the stack, `--remote` sets where its bundles are, `--format yaml\|json` chooses how it is kept. |
| `nemo stack list` | Lists the stacks, the current one marked with `*`. |
| `nemo stack use NAME` | Makes a stack the current one. |
| `nemo stack inspect [NAME]` | Prints a stack's manifest. |
| `nemo stack set [NAME]` | Changes a stack's remote, parameters (`--param NAME=VALUE`) or credentials (`--credential NAME=SOURCE:VALUE`). |
| `nemo stack remove NAME` | Forgets a stack, its manifest and its installations' records. Only once nothing of it is installed. A stack whose records are on its target is only forgotten here: its records stay there. |

### Records on the target

nemo keeps the records of a stack's installations, their state and every action run on them, with its manifest in its home. A stack deployed from a CI job, or by several people, keeps them on its target instead, with `records: target` in its manifest: every home that imports the manifest then sees the same installations. `nemo status` on a laptop shows what the CI deployed, and an upgrade from there upgrades the same installations.

They are in a volume of the target, `nemo-records-<stack>`, which nemo creates the first time. nemo reaches it through a container that never starts, of an empty image it makes on the target, `nemo-records:1`: Docker copies the records in and out of it over the connection nemo already has, with nothing running and no port open. The volume is local to the Docker nemo reaches: on a swarm of several managers, reach the stack through the same one.

One nemo writes a stack at a time: the container of the one writing is named after the stack, and another nemo writing it meanwhile stops, the stack busy. A nemo killed while writing leaves it; `docker rm nemo-records-<stack>` on the target frees the stack. Reading never waits.

A stack Terraform manages refuses to be written by nemo, which says so: `up`, `down` and the actions on its installations change it only with `--force`, which Terraform undoes at its next apply. Reading it works as for any stack.

### The editor

`nemo stack create`, `nemo stack edit` and `nemo stack import` share an editor that takes the terminal, one question at a time. The steps run left to right at the top, each marked complete (✓), incomplete (!) or not reached yet:

1. **Bundles**: where they are, a directory of OCI layouts or a registry, then which ones and their version. A registry and namespace is given as `REGISTRY/NAMESPACE`: `registry.example.com/shop` for bundles pushed as `registry.example.com/shop/NAME:VERSION`, from which the editor writes the stack's remote, `docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}`.
2. **Target**: which Docker it is, the environment's (`DOCKER_HOST`, the current docker context, or the local socket), an address or a docker context; the orchestrator; how images reach the target; and what each volume type the bundles use is made of: Docker's own local volumes, a directory of the host each, or a directory of an NFS export each, as [the targets guide](targets.md#volume-types) writes them. A type written otherwise in the manifest is kept as it is.
3. **Parameters**: each parameter the bundles declare, with the description and the default their authors wrote.
4. **Credentials**: each credential, where it is read from (a file, an environment variable, a command, or its value), then the file, variable, command or value itself.
5. **Review**: the whole stack, what is missing marked `!`. Each line opens the step that changes it; the last saves the stack. Each installation shows the values the manifest gives it of its own, over the stack's; they are changed in the manifest. A review taller than the terminal shows the lines around the cursor, and how many it leaves out above and below.

Enter goes on, shift+tab goes back, esc quits without saving. `edit` and `import` open on the review.

## Bringing a stack up

```bash
nemo up                           # the current stack
nemo up --stack shop              # another stack nemo keeps
nemo up -f deploy/shop.yaml       # a manifest nemo does not keep
```

`nemo up` brings the target to the manifest:

1. It locates every bundle, resolves the dependencies the stack does not list, and checks that every dependency's version satisfies the ranges its dependant accepts. Nothing is touched if one does not.
2. It runs every bundle's `validate` action, with what it would install it with: gill checks the bundle against the target, such as a service name too long for Swarm or a compose option Swarm does not support, and changes nothing. Nothing is touched if one fails. A bundle built by nemo 0.2 or before can't be checked: it is checked when its turn comes, as it installs.
3. It installs the installations that are not installed, and upgrades those that are, dependencies first. An upgrade with nothing changed is quick: gill keeps every container or service whose specification did not change.
4. It uninstalls the installations of the stack that the manifest no longer lists, dependants first. Their volumes stay, unless `--purge`.

It stops at the first action that fails. Fix the cause, and run `nemo up` again: it starts over from the manifest.

### One installation at a time

After rebuilding one component, bring that installation alone up to date:

```bash
nemo up api
nemo up api frontend
```

With installation names, `nemo up` acts on those alone, as the manifest describes them, plus any of their dependencies that are not installed yet. It uninstalls nothing.

### Dependencies

A bundle declares the bundles it requires, with version ranges. When the stack lists them, nemo checks their versions against the ranges and installs them first. When it does not, nemo looks for them at the stack's `remote`, at the version of the bundle that requires them, which is how bundles built or delivered together are found; else it pulls them from the repository the dependency names, at the highest version its ranges allow.

## Taking a stack down

```bash
nemo down                 # the current stack
nemo down --purge         # and its volumes
nemo down --stack shop
```

`nemo down` uninstalls every installation of the stack, dependants first. It reads what to remove from nemo's records, so it needs no manifest and no bundle at hand. An installation whose install failed is uninstalled too, since it may have left part of its services on the target. Volumes stay unless `--purge`: the next `nemo up` finds the data where it was.

## Reading what happened

| Command | Shows |
|---|---|
| `nemo status` | Each installation, its version, its last action, and how many of its services run. Installations not ready are unfolded; `-a` unfolds all. |
| `nemo logs [INSTALLATION [SERVICE]]` | The logs of the stack's services, one installation's, or one service's. `-f` follows them, `-n` limits the lines. |
| `nemo installation list` | The installations of the stack, their version, their last action. |
| `nemo installation show NAME` | An installation's bundle, target, parameters and credential sources. |
| `nemo installation runs NAME` | Every action run on an installation, when, with which version, and how it ended. |
| `nemo installation outputs NAME [OUTPUT]` | The outputs of an installation's bundle, or one output's raw value for scripts. |

Each of them prints JSON with `--json`, except `logs`.

## Without a manifest

`nemo installation install`, `upgrade` and `uninstall` run one action on one bundle, with flags for everything the manifest would say. They are the steps `nemo up` and `nemo down` are made of: use them for a one-off bundle, or in a script that drives each step itself.

```bash
nemo installation install -f docker://registry.example.com/tools/backup:1.0.0 \
  --stack ops --orchestrator compose --param schedule="0 3 * * *"
nemo installation upgrade backup --param schedule="0 4 * * *"   # the rest is kept from the last run
nemo installation uninstall backup
```
