package convert

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/opencontainers/go-digest"

	"github.com/sntns/nemo/pkg/docker"
)

// File is a config or a secret, copied into each container before it starts.
type File struct {
	Path     string
	Data     []byte `json:"-"`
	Digest   digest.Digest
	UID, GID int
	Mode     os.FileMode
}

func files(project *types.Project, service types.ServiceConfig) ([]File, error) {
	var result []File
	add := func(kind, source, target, uid, gid string, mode *types.FileMode, object types.FileObjectConfig, directory string) error {
		data := docker.Content(object)
		if target == "" {
			target = source
		}
		if !path.IsAbs(target) {
			target = path.Join(directory, target)
		}
		file := File{Path: target, Data: data, Digest: digest.FromBytes(data), Mode: 0o444}
		var err error
		if file.UID, err = id(uid); err != nil {
			return fmt.Errorf("%s %s uid: %w", kind, source, err)
		}
		if file.GID, err = id(gid); err != nil {
			return fmt.Errorf("%s %s gid: %w", kind, source, err)
		}
		if mode != nil {
			file.Mode = os.FileMode(*mode)
		}
		result = append(result, file)
		return nil
	}
	for _, config := range service.Configs {
		declared, ok := project.Configs[config.Source]
		if !ok {
			return nil, fmt.Errorf("undefined config %q", config.Source)
		}
		if err := add("config", config.Source, config.Target, config.UID, config.GID, config.Mode, types.FileObjectConfig(declared), "/"); err != nil {
			return nil, err
		}
	}
	for _, secret := range service.Secrets {
		declared, ok := project.Secrets[secret.Source]
		if !ok {
			return nil, fmt.Errorf("undefined secret %q", secret.Source)
		}
		if err := add("secret", secret.Source, secret.Target, secret.UID, secret.GID, secret.Mode, types.FileObjectConfig(declared), "/run/secrets"); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(result, func(left, right File) int { return strings.Compare(left.Path, right.Path) })
	return result, nil
}

func id(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.Atoi(value)
}
