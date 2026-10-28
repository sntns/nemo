package main

import (
	"errors"
	"path"
	"reflect"
	"strings"

	"github.com/sntns/nemo/pkg/target"
)

// The kinds of volume types the editor makes, those of the targets guide;
// any other stays as the manifest writes it.
const (
	volumeLocal   = "local"
	volumeHost    = "host"
	volumeNFS     = "nfs"
	volumeWritten = "manifest"
)

// volumeTitles say what the value of each kind of volume type is.
var volumeTitles = map[string]string{volumeHost: "Directory", volumeNFS: "Export"}

// volumeKindTitles say what each kind makes of the volumes.
var volumeKindTitles = map[string]string{
	volumeLocal:   "Docker's own volumes",
	volumeHost:    "a directory of the host each",
	volumeNFS:     "a directory of an NFS export each",
	volumeWritten: "as the manifest writes them",
}

// hostVolumes makes each volume a directory of the host, under base.
func hostVolumes(base string) target.VolumeType {
	bind := func(device string) target.VolumeDriver {
		return target.VolumeDriver{Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": device}}
	}
	return target.VolumeType{
		VolumeDriver: bind(base + "/{{.Stack}}/{{.Name}}"),
		Provision:    &target.VolumeProvision{Directory: "{{.Stack}}/{{.Name}}", Volume: bind(base)},
	}
}

// nfsVolumes makes each volume a directory of the server's export.
func nfsVolumes(server, export string) target.VolumeType {
	mount := "addr=" + server + ",rw,nfsvers=4"
	nfs := func(o, device string) target.VolumeDriver {
		return target.VolumeDriver{Driver: "local", Options: map[string]string{"type": "nfs", "o": o, "device": ":" + device}}
	}
	return target.VolumeType{
		VolumeDriver: nfs(mount+`{{AppendJoin .Options ","}}`, export+"/{{.Stack}}/{{.Name}}"),
		Provision:    &target.VolumeProvision{Directory: "{{.Stack}}/{{.Name}}", Volume: nfs(mount, export)},
	}
}

// volumeKindOf recognizes the kind of a volume type, and the value it was
// made of: a type the editor did not make is the manifest's.
func volumeKindOf(volumeType target.VolumeType) (string, string) {
	if (volumeType.Driver == "" || volumeType.Driver == "local") && len(volumeType.Options) == 0 && volumeType.Provision == nil {
		return volumeLocal, ""
	}
	if provision := volumeType.Provision; provision != nil {
		options := provision.Volume.Options
		if base := options["device"]; reflect.DeepEqual(volumeType, hostVolumes(base)) {
			return volumeHost, base
		}
		server := strings.TrimSuffix(strings.TrimPrefix(options["o"], "addr="), ",rw,nfsvers=4")
		export := strings.TrimPrefix(options["device"], ":")
		if reflect.DeepEqual(volumeType, nfsVolumes(server, export)) {
			return volumeNFS, server + ":" + export
		}
	}
	return volumeWritten, ""
}

// volumeTypeOf makes the volume type of the kind from its value; the
// manifest's kind keeps the type written.
func volumeTypeOf(kind, value string, written target.VolumeType) (target.VolumeType, error) {
	switch kind {
	case volumeHost:
		if !path.IsAbs(value) {
			return target.VolumeType{}, errors.New("an absolute directory of the host, /srv")
		}
		return hostVolumes(strings.TrimSuffix(path.Clean(value), "/")), nil
	case volumeNFS:
		server, export, found := strings.Cut(value, ":/")
		if !found || server == "" {
			return target.VolumeType{}, errors.New("SERVER:/EXPORT, 10.0.0.20:/exports")
		}
		return nfsVolumes(server, strings.TrimSuffix(path.Clean("/"+export), "/")), nil
	case volumeWritten:
		return written, nil
	}
	return target.VolumeType{}, nil
}

// volumeSummary says what the volumes of a type are made of.
func volumeSummary(volumeType target.VolumeType) string {
	switch kind, value := volumeKindOf(volumeType); kind {
	case volumeHost:
		return "directories of the host, under " + value
	case volumeNFS:
		return "directories of the NFS export " + value
	case volumeWritten:
		return "as the manifest writes it, driver " + volumeType.Driver
	}
	return "local"
}
