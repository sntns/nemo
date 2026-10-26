# Environment and files

## Variables nemo reads

| Variable | |
|---|---|
| `NEMO_HOME` | nemo's home directory, where it keeps stacks and records. Default: `~/.nemo`. A tool built on nemo can give its own, to keep its stacks apart. |
| `NEMO_INSTALLER` | The installer bundles are built with, instead of the gill of nemo's version: `docker://REFERENCE`, `oci:PATH`, or an image name. |
| `DOCKER_HOST`, `DOCKER_CONTEXT` | The target's Docker, when the stack's target gives neither `host` nor `context`. |
| `DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY` | The TLS files of a `tcp://` target. |
| `DOCKER_CONFIG` | Where the credentials of `docker login` are read from. Default: `~/.docker`. |
| `KUBECONFIG` | The kubeconfig of the cluster, with the `kubernetes` driver. |

Credentials given with an `env` source are read from nemo's environment when an action runs.

## Variables gill reads

gill reads the CNAB operation from its environment and from `/cnab`, as any CNAB invocation image does: `CNAB_ACTION`, `CNAB_INSTALLATION_NAME`, `CNAB_REVISION`, `/cnab/bundle.json`, the parameters and the credentials at their destinations. The `nemo-*` parameters reach it as variables:

| Variable | |
|---|---|
| `NEMO_ORCHESTRATOR` | `compose` or `swarm`. Required: gill never guesses it. |
| `NEMO_TIMEOUT` | How long the action may take. Default: `5m`. |
| `NEMO_PURGE` | `true` to remove the volumes on uninstall. |
| `NEMO_VOLUMES` | The target's volume types, as JSON. |
| `NEMO_REGISTRY_AUTH` | The registries' credentials, as JSON. |
| `NEMO_PROGRESS` | `json` to write its events as JSON lines, as nemo asks; plain text otherwise. |

Running gill by hand, without nemo, is described in [standards](standards.md#running-gill-without-nemo).

## Files nemo keeps

```
$NEMO_HOME/
├── current                                   the name of the current stack
└── <stack>/
    ├── manifest.yaml                         or manifest.json
    └── installations/
        └── <installation>/
            ├── installation.json             spec and status
            ├── runs/<claim id>.json          one CNAB claim per action
            ├── results/<result id>.json      one CNAB result per action
            └── outputs/<result id>/<output>  the outputs of each result
```

- Files are `0600`, directories `0700`.
- Credentials are kept by their source only; sensitive parameters and outputs are never written.
- `nemo stack remove` deletes a stack's directory, once nothing of it is installed.

## Exit status

nemo exits with `0` when the command succeeded, and `1` when it failed. When an action fails, the error of the service that failed and gill's output are shown in the progress, once.
