package main

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/sntns/nemo/pkg/nemo"
	"github.com/sntns/nemo/pkg/target"
)

func TestStatusText(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	current := nemo.StackStatus{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Swarm, Configuration: target.Configuration{Context: "remote"}},
		Installations: []nemo.InstallationState{
			{Name: "runtime", Version: "1.2.0", Installed: true, Action: "install", Result: "succeeded", Modified: now.Add(-3 * time.Hour),
				Services: []nemo.ServiceState{{Name: "mongo", Running: 1, Desired: 1, State: "running"}}},
			{Name: "iam", Version: "1.10.0", Installed: true, Action: "upgrade", Result: "succeeded", Modified: now.Add(-5 * time.Minute),
				Services: []nemo.ServiceState{{Name: "api", Running: 1, Desired: 1, State: "running"}, {Name: "edge", Running: 0, Desired: 2, State: "starting"}}},
			{Name: "web", Version: "0.1.0", Installed: true, Action: "upgrade", Result: "failed", Modified: now.Add(-30 * time.Second),
				Services: []nemo.ServiceState{{Name: "nginx", Running: 0, Desired: 1, State: "exited"}}},
			{Name: "old", Version: "0.1.0", Action: "uninstall", Result: "succeeded", Modified: now.Add(-72 * time.Hour)},
		},
	}
	assert.Equal(t, `demo · swarm · remote

✔ runtime  1.2.0   installed 3h ago         1/1 services
◌ iam      1.10.0  upgraded 5m ago          1/2 services
  ✔ api                                     1/1
  ◌ edge           starting                 0/2
✖ web      0.1.0   upgrade failed just now  0/1 services
  ✖ nginx          exited                   0/1
· old      0.1.0   uninstalled 3d ago
`, ansi.Strip(statusText(current, false, now)), "the installations ready folded, the others unfolded")
}
