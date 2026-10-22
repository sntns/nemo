package compose

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/sntns/nemo/pkg/installer"
)

// validate reports what of the project Compose can't honour, as errors, and
// what it ignores, as warnings: the settings only Swarm has, which a bundle
// may carry so that it runs on both.
func validate(project *types.Project) installer.Problems {
	problems := installer.NewProblems()
	validateServices(problems, project)
	validateConfigs(problems, project)
	validateSecrets(problems, project)
	return problems
}

const unsupported = "not supported by Compose"

func validateServices(problems installer.Problems, project *types.Project) {
	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		serviceConfig := project.Services[name]
		serviceProblems := problems.At("services", name)

		validateServiceOptions(serviceProblems, serviceConfig)
		validateVolumes(serviceProblems, project, serviceConfig)
		validateServiceNetworks(serviceProblems, serviceConfig)
		validatePorts(serviceProblems, serviceConfig)
		validateDeploy(serviceProblems, serviceConfig)
	}
}

func validateServiceOptions(problems installer.Problems, service types.ServiceConfig) {
	problems.At("container_name").AddIf(
		service.ContainerName != "",
		"not supported: nemo names containers <stack>-<service>-<n>",
	)

	problems.At("links").AddIf(
		len(service.Links) > 0,
		"not supported: legacy links, use networks",
	)

	problems.At("external_links").AddIf(
		len(service.ExternalLinks) > 0,
		"not supported: use networks",
	)

	problems.At("net").AddIf(service.Net != "", "not supported: use networks")
	problems.At("blkio_config").AddIf(service.BlkioConfig != nil, unsupported)

	problems.At("cpu_count").AddIf(service.CPUCount != 0, unsupported)
	problems.At("cpu_percent").AddIf(service.CPUPercent != 0, unsupported)
	problems.At("cpu_rt_period").AddIf(service.CPURTPeriod != 0, unsupported)
	problems.At("cpu_rt_runtime").AddIf(service.CPURTRuntime != 0, unsupported)

	problems.At("credential_spec").AddIf(service.CredentialSpec != nil, unsupported)
	problems.At("gpus").AddIf(len(service.Gpus) > 0, unsupported)

	problems.At("mac_address").AddIf(
		service.MacAddress != "",
		"not supported: use networks.<network>.mac_address",
	)

	problems.At("pull_refresh_after").AddIf(service.PullRefreshAfter != "", unsupported)

	problems.At("pull_policy").AddIf(
		service.PullPolicy != "" &&
			!slices.Contains([]string{
				types.PullPolicyAlways,
				types.PullPolicyMissing,
				types.PullPolicyIfNotPresent,
				types.PullPolicyNever,
			}, service.PullPolicy),
		unsupported,
	)

	problems.At("use_api_socket").AddIf(service.UseAPISocket, unsupported)

	problems.At("healthcheck.start_interval").AddIf(
		service.HealthCheck != nil && service.HealthCheck.StartInterval != nil,
		unsupported,
	)

	problems.At("log_driver").AddIf(
		service.LogDriver != "",
		"not supported: use logging",
	)

	problems.At("log_opt").AddIf(
		len(service.LogOpt) > 0,
		"not supported: use logging",
	)
}

func validateVolumes(problems installer.Problems, project *types.Project, service types.ServiceConfig) {
	for i, volume := range service.Volumes {
		path := fmt.Sprintf("volumes[%d]", i)

		problems.At(path, "type").AddIf(
			volume.Type == types.VolumeTypeImage ||
				volume.Type == types.VolumeTypeCluster,
			unsupported,
		)

		problems.At(path, "source").AddIf(
			volume.Type == types.VolumeTypeBind &&
				(!filepath.IsAbs(volume.Source) ||
					strings.HasPrefix(volume.Source, project.WorkingDir+"/") ||
					volume.Source == project.WorkingDir),
			"a bind mount needs an absolute path on the host",
		)

		problems.At(path, "bind").AddIf(
			volume.Bind != nil &&
				(volume.Bind.SELinux != "" || volume.Bind.Recursive != ""),
			"selinux and recursive are not supported by Compose",
		)
	}

	for i, from := range service.VolumesFrom {
		problems.At(fmt.Sprintf("volumes_from[%d]", i)).AddIf(
			!strings.HasPrefix(from, "container:") &&
				strings.Contains(from, ":") &&
				!strings.HasPrefix(from, "service:"),
			fmt.Sprintf("%q: use service:<name> or container:<name>", from),
		)
	}
}

func validateServiceNetworks(problems installer.Problems, service types.ServiceConfig) {
	for _, name := range slices.Sorted(maps.Keys(service.Networks)) {
		config := service.Networks[name]

		problems.At("networks", name).AddIf(
			config != nil &&
				(len(config.LinkLocalIPs) > 0 || config.InterfaceName != ""),
			"link_local_ips and interface_name are not supported by Compose",
		)
	}
}

func validatePorts(problems installer.Problems, service types.ServiceConfig) {
	for i, port := range service.Ports {
		problems.At(fmt.Sprintf("ports[%d].app_protocol", i)).AddIf(
			port.AppProtocol != "",
			unsupported,
		)
	}
}

func validateDeploy(problems installer.Problems, service types.ServiceConfig) {
	if service.Deploy == nil {
		return
	}

	deployConfig := service.Deploy
	deployProblems := problems.At("deploy")

	deployProblems.At("mode").AddIf(
		deployConfig.Mode != "" && deployConfig.Mode != "replicated",
		"only replicated is supported by Compose",
	)

	deployProblems.At("labels").WarnIf(
		len(deployConfig.Labels) > 0,
		"ignored: Compose has no services to put them on, use labels",
	)

	deployProblems.At("placement").WarnIf(
		len(deployConfig.Placement.Constraints)+len(deployConfig.Placement.Preferences) > 0 ||
			deployConfig.Placement.MaxReplicas != 0,
		"ignored: Compose runs on one host",
	)

	deployProblems.At("update_config").WarnIf(
		deployConfig.UpdateConfig != nil,
		"ignored: Compose recreates changed containers",
	)

	deployProblems.At("rollback_config").WarnIf(
		deployConfig.RollbackConfig != nil,
		"ignored: Compose recreates changed containers",
	)

	deployProblems.At("endpoint_mode").WarnIf(
		deployConfig.EndpointMode != "",
		"ignored: Compose reaches containers by their name",
	)

	deployProblems.At("restart_policy").WarnIf(
		deployConfig.RestartPolicy != nil &&
			(deployConfig.RestartPolicy.Delay != nil || deployConfig.RestartPolicy.Window != nil),
		"delay and window are ignored: the daemon restarts containers itself",
	)

	for _, resource := range []*types.Resource{
		deployConfig.Resources.Limits,
		deployConfig.Resources.Reservations,
	} {
		deployProblems.At("resources").AddIf(
			resource != nil &&
				(len(resource.Devices) > 0 || len(resource.GenericResources) > 0),
			"devices and generic resources are not supported by Compose",
		)
	}
}

func validateFileObjects[T any](problems installer.Problems, kind string, objects map[string]T, convert func(T) types.FileObjectConfig) {
	for _, name := range slices.Sorted(maps.Keys(objects)) {
		object := convert(objects[name])
		resource := problems.At(kind, name)

		resource.At("external").AddIf(
			bool(object.External),
			fmt.Sprintf("not supported: %s are files copied into the containers", kind),
		)

		resource.At("driver").AddIf(
			object.Driver != "" || object.TemplateDriver != "",
			unsupported,
		)
	}
}

func validateConfigs(problems installer.Problems, project *types.Project) {
	validateFileObjects(
		problems,
		"configs",
		project.Configs,
		func(config types.ConfigObjConfig) types.FileObjectConfig {
			return types.FileObjectConfig(config)
		},
	)
}

func validateSecrets(problems installer.Problems, project *types.Project) {
	validateFileObjects(
		problems,
		"secrets",
		project.Secrets,
		func(secret types.SecretConfig) types.FileObjectConfig {
			return types.FileObjectConfig(secret)
		},
	)
}
