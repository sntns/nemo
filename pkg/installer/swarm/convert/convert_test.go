package convert

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/labels"
)

func TestNamespace(t *testing.T) {
	t.Parallel()

	namespace := NewNamespace("demo")
	assert.Equal(t, "demo_web", namespace.Scope("web"))
	assert.Equal(t, "web", namespace.Descope("demo_web"))
	assert.Equal(t, "other_web", namespace.Descope("other_web"), "a name of another namespace is left as is")

	base := map[string]string{labels.Stack: "demo"}
	withStack := AddStackLabel(namespace, base)
	assert.Equal(t, "demo", withStack[LabelNamespace])
	assert.NotContains(t, base, LabelNamespace, "the labels given are not changed")
}

func TestService_labels(t *testing.T) {
	t.Parallel()

	project := &types.Project{Name: "demo"}
	service := types.ServiceConfig{Name: "web"}
	service.Image = "nginx"
	service.Labels = types.Labels{"container": "yes"}
	service.Deploy = &types.DeployConfig{Labels: types.Labels{"service": "yes"}}

	spec, err := Service(NewNamespace("demo"), project, service, nil, nil, map[string]string{labels.Stack: "demo"})
	require.NoError(t, err)

	assert.Equal(t, "demo_web", spec.Name)
	assert.Equal(t, "yes", spec.Labels["service"], "deploy.labels go on the service")
	assert.NotContains(t, spec.Labels, "container")
	assert.Equal(t, "yes", spec.TaskTemplate.ContainerSpec.Labels["container"], "labels go on the containers")
	assert.NotContains(t, spec.TaskTemplate.ContainerSpec.Labels, "service")
	for _, set := range []map[string]string{spec.Labels, spec.TaskTemplate.ContainerSpec.Labels} {
		assert.Equal(t, "demo", set[labels.Stack])
		assert.Equal(t, "web", set[labels.Service])
	}
	assert.NotEmpty(t, spec.Labels[labels.Hash])
}

func TestHash_ignoresRevision(t *testing.T) {
	t.Parallel()

	project := &types.Project{Name: "demo"}
	service := types.ServiceConfig{Name: "web"}
	service.Image = "nginx:1.27"
	spec, err := Service(NewNamespace("demo"), project, service, nil, nil, nil)
	require.NoError(t, err)
	before := spec.Labels[labels.Hash]

	spec.Labels[labels.Revision] = "02"
	after, err := Hash(spec)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	service.Image = "nginx:1.28"
	changed, err := Service(NewNamespace("demo"), project, service, nil, nil, nil)
	require.NoError(t, err)
	assert.NotEqual(t, before, changed.Labels[labels.Hash])
}

func TestConfig(t *testing.T) {
	t.Parallel()

	config := types.ConfigObjConfig{Name: "demo_page", Content: "hello"}
	spec, err := Config("page", config, nil)
	require.NoError(t, err)
	assert.Regexp(t, `^demo_page-[0-9a-f]{14}$`, spec.Name, "named after its content")
	assert.Equal(t, []byte("hello"), spec.Data)
	assert.Equal(t, "page", spec.Labels[labels.Config])

	external, err := Config("page", types.ConfigObjConfig{Name: "shared", External: true}, nil)
	require.NoError(t, err)
	assert.Equal(t, "shared", external.Name)
	assert.Nil(t, external.Data, "only referenced")
}
