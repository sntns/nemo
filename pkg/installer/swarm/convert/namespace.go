package convert

import (
	"maps"
	"strings"
)

// LabelNamespace lets docker stack ps and docker stack rm see what nemo created.
const LabelNamespace = "com.docker.stack.namespace"

// Namespace mangles names by prepending the name
type Namespace struct {
	name string
}

// NewNamespace returns a new Namespace for scoping of names
func NewNamespace(name string) Namespace {
	return Namespace{name: name}
}

// Scope prepends the namespace to a name
func (namespace Namespace) Scope(name string) string {
	return namespace.name + "_" + name
}

// Descope returns the name without the namespace prefix
func (namespace Namespace) Descope(name string) string {
	return strings.TrimPrefix(name, namespace.name+"_")
}

// Name returns the name of the namespace
func (namespace Namespace) Name() string {
	return namespace.name
}

// AddStackLabel returns a copy of the labels with the namespace label added
func AddStackLabel(namespace Namespace, labels map[string]string) map[string]string {
	result := maps.Clone(labels)
	if result == nil {
		result = map[string]string{}
	}
	result[LabelNamespace] = namespace.name
	return result
}
