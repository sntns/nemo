package swarm

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_errors(t *testing.T) {
	t.Parallel()

	web := types.ServiceConfig{Name: "web"}
	web.Image = "nginx"
	web.Ports = []types.ServicePortConfig{{Target: 80, Published: "8080", HostIP: "127.0.0.1"}}
	project := &types.Project{
		Name:     "demo",
		Services: types.Services{"web": web},
		Configs:  types.Configs{"page": {Name: "demo_page", Content: "hello", TemplateDriver: "golang"}},
	}

	err := validate(project).Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.web.ports[0].host_ip")
	assert.Contains(t, err.Error(), "configs.page.template_driver")
	assert.Empty(t, validate(project).Warnings())
}
