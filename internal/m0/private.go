package m0

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pgsty/piglet/internal/cloudinit"
	"github.com/pgsty/piglet/internal/disk"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/identity"
	darwinnet "github.com/pgsty/piglet/internal/network/darwin"
	linuxnet "github.com/pgsty/piglet/internal/network/linux"
	"github.com/pgsty/piglet/internal/network/subnet"
	usernet "github.com/pgsty/piglet/internal/network/user"
	"github.com/pgsty/piglet/internal/openssh"
	"github.com/pgsty/piglet/internal/platform"
	"github.com/pgsty/piglet/internal/qemu"
	"github.com/pgsty/piglet/internal/qmp"
	"github.com/pgsty/piglet/internal/spec"
)

type PrivateOptions struct {
	Image         string
	ExpectedSHA   string
	WorkDir       string
	ReadyTimeout  time.Duration
	RestartDaemon bool
	LinuxHelper   string
	NetworkCIDR   string
	// DiagnosticShared records all reachable shared-mode contract failures
	// before returning an error. The default fail-fast M0 gate is unchanged.
	DiagnosticShared bool
}

func diagnosticPrivateAddresses(cidr string) (network, host, meta, peer string, err error) {
	if cidr == "" {
		cidr = subnet.DefaultCIDR
	}
	layout, parseErr := subnet.Parse(cidr)
	if parseErr != nil {
		return "", "", "", "", parseErr
	}
	return layout.CIDR(), layout.HostAddress(), layout.Address(10), layout.Address(11), nil
}

type PrivateNodeEvidence struct {
	Name       string            `json:"name"`
	Address    string            `json:"address"`
	SSHPort    uint16            `json:"ssh_port"`
	Invocation qemu.Invocation   `json:"invocation"`
	Process    ProcessEvidence   `json:"process"`
	Checks     map[string]string `json:"checks"`
}

type PrivateEvidence struct {
	Schema      int                   `json:"schema"`
	StartedAt   time.Time             `json:"started_at"`
	FinishedAt  time.Time             `json:"finished_at"`
	HostOS      string                `json:"host_os"`
	HostArch    string                `json:"host_arch"`
	QEMUVersion string                `json:"qemu_version"`
	ImagePath   string                `json:"image_path"`
	ImageSHA256 string                `json:"image_sha256"`
	ProjectID   string                `json:"project_id"`
	SpecHash    string                `json:"spec_hash"`
	Backend     string                `json:"backend"`
	Socket      string                `json:"socket,omitempty"`
	Bridge      string                `json:"bridge,omitempty"`
	Helper      string                `json:"helper,omitempty"`
	Nodes       []PrivateNodeEvidence `json:"nodes"`
	Checks      map[string]string     `json:"checks"`
	Events      []Event               `json:"events"`
}

func (e *PrivateEvidence) event(step, result, detail string) {
	e.Events = append(e.Events, Event{Time: time.Now().UTC(), Step: step, Result: result, Detail: detail})
}

type privateNodeRuntime struct {
	evidence PrivateNodeEvidence
	pid      int
	qmp      string
	pidfile  string
	runtime  string
	key      string
	known    string
	uuid     string
}

func privateSSHArgs(key, knownHosts, address string, command ...string) []string {
	knownHostsOption, err := openssh.QuoteConfigValue(knownHosts)
	if err != nil {
		return nil
	}
	args := []string{
		"-F", "/dev/null", "-i", key,
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHostsOption,
		"-o", "ConnectTimeout=5", "dba@" + address,
	}
	return append(args, command...)
}

func runPrivateSSH(ctx context.Context, runner execx.Runner, sshPath, key, knownHosts, address string, command ...string) (string, error) {
	args := privateSSHArgs(key, knownHosts, address, command...)
	if args == nil {
		return "", errors.New("invalid OpenSSH known_hosts path")
	}
	result, err := runner.Run(ctx, sshPath, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func choosePrivateSSHPorts(count int) ([]uint16, error) {
	chosen := make(map[uint16]struct{})
	available := func(port uint16) bool {
		if _, exists := chosen[port]; exists {
			return false
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}
	ports := make([]uint16, 0, count)
	for index := 0; index < count; index++ {
		port, err := usernet.Choose(uint16(2222+index), available)
		if err != nil {
			return nil, err
		}
		chosen[port] = struct{}{}
		ports = append(ports, port)
	}
	return ports, nil
}

func waitPrivateSSH(ctx context.Context, runner execx.Runner, sshPath, key, knownHosts, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := runPrivateSSH(ctx, runner, sshPath, key, knownHosts, address, "true"); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("private SSH %s did not become ready: %w", address, lastErr)
}

func privateSpecHash() (string, error) {
	value := spec.Resolved{
		Schema: 1, Name: "m0-private", Image: "u24", Network: "private", SSHUser: "dba",
		Nodes: []spec.Node{
			{Name: "meta", Control: true, CPUs: 2, Memory: 4 * spec.GiB, RootDisk: 64 * spec.GiB},
			{Name: "node-1", CPUs: 1, Memory: 2 * spec.GiB, RootDisk: 64 * spec.GiB},
		},
	}
	return spec.Hash(value)
}

func rootStat(info os.FileInfo) (*syscall.Stat_t, error) {
	statistics, ok := info.Sys().(*syscall.Stat_t)
	if !ok || statistics.Uid != 0 {
		return nil, errors.New("resource is not root-owned")
	}
	return statistics, nil
}

func preflightDarwinPrivate(evidence *PrivateEvidence) (*qemu.PrivateNetwork, error) {
	socketInfo, err := os.Lstat(darwinnet.SocketPath)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("root-owned socket_vmnet socket is not ready: %s", darwinnet.SocketPath)
	}
	if _, err := rootStat(socketInfo); err != nil {
		return nil, fmt.Errorf("socket_vmnet socket ownership: %w", err)
	}
	evidence.Backend = "socket_vmnet-stream"
	evidence.Socket = darwinnet.SocketPath
	return &qemu.PrivateNetwork{StreamSocket: darwinnet.SocketPath, ReconnectMS: 1000}, nil
}

func preflightLinuxPrivate(ctx context.Context, runner execx.Runner, requestedHelper, hostAddress string, evidence *PrivateEvidence) (*qemu.PrivateNetwork, error) {
	helper := requestedHelper
	if helper == "" {
		for _, candidate := range []string{"/usr/lib/qemu/qemu-bridge-helper", "/usr/libexec/qemu-bridge-helper"} {
			if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() {
				helper = candidate
				break
			}
		}
	}
	if helper != "/usr/lib/qemu/qemu-bridge-helper" && helper != "/usr/libexec/qemu-bridge-helper" {
		return nil, fmt.Errorf("unsupported Linux bridge helper path %q", helper)
	}
	helperInfo, err := os.Lstat(helper)
	if err != nil || !helperInfo.Mode().IsRegular() || helperInfo.Mode()&os.ModeSymlink != 0 || helperInfo.Mode().Perm() != 0o750 || helperInfo.Mode()&os.ModeSetuid == 0 {
		return nil, fmt.Errorf("linux bridge helper is not a regular root:kvm mode-4750 file: %s", helper)
	}
	helperStat, err := rootStat(helperInfo)
	if err != nil {
		return nil, err
	}
	group, err := osuser.LookupGroupId(strconv.FormatUint(uint64(helperStat.Gid), 10))
	if err != nil || group.Name != "kvm" {
		return nil, errors.New("linux bridge helper group is not kvm")
	}
	groups, err := os.Getgroups()
	if err != nil {
		return nil, err
	}
	inHelperGroup := os.Getegid() == int(helperStat.Gid)
	for _, gid := range groups {
		inHelperGroup = inHelperGroup || gid == int(helperStat.Gid)
	}
	if !inHelperGroup {
		return nil, errors.New("invoking user is not in the bridge helper group")
	}
	bridgeConfInfo, err := os.Lstat(linuxnet.BridgeConfPath)
	if err != nil || !bridgeConfInfo.Mode().IsRegular() || bridgeConfInfo.Mode()&os.ModeSymlink != 0 || bridgeConfInfo.Mode().Perm() != 0o644 {
		return nil, errors.New("linux qemu bridge.conf is missing or unsafe")
	}
	if _, err := rootStat(bridgeConfInfo); err != nil {
		return nil, err
	}
	bridgeConf, err := os.ReadFile(linuxnet.BridgeConfPath)
	if err != nil || !strings.Contains(string(bridgeConf), "# BEGIN PIGLET MANAGED: piglet0\nallow piglet0\n# END PIGLET MANAGED: piglet0\n") {
		return nil, errors.New("linux qemu bridge.conf lacks the exact Piglet marker block")
	}
	leaseInfo, err := os.Lstat(linuxnet.LeaseRoot)
	if err != nil || !leaseInfo.IsDir() || leaseInfo.Mode()&os.ModeSymlink != 0 || leaseInfo.Mode().Perm() != 0o777 || leaseInfo.Mode()&os.ModeSticky == 0 {
		return nil, errors.New("linux private lease root is not a real root-owned mode-1777 directory")
	}
	if _, err := rootStat(leaseInfo); err != nil {
		return nil, err
	}
	ipPath, err := exec.LookPath("ip")
	if err != nil {
		return nil, err
	}
	link, err := runner.Run(ctx, ipPath, "-d", "link", "show", "dev", linuxnet.BridgeName)
	if err != nil || !strings.Contains(string(link.Stdout), "bridge") {
		return nil, errors.New("linux piglet0 bridge is not present")
	}
	address, err := runner.Run(ctx, ipPath, "-4", "-o", "address", "show", "dev", linuxnet.BridgeName)
	if err != nil || !strings.Contains(string(address.Stdout), hostAddress+"/24") {
		return nil, fmt.Errorf("linux piglet0 does not own %s/24", hostAddress)
	}
	evidence.Backend = "qemu-bridge-helper"
	evidence.Bridge = linuxnet.BridgeName
	evidence.Helper = helper
	evidence.Checks["linux-bridge"] = strings.TrimSpace(string(address.Stdout))
	evidence.Checks["linux-helper"] = "root:kvm mode=4750"
	return &qemu.PrivateNetwork{Bridge: linuxnet.BridgeName, BridgeHelper: helper}, nil
}

// PrivateSmoke requires an already installed root-owned socket_vmnet daemon
// on macOS or a root-owned persistent piglet0/helper boundary on Linux.
// It never installs or uninstalls privileged resources.
func PrivateSmoke(ctx context.Context, options PrivateOptions) (evidence PrivateEvidence, returnErr error) {
	evidence.Schema = 1
	evidence.StartedAt = time.Now().UTC()
	evidence.Checks = make(map[string]string)
	diagnosticFailures := make([]string, 0)
	networkCIDR, hostAddress, metaAddress, peerAddress, err := diagnosticPrivateAddresses(options.NetworkCIDR)
	if err != nil {
		return evidence, err
	}
	evidence.Checks["network-cidr"] = networkCIDR
	if options.ReadyTimeout <= 0 {
		options.ReadyTimeout = 180 * time.Second
	}
	if err := prepareWorkDir(options.WorkDir); err != nil {
		return evidence, err
	}
	evidencePath := filepath.Join(options.WorkDir, "evidence.json")
	defer func() {
		evidence.FinishedAt = time.Now().UTC()
		if returnErr != nil {
			evidence.event("private-smoke", "failed", returnErr.Error())
		} else {
			evidence.event("private-smoke", "passed", "two-node private lifecycle")
		}
		if err := writeEvidence(evidencePath, evidence); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	imageInfo, err := os.Lstat(options.Image)
	if err != nil || !imageInfo.Mode().IsRegular() || imageInfo.Mode().Perm()&0o222 != 0 {
		return evidence, errors.New("private smoke image must be a read-only regular file")
	}
	absImage, err := filepath.Abs(options.Image)
	if err != nil {
		return evidence, err
	}
	evidence.ImagePath = absImage
	evidence.ImageSHA256, err = fileSHA256(absImage)
	if err != nil || !strings.EqualFold(evidence.ImageSHA256, options.ExpectedSHA) {
		return evidence, fmt.Errorf("private smoke image digest mismatch: %s", evidence.ImageSHA256)
	}
	profile, err := platform.Native()
	if err != nil || (profile.OS != "darwin" && profile.OS != "linux") {
		return evidence, errors.New("private M0 harness requires native macOS or Linux")
	}
	evidence.HostOS, evidence.HostArch = profile.OS, profile.Arch
	qemuPath, err := exec.LookPath(profile.QEMUBinary)
	if err != nil {
		return evidence, err
	}
	qemuImgPath, err := exec.LookPath("qemu-img")
	if err != nil {
		return evidence, err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return evidence, err
	}
	sshKeygenPath, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return evidence, err
	}
	runner := execx.OSRunner{Timeout: 15 * time.Second, OutputLimit: 1 << 20}
	privateTemplate := &qemu.PrivateNetwork{}
	switch profile.OS {
	case "darwin":
		privateTemplate, err = preflightDarwinPrivate(&evidence)
	case "linux":
		privateTemplate, err = preflightLinuxPrivate(ctx, runner, options.LinuxHelper, hostAddress, &evidence)
	}
	if err != nil {
		return evidence, err
	}
	versionResult, err := runner.Run(ctx, qemuPath, "--version")
	if err != nil {
		return evidence, err
	}
	version, err := platform.ParseQEMUVersion(string(versionResult.Stdout) + string(versionResult.Stderr))
	if err != nil {
		return evidence, err
	}
	evidence.QEMUVersion = version.String()
	firmware := platform.Firmware{}
	if profile.RequiresUEFI {
		firmware, err = platform.FindFirmware(profile)
		if err != nil {
			return evidence, err
		}
	}
	projectID, err := randomUUID()
	if err != nil {
		return evidence, err
	}
	evidence.ProjectID = projectID
	evidence.SpecHash, err = privateSpecHash()
	if err != nil {
		return evidence, err
	}
	keyPath := filepath.Join(options.WorkDir, "id_ed25519")
	if _, err := runner.Run(ctx, sshKeygenPath, "-q", "-t", "ed25519", "-N", "", "-C", "piglet-m0-private", "-f", keyPath); err != nil {
		return evidence, err
	}
	privateKey, err := os.ReadFile(keyPath)
	if err != nil {
		return evidence, err
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return evidence, err
	}
	knownHosts := filepath.Join(options.WorkDir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		return evidence, err
	}
	ports, err := choosePrivateSSHPorts(2)
	if err != nil {
		return evidence, err
	}
	hosts := []cloudinit.Host{{Name: "meta", Address: metaAddress}, {Name: "node-1", Address: peerAddress}}
	nodeDefs := []struct {
		name, address string
		control       bool
		cpus          int
		memory        int64
	}{
		{name: "meta", address: metaAddress, control: true, cpus: 2, memory: 4 * spec.GiB},
		{name: "node-1", address: peerAddress, cpus: 1, memory: 2 * spec.GiB},
	}
	diskManager := disk.Manager{QEMUImg: qemuImgPath, Runner: runner}
	client := &qmp.Client{Timeout: 5 * time.Second}
	runtimes := make([]privateNodeRuntime, 0, 2)
	defer func() {
		cleanupProblems := make([]string, 0)
		for index := range runtimes {
			node := &runtimes[index]
			if node.pid > 0 && processRunning(node.pid) {
				cleanupContext, cancel := context.WithTimeout(context.Background(), 75*time.Second)
				if err := stopQEMU(cleanupContext, client, node.qmp, node.evidence.Name, node.uuid, node.pid); err != nil {
					cleanupProblems = append(cleanupProblems, node.evidence.Name+": "+err.Error())
				} else {
					node.pid = 0
				}
				cancel()
			}
			if node.pid == 0 || !processRunning(node.pid) {
				if err := removeRuntimeFiles(node.qmp, node.pidfile); err != nil {
					cleanupProblems = append(cleanupProblems, node.evidence.Name+": "+err.Error())
					continue
				}
				if err := os.Remove(node.runtime); err != nil && !errors.Is(err, os.ErrNotExist) {
					cleanupProblems = append(cleanupProblems, node.evidence.Name+": remove runtime directory: "+err.Error())
				}
			}
		}
		if len(cleanupProblems) == 0 {
			evidence.Checks["deferred-runtime-cleanup"] = "none"
		} else {
			evidence.Checks["deferred-runtime-cleanup"] = strings.Join(cleanupProblems, "; ")
		}
	}()

	for index, definition := range nodeDefs {
		nodeUUID, err := randomUUID()
		if err != nil {
			return evidence, err
		}
		nodeDir := filepath.Join(options.WorkDir, definition.name)
		if err := os.Mkdir(nodeDir, 0o700); err != nil {
			return evidence, err
		}
		rootPath := filepath.Join(nodeDir, "root.qcow2")
		if _, err := diskManager.CreateOverlay(ctx, absImage, rootPath, 64*spec.GiB); err != nil {
			return evidence, err
		}
		dataPath := filepath.Join(nodeDir, "data.qcow2")
		if _, err := diskManager.CreateBlank(ctx, dataPath, 64*spec.GiB); err != nil {
			return evidence, err
		}
		rootSerial, _ := identity.DiskSerial(projectID, definition.name, "root")
		dataSerial, _ := identity.DiskSerial(projectID, definition.name, "data")
		mgmtMAC, _ := identity.MAC(projectID, definition.name, "management")
		privateMAC, _ := identity.MAC(projectID, definition.name, "private")
		cloudInput := cloudinit.Input{
			ProjectID: projectID, Node: definition.name, Hostname: definition.name,
			Generation: 1, SpecHash: evidence.SpecHash, SSHUser: "dba",
			PublicKey: strings.TrimSpace(string(publicKey)), Control: definition.control,
			MgmtMAC: mgmtMAC, Private: &cloudinit.PrivateNetwork{MAC: privateMAC, Address: definition.address, Prefix: 24, HostAddress: hostAddress},
			Hosts: hosts, Disks: []cloudinit.Disk{{Serial: dataSerial, Mount: "/data", Filesystem: "auto"}},
		}
		if definition.control {
			cloudInput.PrivateKey = string(privateKey)
		}
		seedFiles, err := cloudinit.Render(cloudInput)
		if err != nil {
			return evidence, err
		}
		seedPath := filepath.Join(nodeDir, "seed.iso")
		if err := cloudinit.BuildISO(seedPath, seedFiles); err != nil {
			return evidence, err
		}
		var qemuFirmware *qemu.Firmware
		if profile.RequiresUEFI {
			nvramPath := filepath.Join(nodeDir, "nvram.fd")
			if err := copyExclusive(firmware.Vars, nvramPath, 0o600); err != nil {
				return evidence, err
			}
			qemuFirmware = &qemu.Firmware{Code: firmware.Code, Vars: nvramPath}
		}
		runtimeDir := filepath.Join("/tmp", "piglet-m0-"+projectID[:8]+"-"+strconv.Itoa(index))
		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			return evidence, err
		}
		qmpSocket := filepath.Join(runtimeDir, "qmp.sock")
		pidfile := filepath.Join(runtimeDir, "qemu.pid")
		privateNetwork := *privateTemplate
		privateNetwork.MAC = privateMAC
		invocation, err := qemu.Build(qemu.Config{
			Profile: profile, Binary: qemuPath, Name: definition.name, UUID: nodeUUID,
			CPUs: definition.cpus, Memory: definition.memory,
			Firmware: qemuFirmware,
			Root:     qemu.Disk{Path: rootPath, Serial: rootSerial},
			Data:     []qemu.Disk{{Path: dataPath, Serial: dataSerial}}, Seed: seedPath,
			QMP: qmpSocket, PIDFile: pidfile, SerialLog: filepath.Join(nodeDir, "serial.log"),
			MgmtMAC: mgmtMAC, Forwards: []qemu.Forward{{Bind: "127.0.0.1", Host: ports[index], Guest: 22}},
			Private: &privateNetwork,
			Detach:  true,
		})
		if err != nil {
			return evidence, err
		}
		runtimes = append(runtimes, privateNodeRuntime{
			evidence: PrivateNodeEvidence{Name: definition.name, Address: definition.address, SSHPort: ports[index], Invocation: invocation, Checks: make(map[string]string)},
			qmp:      qmpSocket, pidfile: pidfile, runtime: runtimeDir, key: keyPath, known: knownHosts, uuid: nodeUUID,
		})
	}

	for index := range runtimes {
		node := &runtimes[index]
		node.pid, err = startQEMU(ctx, runner, node.evidence.Invocation, client, node.qmp, node.pidfile, node.evidence.Name, node.uuid)
		if err != nil {
			return evidence, err
		}
		node.evidence.Process, err = captureProcess(ctx, runner, node.evidence.Invocation, node.pid)
		if err != nil || node.evidence.Process.UID != os.Getuid() {
			return evidence, fmt.Errorf("private QEMU identity failed for %s: %w", node.evidence.Name, err)
		}
		expected := readyMarker{Project: projectID, Node: node.evidence.Name, Generation: 1, SpecHash: evidence.SpecHash}
		if _, err := waitSSHReady(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.SSHPort, expected, options.ReadyTimeout); err != nil {
			return evidence, err
		}
	}

	if profile.OS == "darwin" {
		ifconfigPath, lookupErr := exec.LookPath("ifconfig")
		if lookupErr != nil {
			return evidence, lookupErr
		}
		ifconfig, runErr := runner.Run(ctx, ifconfigPath)
		if runErr != nil || !strings.Contains(string(ifconfig.Stdout), hostAddress) {
			if !options.DiagnosticShared {
				return evidence, fmt.Errorf("host address %s not present", hostAddress)
			}
			diagnosticFailures = append(diagnosticFailures, "host address "+hostAddress+" not present")
			evidence.Checks["host-address"] = "failed: " + hostAddress + " not present"
		} else {
			evidence.Checks["host-address"] = hostAddress
		}
	} else if evidence.Checks["linux-bridge"] == "" {
		return evidence, errors.New("linux bridge host address evidence is missing")
	} else {
		evidence.Checks["host-address"] = hostAddress
	}
	for index := range runtimes {
		node := &runtimes[index]
		hostTimeout := 30 * time.Second
		if options.DiagnosticShared {
			hostTimeout = 10 * time.Second
		}
		if err := waitPrivateSSH(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.Address, hostTimeout); err != nil {
			if !options.DiagnosticShared {
				return evidence, err
			}
			diagnosticFailures = append(diagnosticFailures, "host-to-"+node.evidence.Name+": "+err.Error())
			node.evidence.Checks["host-to-vm"] = "failed: " + err.Error()
		} else {
			node.evidence.Checks["host-to-vm"] = "passed"
		}
		address, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.SSHPort, "ip", "-4", "-o", "addr", "show", "dev", "private0")
		if err != nil || !strings.Contains(address, node.evidence.Address+"/24") {
			return evidence, fmt.Errorf("private address failed for %s: %s: %w", node.evidence.Name, address, err)
		}
		routes, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.SSHPort, "ip", "route")
		if err != nil {
			return evidence, fmt.Errorf("read routes for %s: %w", node.evidence.Name, err)
		}
		for _, route := range strings.Split(routes, "\n") {
			if strings.HasPrefix(route, "default ") && strings.Contains(route, " dev private0") {
				return evidence, fmt.Errorf("private default route detected for %s", node.evidence.Name)
			}
		}
		privateContract, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.SSHPort, "/usr/local/libexec/piglet-private-contract")
		if err != nil {
			return evidence, fmt.Errorf("private route/DNS contract failed for %s: %w", node.evidence.Name, err)
		}
		node.evidence.Checks["routes"] = routes
		node.evidence.Checks["private-route-dns"] = privateContract
		internet, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.SSHPort, "/usr/local/libexec/piglet-network-check")
		if err != nil {
			return evidence, err
		}
		node.evidence.Checks["internet"] = internet
	}
	pathToPeer, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, runtimes[0].evidence.SSHPort, "ip", "route", "get", peerAddress)
	if err != nil || !strings.Contains(pathToPeer, "dev private0") || !strings.Contains(pathToPeer, "src "+metaAddress) {
		if !options.DiagnosticShared {
			return evidence, fmt.Errorf("VM-to-VM traffic is not on private0: %s", pathToPeer)
		}
		diagnosticFailures = append(diagnosticFailures, "VM-to-VM route: "+pathToPeer)
		evidence.Checks["vm-to-vm-route"] = "failed: " + pathToPeer
	} else {
		evidence.Checks["vm-to-vm-route"] = pathToPeer
	}
	if _, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, runtimes[0].evidence.SSHPort, "ping", "-c", "1", "-W", "3", peerAddress); err != nil {
		if !options.DiagnosticShared {
			return evidence, fmt.Errorf("VM-to-VM ping: %w", err)
		}
		diagnosticFailures = append(diagnosticFailures, "VM-to-VM ping: "+err.Error())
		evidence.Checks["vm-to-vm"] = "failed: " + err.Error()
	} else {
		evidence.Checks["vm-to-vm"] = "passed"
	}
	if _, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, runtimes[0].evidence.SSHPort, "ssh", "-F", "/home/dba/.ssh/config", "-o", "BatchMode=yes", "node-1", "true"); err != nil {
		if !options.DiagnosticShared {
			return evidence, fmt.Errorf("control lateral SSH: %w", err)
		}
		diagnosticFailures = append(diagnosticFailures, "control lateral SSH: "+err.Error())
		evidence.Checks["control-lateral-ssh"] = "failed: " + err.Error()
	} else {
		evidence.Checks["control-lateral-ssh"] = "passed"
	}

	if options.RestartDaemon && profile.OS == "darwin" {
		sudoPath, err := exec.LookPath("sudo")
		if err != nil {
			return evidence, err
		}
		if _, err := runner.Run(ctx, sudoPath, "-n", "/bin/launchctl", "kickstart", "-k", "system/"+darwinnet.ServiceID); err != nil {
			return evidence, fmt.Errorf("restart socket_vmnet daemon: %w", err)
		}
		if options.DiagnosticShared {
			for index := range runtimes {
				node := &runtimes[index]
				expected := readyMarker{Project: projectID, Node: node.evidence.Name, Generation: 1, SpecHash: evidence.SpecHash}
				if _, err := waitSSHReady(ctx, runner, sshPath, keyPath, knownHosts, node.evidence.SSHPort, expected, 30*time.Second); err != nil {
					return evidence, fmt.Errorf("management SSH after daemon restart for %s: %w", node.evidence.Name, err)
				}
			}
			evidence.Checks["daemon-restart-management"] = "passed"
			if _, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, runtimes[0].evidence.SSHPort, "ping", "-c", "1", "-W", "3", peerAddress); err != nil {
				diagnosticFailures = append(diagnosticFailures, "VM-to-VM ping after daemon restart: "+err.Error())
				evidence.Checks["daemon-restart-private"] = "failed: " + err.Error()
			} else {
				evidence.Checks["daemon-restart-private"] = "passed"
			}
		} else if err := waitPrivateSSH(ctx, runner, sshPath, keyPath, knownHosts, metaAddress, 30*time.Second); err != nil {
			return evidence, fmt.Errorf("stream reconnect after daemon restart: %w", err)
		}
		evidence.Checks["daemon-restart-reconnect"] = "passed"
	} else if profile.OS == "linux" {
		evidence.Checks["daemon-restart-reconnect"] = "not-applicable-linux-bridge"
	}

	for index := len(runtimes) - 1; index >= 0; index-- {
		node := &runtimes[index]
		if err := stopQEMU(ctx, client, node.qmp, node.evidence.Name, node.uuid, node.pid); err != nil {
			return evidence, err
		}
		node.pid = 0
		if err := removeRuntimeFiles(node.qmp, node.pidfile); err != nil {
			return evidence, err
		}
		if err := os.Remove(node.runtime); err != nil {
			return evidence, err
		}
	}
	for index := range runtimes {
		evidence.Nodes = append(evidence.Nodes, runtimes[index].evidence)
	}
	evidence.Checks["runtime-residue"] = "none"
	if len(diagnosticFailures) != 0 {
		evidence.Checks["diagnostic-shared-failures"] = strings.Join(diagnosticFailures, "; ")
		return evidence, fmt.Errorf("shared diagnostic found %d private contract failures", len(diagnosticFailures))
	}
	return evidence, nil
}
