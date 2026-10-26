## nemo stack import

Keep a stack from its manifest, YAML or JSON, and make it the current one

```
nemo stack import FILE [flags]
```

### Examples

```
  nemo stack import stack.yaml --name staging
  nemo stack import stack.yaml --remote "oci:$PWD/build/bundles/{{ .Component }}.oci"
```

### Options

```
      --format string   format to keep it in, yaml or json (default: the file's)
  -h, --help            help for import
      --name string     name of the stack, over the manifest's
      --remote string   where the bundles given by version are, over the manifest's: where they were built or pushed
```

### SEE ALSO

* [nemo stack](nemo_stack.md)	 - Keep stacks: their manifests, in nemo's home, and the current one

