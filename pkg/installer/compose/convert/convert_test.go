package convert

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// service returns a service of that name, changed by edit: most fields of a
// ServiceConfig are promoted from an embedded struct, so literals can't set them.
func service(name string, edit func(service *types.ServiceConfig)) types.ServiceConfig {
	service := types.ServiceConfig{Name: name}
	if edit != nil {
		edit(&service)
	}
	return service
}

func TestConditions(t *testing.T) {
	t.Parallel()

	project := &types.Project{Services: types.Services{
		"db": service("db", func(service *types.ServiceConfig) {
			service.HealthCheck = &types.HealthCheckConfig{Test: []string{"CMD", "true"}}
		}),
		"migrate": service("migrate", nil),
		"cache":   service("cache", nil),
		"web": service("web", func(service *types.ServiceConfig) {
			service.DependsOn = types.DependsOnConfig{
				"migrate": {Condition: types.ServiceConditionCompletedSuccessfully},
				"cache":   {Condition: types.ServiceConditionStarted},
			}
		}),
	}}
	assert.Equal(t, map[string]Condition{
		"db":      Healthy,
		"migrate": Completed,
		"cache":   Started,
		"web":     Started,
	}, Conditions(project))
}

func TestService_hash(t *testing.T) {
	t.Parallel()

	project := &types.Project{Name: "demo", Networks: types.Networks{"default": {Name: "demo_default"}}}
	web := service("web", func(service *types.ServiceConfig) {
		service.Image = "nginx:1.27"
		service.Networks = map[string]*types.ServiceNetworkConfig{"default": nil}
	})
	first, err := Service(project, web, map[string]string{"io.nemo.stack": "demo"})
	require.NoError(t, err)
	second, err := Service(project, web, map[string]string{"io.nemo.stack": "demo"})
	require.NoError(t, err)
	assert.Equal(t, first.Hash, second.Hash, "the same service hashes the same")

	web.Image = "nginx:1.28"
	changed, err := Service(project, web, map[string]string{"io.nemo.stack": "demo"})
	require.NoError(t, err)
	assert.NotEqual(t, first.Hash, changed.Hash, "a new image recreates the containers")
}
