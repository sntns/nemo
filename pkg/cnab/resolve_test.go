package cnab

import (
	"context"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolver_ResolveDependencies(t *testing.T) {
	t.Parallel()

	t.Run("no dependencies", func(t *testing.T) {
		t.Parallel()
		locks, err := Resolver{}.ResolveDependencies(context.Background(), testBundle(t, nil))
		require.NoError(t, err)
		assert.Empty(t, locks)
	})

	extendedBundle := testBundle(t, func(bundle *bundle.Bundle) {
		bundle.Custom[DependenciesExtension.Key] = Dependencies{
			Requires: map[string]Dependency{
				"nginx": {Bundle: "localhost:5000/nginx:1.19"},
				"mysql": {Bundle: "example/mysql:5.7"},
			},
		}
	})

	locks, err := Resolver{}.ResolveDependencies(context.Background(), extendedBundle)
	require.NoError(t, err)
	assert.Equal(t, []DependencyLock{
		{Alias: "mysql", Reference: "example/mysql:5.7"},
		{Alias: "nginx", Reference: "localhost:5000/nginx:1.19"},
	}, locks, "without a sequence, dependencies come sorted by alias")
}

// mockTagLister mocks the registry for testing
type mockTagLister struct {
	tags map[string][]string // map repository to tags
	err  error
}

func (lister *mockTagLister) ListTags(ctx context.Context, reference OCIReference) ([]string, error) {
	if lister.err != nil {
		return nil, lister.err
	}
	return lister.tags[reference.Repository()], nil
}

func TestResolver_ResolveVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		dependency  Dependency
		strategy    VersionStrategy
		wantVersion string
		wantError   string
	}{
		{name: "pinned version",
			dependency:  Dependency{Bundle: "mysql:5.7"},
			wantVersion: "5.7"},
		{name: "range picks highest by default",
			dependency:  Dependency{Bundle: "example/with-versions", Version: &DependencyVersion{Ranges: []string{">=1.0 <1.3"}}},
			wantVersion: "v1.2.0"},
		{name: "range with exact strategy and no tag errors",
			dependency: Dependency{Bundle: "example/with-versions", Version: &DependencyVersion{Ranges: []string{">=1.0 <1.5"}}},
			strategy:   VersionExact,
			wantError:  "version strategy is \"exact\""},
		{name: "range with exact strategy and pinned tag uses tag",
			dependency:  Dependency{Bundle: "mysql:5.7", Version: &DependencyVersion{Ranges: []string{">=1.0 <1.5"}}},
			strategy:    VersionExact,
			wantVersion: "5.7"},
		{name: "range max-patch stays on the reference's minor",
			dependency:  Dependency{Bundle: "example/with-versions:v1.1.0", Version: &DependencyVersion{Ranges: []string{">=1.0 <2"}}},
			strategy:    VersionMaxPatch,
			wantVersion: "v1.1.1"},
		{name: "range max-minor stays on the reference's major",
			dependency:  Dependency{Bundle: "example/with-versions:v1.0.0", Version: &DependencyVersion{Ranges: []string{">=1.0"}}},
			strategy:    VersionMaxMinor,
			wantVersion: "v1.2.0"},
		{name: "range min picks lowest",
			dependency:  Dependency{Bundle: "example/with-versions", Version: &DependencyVersion{Ranges: []string{">=1.0 <1.3"}}},
			strategy:    VersionMin,
			wantVersion: "v1.0.0"},
		{name: "default tag to latest",
			dependency:  Dependency{Bundle: "example/only-latest"},
			wantVersion: "latest"},
		{name: "no default tag",
			dependency: Dependency{Bundle: "example/no-default-tag"},
			wantError:  "no tag was specified"},
		{name: "default tag to highest semver",
			dependency:  Dependency{Bundle: "example/with-versions", Version: &DependencyVersion{Ranges: nil, AllowPrereleases: true}},
			wantVersion: "v2.0.0-beta1"},
		{name: "default tag to highest semver, explicitly excluding prereleases",
			dependency:  Dependency{Bundle: "example/with-versions", Version: &DependencyVersion{Ranges: nil, AllowPrereleases: false}},
			wantVersion: "v1.2.0"},
		{name: "default tag to highest semver, excluding prereleases by default",
			dependency:  Dependency{Bundle: "example/with-versions"},
			wantVersion: "v1.2.0"},
	}

	tags := &mockTagLister{
		tags: map[string][]string{
			"example/only-latest":    {"latest"},
			"example/no-default-tag": {"not-a-semver"},
			"example/with-versions":  {"latest", "v1.0.0", "v1.1.0", "v1.1.1", "v1.2.0", "v2.0.0-beta1"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			resolver := Resolver{Tags: tags, Strategy: testCase.strategy}
			version, err := resolver.ResolveVersion(context.Background(), "mysql", testCase.dependency)
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
				return
			}
			require.NoError(t, err, "ResolveVersion should not have returned an error")
			assert.Equal(t, testCase.wantVersion, version.Tag(), "incorrect version resolved")
		})
	}

	t.Run("no registry", func(t *testing.T) {
		t.Parallel()

		_, err := Resolver{}.ResolveVersion(context.Background(), "mysql", Dependency{Bundle: "example/with-versions"})
		require.ErrorContains(t, err, "no registry to list the tags of example/with-versions")
	})
}
