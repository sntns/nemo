# Security Policy

## Reporting a vulnerability

If you believe you've found a security vulnerability in nemo or gill (for
example, a way for a crafted bundle or manifest to read or write outside of
what it declares on the target, to leak a credential, or to run something
other than its compose project), please **do not** open a public GitHub
issue.

Instead, report it privately using
[GitHub's private vulnerability reporting](https://github.com/sntns/nemo/security/advisories/new)
for this repository, or email **security@sentiens.fr** with:

- A description of the issue and its potential impact.
- Steps to reproduce, or a minimal bundle or manifest that shows it.
- The affected version (`nemo --version`, gill's image tag) or commit, and
  the target: Compose or Swarm, Docker version.

We'll acknowledge your report and aim to keep you updated as it's
investigated and fixed. Please give us a reasonable amount of time to
address the issue before any public disclosure.

## How nemo handles secrets

- Credentials are read from their source (a file, an environment variable,
  a command) when an action runs, and handed to gill for that action only.
  nemo never writes their values: it keeps their sources.
- Sensitive parameters, and the registry credentials nemo reads from
  `docker login`, are never stored and show as `*******` in the records.
- The files nemo keeps in its home are `0600`, its directories `0700`.
- gill runs with the target's Docker socket mounted, as any tool that
  deploys onto Docker must: only deploy bundles you trust.

## Supported versions

This project is pre-1.0 and does not yet maintain separate maintenance
branches. Security fixes are applied to `main` and the latest release.
