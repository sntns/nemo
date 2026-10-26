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

nemo keeps stacks by name in its home directory, `~/.nemo` (or `$NEMO_HOME`), and one of them is the **current stack**: commands act on it unless given `--stack NAME`.

| Command | Does |
|---|---|
| `nemo stack create [NAME]` | Asks for the bundles, the target, then every parameter and credential the bundles declare, in a form, and writes the manifest. |
| `nemo stack import FILE` | Keeps a manifest written elsewhere. `--name` renames the stack, `--remote` sets where its bundles are, `--format yaml\|json` chooses how it is kept. |
| `nemo stack list` | Lists the stacks, the current one marked with `*`. |
| `nemo stack use NAME` | Makes a stack the current one. |
| `nemo stack inspect [NAME]` | Prints a stack's manifest. |
| `nemo stack set [NAME]` | Changes a stack's remote, parameters (`--param NAME=VALUE`) or credentials (`--credential NAME=SOURCE:VALUE`). |
| `nemo stack remove NAME` | Forgets a stack, its manifest and its installations' records. Only once nothing of it is installed. |

`nemo stack create` lists the bundles of a directory of OCI layouts, or of a registry and namespace, and lets you pick them and their versions. It then asks for the orchestrator, how images reach the target, the format of the manifest, and the value or source of each parameter and credential the bundles declare, with the descriptions their authors wrote. The volume types the bundles use get a local volume: change them in the manifest for another driver.

## Bringing a stack up

```bash
nemo up                           # the current stack
nemo up --stack shop              # another stack nemo keeps
nemo up -f deploy/shop.yaml       # a manifest nemo does not keep
```

`nemo up` brings the target to the manifest:

1. It locates every bundle, resolves the dependencies the stack does not list, and checks that every dependency's version satisfies the ranges its dependant accepts. Nothing is touched if one does not.
2. It installs the installations that are not installed, and upgrades those that are, dependencies first. An upgrade with nothing changed is quick: gill keeps every container or service whose specification did not change.
3. It uninstalls the installations of the stack that the manifest no longer lists, dependants first. Their volumes stay, unless `--purge`.

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

`nemo down` uninstalls every installation of the stack, dependants first. It reads what to remove from nemo's records, so it needs no manifest and no bundle at hand. Volumes stay unless `--purge`: the next `nemo up` finds the data where it was.

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
