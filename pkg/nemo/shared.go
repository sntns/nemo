package nemo

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/installer"
	swarmconvert "github.com/sntns/nemo/pkg/installer/swarm/convert"
	"github.com/sntns/nemo/pkg/progress"
	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/target"
)

// The compose project of every installation of a stack is named after the
// stack, so two installations that declare a network or a volume of the same
// name, or a Swarm config or secret of the same name and content, declare
// the same object. That object is none of theirs: it has an installation of
// its own, whose bundle declares it alone, with no service. Its users depend
// on it: it is created before them, and removed once they are gone.

// object is an object of the target, by the group its installer reports it
// in, and its name there.
type object struct {
	group string
	name  string
}

// The groups of the objects an installation can share.
const (
	groupNetworks = "networks"
	groupVolumes  = "volumes"
	groupConfigs  = "configs"
	groupSecrets  = "secrets"
)

// declaration is how an installation declares an object: one it only
// refers to is external; settings are what two declarations must agree on;
// key and config are the object as its compose project has it, rendered.
type declaration struct {
	external bool
	settings any
	key      string
	config   any
}

// inventory is what an installation's project declares on the target.
type inventory struct {
	objects map[object]declaration
	// services are the names of its services on the target.
	services []string
	// ports are the ports it publishes, as HOST:PORT/PROTOCOL.
	ports []string
}

// inventoryOf renders the installation's compose project the way its
// installer will, with the same code and the action's parameters and
// credentials, and returns what it declares: nil for a bundle that is not
// one of nemo's. With no credentials, as for an uninstall, configs and
// secrets are known by their name without their content.
func inventoryOf(ctx context.Context, request runtime.Request, values valuesource.Set) (*inventory, error) {
	bundle := request.Bundle
	if bundle.Compose == nil {
		return nil, nil
	}
	parameters, err := runtime.Parameters(request)
	if err != nil {
		return nil, err
	}
	project, err := installer.LoadProject(ctx, installer.Operation{
		Installation: request.Stack + "/" + bundle.Name,
		Action:       request.Action,
		Bundle:       bundle,
		Parameters:   parameters,
		Credentials:  values,
	})
	if err != nil {
		return nil, err
	}
	compose := project.Compose
	declared := &inventory{objects: map[object]declaration{}}
	for key, network := range compose.Networks {
		declared.objects[object{groupNetworks, network.Name}] = declaration{bool(network.External), networkSettingsOf(network, request.Target.Orchestrator), key, network}
	}
	for key, volume := range compose.Volumes {
		declared.objects[object{groupVolumes, volume.Name}] = declaration{bool(volume.External), volumeSettings{volume.Driver, options(volume.DriverOpts)}, key, volume}
	}
	// With Compose, configs and secrets are files in the containers, not objects.
	if request.Target.Orchestrator == target.Swarm {
		for key, config := range compose.Configs {
			name := config.Name
			if spec, err := swarmconvert.Config(key, config, nil); err == nil && spec.Data != nil {
				name = spec.Name
			}
			declared.objects[object{groupConfigs, name}] = declaration{external: bool(config.External), key: key, config: config}
		}
		for key, secret := range compose.Secrets {
			name := secret.Name
			if spec, err := swarmconvert.Secret(key, secret, nil); err == nil && spec.Data != nil {
				name = spec.Name
			}
			declared.objects[object{groupSecrets, name}] = declaration{external: bool(secret.External), key: key, config: secret}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(compose.Services)) {
		declared.services = append(declared.services, compose.Name+"_"+name)
		for _, port := range compose.Services[name].Ports {
			// A Swarm port of the host mode is published by each node that runs a task: it can't be told here.
			if port.Published == "" || request.Target.Orchestrator == target.Swarm && port.Mode == "host" {
				continue
			}
			declared.ports = append(declared.ports, fmt.Sprintf("%s:%s/%s", cmp.Or(port.HostIP, "*"), port.Published, cmp.Or(port.Protocol, "tcp")))
		}
	}
	return declared, nil
}

// networkSettings are what two declarations of a network must agree on:
// all but its labels, the driver as the orchestrator defaults it.
type networkSettings struct {
	Driver     string
	Options    map[string]string
	IPAM       types.IPAMConfig
	Internal   bool
	Attachable bool
	IPv4, IPv6 *bool
}

func networkSettingsOf(network types.NetworkConfig, orchestrator string) networkSettings {
	driver := network.Driver
	if driver == "" && orchestrator == target.Swarm {
		driver = "overlay"
	}
	ipam := network.Ipam
	ipam.Extensions, ipam.Options = nil, options(ipam.Options)
	if len(ipam.Config) == 0 {
		ipam.Config = nil
	}
	return networkSettings{driver, options(network.DriverOpts), ipam, network.Internal, network.Attachable, network.EnableIPv4, network.EnableIPv6}
}

// volumeSettings are what two declarations of a volume must agree on: its
// driver and options, as the target's volume type made them.
type volumeSettings struct {
	Driver  string
	Options map[string]string
}

// options are the options, none when empty.
func options(given map[string]string) map[string]string {
	if len(given) == 0 {
		return nil
	}
	return given
}

// sharedObject is an object two installations or more of the stack use.
type sharedObject struct {
	object
	// users are the installations that use it, in the stack's order.
	users []string
	// declarer is the first of them to declare it, not only refer to it: its
	// declaration is the one the object's installation makes.
	declarer string
	declared declaration
	// warning says how a user declares it otherwise than the declarer.
	warning string
}

// sharing returns the objects the installations share, by group then name,
// from their inventories in the stack's order. It refuses what can't be
// shared: a service or a published port of two installations, and a volume
// two of them make otherwise. A network declared otherwise is made as the
// first declares it, with a warning.
func sharing(ordered []string, inventories map[string]*inventory) ([]sharedObject, error) {
	type use struct {
		installation string
		declaration
	}
	uses := map[object][]use{}
	services, ports := map[string][]string{}, map[string][]string{}
	for _, name := range ordered {
		declared := inventories[name]
		if declared == nil {
			continue
		}
		for _, key := range slices.SortedFunc(maps.Keys(declared.objects), compareObjects) {
			uses[key] = append(uses[key], use{name, declared.objects[key]})
		}
		for _, service := range declared.services {
			services[service] = append(services[service], name)
		}
		for _, port := range slices.Compact(slices.Sorted(slices.Values(declared.ports))) {
			ports[port] = append(ports[port], name)
		}
	}

	var errs []error
	for _, service := range slices.Sorted(maps.Keys(services)) {
		if by := services[service]; len(by) > 1 {
			errs = append(errs, fmt.Errorf("service %s is declared by %s: a service can't be shared, rename it in one of them", service, strings.Join(by, " and ")))
		}
	}
	for _, port := range slices.Sorted(maps.Keys(ports)) {
		if by := ports[port]; len(by) > 1 {
			errs = append(errs, fmt.Errorf("port %s is published by %s: one of them must publish another", port, strings.Join(by, " and ")))
		}
	}

	var shared []sharedObject
	for _, key := range slices.SortedFunc(maps.Keys(uses), compareObjects) {
		users := uses[key]
		first := slices.IndexFunc(users, func(user use) bool { return !user.external })
		if len(users) < 2 || first < 0 {
			continue
		}
		found := sharedObject{object: key, declarer: users[first].installation, declared: users[first].declaration}
		for _, user := range users {
			found.users = append(found.users, user.installation)
			if user.external || reflect.DeepEqual(user.settings, users[first].settings) {
				continue
			}
			if key.group == groupVolumes {
				errs = append(errs, fmt.Errorf("volume %s: %s and %s make it with other drivers or options: give it the same in both", key.name, users[first].installation, user.installation))
				continue
			}
			found.warning = fmt.Sprintf("%s declares it otherwise than %s, whose declaration makes it", user.installation, users[first].installation)
		}
		shared = append(shared, found)
	}
	return shared, errors.Join(errs...)
}

// groupOrder sorts the groups as the installer creates their objects.
var groupOrder = []string{groupVolumes, groupNetworks, groupConfigs, groupSecrets}

func compareObjects(left, right object) int {
	return cmp.Or(cmp.Compare(slices.Index(groupOrder, left.group), slices.Index(groupOrder, right.group)), cmp.Compare(left.name, right.name))
}

// sharedVolumes returns, for each volume the installation declares that
// one of others declares too, those that do.
func sharedVolumes(declared *inventory, others map[string]*inventory) map[string][]string {
	found := map[string][]string{}
	if declared == nil {
		return found
	}
	for key, own := range declared.objects {
		if key.group != groupVolumes || own.external {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(others)) {
			if other := others[name]; other != nil {
				if _, uses := other.objects[key]; uses {
					found[key.name] = append(found[key.name], name)
				}
			}
		}
	}
	return found
}

// usersText says how many installations share the object.
func usersText(shared sharedObject) string {
	return fmt.Sprintf("shared by %d installations", len(shared.users))
}

// node names the installation of the object: network.NAME, volume.NAME…
func (shared sharedObject) node() string {
	return installer.SharedInstallation(shared.group, shared.name)
}

// sharedMark marks the compose project of an object's own installation.
const sharedMark = "x-nemo-shared"

// sharedVersion is the version of the bundles of the objects.
const sharedVersion = "1.0.0"

// sharedOf returns the object an installation's bundle is made for, if it is
// the bundle of an object installations share.
func sharedOf(bundle cnab.ExtendedBundle, declared *inventory) (object, bool) {
	if bundle.Compose == nil || declared == nil || len(declared.objects) != 1 {
		return object{}, false
	}
	marked := slices.ContainsFunc(bundle.Compose.Documents, func(document cnab.ComposeDocument) bool {
		_, found := document.Config[sharedMark]
		return found
	})
	for key := range declared.objects {
		return key, marked
	}
	return object{}, false
}

// sharedBundle makes the bundle of the object: the installer and the nemo-*
// parameters of the bundle that declares it first, and a compose project
// that declares the object alone, as that bundle does. A config's or a
// secret's content is a credential of the bundle, which nothing records:
// it is returned, to be given to its action.
func sharedBundle(base cnab.ExtendedBundle, shared sharedObject) (cnab.ExtendedBundle, valuesource.Set, error) {
	made := base.Bundle
	made.Name, made.Version = shared.node(), sharedVersion
	made.Description = fmt.Sprintf("The %s %s the stack's installations share", strings.TrimSuffix(shared.group, "s"), shared.name)
	made.Images, made.Outputs, made.Credentials = nil, nil, nil
	made.Parameters, made.Definitions = map[string]bundle.Parameter{}, definition.Definitions{}
	for name, parameter := range base.Parameters {
		if strings.HasPrefix(name, "nemo-") && name != cnab.ParameterOverride {
			made.Parameters[name] = parameter
			made.Definitions[parameter.Definition] = base.Definitions[parameter.Definition]
		}
	}
	made.Custom = map[string]any{}
	for key, value := range base.Custom {
		if key != cnab.ComposeExtension.Key && key != cnab.DependenciesExtension.Key && key != cnab.ParameterSourcesExtension.Key && key != cnab.OutputsExtension.Key {
			made.Custom[key] = value
		}
	}
	made.RequiredExtensions = slices.DeleteFunc(slices.Clone(base.RequiredExtensions), func(key string) bool { _, kept := made.Custom[key]; return !kept })

	declaration, values := map[string]any{}, valuesource.Set(nil)
	switch config := shared.declared.config.(type) {
	case types.NetworkConfig:
		config.External = false
		if err := asMap(config, &declaration); err != nil {
			return cnab.ExtendedBundle{}, nil, err
		}
	case types.VolumeConfig:
		volumeType, typed := config.Extensions[installer.VolumeExtension]
		if typed {
			config.Driver, config.DriverOpts = "", nil
		}
		if err := asMap(config, &declaration); err != nil {
			return cnab.ExtendedBundle{}, nil, err
		}
		if typed {
			declaration[installer.VolumeExtension] = volumeType
		}
	case types.ConfigObjConfig:
		declaration, values = fileObject(config.Name, config.Labels), valuesource.Set{"nemo-shared": config.Content}
	case types.SecretConfig:
		declaration, values = fileObject(config.Name, config.Labels), valuesource.Set{"nemo-shared": config.Content}
	default:
		return cnab.ExtendedBundle{}, nil, fmt.Errorf("%s %s: nemo can't make it alone", shared.group, shared.name)
	}
	if values != nil {
		made.Credentials = map[string]bundle.Credential{"nemo-shared": {Location: bundle.Location{EnvironmentVariable: "NEMO_SHARED"}, Required: true, ApplyTo: []string{cnab.ActionInstall, cnab.ActionUpgrade}, Description: "The content of the " + strings.TrimSuffix(shared.group, "s")}}
	}
	compose := cnab.Compose{SchemaVersion: cnab.ComposeSchemaVersion, Documents: []cnab.ComposeDocument{{
		Name:   "shared.yaml",
		Config: map[string]any{sharedMark: map[string]any{}, shared.group: map[string]any{shared.declared.key: declaration}},
	}}}
	if err := cnab.ComposeExtension.Write(&made, compose); err != nil {
		return cnab.ExtendedBundle{}, nil, err
	}
	extended, err := cnab.NewBundle(made)
	return extended, values, err
}

// fileObject declares a config or a secret of that name, read from the
// credential that holds its content.
func fileObject(name string, labels types.Labels) map[string]any {
	declared := map[string]any{"name": name, "environment": "NEMO_SHARED"}
	if len(labels) > 0 {
		declared["labels"] = labels
	}
	return declared
}

// asMap turns a compose object into the document that declares it.
func asMap(value any, into *map[string]any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

// sameBundle tells whether the object's bundle is the one its installation
// last ran: then the object is there, as it is, and nothing needs to run.
func sameBundle(recorded, made cnab.ExtendedBundle) bool {
	if recorded.Compose == nil || made.Compose == nil || len(recorded.InvocationImages) == 0 {
		return false
	}
	left, leftErr := json.Marshal(recorded.Compose.Documents)
	right, rightErr := json.Marshal(made.Compose.Documents)
	return leftErr == nil && rightErr == nil && string(left) == string(right) && recorded.InvocationImages[0].Digest == made.InvocationImages[0].Digest
}

// sharedEvents tells the stack's row of the object what its installation
// does, once it ends: what its installer said of the object, its warnings,
// or its error.
func sharedEvents(events progress.Sink, stackName string, shared object) progress.Sink {
	var mutex sync.Mutex
	said := ""
	return progress.SinkFunc(func(event progress.Event) {
		mutex.Lock()
		defer mutex.Unlock()
		row := progress.Event{Installation: stackName, Group: shared.group, Item: shared.name, State: event.State}
		switch {
		case event.Group == shared.group && event.Item == shared.name && event.State == progress.Warning:
			row.Message = event.Message
			events.Emit(row)
		case event.Group == shared.group && event.Item == shared.name:
			said = cmp.Or(event.Message, said)
		case event.Group == progress.Installation && event.State == progress.Done:
			row.Message = said
			events.Emit(row)
		case event.Group == progress.Installation && event.State != progress.Pending:
			if event.State == progress.Error {
				row.Message = event.Message
			}
			events.Emit(row)
		}
	})
}

// announceShared tells each shared object pending, on the stack's row of
// its own, with how it is declared otherwise.
func announceShared(events progress.Sink, stackName string, shared []sharedObject) {
	for _, object := range shared {
		events.Emit(progress.Event{Installation: stackName, Group: object.group, Item: object.name, State: progress.Pending, Message: usersText(object)})
		if object.warning != "" {
			events.Emit(progress.Event{Installation: stackName, Group: object.group, Item: object.name, State: progress.Warning, Message: object.warning})
		}
	}
}

// orderedNames are the names of the bundles, in their order.
func orderedNames(bundles []cnab.ExtendedBundle) []string {
	names := make([]string, len(bundles))
	for i, bundle := range bundles {
		names[i] = bundle.Name
	}
	return names
}
