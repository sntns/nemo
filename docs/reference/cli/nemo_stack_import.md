## nemo stack import

Keep a stack from its manifest, YAML or JSON, and make it the current one

### Synopsis

Keep a stack from its manifest, YAML or JSON, and make it the current one. In a
terminal, the editor opens on its review first: what the manifest misses is
marked, and can be completed before the stack is kept. --yes keeps it as it is.

```
nemo stack import FILE [flags]
```

### Examples

```
  nemo stack import stack.yaml --name staging
  nemo stack import stack.yaml --remote "oci:$PWD/build/bundles/{{ .Component }}.oci" --yes
```

### Options

```
      --format string   format to keep it in, yaml or json (default: the file's)
  -h, --help            help for import
      --name string     name of the stack, over the manifest's
      --remote string   where the bundles given by version are, over the manifest's: where they were built or pushed
  -y, --yes             keep the manifest as it is, without the editor
```

### SEE ALSO

* [nemo stack](nemo_stack.md)	 - Keep stacks: their manifests, in nemo's home, and the current one

