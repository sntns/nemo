package docker

import (
	"errors"
	"strings"

	"github.com/containerd/errdefs"
)

// IsNotFound reports whether the daemon answered that the object does not exist.
func IsNotFound(err error) bool {
	return errdefs.IsNotFound(err) || errors.Is(err, errdefs.ErrNotFound)
}

// IsInUse reports whether the daemon refused to remove an object because
// something still uses it: a network with endpoints, a config or a secret a
// service references, a volume a container mounts.
// The daemon gives no error kind for it, so the messages are matched.
func IsInUse(err error) bool {
	message := err.Error()
	return errdefs.IsConflict(err) || strings.Contains(message, "in use") || strings.Contains(message, "active endpoints")
}
