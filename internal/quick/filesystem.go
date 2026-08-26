package quick

import (
	"github.com/pgsty/farrow/internal/cloudinit"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

// quickDiskRecords keeps the filesystem requested in the resolved spec
// distinct from the filesystem eventually observed in the guest. Prepare can
// persist the former immediately; the latter remains empty until it has been
// verified from guest evidence.
func quickDiskRecords(definition spec.Disk, path, serial string) (state.DataDisk, cloudinit.Disk) {
	requested := normalizedFilesystem(definition.Filesystem)
	return state.DataDisk{
		Name: definition.Name, Path: path, Serial: serial, Size: definition.Size,
		Mount: definition.Mount, Persistent: definition.Persistent,
		RequestedFilesystem: requested,
	}, cloudinit.Disk{Serial: serial, Mount: definition.Mount, Filesystem: requested}
}
