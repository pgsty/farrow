// Package lease owns the host-global single-active-project private-network
// reservation. The privileged network installer creates the fixed sticky
// runtime root; unprivileged Farrow processes coordinate beneath it.
package lease

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"golang.org/x/sys/unix"
)

const Schema = 1
const maxLeaseBytes = 1 << 20

const (
	Reserved NodePhase = "reserved"
	Prepared NodePhase = "prepared"
	Starting NodePhase = "starting"
	Running  NodePhase = "running"
	Stopping NodePhase = "stopping"
	Stopped  NodePhase = "stopped"
)

var nodePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type NodePhase string

type RuntimePaths struct {
	Directory string `json:"directory,omitempty"`
	QMP       string `json:"qmp,omitempty"`
	PIDFile   string `json:"pidfile,omitempty"`
}

type Node struct {
	Name          string           `json:"name"`
	Address       string           `json:"address"`
	ManagementMAC string           `json:"management_mac"`
	PrivateMAC    string           `json:"private_mac"`
	VMUUID        string           `json:"vm_uuid"`
	Phase         NodePhase        `json:"phase"`
	Runtime       RuntimePaths     `json:"runtime"`
	Invocation    qemu.Invocation  `json:"invocation"`
	Process       process.Identity `json:"process"`
}

type Lease struct {
	Schema      int       `json:"schema"`
	Generation  uint64    `json:"generation"`
	ProjectID   string    `json:"project_id"`
	OwnerUID    int       `json:"owner_uid"`
	CIDR        string    `json:"cidr"`
	HostAddress string    `json:"host_address"`
	DHCPEnd     string    `json:"dhcp_end"`
	Nodes       []Node    `json:"nodes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Observation struct {
	Node      string `json:"node"`
	Live      bool   `json:"live"`
	Authority string `json:"authority"`
	Evidence  string `json:"evidence"`
}

type RuntimeAuditor func(context.Context, Node) (Observation, error)

type Result struct {
	Action       string        `json:"action"`
	Apply        bool          `json:"apply"`
	Lease        Lease         `json:"lease"`
	Previous     *Lease        `json:"previous,omitempty"`
	Observations []Observation `json:"observations,omitempty"`
}

type Status struct {
	Root      string `json:"root"`
	Available bool   `json:"available"`
	Active    bool   `json:"active"`
	Lease     *Lease `json:"lease,omitempty"`
}

type ConflictError struct {
	Reason   string
	Existing Lease
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("private lease conflict: %s (project=%s owner_uid=%d)", e.Reason, e.Existing.ProjectID, e.Existing.OwnerUID)
}

type Store struct {
	Root            string
	OwnerUID        int
	ExpectedRootUID int
	StaleAfter      time.Duration
	Now             func() time.Time
}

func DefaultRoot() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "/private/var/run/farrow", nil
	case "linux":
		return "/run/farrow", nil
	default:
		return "", fmt.Errorf("private lease has no root for %s", runtime.GOOS)
	}
}

func (s Store) root() (string, error) {
	root := s.Root
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return "", errors.New("private lease root must be a non-root absolute path")
	}
	return filepath.Clean(root), nil
}

func (s Store) ownerUID() int {
	if s.OwnerUID <= 0 {
		return os.Getuid()
	}
	return s.OwnerUID
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Store) staleAfter() time.Duration {
	if s.StaleAfter < 0 {
		return 0
	}
	if s.StaleAfter == 0 {
		return 5 * time.Minute
	}
	return s.StaleAfter
}

func (s Store) paths() (string, string, error) {
	root, err := s.root()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(root, "private-lease.json"), filepath.Join(root, "private-lease.lock"), nil
}

func statUID(info os.FileInfo) (int, bool) {
	statistics, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(statistics.Uid), true
}

func (s Store) validateRoot() (string, error) {
	root, err := s.root()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	uid, ok := statUID(info)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o777 || info.Mode()&os.ModeSticky == 0 || !ok || uid != s.ExpectedRootUID {
		return "", fmt.Errorf("private lease root must be a real owner-%d mode-1777 directory: %s", s.ExpectedRootUID, root)
	}
	return root, nil
}

func validPhase(phase NodePhase) bool {
	switch phase {
	case Reserved, Prepared, Starting, Running, Stopping, Stopped:
		return true
	default:
		return false
	}
}

func completeProcess(identity process.Identity) bool {
	return identity.PID > 0 && identity.Executable != "" && identity.Started != "" && identity.ArgvHash != ""
}

func zeroProcess(identity process.Identity) bool {
	return identity == (process.Identity{})
}

func validMAC(value string) bool {
	mac, err := net.ParseMAC(value)
	return err == nil && len(mac) == 6 && mac[0]&1 == 0 && mac[0]&2 != 0
}

func validateNode(node Node) error {
	if !nodePattern.MatchString(node.Name) || !project.ValidUUID(node.VMUUID) || !validPhase(node.Phase) || !validMAC(node.ManagementMAC) || !validMAC(node.PrivateMAC) || strings.EqualFold(node.ManagementMAC, node.PrivateMAC) {
		return fmt.Errorf("node %q has invalid identity, phase, UUID, or MAC", node.Name)
	}
	ip := net.ParseIP(node.Address).To4()
	if ip == nil || ip.String() != node.Address {
		return fmt.Errorf("node %s private address must be canonical IPv4", node.Name)
	}
	hasRuntime := node.Runtime.Directory != "" || node.Runtime.QMP != "" || node.Runtime.PIDFile != "" || node.Invocation.Binary != "" || len(node.Invocation.Args) > 0
	if hasRuntime {
		if !filepath.IsAbs(node.Runtime.Directory) || node.Runtime.QMP != filepath.Join(node.Runtime.Directory, "qmp.sock") || node.Runtime.PIDFile != filepath.Join(node.Runtime.Directory, "qemu.pid") || !filepath.IsAbs(node.Invocation.Binary) || len(node.Invocation.Args) == 0 {
			return fmt.Errorf("node %s runtime paths or invocation are incomplete", node.Name)
		}
		maxQMPPath := 107
		if runtime.GOOS == "darwin" {
			maxQMPPath = 103
		}
		if len(node.Runtime.QMP) > maxQMPPath {
			return fmt.Errorf("node %s QMP path is too long", node.Name)
		}
		total := 0
		for _, argument := range node.Invocation.Args {
			if strings.ContainsRune(argument, '\x00') {
				return fmt.Errorf("node %s invocation contains NUL", node.Name)
			}
			total += len(argument)
		}
		if len(node.Invocation.Args) > 512 || total > 256<<10 {
			return fmt.Errorf("node %s invocation exceeds bounds", node.Name)
		}
	} else if node.Phase != Reserved {
		return fmt.Errorf("node %s phase %s requires runtime intent", node.Name, node.Phase)
	}
	if !zeroProcess(node.Process) && !completeProcess(node.Process) {
		return fmt.Errorf("node %s process identity is partial", node.Name)
	}
	if node.Phase == Running && !completeProcess(node.Process) {
		return fmt.Errorf("running node %s requires complete process identity", node.Name)
	}
	if strings.ContainsAny(node.Process.Executable+node.Process.Started+node.Process.ArgvHash, "\x00\r\n") || len(node.Process.Executable)+len(node.Process.Started)+len(node.Process.ArgvHash) > 16<<10 {
		return fmt.Errorf("node %s process identity text is unsafe", node.Name)
	}
	return nil
}

func Validate(value Lease) error {
	if value.Schema != Schema || value.Generation == 0 || !project.ValidUUID(value.ProjectID) || value.OwnerUID < 0 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) || len(value.Nodes) == 0 || len(value.Nodes) > 20 {
		return errors.New("private lease schema, identity, network, time, or node count is invalid")
	}
	layout, layoutErr := subnet.Parse(value.CIDR)
	if layoutErr != nil || value.HostAddress != layout.HostAddress() || value.DHCPEnd != layout.DHCPEnd() {
		return errors.New("private lease network must be a canonical RFC1918 /24 with host .1 and DHCP end .8")
	}
	_, networkValue, err := net.ParseCIDR(value.CIDR)
	if err != nil {
		return errors.New("private lease CIDR is invalid")
	}
	ones, bits := networkValue.Mask.Size()
	networkIP := networkValue.IP.To4()
	hostIP := net.ParseIP(value.HostAddress).To4()
	dhcpEndIP := net.ParseIP(value.DHCPEnd).To4()
	if bits != 32 || ones != 24 || networkIP == nil || networkValue.String() != value.CIDR || hostIP == nil || dhcpEndIP == nil || hostIP.String() != value.HostAddress || dhcpEndIP.String() != value.DHCPEnd || !networkValue.Contains(hostIP) || !networkValue.Contains(dhcpEndIP) {
		return errors.New("private lease requires canonical IPv4 /24 network, host, and DHCP end")
	}
	networkNumber := binary.BigEndian.Uint32(networkIP)
	hostNumber := binary.BigEndian.Uint32(hostIP)
	dhcpEndNumber := binary.BigEndian.Uint32(dhcpEndIP)
	broadcastNumber := networkNumber | 0xff
	if hostNumber == networkNumber || hostNumber == broadcastNumber || dhcpEndNumber <= networkNumber+1 || dhcpEndNumber >= broadcastNumber || dhcpEndNumber == hostNumber {
		return errors.New("private lease host/DHCP addresses overlap reserved network positions")
	}
	names := make(map[string]struct{})
	addresses := make(map[string]struct{})
	macs := make(map[string]struct{})
	uuids := make(map[string]struct{})
	for _, node := range value.Nodes {
		if err := validateNode(node); err != nil {
			return err
		}
		nodeIP := net.ParseIP(node.Address).To4()
		nodeNumber := binary.BigEndian.Uint32(nodeIP)
		if !networkValue.Contains(nodeIP) || nodeNumber <= dhcpEndNumber || nodeNumber == hostNumber || nodeNumber >= broadcastNumber {
			return fmt.Errorf("node %s private address is outside the static pool after DHCP end", node.Name)
		}
		for label, key := range map[string]string{"name": node.Name, "address": node.Address, "management MAC": strings.ToLower(node.ManagementMAC), "private MAC": strings.ToLower(node.PrivateMAC), "VM UUID": strings.ToLower(node.VMUUID)} {
			target := names
			switch label {
			case "address":
				target = addresses
			case "management MAC", "private MAC":
				target = macs
			case "VM UUID":
				target = uuids
			}
			if _, duplicate := target[key]; duplicate {
				return fmt.Errorf("duplicate private lease %s %q", label, key)
			}
			target[key] = struct{}{}
		}
	}
	return nil
}

func canonicalNodes(nodes []Node) []Node {
	result := append([]Node(nil), nodes...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func reservationEqual(left, right Lease) bool {
	if left.ProjectID != right.ProjectID || left.OwnerUID != right.OwnerUID || left.CIDR != right.CIDR || left.HostAddress != right.HostAddress || left.DHCPEnd != right.DHCPEnd || len(left.Nodes) != len(right.Nodes) {
		return false
	}
	leftNodes, rightNodes := canonicalNodes(left.Nodes), canonicalNodes(right.Nodes)
	for index := range leftNodes {
		leftNode, rightNode := leftNodes[index], rightNodes[index]
		if leftNode.Name != rightNode.Name || leftNode.Address != rightNode.Address || !strings.EqualFold(leftNode.ManagementMAC, rightNode.ManagementMAC) || !strings.EqualFold(leftNode.PrivateMAC, rightNode.PrivateMAC) || !strings.EqualFold(leftNode.VMUUID, rightNode.VMUUID) {
			return false
		}
	}
	return true
}

func strictDecode(data []byte) (Lease, error) {
	if len(data) == 0 || len(data) > maxLeaseBytes {
		return Lease{}, errors.New("private lease size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value Lease
	if err := decoder.Decode(&value); err != nil {
		return Lease{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Lease{}, errors.New("private lease has trailing JSON data")
	}
	if err := Validate(value); err != nil {
		return Lease{}, err
	}
	return value, nil
}

func (s Store) Read() (Lease, error) {
	if _, err := s.validateRoot(); err != nil {
		return Lease{}, err
	}
	leasePath, _, _ := s.paths()
	info, err := os.Lstat(leasePath)
	if err != nil {
		return Lease{}, err
	}
	uid, ok := statUID(info)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o644 || info.Size() > maxLeaseBytes || !ok {
		return Lease{}, errors.New("private lease file is unsafe")
	}
	data, err := os.ReadFile(leasePath)
	if err != nil {
		return Lease{}, err
	}
	value, err := strictDecode(data)
	if err != nil {
		return Lease{}, err
	}
	if uid != value.OwnerUID {
		return Lease{}, errors.New("private lease file owner does not match owner_uid")
	}
	return value, nil
}

func (s Store) Inspect() (Status, error) {
	root, err := s.root()
	if err != nil {
		return Status{}, err
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return Status{Root: root}, nil
	} else if err != nil {
		return Status{}, err
	}
	if _, err := s.validateRoot(); err != nil {
		return Status{}, err
	}
	value, err := s.Read()
	if errors.Is(err, os.ErrNotExist) {
		return Status{Root: root, Available: true}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return Status{Root: root, Available: true, Active: true, Lease: &value}, nil
}

type fileLock struct{ file *os.File }

// Guard exposes the host-global lease flock as a scoped closure for ordered
// network uninstall. Callers must not invoke another Store method that tries
// to acquire the same flock while the guard is held.
func (s Store) Guard(ctx context.Context) (func() error, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	return lock.release, nil
}

func (s Store) acquireLock(ctx context.Context) (*fileLock, error) {
	if _, err := s.validateRoot(); err != nil {
		return nil, err
	}
	_, lockPath, _ := s.paths()
	lockExisted := false
	for {
		info, err := os.Lstat(lockPath)
		if err == nil {
			uid, hasUID := statUID(info)
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !hasUID || (uid != 0 && uid != os.Getuid()) {
				return nil, errors.New("private lease lock is unsafe")
			}
			if info.Mode().Perm() != 0o666 {
				// A same-process-UID contender may observe the short interval
				// between O_EXCL creation (umask applied) and owner chmod. Never
				// wait for a root/foreign or structurally unsafe inode.
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(10 * time.Millisecond):
					continue
				}
			}
			lockExisted = true
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		break
	}
	descriptor, err := unix.Open(lockPath, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o666)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), lockPath)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("create private lease lock handle")
	}
	if !lockExisted {
		if err := file.Chmod(0o666); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	openedUID, hasOpenedUID := statUID(openedInfo)
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o666 || !hasOpenedUID || (openedUID != 0 && openedUID != os.Getuid()) {
		_ = file.Close()
		return nil, errors.New("opened private lease lock identity or mode mismatch")
	}
	for {
		if err := unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return &fileLock{file: file}, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (lock *fileLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	descriptor := int(lock.file.Fd())
	unlocked := unix.Flock(descriptor, unix.LOCK_UN)
	closed := lock.file.Close()
	lock.file = nil
	if unlocked != nil {
		return unlocked
	}
	return closed
}

func (s Store) write(value Lease) error {
	if err := Validate(value); err != nil {
		return err
	}
	if value.OwnerUID != s.ownerUID() {
		return errors.New("cannot write a private lease for another owner UID")
	}
	leasePath, _, err := s.paths()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.AtomicWrite(leasePath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	info, err := os.Lstat(leasePath)
	if err != nil {
		return err
	}
	uid, ok := statUID(info)
	if !ok || uid != value.OwnerUID || info.Mode().Perm() != 0o644 {
		return errors.New("published private lease owner or mode mismatch")
	}
	return nil
}

func normalizeDesired(value Lease, ownerUID int, now time.Time) (Lease, error) {
	value.Schema = Schema
	value.OwnerUID = ownerUID
	value.Generation = 1
	value.CreatedAt = now
	value.UpdatedAt = now
	value.Nodes = canonicalNodes(value.Nodes)
	if err := Validate(value); err != nil {
		return Lease{}, err
	}
	return value, nil
}

func (s Store) Acquire(ctx context.Context, desired Lease) (Result, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer lock.release()
	normalized, err := normalizeDesired(desired, s.ownerUID(), s.now())
	if err != nil {
		return Result{}, err
	}
	existing, readErr := s.Read()
	if errors.Is(readErr, os.ErrNotExist) {
		if err := s.write(normalized); err != nil {
			return Result{}, err
		}
		return Result{Action: "acquired", Apply: true, Lease: normalized}, nil
	}
	if readErr != nil {
		return Result{}, readErr
	}
	if existing.ProjectID != normalized.ProjectID || existing.OwnerUID != normalized.OwnerUID {
		return Result{}, &ConflictError{Reason: "another project or owner holds the host-global private network", Existing: existing}
	}
	if !reservationEqual(existing, normalized) {
		return Result{}, &ConflictError{Reason: "same project requested different private IP/MAC/UUID reservations", Existing: existing}
	}
	return Result{Action: "reentered", Apply: false, Lease: existing}, nil
}

func (s Store) Update(ctx context.Context, desired Lease) (Result, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer lock.release()
	existing, err := s.Read()
	if err != nil {
		return Result{}, err
	}
	desired.OwnerUID = existing.OwnerUID
	if existing.ProjectID != desired.ProjectID || existing.OwnerUID != s.ownerUID() || !reservationEqual(existing, desired) {
		return Result{}, &ConflictError{Reason: "update does not match the owned reservation", Existing: existing}
	}
	desired.Schema = Schema
	desired.OwnerUID = existing.OwnerUID
	desired.Generation = existing.Generation + 1
	desired.CreatedAt = existing.CreatedAt
	desired.UpdatedAt = s.now()
	desired.Nodes = canonicalNodes(desired.Nodes)
	if err := s.write(desired); err != nil {
		return Result{}, err
	}
	previous := existing
	return Result{Action: "updated", Apply: true, Lease: desired, Previous: &previous}, nil
}

func auditAll(ctx context.Context, value Lease, auditor RuntimeAuditor) ([]Observation, bool, error) {
	if auditor == nil {
		return nil, false, errors.New("private lease runtime auditor is required")
	}
	observations := make([]Observation, 0, len(value.Nodes))
	allDead := true
	for _, node := range value.Nodes {
		observation, err := auditor(ctx, node)
		if err != nil {
			return observations, false, err
		}
		validAuthority := observation.Authority == "qmp" || observation.Authority == "process" || observation.Authority == "dead"
		if observation.Node != node.Name || !validAuthority || observation.Evidence == "" || (observation.Live && observation.Authority == "dead") {
			return observations, false, errors.New("runtime auditor returned incomplete identity evidence")
		}
		if observation.Live {
			allDead = false
		}
		observations = append(observations, observation)
	}
	return observations, allDead, nil
}

func (s Store) Release(ctx context.Context, projectID string, apply bool, auditor RuntimeAuditor) (Result, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer lock.release()
	existing, err := s.Read()
	if errors.Is(err, os.ErrNotExist) {
		return Result{Action: "absent", Apply: false}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if existing.ProjectID != projectID || existing.OwnerUID != s.ownerUID() {
		return Result{}, &ConflictError{Reason: "release requester does not own the active lease", Existing: existing}
	}
	for _, node := range existing.Nodes {
		if node.Phase != Stopped {
			return Result{}, &ConflictError{Reason: "ordinary release requires every leased node phase to be stopped; use stale reclaim after crash audit", Existing: existing}
		}
	}
	observations, allDead, err := auditAll(ctx, existing, auditor)
	if err != nil {
		return Result{}, err
	}
	if !allDead {
		return Result{}, &ConflictError{Reason: "one or more leased nodes remain live", Existing: existing}
	}
	result := Result{Action: "release", Apply: apply, Lease: existing, Observations: observations}
	if !apply {
		return result, nil
	}
	leasePath, _, _ := s.paths()
	if err := os.Remove(leasePath); err != nil {
		return Result{}, err
	}
	root, _ := s.root()
	if err := fsutil.SyncDir(root); err != nil {
		return Result{}, err
	}
	return result, nil
}

// Abort removes a same-owner reservation that never entered a start phase.
// It is intentionally separate from ordinary stopped-node Release so callers
// cannot relabel a starting/running lease merely to bypass the runtime audit.
func (s Store) Abort(ctx context.Context, projectID string, apply bool, auditor RuntimeAuditor) (Result, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer lock.release()
	existing, err := s.Read()
	if errors.Is(err, os.ErrNotExist) {
		return Result{Action: "absent", Apply: false}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if existing.ProjectID != projectID || existing.OwnerUID != s.ownerUID() {
		return Result{}, &ConflictError{Reason: "abort requester does not own the active lease", Existing: existing}
	}
	for _, node := range existing.Nodes {
		if node.Phase != Reserved && node.Phase != Prepared {
			return Result{}, &ConflictError{Reason: "pre-start abort requires every leased node phase to be reserved or prepared", Existing: existing}
		}
	}
	observations, allDead, err := auditAll(ctx, existing, auditor)
	if err != nil {
		return Result{}, err
	}
	if !allDead {
		return Result{}, &ConflictError{Reason: "one or more pre-start leased nodes are live", Existing: existing}
	}
	result := Result{Action: "abort", Apply: apply, Lease: existing, Observations: observations}
	if !apply {
		return result, nil
	}
	leasePath, _, _ := s.paths()
	if err := os.Remove(leasePath); err != nil {
		return Result{}, err
	}
	root, _ := s.root()
	if err := fsutil.SyncDir(root); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s Store) Reclaim(ctx context.Context, desired Lease, apply bool, auditor RuntimeAuditor) (Result, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer lock.release()
	existing, err := s.Read()
	if err != nil {
		return Result{}, err
	}
	if existing.OwnerUID != s.ownerUID() {
		return Result{}, &ConflictError{Reason: "stale lease belongs to another UID and requires privileged/operator review", Existing: existing}
	}
	if grace := s.staleAfter(); grace > 0 && s.now().Sub(existing.UpdatedAt) < grace {
		return Result{}, &ConflictError{Reason: fmt.Sprintf("lease heartbeat is newer than stale grace %s", grace), Existing: existing}
	}
	observations, allDead, err := auditAll(ctx, existing, auditor)
	if err != nil {
		return Result{}, err
	}
	if !allDead {
		return Result{}, &ConflictError{Reason: "lease cannot be reclaimed while a node remains live", Existing: existing}
	}
	normalized, err := normalizeDesired(desired, s.ownerUID(), s.now())
	if err != nil {
		return Result{}, err
	}
	previous := existing
	result := Result{Action: "reclaim", Apply: apply, Lease: normalized, Previous: &previous, Observations: observations}
	if !apply {
		return result, nil
	}
	if err := s.write(normalized); err != nil {
		return Result{}, err
	}
	return result, nil
}
