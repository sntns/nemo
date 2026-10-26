---
name: Bug report
about: Something doesn't work the way it should
title: ""
labels: bug
assignees: ""
---

**What happened**
A clear description of what went wrong.

**Command run**

```
nemo ...
```

**Full output**

Run it again with `2>&1 | cat` so that the progress prints a line for each
change, and paste all of it.

```
...
```

**Expected behavior**
What you expected to happen instead.

**Environment**
- `nemo --version`:
- The target: `compose` or `swarm`, local or remote (`ssh://`, `tcp://`, a docker context), number of nodes:
- `docker version` on the target:
- How images reach it: `registry` or `load`:
- Where gill runs: `docker` or `kubernetes`:

**The stack or the bundle (if relevant)**
The manifest (`nemo stack inspect`) and the compose file of the bundle, with
secrets removed.
