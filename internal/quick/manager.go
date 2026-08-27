// Package quick promotes the proven M0 path into the actual single-node
// project lifecycle.
package quick

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/cloudinit"
	"github.com/pgsty/farrow/internal/disk"
	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/hostshare"
	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/lock"
	usernet "github.com/pgsty/farrow/internal/network/user"
	"github.com/pgsty/farrow/internal/openssh"
	"github.com/pgsty/farrow/internal/persistent"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/portregistry"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/runtimepath"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

const nodeName = "meta"

type Manager struct {
	CWD                string
	FarrowVersion      string
	OperationID        string
	QEMUImg            string
	LogLevel           string
	LeaseStore         *lease.Store
	Runner             execx.Runner
	ReadyTimeout       time.Duration
	ConfiguredDataRoot string
	Repository         string
	Progress           activity.Reporter
	NoWait             bool
	NativeProfile      func() (platform.Profile, error)
	LookPath           func(string) (string, error)
}

type Status struct {
	ProjectID   string         `json:"project_id"`
	OperationID string         `json:"operation_id,omitempty"`
	Node        string         `json:"node"`
	State       state.Phase    `json:"state"`
	SSHUser     string         `json:"ssh_user"`
	SSHHost     string         `json:"ssh_host"`
	SSHPort     uint16         `json:"ssh_port"`
	Forwards    []qemu.Forward `json:"forwards"`
	Image       state.Image    `json:"image"`
	SpecHash    string         `json:"spec_hash"`
	Message     string         `json:"message,omitempty"`
}

type Connection struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	Port       uint16 `json:"port"`
	PrivateKey string `json:"private_key"`
	KnownHosts string `json:"known_hosts"`
}

type ImageInfo struct {
	Entry    image.Entry         `json:"entry"`
	Manifest image.ManifestState `json:"manifest"`
	Cached   bool                `json:"cached"`
	Path     string              `json:"path,omitempty"`
	Metadata *image.Metadata     `json:"metadata,omitempty"`
}

func (m Manager) runner() execx.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return execx.OSRunner{Timeout: 15 * time.Second, OutputLimit: 1 << 20}
}

func (m Manager) readyTimeout(resolved spec.Resolved) (time.Duration, error) {
	if m.ReadyTimeout < 0 {
		return 0, errors.New("manager readiness timeout must be positive")
	}
	if m.ReadyTimeout > 0 {
		return m.ReadyTimeout, nil
	}
	return resolved.SSHWaitTimeout()
}

func (m Manager) privateLeaseStore() lease.Store {
	if m.LeaseStore != nil {
		return *m.LeaseStore
	}
	return lease.Store{}
}

func (m Manager) workDir() (string, error) {
	if m.CWD != "" {
		return filepath.Abs(m.CWD)
	}
	return os.Getwd()
}

func (m Manager) dataRoot() (string, error) {
	cwd, err := m.workDir()
	if err != nil {
		return "", err
	}
	if current, openErr := project.Open(cwd); openErr == nil {
		return current.DataRoot, nil
	} else if !missingPath(openErr) {
		return "", openErr
	}
	return project.ResolveDataRootWithConfig(cwd, m.ConfiguredDataRoot, nil)
}

func (m Manager) imageStore(profile platform.Profile, dataRoot, repository string) (image.Store, error) {
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		return image.Store{}, err
	}
	_ = profile
	return image.Store{DataRoot: dataRoot, Repository: repository, QEMUImg: qemuImg, Runner: m.runner(), Progress: m.Progress}, nil
}

func (m Manager) report(phase, message string) {
	m.Progress.Report(activity.Event{Phase: phase, Message: message})
}

func (m Manager) configuredRepository() (string, bool, error) {
	repository := strings.TrimSpace(m.Repository)
	explicit := repository != ""
	if repository == "" {
		repository = strings.TrimSpace(os.Getenv("FARROW_REPO"))
		explicit = repository != ""
	}
	if repository == "" {
		repository = image.DefaultRepositoryURL
	}
	normalized, err := image.NormalizeRepository(repository)
	return normalized, explicit, err
}

func (m Manager) imageCatalog(ctx context.Context, dataRoot string, syncRepository bool) (image.Catalog, image.ManifestState, string, error) {
	repository, explicit, err := m.configuredRepository()
	if err != nil {
		return image.Catalog{}, image.ManifestState{}, "", err
	}
	manager := image.ManifestManager{DataRoot: dataRoot}
	defaultSyncFailed := false
	if syncRepository && repository != "" {
		source, sourceErr := image.RepositoryCatalogSource(repository)
		if sourceErr != nil {
			return image.Catalog{}, image.ManifestState{}, "", sourceErr
		}
		m.Progress.Report(activity.Event{Phase: "image-catalog", Message: "Refreshing the image catalog", Source: source})
		if _, syncErr := manager.Sync(ctx, source, false); syncErr != nil {
			if explicit {
				return image.Catalog{}, image.ManifestState{}, "", fmt.Errorf("sync explicit image repository: %w", syncErr)
			}
			m.report("image-catalog", "Default image repository unavailable; using the embedded catalog")
			repository = ""
			defaultSyncFailed = true
		} else {
			m.Progress.Report(activity.Event{Phase: "image-catalog", Message: "Image catalog is current", Source: source, Done: true})
		}
	}
	catalog, state, err := manager.Current()
	if err != nil {
		return image.Catalog{}, image.ManifestState{}, "", err
	}
	if repository == "" && !defaultSyncFailed {
		repository = image.RepositoryFromCatalogSource(state.Source)
	}
	return catalog, state, repository, nil
}

func (m Manager) ImportImage(ctx context.Context, source, expectedDigest string) (string, image.Metadata, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return "", image.Metadata{}, err
	}
	profile, err := platform.Native()
	if err != nil {
		return "", image.Metadata{}, err
	}
	store, err := m.imageStore(profile, dataRoot, "")
	if err != nil {
		return "", image.Metadata{}, err
	}
	return store.Import(ctx, source, expectedDigest)
}

func (m Manager) ImportNamedImage(ctx context.Context, source, expectedDigest, name, boot, sourceUser string) (image.Entry, string, image.Metadata, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	profile, err := platform.Native()
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	store, err := m.imageStore(profile, dataRoot, "")
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	path, metadata, err := store.Import(ctx, source, expectedDigest)
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	entry, path, metadata, err := store.RegisterLocalAlias(ctx, name, path, metadata, profile.Arch, boot, sourceUser)
	return entry, path, metadata, err
}

func (m Manager) ManifestSync(ctx context.Context, source string, allowDowngrade bool) (image.ManifestState, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return image.ManifestState{}, err
	}
	return (image.ManifestManager{DataRoot: dataRoot}).Sync(ctx, source, allowDowngrade)
}

func (m Manager) ManifestReset(ctx context.Context) (image.ManifestState, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return image.ManifestState{}, err
	}
	return (image.ManifestManager{DataRoot: dataRoot}).Reset(ctx)
}

func (m Manager) Images(ctx context.Context) ([]image.Entry, image.ManifestState, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return nil, image.ManifestState{}, err
	}
	catalog, manifestState, repository, err := m.imageCatalog(ctx, dataRoot, true)
	if err != nil {
		return nil, image.ManifestState{}, err
	}
	entries := catalog.Entries()
	profile, err := platform.Native()
	if err != nil {
		return nil, image.ManifestState{}, err
	}
	store, err := m.imageStore(profile, dataRoot, repository)
	if err != nil {
		return nil, image.ManifestState{}, err
	}
	locals, err := store.LocalEntries()
	if err != nil {
		return nil, image.ManifestState{}, err
	}
	for _, local := range locals {
		entry, _, _, resolveErr := store.ResolveLocalAlias(ctx, local.Name, profile.Arch)
		if resolveErr != nil {
			return nil, image.ManifestState{}, resolveErr
		}
		entries = append(entries, entry)
	}
	return entries, manifestState, nil
}

func (m Manager) ImageInfo(ctx context.Context, alias string) (ImageInfo, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return ImageInfo{}, err
	}
	profile, err := platform.Native()
	if err != nil {
		return ImageInfo{}, err
	}
	catalog, manifestState, repository, err := m.imageCatalog(ctx, dataRoot, true)
	if err != nil {
		return ImageInfo{}, err
	}
	store, err := m.imageStore(profile, dataRoot, repository)
	if err != nil {
		return ImageInfo{}, err
	}
	entry, entryErr := catalog.Entry(alias, profile.Arch)
	if entryErr != nil {
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, profile.Arch)
		if localErr != nil {
			return ImageInfo{}, entryErr
		}
		return ImageInfo{Entry: localEntry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
	}
	path, metadata, cacheErr := store.ValidateCached(ctx, entry)
	if errors.Is(cacheErr, os.ErrNotExist) {
		return ImageInfo{Entry: entry, Manifest: manifestState}, nil
	}
	if cacheErr != nil {
		return ImageInfo{}, cacheErr
	}
	return ImageInfo{Entry: entry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
}

func (m Manager) PullImage(ctx context.Context, alias string) (ImageInfo, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return ImageInfo{}, err
	}
	profile, err := platform.Native()
	if err != nil {
		return ImageInfo{}, err
	}
	catalog, manifestState, repository, err := m.imageCatalog(ctx, dataRoot, true)
	if err != nil {
		return ImageInfo{}, err
	}
	store, err := m.imageStore(profile, dataRoot, repository)
	if err != nil {
		return ImageInfo{}, err
	}
	entry, entryErr := catalog.Entry(alias, profile.Arch)
	if entryErr != nil {
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, profile.Arch)
		if localErr != nil {
			return ImageInfo{}, entryErr
		}
		return ImageInfo{Entry: localEntry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
	}
	path, metadata, err := store.Pull(ctx, entry)
	if err != nil {
		return ImageInfo{}, err
	}
	return ImageInfo{Entry: entry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
}

func (m Manager) openProject(create bool) (project.Project, error) {
	cwd, err := m.workDir()
	if err != nil {
		return project.Project{}, err
	}
	return project.OpenConfigured(cwd, m.ConfiguredDataRoot, create)
}

func processToState(identity process.Identity) state.ProcessIdentity {
	return state.ProcessIdentity{PID: identity.PID, Executable: identity.Executable, Started: identity.Started, ArgvHash: identity.ArgvHash}
}

func processFromState(identity state.ProcessIdentity) process.Identity {
	return process.Identity{PID: identity.PID, Executable: identity.Executable, Started: identity.Started, ArgvHash: identity.ArgvHash}
}

func lifecycle(runner execx.Runner) vm.Lifecycle {
	return vm.Lifecycle{Runner: runner, QMP: &qmp.Client{Timeout: 5 * time.Second}}
}

func ensureModeDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe directory %s", path)
	}
	return os.Chmod(path, 0o700)
}

func ensureKeys(ctx context.Context, runner execx.Runner, root string) (string, string, string, error) {
	directory := filepath.Join(root, "keys")
	if err := ensureModeDirectory(directory); err != nil {
		return "", "", "", err
	}
	privateKey := filepath.Join(directory, "id_ed25519")
	publicKey := privateKey + ".pub"
	knownHosts := filepath.Join(directory, "known_hosts")
	if _, err := os.Lstat(privateKey); errors.Is(err, os.ErrNotExist) {
		sshKeygen, lookErr := exec.LookPath("ssh-keygen")
		if lookErr != nil {
			return "", "", "", lookErr
		}
		if _, runErr := runner.Run(ctx, sshKeygen, "-q", "-t", "ed25519", "-N", "", "-C", "farrow-project", "-f", privateKey); runErr != nil {
			return "", "", "", runErr
		}
	} else if err != nil {
		return "", "", "", err
	}
	for _, path := range []string{privateKey, publicKey, knownHosts} {
		if path == knownHosts {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", "", "", fmt.Errorf("project key file is unsafe: %s", path)
		}
	}
	if err := os.Chmod(privateKey, 0o600); err != nil {
		return "", "", "", err
	}
	if err := os.Chmod(publicKey, 0o644); err != nil {
		return "", "", "", err
	}
	if _, err := os.Lstat(knownHosts); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
			return "", "", "", err
		}
	} else if err != nil {
		return "", "", "", err
	}
	knownInfo, err := os.Lstat(knownHosts)
	if err != nil || !knownInfo.Mode().IsRegular() {
		return "", "", "", fmt.Errorf("project known_hosts file is unsafe: %s", knownHosts)
	}
	if err := os.Chmod(knownHosts, 0o600); err != nil {
		return "", "", "", err
	}
	for path, mode := range map[string]os.FileMode{privateKey: 0o600, publicKey: 0o644, knownHosts: 0o600} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
			return "", "", "", fmt.Errorf("project key file is unsafe: %s", path)
		}
	}
	publicBytes, err := os.ReadFile(publicKey)
	if err != nil {
		return "", "", "", err
	}
	return privateKey, knownHosts, strings.TrimSpace(string(publicBytes)), nil
}

// EnsureProjectKeys exposes the shared, ownership-checked project key
// boundary to the private multi-node product manager.
func EnsureProjectKeys(ctx context.Context, runner execx.Runner, root string) (string, string, string, error) {
	return ensureKeys(ctx, runner, root)
}

func copyExclusive(source, target string, mode os.FileMode) error {
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("target already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tempPath, _, err := fsutil.CopyToTemp(source, filepath.Dir(target), ".nvram-*.partial", mode, 512<<20)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	keep = true
	return fsutil.SyncDir(filepath.Dir(target))
}

func loopbackPortAvailable(port uint16) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func choosePortsWithProbe(desired spec.Resolved, reserved map[uint16]struct{}, probe func(uint16) bool) (uint16, spec.Resolved, error) {
	if probe == nil {
		return 0, spec.Resolved{}, errors.New("quick port availability probe is nil")
	}
	selected := make(map[uint16]struct{})
	available := func(port uint16) bool {
		if _, found := reserved[port]; found {
			return false
		}
		if _, found := selected[port]; found {
			return false
		}
		return probe(port)
	}
	sshPort, err := usernet.Choose(2222, available)
	if err != nil {
		return 0, spec.Resolved{}, err
	}
	selected[sshPort] = struct{}{}
	for index := range desired.Nodes[0].Forwards {
		forward := desired.Nodes[0].Forwards[index]
		port, err := usernet.Choose(spec.RequestedHostPort(forward), available)
		if err != nil {
			return 0, spec.Resolved{}, err
		}
		selected[port] = struct{}{}
		desired.Nodes[0].Forwards[index] = spec.WithMaterializedHost(forward, port)
	}
	return sshPort, desired, nil
}

func choosePorts(desired spec.Resolved, reserved map[uint16]struct{}) (uint16, spec.Resolved, error) {
	return choosePortsWithProbe(desired, reserved, loopbackPortAvailable)
}

func newRuntimeDirectory(projectID string) (string, error) {
	return runtimepath.Directory(projectID, nodeName, os.Getuid())
}

func ensureRuntime(path string) error {
	return runtimepath.Ensure(path, os.Getuid())
}

func legacyRuntimeDirectory(projectID string) string {
	return filepath.Join("/tmp", fmt.Sprintf("farrow-%d-%s-%s", os.Getuid(), projectID[:8], nodeName))
}

func qemuForwards(resolved spec.Resolved, sshPort uint16) []qemu.Forward {
	forwards := []qemu.Forward{{Bind: "127.0.0.1", Host: sshPort, Guest: 22}}
	for _, forward := range resolved.Nodes[0].Forwards {
		forwards = append(forwards, qemu.Forward{Bind: forward.Bind, Host: forward.Host, Guest: forward.Guest})
	}
	return forwards
}

func cloudShares(shares []spec.Share) []cloudinit.Share {
	result := make([]cloudinit.Share, 0, len(shares))
	for _, share := range shares {
		result = append(result, cloudinit.Share{Tag: spec.ShareTag(share), Guest: share.Guest, Readonly: share.Readonly})
	}
	return result
}

func (m Manager) ensureImage(ctx context.Context, profile platform.Profile, dataRoot, alias string) (image.Entry, string, image.Metadata, error) {
	m.report("image-resolve", fmt.Sprintf("Resolving image %s for %s", alias, profile.Arch))
	catalog, _, repository, err := m.imageCatalog(ctx, dataRoot, true)
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	store, err := m.imageStore(profile, dataRoot, repository)
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	entry, entryErr := catalog.Entry(alias, profile.Arch)
	if entryErr != nil {
		m.report("image-resolve", fmt.Sprintf("Looking for local image alias %s", alias))
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, profile.Arch)
		if localErr != nil {
			return image.Entry{}, "", image.Metadata{}, entryErr
		}
		m.Progress.Report(activity.Event{Phase: "image-ready", Message: fmt.Sprintf("Using local image %s (%s)", localEntry.Alias, profile.Arch), Done: true})
		return localEntry, path, metadata, nil
	}
	path, metadata, err := store.Pull(ctx, entry)
	return entry, path, metadata, err
}

// ResolveImage resolves and validates one native-architecture catalog entry
// into the readable local image directory without creating a project marker.
func (m Manager) ResolveImage(ctx context.Context, alias string) (image.Entry, string, image.Metadata, error) {
	profile, err := platform.Native()
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	dataRoot, err := m.dataRoot()
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	return m.ensureImage(ctx, profile, dataRoot, alias)
}

func transaction(store state.Store, version, projectID, operationID string, from, to state.Phase, completed []state.Action, started time.Time) error {
	return store.WriteTransaction(state.Transaction{
		Schema: state.TransactionSchema, FarrowVersion: version, OperationID: operationID,
		ProjectID: projectID, Node: nodeName, From: from, To: to, Completed: completed,
		StartedAt: started, UpdatedAt: time.Now().UTC(),
	})
}

func (m Manager) prepare(ctx context.Context, projectValue project.Project, resolved spec.Resolved, specHash string, sshPort uint16, entry image.Entry, basePath string, metadata image.Metadata, profile platform.Profile, qemuPath string) (state.NodeState, error) {
	m.report("prepare", "Preparing VM state for meta")
	runner := m.runner()
	store := state.Store{Project: projectValue}
	persistentIdentities, err := quickPersistentIdentities(projectValue, resolved)
	if err != nil {
		return state.NodeState{}, err
	}
	if _, err := persistent.ValidateDesired(projectValue, persistentIdentities); err != nil {
		return state.NodeState{}, err
	}
	nodeDir, err := projectValue.EnsureNodeDir(nodeName)
	if err != nil {
		return state.NodeState{}, err
	}
	operationID, err := m.operationID()
	if err != nil {
		return state.NodeState{}, err
	}
	started := time.Now().UTC()
	completed := make([]state.Action, 0, 8)
	if err := transaction(store, m.FarrowVersion, projectValue.Marker.ProjectID, operationID, state.Absent, state.Preparing, completed, started); err != nil {
		return state.NodeState{}, err
	}
	m.report("prepare-keys", "Creating or validating the project SSH key")
	privateKey, knownHosts, publicKey, err := ensureKeys(ctx, runner, projectValue.Root)
	if err != nil {
		return state.NodeState{}, err
	}
	_ = privateKey
	_ = knownHosts
	completed = append(completed, state.Action{Name: "project-key", Resource: filepath.Join(projectValue.Root, "keys")})
	rootPath := filepath.Join(nodeDir, "root.qcow2")
	diskManager := disk.Manager{Runner: runner}
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		return state.NodeState{}, err
	}
	diskManager.QEMUImg = qemuImg
	m.report("prepare-root-disk", "Creating the meta root-disk overlay")
	if _, err := diskManager.CreateOverlay(ctx, basePath, rootPath, resolved.Nodes[0].RootDisk); err != nil {
		return state.NodeState{}, err
	}
	completed = append(completed, state.Action{Name: "root-overlay", Resource: rootPath})
	if len(resolved.Nodes[0].Disks) > 1 {
		return state.NodeState{}, errors.New("quick resolved spec supports at most one data disk")
	}
	rootSerial, _ := identity.DiskSerial(projectValue.Marker.ProjectID, nodeName, "root")
	mgmtMAC, _ := identity.MAC(projectValue.Marker.ProjectID, nodeName, "management")
	var qemuData []qemu.Disk
	var stateData []state.DataDisk
	var cloudDisks []cloudinit.Disk
	if len(resolved.Nodes[0].Disks) == 1 {
		dataSpec := resolved.Nodes[0].Disks[0]
		m.report("prepare-data-disk", fmt.Sprintf("Preparing the meta data disk %s", dataSpec.Name))
		dataPath, dataSerial, err := resolveQuickDataDisk(ctx, projectValue, resolved, diskManager, nodeDir, dataSpec)
		if err != nil {
			return state.NodeState{}, err
		}
		if dataPath == filepath.Join(nodeDir, "data.qcow2") {
			completed = append(completed, state.Action{Name: "data-disk", Resource: dataPath})
		}
		qemuData = []qemu.Disk{{Path: dataPath, Serial: dataSerial}}
		stateDisk, cloudDisk := quickDiskRecords(dataSpec, dataPath, dataSerial)
		stateData = []state.DataDisk{stateDisk}
		cloudDisks = []cloudinit.Disk{cloudDisk}
	}
	m.report("prepare-seed", "Building the meta cloud-init seed")
	seedFiles, err := cloudinit.Render(cloudinit.Input{
		ProjectID: projectValue.Marker.ProjectID, Node: nodeName, Hostname: nodeName,
		Generation: 1, SpecHash: specHash, SSHUser: resolved.SSHUser, PublicKey: publicKey,
		MgmtMAC: mgmtMAC, Disks: cloudDisks, Shares: cloudShares(resolved.Nodes[0].Shares),
	})
	if err != nil {
		return state.NodeState{}, err
	}
	seedPath := filepath.Join(nodeDir, "seed.iso")
	if err := cloudinit.BuildISO(seedPath, seedFiles); err != nil {
		return state.NodeState{}, err
	}
	completed = append(completed, state.Action{Name: "seed", Resource: seedPath})
	firmware, err := platform.FindFirmwareForBoot(profile, entry.Boot)
	if err != nil {
		return state.NodeState{}, err
	}
	nvramPath := ""
	useUEFI := entry.Boot == "uefi"
	if useUEFI {
		nvramPath = filepath.Join(nodeDir, "nvram.fd")
		if err := copyExclusive(firmware.Vars, nvramPath, 0o600); err != nil {
			return state.NodeState{}, err
		}
		completed = append(completed, state.Action{Name: "nvram", Resource: nvramPath})
	}
	runtimeDir, err := newRuntimeDirectory(projectValue.Marker.ProjectID)
	if err != nil {
		return state.NodeState{}, err
	}
	if err := ensureRuntime(runtimeDir); err != nil {
		return state.NodeState{}, err
	}
	qmpPath := filepath.Join(runtimeDir, "qmp.sock")
	pidfile := filepath.Join(runtimeDir, "qemu.pid")
	vmUUID, err := project.NewUUID()
	if err != nil {
		return state.NodeState{}, err
	}
	var qemuFirmware *qemu.Firmware
	if useUEFI {
		qemuFirmware = &qemu.Firmware{Code: firmware.Code, Vars: nvramPath}
	}
	invocation, err := qemu.Build(qemu.Config{
		Profile: profile, Binary: qemuPath, Name: nodeName, UUID: vmUUID,
		CPUs: resolved.Nodes[0].CPUs, Memory: resolved.Nodes[0].Memory,
		Firmware: qemuFirmware, Root: qemu.Disk{Path: rootPath, Serial: rootSerial},
		Data: qemuData, Seed: seedPath,
		QMP: qmpPath, PIDFile: pidfile, SerialLog: filepath.Join(nodeDir, "serial.log"),
		MgmtMAC: mgmtMAC, Forwards: qemuForwards(resolved, sshPort), Shares: hostshare.QEMU(resolved.Nodes[0].Shares), Detach: true,
	})
	if err != nil {
		return state.NodeState{}, err
	}
	now := time.Now().UTC()
	node := state.NodeState{
		Schema: state.NodeSchema, FarrowVersion: m.FarrowVersion, ProjectID: projectValue.Marker.ProjectID,
		Node: nodeName, VMUUID: vmUUID, Phase: state.Prepared, Generation: 1, SpecHash: specHash,
		Image:     state.Image{Alias: entry.Alias, Release: entry.Release, Digest: entry.SHA256, VirtualSize: metadata.VirtualSize},
		RootDisk:  rootPath,
		DataDisks: stateData,
		Seed:      seedPath, NVRAM: nvramPath, SSHPort: sshPort, Forwards: qemuForwards(resolved, sshPort),
		Runtime: state.RuntimePaths{Directory: runtimeDir, QMP: qmpPath, PIDFile: pidfile}, Invocation: invocation,
		CreatedAt: now, UpdatedAt: now,
	}
	completed = append(completed, state.Action{Name: "invocation", Resource: qmpPath})
	if err := transaction(store, m.FarrowVersion, projectValue.Marker.ProjectID, operationID, state.Absent, state.Prepared, completed, started); err != nil {
		return state.NodeState{}, err
	}
	if err := store.WriteNode(node); err != nil {
		return state.NodeState{}, err
	}
	if err := removeOwnedRegular(nodeDir, filepath.Join(nodeDir, "transaction.json")); err != nil {
		return state.NodeState{}, fmt.Errorf("finalize prepare transaction after stable node state: %w", err)
	}
	m.Progress.Report(activity.Event{Phase: "prepare", Message: "VM state for meta is prepared", Done: true})
	return node, nil
}

func validateNodePaths(projectValue project.Project, node state.NodeState) error {
	nodeDir, err := projectValue.NodeDir(node.Node)
	if err != nil {
		return err
	}
	runtimeValid := runtimepath.Validate(node.Runtime.Directory, projectValue.Marker.ProjectID, node.Node, os.Getuid()) == nil
	if !runtimeValid {
		runtimeValid = node.Runtime.Directory == legacyRuntimeDirectory(projectValue.Marker.ProjectID)
	}
	if !runtimeValid || node.Runtime.QMP != filepath.Join(node.Runtime.Directory, "qmp.sock") || node.Runtime.PIDFile != filepath.Join(node.Runtime.Directory, "qemu.pid") {
		return errors.New("node runtime paths do not match project identity")
	}
	expectedFiles := map[string]string{
		node.RootDisk: "root.qcow2",
		node.Seed:     "seed.iso",
	}
	if node.NVRAM != "" {
		expectedFiles[node.NVRAM] = "nvram.fd"
	}
	if len(node.DataDisks) > 1 {
		return errors.New("quick node state contains more than one data disk")
	}
	if len(node.DataDisks) == 1 {
		dataDisk := node.DataDisks[0]
		if dataDisk.Persistent && dataDisk.Path != filepath.Join(nodeDir, "data.qcow2") {
			identityValue := persistent.Identity{ProjectID: projectValue.Marker.ProjectID, Node: node.Node, Name: dataDisk.Name, Serial: dataDisk.Serial, Size: dataDisk.Size, Mount: dataDisk.Mount}
			record, found, err := persistent.Find(projectValue, []persistent.Identity{identityValue}, identityValue)
			if err != nil {
				return fmt.Errorf("persistent node disk ownership is incompatible: %w", err)
			}
			if !found || dataDisk.Path != record.Path {
				return errors.New("persistent node disk ownership is missing or points elsewhere")
			}
			info, err := os.Lstat(dataDisk.Path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("node artifact is missing or unsafe: %s", dataDisk.Path)
			}
		} else {
			if dataDisk.Persistent {
				if err := persistent.ValidateSource(projectValue, dataDisk.Path); err != nil {
					return err
				}
			}
			expectedFiles[dataDisk.Path] = "data.qcow2"
		}
	}
	for actual, basename := range expectedFiles {
		if actual != filepath.Join(nodeDir, basename) {
			return fmt.Errorf("node artifact path mismatch for %s: %s", basename, actual)
		}
		inside, err := fsutil.IsWithin(nodeDir, actual)
		if err != nil || !inside {
			return fmt.Errorf("node artifact escapes node directory: %s", actual)
		}
		info, err := os.Lstat(actual)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("node artifact is missing or unsafe: %s", actual)
		}
	}
	return nil
}

func readConsistent(store state.Store, name string) (state.ProjectState, state.NodeState, error) {
	projectState, err := store.ReadProject()
	if err != nil {
		return state.ProjectState{}, state.NodeState{}, err
	}
	node, err := store.ReadNode(name)
	if err != nil {
		return state.ProjectState{}, state.NodeState{}, err
	}
	if node.SpecHash != projectState.SpecHash || node.ProjectID != projectState.ProjectID {
		return state.ProjectState{}, state.NodeState{}, errors.New("project and node state identities or spec hashes differ")
	}
	if err := validateNodePaths(store.Project, node); err != nil {
		return state.ProjectState{}, state.NodeState{}, err
	}
	if err := validateInvocation(store.Project, projectState, node); err != nil {
		return state.ProjectState{}, state.NodeState{}, err
	}
	return projectState, node, nil
}

func ensureNoPendingTransaction(store state.Store, name string) error {
	transaction, err := store.ReadTransaction(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("transaction journal is unreadable; run repair: %w", err)
	}
	return fmt.Errorf("operation %s has a pending %s->%s transaction; run farrow repair --dry-run", transaction.OperationID, transaction.From, transaction.To)
}

func buildInvocation(projectValue project.Project, projectState state.ProjectState, node state.NodeState) (qemu.Invocation, error) {
	profile, err := platform.Native()
	if err != nil {
		return qemu.Invocation{}, err
	}
	qemuPath, err := platform.FindQEMUBinary(profile, exec.LookPath)
	if err != nil {
		return qemu.Invocation{}, err
	}
	rootSerial, err := identity.DiskSerial(projectValue.Marker.ProjectID, node.Node, "root")
	if err != nil {
		return qemu.Invocation{}, err
	}
	mgmtMAC, err := identity.MAC(projectValue.Marker.ProjectID, node.Node, "management")
	if err != nil {
		return qemu.Invocation{}, err
	}
	var firmwareConfig *qemu.Firmware
	if node.NVRAM != "" {
		firmware, err := platform.FindFirmwareForBoot(profile, "uefi")
		if err != nil {
			return qemu.Invocation{}, err
		}
		firmwareConfig = &qemu.Firmware{Code: firmware.Code, Vars: node.NVRAM}
	}
	var qemuData []qemu.Disk
	if len(node.DataDisks) == 1 {
		qemuData = []qemu.Disk{{Path: node.DataDisks[0].Path, Serial: node.DataDisks[0].Serial}}
	}
	return qemu.Build(qemu.Config{
		Profile: profile, Binary: qemuPath, Name: node.Node, UUID: node.VMUUID,
		CPUs: projectState.Resolved.Nodes[0].CPUs, Memory: projectState.Resolved.Nodes[0].Memory,
		Firmware: firmwareConfig, Root: qemu.Disk{Path: node.RootDisk, Serial: rootSerial},
		Data: qemuData, Seed: node.Seed,
		QMP: node.Runtime.QMP, PIDFile: node.Runtime.PIDFile,
		SerialLog: filepath.Join(filepath.Dir(node.RootDisk), "serial.log"),
		MgmtMAC:   mgmtMAC, Forwards: qemuForwards(projectState.Resolved, node.SSHPort), Shares: hostshare.QEMU(projectState.Resolved.Nodes[0].Shares), Detach: true,
	})

}

func validateInvocation(projectValue project.Project, projectState state.ProjectState, node state.NodeState) error {
	expected, err := buildInvocation(projectValue, projectState, node)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, node.Invocation) {
		return errors.New("persisted QEMU invocation does not match typed project/node state")
	}
	return nil
}

func (m Manager) start(ctx context.Context, projectValue project.Project, node state.NodeState) (state.NodeState, error) {
	m.report("qemu-launch", fmt.Sprintf("Launching QEMU for %s", node.Node))
	// Callers must establish QEMU version and any required device evidence
	// before entering this mutating launch path.
	if err := validateNodePaths(projectValue, node); err != nil {
		return state.NodeState{}, err
	}
	if process.Alive(node.Process.PID) {
		return state.NodeState{}, errors.New("refuse start while recorded QEMU PID is alive")
	}
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil {
		return state.NodeState{}, err
	}
	shareBundle, err := hostshare.Open(projectValue, projectState.Resolved.Nodes[0].Shares)
	if err != nil {
		return state.NodeState{}, err
	}
	defer shareBundle.Close()
	if err := shareBundle.ValidateInvocation(node.Invocation, 0); err != nil {
		return state.NodeState{}, err
	}
	if err := ensureRuntime(node.Runtime.Directory); err != nil {
		return state.NodeState{}, err
	}
	operationID, err := m.operationID()
	if err != nil {
		return state.NodeState{}, err
	}
	if err := m.recordQEMULog(ctx, projectValue, node, operationID, "launch", "info", execx.Display(node.Invocation.Binary, node.Invocation.Args...)); err != nil {
		return state.NodeState{}, fmt.Errorf("append QEMU launch log: %w", err)
	}
	for _, stale := range []string{node.Runtime.QMP, node.Runtime.PIDFile} {
		if err := os.Remove(stale); err != nil && !errors.Is(err, os.ErrNotExist) {
			return state.NodeState{}, err
		}
	}
	readyTimeout, err := m.readyTimeout(projectState.Resolved)
	if err != nil {
		return state.NodeState{}, err
	}
	node.Phase = state.Starting
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		return state.NodeState{}, err
	}
	life := lifecycle(m.runner())
	life.SSHUser = projectState.Resolved.SSHUser
	var identityValue process.Identity
	if files := shareBundle.Files(); len(files) != 0 {
		identityValue, err = life.StartWithExtraFiles(ctx, node.Invocation, node.Runtime.QMP, node.Runtime.PIDFile, node.Node, node.VMUUID, files)
	} else {
		identityValue, err = life.Start(ctx, node.Invocation, node.Runtime.QMP, node.Runtime.PIDFile, node.Node, node.VMUUID)
	}
	if err != nil {
		_ = m.recordQEMULog(ctx, projectValue, node, operationID, "launch", "error", err.Error())
		return state.NodeState{}, err
	}
	if err := shareBundle.Recheck(); err != nil {
		stopErr := life.Stop(ctx, node.Runtime.QMP, node.Node, node.VMUUID, identityValue, node.Invocation, 0)
		if stopErr == nil {
			_ = cleanupRuntime(node)
			node.Phase = state.Stopped
			node.Process = state.ProcessIdentity{}
			node.UpdatedAt = time.Now().UTC()
			_ = store.WriteNode(node)
		}
		return state.NodeState{}, fmt.Errorf("host share changed while QEMU launched: %w", errors.Join(err, stopErr))
	}
	node.Process = processToState(identityValue)
	node.Phase = state.Running
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		return state.NodeState{}, err
	}
	if err := m.recordQEMULog(ctx, projectValue, node, operationID, "qmp-ready", "info", fmt.Sprintf("QMP/process identity verified for PID %d", identityValue.PID)); err != nil {
		return node, fmt.Errorf("QEMU started but readiness log append failed: %w", err)
	}
	if m.NoWait {
		m.Progress.Report(activity.Event{Phase: "qemu-ready", Message: fmt.Sprintf("QEMU for %s is running with PID %d; guest readiness was skipped", node.Node, identityValue.PID), Done: true})
		if err := m.recordQEMULog(ctx, projectValue, node, operationID, "guest-ready-skipped", "info", "--no-wait requested; returning after QMP/process identity verification"); err != nil {
			return node, fmt.Errorf("QEMU started but no-wait log append failed: %w", err)
		}
		return node, nil
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return state.NodeState{}, err
	}
	m.report("guest-ready", fmt.Sprintf("QEMU is running with PID %d; waiting up to %s for %s SSH and ready marker on 127.0.0.1:%d", identityValue.PID, readyTimeout, node.Node, node.SSHPort))
	if err := life.WaitReady(ctx, sshPath, filepath.Join(projectValue.Root, "keys", "id_ed25519"), filepath.Join(projectValue.Root, "keys", "known_hosts"), node.SSHPort, vm.ReadyMarker{Project: projectValue.Marker.ProjectID, Node: node.Node, Generation: node.Generation, SpecHash: node.SpecHash}, readyTimeout); err != nil {
		_ = m.recordQEMULog(ctx, projectValue, node, operationID, "guest-ready", "error", err.Error())
		return node, err
	}
	if err := m.recordQEMULog(ctx, projectValue, node, operationID, "guest-ready", "info", fmt.Sprintf("generation %d ready marker matched spec %s", node.Generation, node.SpecHash)); err != nil {
		return node, fmt.Errorf("guest became ready but QEMU log append failed: %w", err)
	}
	m.Progress.Report(activity.Event{Phase: "guest-ready", Message: fmt.Sprintf("Guest %s is ready on SSH port %d", node.Node, node.SSHPort), Done: true})
	return node, nil
}

func statusFrom(projectValue project.Project, node state.NodeState, sshUser, message string) Status {
	sshUser = statusSSHUser(sshUser)
	if node.Phase == state.Absent {
		return Status{ProjectID: projectValue.Marker.ProjectID, Node: node.Node, State: state.Absent, SSHUser: sshUser, SpecHash: node.SpecHash, Message: message}
	}
	return Status{ProjectID: projectValue.Marker.ProjectID, Node: node.Node, State: node.Phase, SSHUser: sshUser, SSHHost: "127.0.0.1", SSHPort: node.SSHPort, Forwards: node.Forwards, Image: node.Image, SpecHash: node.SpecHash, Message: message}
}

func (m Manager) Up(ctx context.Context) (Status, error) {
	return m.UpWithOptions(ctx, Options{})
}

type UpPolicy struct {
	Restart bool
}

func chooseSSHPort(resolved spec.Resolved, reserved map[uint16]struct{}) (uint16, error) {
	for _, forward := range resolved.Nodes[0].Forwards {
		reserved[forward.Host] = struct{}{}
	}
	available := func(port uint16) bool {
		if _, exists := reserved[port]; exists {
			return false
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}
	return usernet.Choose(2222, available)
}

func (m Manager) UpWithOptions(ctx context.Context, options Options) (Status, error) {
	return m.UpWithOptionsPolicy(ctx, options, UpPolicy{})
}

func (m Manager) UpWithOptionsPolicy(ctx context.Context, options Options, policy UpPolicy) (Status, error) {
	requested, err := options.Resolve()
	if err != nil {
		return Status{}, err
	}
	return m.upDesired(ctx, requested, options.HasOverrides(), policy)
}

func (m Manager) UpResolved(ctx context.Context, requested spec.Resolved) (Status, error) {
	return m.UpResolvedWithPolicy(ctx, requested, UpPolicy{})
}

func (m Manager) UpResolvedWithPolicy(ctx context.Context, requested spec.Resolved, policy UpPolicy) (Status, error) {
	m.ConfiguredDataRoot = requested.DataRoot
	requested, err := m.validateQuickResolved(requested)
	if err != nil {
		return Status{}, err
	}
	return m.upDesired(ctx, requested, true, policy)
}

func (m Manager) validateQuickResolved(requested spec.Resolved) (spec.Resolved, error) {
	if _, err := requested.SSHWaitTimeout(); err != nil {
		return spec.Resolved{}, err
	}
	sshUser, err := resolvedSSHUser(requested.SSHUser)
	if err != nil {
		return spec.Resolved{}, err
	}
	requested.SSHUser = sshUser
	if requested.Schema == 1 && requested.Network == "private" && requested.Private != nil && len(requested.Nodes) >= 2 {
		leaseStatus, err := m.privateLeaseStore().Inspect()
		if err != nil {
			return spec.Resolved{}, &CapabilityError{Reason: "private lease root failed integrity inspection: " + err.Error()}
		}
		if !leaseStatus.Available {
			return spec.Resolved{}, &CapabilityError{Reason: "private network/lease root is not installed; run farrow network status"}
		}
		return spec.Resolved{}, &CapabilityError{Reason: "private multi-node runtime remains gated until native M0 network validation passes"}
	}
	if requested.Schema != 1 || requested.Network != "user" || requested.Private != nil || len(requested.Nodes) != 1 || requested.Nodes[0].Name != nodeName || len(requested.Nodes[0].Disks) > 1 {
		return spec.Resolved{}, errors.New("current product runtime supports declarative user mode with one meta node; private/multi-node is not silently downgraded")
	}
	return requested, nil
}

func (m Manager) upDesired(ctx context.Context, requested spec.Resolved, hasOverrides bool, policy UpPolicy) (Status, error) {
	return m.upDesiredWithPreflight(ctx, requested, hasOverrides, policy, qemuPreflightEvidence{})
}

func (m Manager) upDesiredWithPreflight(ctx context.Context, requested spec.Resolved, hasOverrides bool, policy UpPolicy, preflight qemuPreflightEvidence) (Status, error) {
	m.report("preflight", "Checking native virtualization and QEMU capabilities")
	profile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	needsShareCapability, err := m.upNeedsShareCapability(requested, hasOverrides)
	if err != nil {
		return Status{}, err
	}
	if preflight.Binary == "" {
		preflight, err = m.preflightNativeQEMU(ctx, profile, needsShareCapability)
		if err != nil {
			return Status{}, err
		}
	} else if err := validateQEMUPreflight(preflight, preflight.Binary, needsShareCapability); err != nil {
		return Status{}, err
	}
	qemuPath := preflight.Binary
	m.Progress.Report(activity.Event{Phase: "preflight", Message: fmt.Sprintf("Native QEMU preflight passed with %s", qemuPath), Done: true})
	m.report("project-state", "Inspecting the Quick project state")
	projectValue, err := m.openProject(true)
	if err != nil {
		return Status{}, err
	}
	requested.DataRoot = projectValue.DataRoot
	store := state.Store{Project: projectValue}
	preexistingProject, preexistingErr := store.ReadProject()
	resolvedForImage := requested
	if preexistingErr == nil {
		resolvedForImage = preexistingProject.Resolved
	} else if !errors.Is(preexistingErr, os.ErrNotExist) {
		var pathError *os.PathError
		if !errors.As(preexistingErr, &pathError) || !errors.Is(pathError.Err, os.ErrNotExist) {
			return Status{}, preexistingErr
		}
	}
	entry, basePath, metadata, err := m.ensureImage(ctx, profile, projectValue.DataRoot, resolvedForImage.Image)
	if err != nil {
		return Status{}, err
	}
	allocatorContext, cancelAllocator := context.WithTimeout(ctx, 30*time.Second)
	defer cancelAllocator()
	allocator, err := lock.Acquire(allocatorContext, filepath.Join(projectValue.DataRoot, "locks", "allocator.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer allocator.Release()
	projectContext, cancelProject := context.WithTimeout(ctx, 30*time.Second)
	defer cancelProject()
	projectLock, err := lock.Acquire(projectContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	projectState, node, readErr := readConsistent(store, nodeName)
	if readErr == nil {
		m.report("project-state", "Reconciling the existing meta VM")
		if err := ensureNoPendingTransaction(store, nodeName); err != nil {
			return Status{}, err
		}
		desired := projectState.Resolved
		var drift *DriftError
		if hasOverrides {
			desired = materializeExistingPorts(requested, projectState.Resolved)
			if err := compareDesired(projectState.Resolved, desired); err != nil {
				if !errors.As(err, &drift) {
					return Status{}, err
				}
			}
		}
		if err := validateQEMUPreflight(preflight, node.Invocation.Binary, resolvedHasShares(desired)); err != nil {
			return Status{}, err
		}
		life := lifecycle(m.runner())
		qmpRunning := life.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID) == nil
		if drift != nil {
			if drift.Action != "restart" && drift.Action != "stop" && drift.Action != "reconcile" {
				return Status{}, drift
			}
			if err := ensureChangedPortsAvailable(desired, projectState.Resolved, node.SSHPort); err != nil {
				return Status{}, err
			}
			if qmpRunning && !policy.Restart {
				return Status{}, drift
			}
			if err := hostshare.Validate(projectValue, desired.Nodes[0].Shares); err != nil {
				return Status{}, err
			}
			if !qmpRunning && process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
				return Status{}, errors.New("QEMU process matches state but QMP identity is unavailable; run repair after diagnosis")
			}
			node, _, err = m.stopNodeLocked(ctx, store, node)
			if err != nil {
				return Status{}, err
			}
			node, _, err = m.beginReconcile(ctx, store, projectState, node, desired, drift.Action)
			if err != nil {
				return Status{}, fmt.Errorf("apply %s drift (run repair --dry-run if a journal remains): %w", drift.Action, err)
			}
			node, err = m.start(ctx, projectValue, node)
			return statusFrom(projectValue, node, desired.SSHUser, "applied "+drift.Action+" drift and started quick VM"), err
		}
		if qmpRunning {
			m.Progress.Report(activity.Event{Phase: "project-state", Message: "The meta VM is already running", Done: true})
			node.Phase = state.Running
			node.UpdatedAt = time.Now().UTC()
			_ = store.WriteNode(node)
			return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "already running"), nil
		}
		if process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
			return Status{}, errors.New("QEMU process matches state but QMP identity is unavailable; run repair after diagnosis")
		}
		if err := hostshare.Validate(projectValue, projectState.Resolved.Nodes[0].Shares); err != nil {
			return Status{}, err
		}
		node.Phase = state.Stopped
		node.Process = state.ProcessIdentity{}
		node.UpdatedAt = time.Now().UTC()
		if err := store.WriteNode(node); err != nil {
			return Status{}, err
		}
		node, err = m.start(ctx, projectValue, node)
		return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "started existing quick VM"), err
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		var pathError *os.PathError
		if !errors.As(readErr, &pathError) || !errors.Is(pathError.Err, os.ErrNotExist) {
			return Status{}, readErr
		}
	}
	m.report("project-state", "Allocating ports and creating the meta VM")
	var resolved spec.Resolved
	reserved, err := portregistry.Reserved(projectValue.DataRoot)
	if err != nil {
		return Status{}, err
	}
	sshPort := uint16(0)
	existingProject, projectErr := store.ReadProject()
	if projectErr == nil {
		if hasOverrides {
			desired := materializeExistingPorts(requested, existingProject.Resolved)
			if err := compareDesired(existingProject.Resolved, desired); err != nil {
				return Status{}, err
			}
		}
		resolved = existingProject.Resolved
		sshPort, err = chooseSSHPort(resolved, reserved)
	} else if errors.Is(projectErr, os.ErrNotExist) {
		sshPort, resolved, err = choosePorts(requested, reserved)
	} else {
		return Status{}, projectErr
	}
	if err != nil {
		return Status{}, err
	}
	if err := validateQEMUPreflight(preflight, qemuPath, resolvedHasShares(resolved)); err != nil {
		return Status{}, err
	}
	if err := hostshare.Validate(projectValue, resolved.Nodes[0].Shares); err != nil {
		return Status{}, err
	}
	specHash, err := spec.Hash(resolved)
	if err != nil {
		return Status{}, err
	}
	projectState = state.ProjectState{Schema: state.ProjectSchema, FarrowVersion: m.FarrowVersion, ProjectID: projectValue.Marker.ProjectID, SpecHash: specHash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
	if err := store.WriteProject(projectState); err != nil {
		return Status{}, err
	}
	node, err = m.prepare(ctx, projectValue, resolved, specHash, sshPort, entry, basePath, metadata, profile, qemuPath)
	if err != nil {
		return Status{}, err
	}
	if err := clearAbsence(projectValue); err != nil {
		return Status{}, err
	}
	node, err = m.start(ctx, projectValue, node)
	return statusFrom(projectValue, node, resolved.SSHUser, "created and started quick VM"), err
}

func (m Manager) Status(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	projectState, node, err := readConsistent(store, nodeName)
	if err != nil {
		if missingPath(err) {
			projectState, projectErr := store.ReadProject()
			if projectErr == nil {
				if _, absenceErr := readAbsence(projectValue, projectState); absenceErr == nil {
					return absentStatus(projectValue, projectState, "quick node artifacts are absent"), nil
				}
			}
		}
		return Status{}, err
	}
	if err := ensureNoPendingTransaction(store, nodeName); err != nil {
		return Status{}, err
	}
	life := lifecycle(m.runner())
	if err := life.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID); err == nil {
		node.Phase = state.Running
		node.UpdatedAt = time.Now().UTC()
		if err := store.WriteNode(node); err != nil {
			return Status{}, err
		}
		return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "QMP identity verified"), nil
	}
	if process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
		return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "process matches but QMP is unavailable"), errors.New("runtime identity is degraded")
	}
	node.Phase = state.Stopped
	node.Process = state.ProcessIdentity{}
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		return Status{}, err
	}
	return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "not running"), nil
}

func (m Manager) stopNodeLocked(ctx context.Context, store state.Store, node state.NodeState) (state.NodeState, string, error) {
	life := lifecycle(m.runner())
	if err := life.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID); err != nil {
		if !process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
			if process.Alive(node.Process.PID) {
				return state.NodeState{}, "", errors.New("refuse stop: recorded PID is alive but process identity does not match")
			}
			node.Phase = state.Stopped
			node.Process = state.ProcessIdentity{}
			node.UpdatedAt = time.Now().UTC()
			if err := store.WriteNode(node); err != nil {
				return state.NodeState{}, "", err
			}
			return node, "already stopped", nil
		}
	}
	operationID, err := m.operationID()
	if err != nil {
		return state.NodeState{}, "", err
	}
	if err := m.recordQEMULog(ctx, store.Project, node, operationID, "powerdown", "info", "QMP system_powerdown requested"); err != nil {
		return state.NodeState{}, "", fmt.Errorf("append QEMU powerdown log: %w", err)
	}
	node.Phase = state.Stopping
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		return state.NodeState{}, "", err
	}
	if err := life.Stop(ctx, node.Runtime.QMP, node.Node, node.VMUUID, processFromState(node.Process), node.Invocation, vm.GracefulGuestShutdownTimeout); err != nil {
		return state.NodeState{}, "", err
	}
	if err := cleanupRuntime(node); err != nil {
		return state.NodeState{}, "", err
	}
	node.Phase = state.Stopped
	node.Process = state.ProcessIdentity{}
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		return state.NodeState{}, "", err
	}
	if err := m.recordQEMULog(ctx, store.Project, node, operationID, "stopped", "info", "QEMU process exited and runtime artifacts were removed"); err != nil {
		return node, "stopped", fmt.Errorf("QEMU stopped but final log append failed: %w", err)
	}
	return node, "stopped", nil
}

func (m Manager) Stop(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(projectContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	projectState, node, err := readConsistent(store, nodeName)
	if err != nil {
		if missingPath(err) {
			projectState, projectErr := store.ReadProject()
			if projectErr == nil {
				if _, absenceErr := readAbsence(projectValue, projectState); absenceErr == nil {
					return absentStatus(projectValue, projectState, "already destroyed"), nil
				}
			}
		}
		return Status{}, err
	}
	if err := ensureNoPendingTransaction(store, nodeName); err != nil {
		return Status{}, err
	}
	node, message, err := m.stopNodeLocked(ctx, store, node)
	return statusFrom(projectValue, node, projectState.Resolved.SSHUser, message), err
}

func (m Manager) startExisting(ctx context.Context, preflight qemuPreflightEvidence) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(projectContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	projectState, node, err := readConsistent(store, nodeName)
	if err != nil {
		if missingPath(err) {
			persisted, projectErr := store.ReadProject()
			if projectErr == nil {
				if _, absenceErr := readAbsence(projectValue, persisted); absenceErr == nil {
					return Status{}, errors.New("quick node is absent; run farrow up to create it")
				}
			}
		}
		return Status{}, err
	}
	if err := ensureNoPendingTransaction(store, nodeName); err != nil {
		return Status{}, err
	}
	if err := lifecycle(m.runner()).ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID); err == nil {
		return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "already running"), nil
	}
	if process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
		return Status{}, errors.New("refuse start: recorded process is alive without QMP identity")
	}
	shares := resolvedHasShares(projectState.Resolved)
	if preflight.Binary == "" {
		preflight, err = m.preflightQEMU(ctx, node.Invocation.Binary, shares)
		if err != nil {
			return Status{}, err
		}
	} else if err := validateQEMUPreflight(preflight, node.Invocation.Binary, shares); err != nil {
		return Status{}, err
	}
	node.Process = state.ProcessIdentity{}
	node.Phase = state.Stopped
	node, err = m.start(ctx, projectValue, node)
	return statusFrom(projectValue, node, projectState.Resolved.SSHUser, "started"), err
}

func (m Manager) Start(ctx context.Context) (Status, error) {
	return m.startExisting(ctx, qemuPreflightEvidence{})
}

func (m Manager) Restart(ctx context.Context) (Status, error) {
	preflight, err := m.preflightExistingQEMU(ctx)
	if err != nil {
		return Status{}, err
	}
	if _, err := m.Stop(ctx); err != nil {
		return Status{}, err
	}
	return m.startExisting(ctx, preflight)
}

func (m Manager) Recreate(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil {
		return Status{}, err
	}
	return m.RecreateResolved(ctx, projectState.Resolved)
}

// RecreateResolved validates the desired quick spec and plan before destroy,
// then recreates the node from that exact desired state.
func (m Manager) RecreateResolved(ctx context.Context, requested spec.Resolved) (Status, error) {
	m.ConfiguredDataRoot = requested.DataRoot
	requested, err := m.validateQuickResolved(requested)
	if err != nil {
		return Status{}, err
	}
	plan, err := m.planDesired(ctx, requested, true)
	if err != nil {
		return Status{}, err
	}
	if plan.Action == "create" {
		return Status{}, errors.New("quick node is absent; use farrow up instead of recreate")
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	if err := hostshare.Validate(projectValue, requested.Nodes[0].Shares); err != nil {
		return Status{}, err
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	needsShareCapability := resolvedHasShares(requested)
	if plan.Before != nil {
		needsShareCapability = needsShareCapability || resolvedHasShares(*plan.Before)
	}
	preflight, err := m.preflightNativeQEMU(ctx, profile, needsShareCapability)
	if err != nil {
		return Status{}, err
	}
	if _, err := m.Destroy(ctx); err != nil {
		return Status{}, err
	}
	return m.upDesiredWithPreflight(ctx, requested, true, UpPolicy{}, preflight)
}

func (m Manager) Connection(ctx context.Context) (Connection, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Connection{}, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Connection{}, err
	}
	defer projectLock.Release()
	projectValue, err = m.openProject(false)
	if err != nil {
		return Connection{}, err
	}
	return m.ConnectionLocked(ctx, projectValue, projectLock)
}

func (m Manager) connectionState(ctx context.Context) (Connection, state.NodeState, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Connection{}, state.NodeState{}, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Connection{}, state.NodeState{}, err
	}
	defer projectLock.Release()
	projectValue, err = m.openProject(false)
	if err != nil {
		return Connection{}, state.NodeState{}, err
	}
	if err := projectLock.ValidateExclusive(filepath.Join(projectValue.Root, "project.lock")); err != nil {
		return Connection{}, state.NodeState{}, fmt.Errorf("quick connection state requires the matching exclusive project lock: %w", err)
	}
	return m.connectionStateLocked(projectValue)
}

func (m Manager) connectionStateLocked(projectValue project.Project) (Connection, state.NodeState, error) {
	store := state.Store{Project: projectValue}
	projectState, node, err := readConsistent(store, nodeName)
	if err != nil {
		return Connection{}, state.NodeState{}, err
	}
	if err := ensureNoPendingTransaction(store, nodeName); err != nil {
		return Connection{}, state.NodeState{}, err
	}
	sshUser, err := resolvedSSHUser(projectState.Resolved.SSHUser)
	if err != nil {
		return Connection{}, state.NodeState{}, err
	}
	return Connection{
		User: sshUser, Host: "127.0.0.1", Port: node.SSHPort,
		PrivateKey: filepath.Join(projectValue.Root, "keys", "id_ed25519"),
		KnownHosts: filepath.Join(projectValue.Root, "keys", "known_hosts"),
	}, node, nil
}

// ConnectionLocked is the non-locking provisioning entrypoint. The caller
// must pass the live exclusive project.lock token and retain it for the full
// remote operation so the verified state, process, and trust paths cannot be
// invalidated by another Farrow lifecycle command.
func (m Manager) ConnectionLocked(ctx context.Context, projectValue project.Project, projectLock *lock.File) (Connection, error) {
	if err := projectLock.ValidateExclusive(filepath.Join(projectValue.Root, "project.lock")); err != nil {
		return Connection{}, fmt.Errorf("quick connection requires the matching exclusive project lock: %w", err)
	}
	refreshed, err := project.Open(projectValue.WorkDir)
	if err != nil {
		return Connection{}, fmt.Errorf("re-open quick project under its exclusive lock: %w", err)
	}
	if err := projectLock.ValidateExclusive(filepath.Join(refreshed.Root, "project.lock")); err != nil {
		return Connection{}, fmt.Errorf("quick project marker changed while locked: %w", err)
	}
	projectValue = refreshed
	connection, node, err := m.connectionStateLocked(projectValue)
	if err != nil {
		return Connection{}, err
	}
	if node.Phase != state.Running {
		return Connection{}, fmt.Errorf("quick node %s is not running", node.Node)
	}
	privateKey, knownHosts, err := project.ValidateSSHArtifacts(projectValue)
	if err != nil {
		return Connection{}, err
	}
	if err := lifecycle(m.runner()).ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID); err != nil {
		return Connection{}, fmt.Errorf("node is not QMP-verified running: %w", err)
	}
	if !process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
		return Connection{}, errors.New("quick node recorded process identity does not match")
	}
	connection.PrivateKey = privateKey
	connection.KnownHosts = knownHosts
	return connection, nil
}

func (m Manager) SSHConfig(ctx context.Context) (string, error) {
	connection, _, err := m.connectionState(ctx)
	if err != nil {
		return "", err
	}
	identity, err := openssh.QuoteConfigValue(connection.PrivateKey)
	if err != nil {
		return "", err
	}
	knownHosts, err := openssh.QuoteConfigValue(connection.KnownHosts)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Host meta\n  HostName %s\n  User %s\n  Port %d\n  IdentityFile %s\n  UserKnownHostsFile %s\n  IdentitiesOnly yes\n  StrictHostKeyChecking yes\n", connection.Host, connection.User, connection.Port, identity, knownHosts), nil
}

func sshHome(home string) (string, error) {
	if home != "" {
		return filepath.Abs(home)
	}
	return os.UserHomeDir()
}

func (m Manager) InstallSSHConfig(ctx context.Context, name, home string) (sshconfig.Result, error) {
	home, err := sshHome(home)
	if err != nil {
		return sshconfig.Result{}, err
	}
	connection, node, err := m.connectionState(ctx)
	if err != nil {
		return sshconfig.Result{}, err
	}
	return sshconfig.Install(home, sshconfig.Entry{
		ProjectID: node.ProjectID, Name: name, Node: node.Node, User: connection.User,
		Host: connection.Host, Port: connection.Port, Identity: connection.PrivateKey, KnownHosts: connection.KnownHosts,
	})
}

func (m Manager) RemoveSSHConfig(name, home string) (sshconfig.Result, error) {
	home, err := sshHome(home)
	if err != nil {
		return sshconfig.Result{}, err
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return sshconfig.Result{}, err
	}
	return sshconfig.Remove(home, projectValue.Marker.ProjectID, name)
}

func (m Manager) LogPath(ctx context.Context, source string) (string, error) {
	filename := ""
	switch source {
	case "serial":
		filename = "serial.log"
	case "qemu":
		filename = "qemu.log"
	case "events":
		filename = "events.jsonl"
	default:
		return "", fmt.Errorf("unsupported log source %q", source)
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return "", err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), true)
	if err != nil {
		return "", err
	}
	defer projectLock.Release()
	_, node, err := readConsistent(state.Store{Project: projectValue}, nodeName)
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(node.RootDisk), filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s log is missing or unsafe: %s", source, path)
	}
	return path, nil
}

func removeOwnedRegular(root, target string) error {
	inside, err := fsutil.IsWithin(root, target)
	if err != nil || !inside {
		return fmt.Errorf("refuse removal outside node root: %s", target)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refuse removal of unexpected file type: %s", target)
	}
	return os.Remove(target)
}

func cleanupRuntime(node state.NodeState) error {
	for _, target := range []string{node.Runtime.QMP, node.Runtime.PIDFile} {
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse unexpected runtime artifact: %s", target)
		}
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	if err := os.Remove(node.Runtime.Directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("runtime directory is not empty or removable: %w", err)
	}
	return runtimepath.PruneEmptyParents(node.Runtime.Directory, os.Getuid())
}

func (m Manager) Destroy(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(projectContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	projectState, node, err := readConsistent(store, nodeName)
	if err != nil {
		if missingPath(err) {
			persisted, projectErr := store.ReadProject()
			if projectErr == nil {
				if _, absenceErr := readAbsence(projectValue, persisted); absenceErr == nil {
					return absentStatus(projectValue, persisted, "already destroyed"), nil
				}
			}
		}
		return Status{}, err
	}
	if err := ensureNoPendingTransaction(store, nodeName); err != nil {
		return Status{}, err
	}
	persistentIdentities, err := validateQuickPersistentState(projectValue, projectState.Resolved, node)
	if err != nil {
		return Status{}, err
	}
	if err := validateQuickDestroyDirectory(projectValue, node); err != nil {
		return Status{}, err
	}
	life := lifecycle(m.runner())
	qmpErr := life.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID)
	matchingLive := process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation)
	if qmpErr == nil || matchingLive {
		if err := life.Stop(ctx, node.Runtime.QMP, node.Node, node.VMUUID, processFromState(node.Process), node.Invocation, vm.GracefulGuestShutdownTimeout); err != nil {
			return Status{}, err
		}
	} else if process.Alive(node.Process.PID) {
		return Status{}, errors.New("refuse destroy: recorded PID is alive but process identity does not match")
	}
	if process.Alive(node.Process.PID) {
		return Status{}, errors.New("refuse destroy while recorded QEMU PID remains alive")
	}
	node.Process = state.ProcessIdentity{}
	node.Phase = state.Destroying
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		return Status{}, err
	}
	if err := writeAbsence(projectValue, projectState); err != nil {
		return Status{}, err
	}
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return Status{}, err
	}
	knownHosts := filepath.Join(projectValue.Root, "keys", "known_hosts")
	if _, err := m.runner().Run(ctx, sshKeygen, "-f", knownHosts, "-R", fmt.Sprintf("[127.0.0.1]:%d", node.SSHPort)); err != nil {
		return Status{}, err
	}
	knownHostsBackup := knownHosts + ".old"
	if info, err := os.Lstat(knownHostsBackup); err == nil {
		if !info.Mode().IsRegular() {
			return Status{}, errors.New("ssh-keygen created an unexpected known_hosts backup type")
		}
		if err := os.Remove(knownHostsBackup); err != nil {
			return Status{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	nodeDir, _ := projectValue.NodeDir(node.Node)
	targets := []string{node.RootDisk, node.Seed, node.NVRAM, filepath.Join(nodeDir, "serial.log"), filepath.Join(nodeDir, "qemu.log"), filepath.Join(nodeDir, "events.jsonl")}
	for _, dataDisk := range node.DataDisks {
		if !dataDisk.Persistent {
			targets = append(targets, dataDisk.Path)
		}
	}
	for _, identityValue := range persistentIdentities {
		var source string
		for _, dataDisk := range node.DataDisks {
			if dataDisk.Name == identityValue.Name {
				source = dataDisk.Path
				break
			}
		}
		if source == "" {
			return Status{}, fmt.Errorf("persistent disk %s has no node state path", identityValue.Name)
		}
		if _, err := persistent.Preserve(projectValue, identityValue, source); err != nil {
			return Status{}, err
		}
	}
	for _, target := range targets {
		if target == "" {
			continue
		}
		if err := removeOwnedRegular(nodeDir, target); err != nil {
			return Status{}, err
		}
	}
	if err := cleanupRuntime(node); err != nil {
		return Status{}, err
	}
	for _, metadataPath := range []string{filepath.Join(nodeDir, "transaction.json"), filepath.Join(nodeDir, "state.json")} {
		if err := removeOwnedRegular(nodeDir, metadataPath); err != nil {
			return Status{}, err
		}
	}
	if err := os.Remove(nodeDir); err != nil {
		return Status{}, fmt.Errorf("node directory contains unexpected artifacts: %w", err)
	}
	node.Phase = state.Absent
	message := "destroyed node artifacts; image cache, project marker, keys, and persistent data disks preserved"
	return statusFrom(projectValue, node, projectState.Resolved.SSHUser, message), nil
}
