package cnab

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDependencies_ListBySequence(t *testing.T) {
	t.Parallel()

	requires := map[string]Dependency{
		"storage": {Bundle: "somecloud/blob-storage"},
		"mysql":   {Bundle: "somecloud/mysql"},
	}
	names := func(ordered []Dependency) []string {
		var names []string
		for _, dependency := range ordered {
			names = append(names, dependency.Name)
		}
		return names
	}

	assert.Equal(t, []string{"storage", "mysql"}, names(Dependencies{Sequence: []string{"storage", "mysql"}, Requires: requires}.ListBySequence()))
	assert.Equal(t, []string{"mysql", "storage"}, names(Dependencies{Requires: requires}.ListBySequence()), "sorted by name without a sequence")
}
