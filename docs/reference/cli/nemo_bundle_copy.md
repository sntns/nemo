## nemo bundle copy

Copy a bundle and its images, from an OCI layout to a registry for one

```
nemo bundle copy SOURCE DESTINATION [flags]
```

### Examples

```
  nemo bundle copy oci:build/api.oci docker://registry.example.com/shop/api:1.4.0
  nemo bundle copy docker://registry.example.com/shop/api:1.4.0 oci:/media/usb/api.oci
```

### Options

```
  -h, --help   help for copy
```

### SEE ALSO

* [nemo bundle](nemo_bundle.md)	 - Build and copy bundles

