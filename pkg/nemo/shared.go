package nemo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

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
// the same object. Of the installations that use it, the first in the
// stack's order creates it: the others start once its installer announced
// it. On the way down, it goes after them, and its installer removes the
// object, which nothing uses any more.

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
// refers to is external; settings are what two declarations must agree on.
type declaration struct {
	external bool
	settings any
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
	for _, network := range compose.Networks {
		declared.objects[object{groupNetworks, network.Name}] = declaration{bool(network.External), networkSettingsOf(network, request.Target.Orchestrator)}
	}
	for _, volume := range compose.Volumes {
		declared.objects[object{groupVolumes, volume.Name}] = declaration{bool(volume.External), volumeSettings{volume.Driver, options(volume.DriverOpts)}}
	}
	// With Compose, configs and secrets are files in the containers, not objects.
	if request.Target.Orchestrator == target.Swarm {
		for key, config := range compose.Configs {
			name := config.Name
			if spec, err := swarmconvert.Config(key, config, nil); err == nil && spec.Data != nil {
				name = spec.Name
			}
			declared.objects[object{groupConfigs, name}] = declaration{external: bool(config.External)}
		}
		for key, secret := range compose.Secrets {
			name := secret.Name
			if spec, err := swarmconvert.Secret(key, secret, nil); err == nil && spec.Data != nil {
				name = spec.Name
			}
			declared.objects[object{groupSecrets, name}] = declaration{external: bool(secret.External)}
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
	// creator is the first of them to declare it, not only refer to it:
	// it creates the object, and removes it after the others.
	creator string
	// warning says how a user declares it otherwise than the creator.
	warning string
}

// sharing returns the objects the installations share, by group then name,
// from their inventories in the stack's order. It refuses what can't be
// shared: a service or a published port of two installations, and a volume
// two of them make otherwise. A network declared otherwise is the creator's,
// as it is created first, with a warning.
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
		creator := slices.IndexFunc(users, func(user use) bool { return !user.external })
		if len(users) < 2 || creator < 0 {
			continue
		}
		found := sharedObject{object: key, creator: users[creator].installation}
		for _, user := range users {
			found.users = append(found.users, user.installation)
			if user.external || reflect.DeepEqual(user.settings, users[creator].settings) {
				continue
			}
			if key.group == groupVolumes {
				errs = append(errs, fmt.Errorf("volume %s: %s and %s make it with other drivers or options: give it the same in both", key.name, users[creator].installation, user.installation))
				continue
			}
			found.warning = fmt.Sprintf("%s declares it otherwise than %s, which creates it as it declares it", user.installation, users[creator].installation)
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

// sharingPlan is who waits for what, from the objects the installations
// share: the creator of each one, and those after it in the stack's order.
type sharingPlan struct {
	objects []sharedObject
	// position is each installation's place in the stack's order.
	position map[string]int
}

func newSharingPlan(ordered []string, shared []sharedObject) sharingPlan {
	plan := sharingPlan{objects: shared, position: map[string]int{}}
	for i, name := range ordered {
		plan.position[name] = i
	}
	return plan
}

// creates are the objects the installation creates for the others.
func (plan sharingPlan) creates(name string) []object {
	var created []object
	for _, shared := range plan.objects {
		if shared.creator == name {
			created = append(created, shared.object)
		}
	}
	return created
}

// needs are the objects the installation waits for, on the way up: those
// another creates before it in the stack's order. One before the creator may
// be its dependency, so it does not wait.
func (plan sharingPlan) needs(name string) []object {
	var needed []object
	for _, shared := range plan.objects {
		if shared.creator != name && slices.Contains(shared.users, name) && plan.position[name] > plan.position[shared.creator] {
			needed = append(needed, shared.object)
		}
	}
	return needed
}

// leaveAfter are the installations the creator waits for, on the way down:
// the users of what it creates, after it in the stack's order.
func (plan sharingPlan) leaveAfter(name string) []string {
	var after []string
	for _, shared := range plan.objects {
		if shared.creator != name {
			continue
		}
		for _, user := range shared.users {
			if user != name && plan.position[user] > plan.position[name] && !slices.Contains(after, user) {
				after = append(after, user)
			}
		}
	}
	return after
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

// eventsOf passes on the installation's events, and gives those about the
// objects it creates for the others to the stack's row of the object too:
// the creator's are the object's. It calls announced once one is there.
func (plan sharingPlan) eventsOf(events progress.Sink, stackName, name string, announced func(object)) progress.Sink {
	created := plan.creates(name)
	if len(created) == 0 {
		return events
	}
	return progress.SinkFunc(func(event progress.Event) {
		events.Emit(event)
		key := object{event.Group, event.Item}
		if !slices.Contains(created, key) {
			return
		}
		about := event
		about.Installation = stackName
		events.Emit(about)
		if event.State == progress.Done && announced != nil {
			announced(key)
		}
	})
}

// announce tells each shared object pending, on the stack's row of its own,
// with how it is declared otherwise.
func (plan sharingPlan) announce(events progress.Sink, stackName string) {
	for _, shared := range plan.objects {
		events.Emit(progress.Event{Installation: stackName, Group: shared.group, Item: shared.name, State: progress.Pending, Message: usersText(shared)})
		if shared.warning != "" {
			events.Emit(progress.Event{Installation: stackName, Group: shared.group, Item: shared.name, State: progress.Warning, Message: shared.warning})
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
