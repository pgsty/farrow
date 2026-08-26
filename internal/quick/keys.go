package quick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/persistent"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/state"
	"golang.org/x/sys/unix"
)

type KeyPurgeAction struct {
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
}

type KeyPurgeReport struct {
	ProjectID string           `json:"project_id"`
	Apply     bool             `json:"apply"`
	Actions   []KeyPurgeAction `json:"actions"`
}

// KeyPurgeStateError reports a safe, expected precondition failure. The
// caller must destroy every retained node artifact before keys can be purged.
type KeyPurgeStateError struct{ Reason string }

func (e *KeyPurgeStateError) Error() string { return "refuse project key purge: " + e.Reason }

// KeyPurgeIntegrityError reports a path, ownership, mode, or link invariant
// that makes deletion unsafe. No key artifact is removed after this error is
// discovered during preflight.
type KeyPurgeIntegrityError struct{ Reason string }

func (e *KeyPurgeIntegrityError) Error() string {
	return "project key purge integrity check failed: " + e.Reason
}

func purgeState(reason string, args ...any) error {
	return &KeyPurgeStateError{Reason: fmt.Sprintf(reason, args...)}
}

func purgeIntegrity(reason string, args ...any) error {
	return &KeyPurgeIntegrityError{Reason: fmt.Sprintf(reason, args...)}
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func singlyLinked(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Nlink) == 1
}

func validateOwnedDirectory(path string, wantMode os.FileMode) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != wantMode {
		return nil, purgeIntegrity("directory is not a real mode-%04o directory: %s", wantMode, path)
	}
	if !ownedByCurrentUser(info) {
		return nil, purgeIntegrity("directory is not owned by uid %d: %s", os.Geteuid(), path)
	}
	return info, nil
}

func validateOwnedRegular(path string, wantMode os.FileMode) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != wantMode {
		return nil, purgeIntegrity("artifact is not a regular mode-%04o file: %s", wantMode, path)
	}
	if !ownedByCurrentUser(info) || !singlyLinked(info) {
		return nil, purgeIntegrity("artifact owner or hard-link count is unsafe: %s", path)
	}
	return info, nil
}

func validateOwnedRegularInfo(path string, info os.FileInfo, mode os.FileMode) (os.FileInfo, error) {
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode || !ownedByCurrentUser(info) || !singlyLinked(info) {
		return nil, purgeIntegrity("opened key artifact owner, mode, type, or hard-link count is unsafe: %s", path)
	}
	return info, nil
}

func validateKeyProject(projectValue project.Project) error {
	if _, err := validateOwnedDirectory(projectValue.Root, 0o700); err != nil {
		return err
	}
	if _, err := filepath.EvalSymlinks(projectValue.Root); err != nil {
		return purgeIntegrity("project root cannot be resolved: %s", projectValue.Root)
	}
	for _, marker := range []string{projectValue.MarkerPath, filepath.Join(projectValue.Root, "project.json")} {
		if _, err := validateOwnedRegular(marker, 0o600); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return purgeIntegrity("marker-owned project file is missing: %s", marker)
			}
			return err
		}
	}
	return nil
}

// ensureNoRetainedNodes is deliberately conservative. A node directory is a
// retained artifact even when its state cannot be decoded; a readable live
// PID or data-disk declaration is surfaced more specifically for operators.
func ensureNoRetainedNodes(ctx context.Context, projectValue project.Project) error {
	if err := ctx.Err(); err != nil {
		return purgeState("preflight was canceled: %v", err)
	}
	nodesDir := filepath.Join(projectValue.Root, "nodes")
	info, err := os.Lstat(nodesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return purgeIntegrity("inspect node artifact directory: %v", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return purgeIntegrity("node artifact path is not a real directory: %s", nodesDir)
	}
	if info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) {
		return purgeIntegrity("node artifact directory owner or mode is unsafe: %s", nodesDir)
	}
	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		return purgeIntegrity("read node artifact directory: %v", err)
	}
	if len(entries) == 0 {
		return nil
	}
	store := state.Store{Project: projectValue}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return purgeState("preflight was canceled: %v", err)
		}
		nodePath := filepath.Join(nodesDir, entry.Name())
		nodeInfo, statErr := os.Lstat(nodePath)
		if statErr != nil {
			return purgeIntegrity("inspect retained node artifact %s: %v", nodePath, statErr)
		}
		if !nodeInfo.IsDir() || nodeInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		node, readErr := store.ReadNode(entry.Name())
		if readErr != nil {
			continue
		}
		if process.Alive(node.Process.PID) {
			return purgeState("node %s still has a live recorded process pid=%d", node.Node, node.Process.PID)
		}
		if len(node.DataDisks) > 0 {
			return purgeState("node %s still retains %d data disk(s)", node.Node, len(node.DataDisks))
		}
	}
	return purgeState("%d retained node artifact(s) remain under %s; destroy the project first", len(entries), nodesDir)
}

type openedKeyArtifact struct {
	path   string
	mode   os.FileMode
	info   os.FileInfo
	handle *os.File
}

func closeKeyArtifacts(artifacts []openedKeyArtifact) {
	for index := range artifacts {
		if artifacts[index].handle != nil {
			_ = artifacts[index].handle.Close()
			artifacts[index].handle = nil
		}
	}
}

func openKeyArtifact(path string, mode os.FileMode) (openedKeyArtifact, error) {
	info, err := validateOwnedRegular(path, mode)
	if err != nil {
		return openedKeyArtifact{}, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return openedKeyArtifact{}, purgeIntegrity("open key artifact without following links %s: %v", path, err)
	}
	handle := os.NewFile(uintptr(fd), path)
	opened, statErr := handle.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = handle.Close()
		return openedKeyArtifact{}, purgeIntegrity("key artifact identity changed while opening: %s", path)
	}
	if _, err := validateOwnedRegularInfo(path, opened, mode); err != nil {
		_ = handle.Close()
		return openedKeyArtifact{}, err
	}
	return openedKeyArtifact{path: path, mode: mode, info: info, handle: handle}, nil
}

func inspectKeyArtifacts(projectValue project.Project) (string, os.FileInfo, []openedKeyArtifact, error) {
	keysDir := filepath.Join(projectValue.Root, "keys")
	keysInfo, err := os.Lstat(keysDir)
	if errors.Is(err, os.ErrNotExist) {
		return keysDir, nil, nil, nil
	}
	if err != nil {
		return keysDir, nil, nil, purgeIntegrity("inspect project keys directory: %v", err)
	}
	if _, err := validateOwnedDirectory(keysDir, 0o700); err != nil {
		return keysDir, nil, nil, err
	}
	inside, err := fsutil.IsWithin(projectValue.Root, keysDir)
	if err != nil || !inside {
		return keysDir, nil, nil, purgeIntegrity("project keys directory escapes the marker-owned project root: %s", keysDir)
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(projectValue.Root)
	canonicalKeys, keysErr := filepath.EvalSymlinks(keysDir)
	if rootErr != nil || keysErr != nil || canonicalKeys != filepath.Join(canonicalRoot, "keys") {
		return keysDir, nil, nil, purgeIntegrity("project keys directory is not the exact marker-owned path: %s", keysDir)
	}
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		return keysDir, nil, nil, purgeIntegrity("read project keys directory: %v", err)
	}
	allowed := map[string]os.FileMode{
		"id_ed25519":     0o600,
		"id_ed25519.pub": 0o644,
		"known_hosts":    0o600,
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	artifacts := make([]openedKeyArtifact, 0, len(entries))
	for _, entry := range entries {
		mode, ok := allowed[entry.Name()]
		if !ok {
			closeKeyArtifacts(artifacts)
			return keysDir, nil, nil, purgeIntegrity("unexpected key-directory entry %q", entry.Name())
		}
		artifact, err := openKeyArtifact(filepath.Join(keysDir, entry.Name()), mode)
		if err != nil {
			closeKeyArtifacts(artifacts)
			return keysDir, nil, nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return keysDir, keysInfo, artifacts, nil
}

func verifyKeyDeletionSnapshot(keysDir string, keysInfo os.FileInfo, artifacts []openedKeyArtifact) error {
	currentDir, err := os.Lstat(keysDir)
	if err != nil || !os.SameFile(keysInfo, currentDir) {
		return purgeIntegrity("project keys directory identity changed after preflight: %s", keysDir)
	}
	for _, artifact := range artifacts {
		current, err := validateOwnedRegular(artifact.path, artifact.mode)
		if err != nil || !os.SameFile(artifact.info, current) {
			return purgeIntegrity("key artifact identity changed after preflight: %s", artifact.path)
		}
	}
	return nil
}

// PurgeProjectKeys is the shared quick/private deletion boundary. It trusts
// only the exact keys child of a marker-verified project root, plans by
// default, and refuses all deletion while any node artifact remains.
func PurgeProjectKeys(ctx context.Context, projectValue project.Project, apply bool) (KeyPurgeReport, error) {
	report := KeyPurgeReport{ProjectID: projectValue.Marker.ProjectID, Apply: apply, Actions: []KeyPurgeAction{}}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return report, purgeIntegrity("acquire project lock: %v", err)
	}
	defer projectLock.Release()
	if err := validateKeyProject(projectValue); err != nil {
		return report, err
	}
	if err := ensureNoRetainedNodes(ctx, projectValue); err != nil {
		return report, err
	}
	retainedDisks, err := persistent.Inventory(projectValue)
	if err != nil {
		return report, purgeIntegrity("inspect retained persistent disks: %v", err)
	}
	if len(retainedDisks) != 0 {
		return report, purgeState("%d persistent data disk(s) remain; explicitly delete them before purging project keys", len(retainedDisks))
	}
	keysDir, keysInfo, artifacts, err := inspectKeyArtifacts(projectValue)
	if err != nil {
		return report, err
	}
	defer closeKeyArtifacts(artifacts)
	for _, artifact := range artifacts {
		report.Actions = append(report.Actions, KeyPurgeAction{Path: artifact.path})
	}
	if keysInfo == nil || !apply {
		return report, nil
	}
	if err := verifyKeyDeletionSnapshot(keysDir, keysInfo, artifacts); err != nil {
		return report, err
	}
	closeKeyArtifacts(artifacts)
	for index, artifact := range artifacts {
		if err := os.Remove(artifact.path); err != nil {
			return report, purgeIntegrity("remove exact project key artifact %s: %v", artifact.path, err)
		}
		report.Actions[index].Applied = true
	}
	if err := fsutil.SyncDir(keysDir); err != nil {
		return report, purgeIntegrity("sync project keys directory: %v", err)
	}
	currentDir, err := os.Lstat(keysDir)
	if err != nil || !os.SameFile(keysInfo, currentDir) {
		return report, purgeIntegrity("project keys directory identity changed before removal: %s", keysDir)
	}
	entries, err := os.ReadDir(keysDir)
	if err != nil || len(entries) != 0 {
		return report, purgeIntegrity("project keys directory is not empty after exact artifact removal: %s", keysDir)
	}
	if err := os.Remove(keysDir); err != nil {
		return report, purgeIntegrity("remove empty project keys directory: %v", err)
	}
	if err := fsutil.SyncDir(projectValue.Root); err != nil {
		return report, purgeIntegrity("sync marker-owned project root: %v", err)
	}
	return report, nil
}

func (m Manager) PurgeKeys(ctx context.Context, apply bool) (KeyPurgeReport, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return KeyPurgeReport{}, err
	}
	return PurgeProjectKeys(ctx, projectValue, apply)
}
