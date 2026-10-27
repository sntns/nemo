package main

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// shopBundle is a bundle of the shop registry, that declares region, a
// required parameter, size, one with a default, and token, a required credential.
func shopBundle(name, version string) cnab.BundleReference {
	return cnab.BundleReference{Definition: cnab.ExtendedBundle{Bundle: bundle.Bundle{
		Name: name, Version: version,
		Definitions: definition.Definitions{"string": {Type: "string"}, "size": {Type: "string", Default: "10"}},
		Parameters: map[string]bundle.Parameter{
			"region": {Definition: "string", Required: true, Description: "Where it runs"},
			"size":   {Definition: "size"},
		},
		Credentials: map[string]bundle.Credential{"token": {Required: true}},
	}}}
}

func shopEditor(edited stack.Stack, mode editMode, taken bool) *editor {
	e := newEditor(context.Background(), edited, mode, func(string) bool { return taken })
	e.tags = func(context.Context, cnab.OCIReference) ([]string, error) {
		return []string{"1.0.0", "1.1.0", "latest"}, nil
	}
	e.load = func(_ context.Context, source string) (cnab.BundleReference, error) {
		switch source {
		case "docker://registry.example.com/shop/web:1.1.0":
			return shopBundle("web", "1.1.0"), nil
		case "docker://registry.example.com/shop/db:1.1.0":
			return shopBundle("db", "1.1.0"), nil
		}
		return cnab.BundleReference{}, errors.New("no bundle at " + source)
	}
	return e
}

// press types the keys, a name each or text, and runs the reads they start.
func press(e *editor, keys ...string) {
	named := map[string]tea.KeyPressMsg{
		"enter": {Code: tea.KeyEnter}, "up": {Code: tea.KeyUp}, "down": {Code: tea.KeyDown},
		"left": {Code: tea.KeyLeft}, "right": {Code: tea.KeyRight}, "space": {Code: tea.KeySpace, Text: " "},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift}, "esc": {Code: tea.KeyEscape},
	}
	for _, key := range keys {
		if msg, ok := named[key]; ok {
			_, cmd := e.Update(msg)
			drain(e, cmd)
			continue
		}
		for _, r := range key {
			_, cmd := e.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			drain(e, cmd)
		}
	}
}

// drain runs the command and hands the editor what its reads return. The
// others, the cursor's blink or the spinner's ticks, wait: they are dropped.
func drain(e *editor, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, cmd := range msg {
				drain(e, cmd)
			}
		case versionsMsg, layoutsMsg, bundlesMsg:
			_, next := e.Update(msg)
			drain(e, next)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEditor_createFromARegistry(t *testing.T) {
	t.Parallel()
	e := shopEditor(stack.Stack{Name: "shop"}, modeCreate, false)
	drain(e, e.Init())
	assert.Equal(t, screenSource, e.screen().kind, "the name was given")

	press(e, "up", "enter")
	press(e, "registry.example.com/shop", "enter")
	press(e, "web db", "enter")
	require.Equal(t, screenVersion, e.screen().kind)
	assert.Equal(t, []string{"1.1.0", "1.0.0"}, e.versions, "the versions, newest first, tags that are not aside")
	press(e, "enter", "enter", "enter")
	require.Equal(t, screen{kind: screenParameter, step: stepParameters, name: "region"}, e.screen(), "the bundles declare it")
	press(e, "enter")
	assert.Equal(t, "required", e.problem)
	press(e, "eu5", "enter", "enter")
	require.Equal(t, screenCredential, e.screen().kind)
	press(e, "right", "enter", "TOKEN", "enter")
	require.Equal(t, screenReview, e.screen().kind)
	assert.Empty(t, e.problems(stepReview))
	press(e, "enter")

	require.True(t, e.saved)
	assert.Equal(t, "docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}", e.stack.Remote)
	assert.Equal(t, []storage.InstallationSpec{
		{Name: "web", Stack: "shop", Bundle: storage.BundleReference{Version: "1.1.0"}},
		{Name: "db", Stack: "shop", Bundle: storage.BundleReference{Version: "1.1.0"}},
	}, e.stack.Installations)
	assert.Equal(t, target.Target{Orchestrator: target.Swarm, Configuration: target.Configuration{Images: target.ImagesRegistry}}, e.stack.Target)
	assert.Equal(t, map[string]string{"region": "eu5", "size": "10"}, e.stack.Parameters)
	assert.Equal(t, []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "env", Value: "TOKEN"}}}, e.stack.Credentials)
	assert.NoError(t, e.stack.Validate())
}

func TestEditor_importOpensTheReview(t *testing.T) {
	t.Parallel()
	imported := stack.Stack{
		Name:          "shop",
		Remote:        "docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}",
		Target:        target.Target{Orchestrator: target.Swarm},
		Credentials:   []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "env", Value: "TOKEN"}}},
		Installations: []storage.InstallationSpec{{Name: "web", Stack: "shop", Bundle: storage.BundleReference{Version: "1.1.0"}}},
	}
	e := shopEditor(imported, modeImport, false)
	drain(e, e.Init())
	require.Equal(t, screenReview, e.screen().kind)
	assert.Equal(t, []string{"region is required"}, e.problems(stepParameters), "the manifest misses it")

	press(e, "enter")
	assert.False(t, e.saved, "the cursor was on what is missing")
	require.Equal(t, screen{kind: screenParameter, step: stepParameters, name: "region"}, e.screen())
	press(e, "eu5", "enter", "enter")
	require.Equal(t, screenReview, e.screen().kind, "back to the review, once the step is done")
	press(e, "enter")
	require.True(t, e.saved)
	assert.Equal(t, "eu5", e.stack.Parameters["region"])
}

func TestEditor_aNameTaken(t *testing.T) {
	t.Parallel()
	e := shopEditor(stack.Stack{Name: "shop"}, modeCreate, true)
	require.Equal(t, screenName, e.screen().kind, "the name given is taken: it is asked")
	press(e, "enter")
	assert.Equal(t, "shop exists: nemo stack edit shop changes it", e.problem)
}

func TestEditor_paste(t *testing.T) {
	t.Parallel()
	imported := stack.Stack{
		Name:          "shop",
		Remote:        "docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}",
		Target:        target.Target{Orchestrator: target.Swarm},
		Parameters:    map[string]string{"region": "eu5"},
		Installations: []storage.InstallationSpec{{Name: "web", Stack: "shop", Bundle: storage.BundleReference{Version: "1.1.0"}}},
	}
	e := shopEditor(imported, modeImport, false)
	drain(e, e.Init())
	press(e, "enter")
	require.Equal(t, screenCredential, e.screen().kind, "the credential is missing")
	press(e, "right", "right")
	_, cmd := e.Update(tea.PasteMsg{Content: "gh release download cert --pattern key.pem --output -"})
	drain(e, cmd)
	press(e, "enter")
	assert.Equal(t, []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "command", Value: "gh release download cert --pattern key.pem --output -"}}}, e.stack.Credentials, "pasted with the source chosen")
}

func TestEditor_quitAsks(t *testing.T) {
	t.Parallel()
	e := shopEditor(stack.Stack{Name: "shop"}, modeCreate, false)
	press(e, "esc")
	assert.True(t, e.quitting)
	press(e, "n")
	assert.False(t, e.quitting, "any other key stays")
	press(e, "esc")
	_, cmd := e.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.False(t, e.saved)
}
