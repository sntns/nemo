## nemo up

Bring a stack to its manifest: install, upgrade, and uninstall what it no longer has

```
nemo up [INSTALLATION...] [flags]
```

### Examples

```
  nemo up                  # the current stack
  nemo up api              # that installation alone, as the manifest has it
  nemo up --stack staging
  nemo up -f manifest.yaml
```

### Options

```
  -f, --file string    a stack's manifest, instead of a stack nemo keeps
  -h, --help           help for up
      --json           print the record of the runs as JSON on stdout
      --purge          also remove the volumes of the bundles the stack no longer has
      --stack string   a stack nemo keeps (default: the current one)
```

### SEE ALSO

* [nemo](nemo.md)	 - Deploy CNAB bundles to Docker, with Compose or Swarm

