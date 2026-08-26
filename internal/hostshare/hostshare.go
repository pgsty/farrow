// Package hostshare opens user-selected host directories without following
// symlinks and keeps the resulting descriptors anchored through QEMU startup.
package hostshare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
	"golang.org/x/sys/unix"
)

type Identity struct {
	Device uint64
	Inode  uint64
}

type Bundle struct {
	project    project.Project
	shares     []spec.Share
	files      []*os.File
	identities []Identity
}

func contains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func overlaps(first, second string) bool {
	return contains(first, second) || contains(second, first)
}

func validateProtectedPaths(value project.Project, share spec.Share) error {
	for label, protected := range map[string]string{
		"Farrow data root":    value.DataRoot,
		"Farrow project root": value.Root,
	} {
		if protected != "" && overlaps(share.Host, protected) {
			return fmt.Errorf("host share %q overlaps the protected %s %q", share.Host, label, protected)
		}
	}
	if value.MarkerDir == "" {
		return nil
	}
	if contains(value.MarkerDir, share.Host) {
		return fmt.Errorf("host share %q is inside the Farrow marker directory %q", share.Host, value.MarkerDir)
	}
	if !share.Readonly && contains(share.Host, value.MarkerDir) {
		return fmt.Errorf("writable host share %q contains the Farrow marker directory %q; share a safe subdirectory or set readonly: true", share.Host, value.MarkerDir)
	}
	return nil
}

func directoryIdentity(stat *unix.Stat_t) Identity {
	return Identity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}
}

func openDirectory(share spec.Share) (*os.File, Identity, error) {
	if !filepath.IsAbs(share.Host) || filepath.Clean(share.Host) != share.Host || share.Host == "/" || strings.ContainsAny(share.Host, "\x00\r\n") {
		return nil, Identity{}, fmt.Errorf("host share path must be a clean non-root absolute path: %q", share.Host)
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_DIRECTORY
	descriptor, err := unix.Open(string(filepath.Separator), flags, 0)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("open host filesystem root: %w", err)
	}
	closeDescriptor := true
	defer func() {
		if closeDescriptor {
			_ = unix.Close(descriptor)
		}
	}()
	components := strings.Split(strings.TrimPrefix(share.Host, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, Identity{}, fmt.Errorf("host share has an unsafe path component: %q", share.Host)
		}
		next, openErr := unix.Openat(descriptor, component, flags, 0)
		if openErr != nil {
			return nil, Identity{}, fmt.Errorf("secure-open host share %q at %q: %w", share.Host, component, openErr)
		}
		_ = unix.Close(descriptor)
		descriptor = next
	}
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		return nil, Identity{}, fmt.Errorf("fstat host share %q: %w", share.Host, err)
	}
	mode := uint32(stat.Mode)
	if mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, Identity{}, fmt.Errorf("host share is not a directory: %q", share.Host)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return nil, Identity{}, fmt.Errorf("host share %q is owned by uid %d, expected %d", share.Host, stat.Uid, os.Geteuid())
	}
	permissions := mode & 0o777
	if permissions&0o022 != 0 {
		return nil, Identity{}, fmt.Errorf("host share %q is group/world writable (%#o)", share.Host, permissions)
	}
	required := uint32(0o500)
	if !share.Readonly {
		required = 0o700
	}
	if permissions&required != required {
		return nil, Identity{}, fmt.Errorf("host share %q owner permissions %#o do not provide required %#o", share.Host, permissions, required)
	}
	file := os.NewFile(uintptr(descriptor), "farrow-host-share")
	if file == nil {
		return nil, Identity{}, errors.New("wrap host share directory descriptor")
	}
	closeDescriptor = false
	return file, directoryIdentity(&stat), nil
}

// Open validates protected-path boundaries, securely opens every source in
// canonical spec order, and closes the partial set on every error.
func Open(value project.Project, shares []spec.Share) (*Bundle, error) {
	bundle := &Bundle{project: value, shares: append([]spec.Share(nil), shares...)}
	for _, share := range shares {
		if err := validateProtectedPaths(value, share); err != nil {
			_ = bundle.Close()
			return nil, err
		}
		file, identity, err := openDirectory(share)
		if err != nil {
			_ = bundle.Close()
			return nil, err
		}
		bundle.files = append(bundle.files, file)
		bundle.identities = append(bundle.identities, identity)
	}
	return bundle, nil
}

func Validate(value project.Project, shares []spec.Share) error {
	bundle, err := Open(value, shares)
	if err != nil {
		return err
	}
	return bundle.Close()
}

func (b *Bundle) Files() []*os.File {
	if b == nil {
		return nil
	}
	return append([]*os.File(nil), b.files...)
}

func (b *Bundle) ValidateInvocation(invocation qemu.Invocation, prefixFiles int) error {
	if b == nil {
		return errors.New("host share bundle is nil")
	}
	files := invocation.ShareFiles()
	if len(files) != len(b.shares) {
		return fmt.Errorf("QEMU invocation has %d share descriptors for %d sources", len(files), len(b.shares))
	}
	for index, file := range files {
		expectedFD := 3 + prefixFiles + index
		expectedTag := spec.ShareTag(b.shares[index])
		if file.FD != expectedFD || file.ID != expectedTag {
			return fmt.Errorf("QEMU share %d inherited-file layout is %s/fd%d, expected %s/fd%d", index, file.ID, file.FD, expectedTag, expectedFD)
		}
	}
	return nil
}

// Recheck reopens each pathname and proves it still names the inode anchored
// in this bundle. The live QEMU export remains bound to the descriptor even if
// this check fails; callers should stop a just-launched VM before returning.
func (b *Bundle) Recheck() error {
	if b == nil || len(b.shares) != len(b.identities) {
		return errors.New("host share bundle is incomplete")
	}
	for index, share := range b.shares {
		file, identity, err := openDirectory(share)
		if err != nil {
			return err
		}
		_ = file.Close()
		if identity != b.identities[index] {
			return fmt.Errorf("host share %q changed identity during QEMU launch", share.Host)
		}
	}
	return nil
}

func (b *Bundle) Close() error {
	if b == nil {
		return nil
	}
	var joined error
	for _, file := range b.files {
		if file != nil {
			joined = errors.Join(joined, file.Close())
		}
	}
	b.files = nil
	return joined
}

func QEMU(shares []spec.Share) []qemu.Share {
	result := make([]qemu.Share, 0, len(shares))
	for _, share := range shares {
		result = append(result, qemu.Share{Tag: spec.ShareTag(share), Readonly: share.Readonly})
	}
	return result
}
