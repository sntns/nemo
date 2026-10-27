## nemo stack create

Create a stack in the editor: its bundles, its target, and the parameters and credentials they declare

### Synopsis

Create a stack in the editor, and make it the current one. The bundles come
from a directory of OCI layouts or from a registry; the editor then asks for
the target, and for the parameters and the credentials they declare, one at a
time, and reviews the stack before saving it. The volume types the bundles use
get a local volume, to change in the manifest for another driver.

```
nemo stack create [NAME] [flags]
```

### Options

```
      --format string   format to keep it in, yaml or json (default "yaml")
  -h, --help            help for create
```

### SEE ALSO

* [nemo stack](nemo_stack.md)	 - Keep stacks: their manifests, in nemo's home, and the current one

