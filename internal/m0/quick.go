// Package m0 contains replayable technical probes. Its API is not the v1 CLI
// contract; successful observations must still be promoted into ADRs/tests.
package m0

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/cloudinit"
	"github.com/pgsty/farrow/internal/disk"
	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/identity"
	usernet "github.com/pgsty/farrow/internal/network/user"
	"github.com/pgsty/farrow/internal/openssh"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/spec"
)

type QuickOptions struct {
	Image        string
	ExpectedSHA  string
	WorkDir      string
	Boot         string
	ReadyTimeout time.Duration
}

type Event struct {
	Time   time.Time `json:"time"`
	Step   string    `json:"step"`
	Result string    `json:"result"`
	Detail string    `json:"detail,omitempty"`
}

type QuickEvidence struct {
	Schema       int               `json:"schema"`
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   time.Time         `json:"finished_at"`
	HostOS       string            `json:"host_os"`
	HostArch     string            `json:"host_arch"`
	QEMUPath     string            `json:"qemu_path"`
	QEMUVersion  string            `json:"qemu_version"`
	QEMUImgPath  string            `json:"qemu_img_path"`
	FirmwareCode string            `json:"firmware_code"`
	FirmwareVars string            `json:"firmware_vars_template"`
	Boot         string            `json:"boot"`
	ImagePath    string            `json:"image_path"`
	ImageSHA256  string            `json:"image_sha256"`
	ProjectID    string            `json:"project_id"`
	SpecHash     string            `json:"spec_hash"`
	SSHPort      uint16            `json:"ssh_port"`
	Forwards     []qemu.Forward    `json:"forwards"`
	Invocation   qemu.Invocation   `json:"invocation"`
	Processes    []ProcessEvidence `json:"processes"`
	Checks       map[string]string `json:"checks"`
	Events       []Event           `json:"events"`
}

type ProcessEvidence struct {
	PID              int    `json:"pid"`
	UID              int    `json:"uid"`
	Started          string `json:"started"`
	Executable       string `json:"executable"`
	ExpectedArgvHash string `json:"expected_argv_sha256"`
}

func (e *QuickEvidence) event(step, result, detail string) {
	e.Events = append(e.Events, Event{Time: time.Now().UTC(), Step: step, Result: result, Detail: detail})
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func fileSHA256(path string) (string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, handle); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func prepareWorkDir(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("M0 work directory must be an absolute path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.Mkdir(path, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("M0 work path must be a real directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("M0 work directory must be empty")
	}
	return os.Chmod(path, 0o700)
}

func copyExclusive(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func choosePorts(forwards []spec.Forward) (uint16, []qemu.Forward, error) {
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
	sshPort, err := usernet.Choose(2222, available)
	if err != nil {
		return 0, nil, err
	}
	chosen[sshPort] = struct{}{}
	resolved := []qemu.Forward{{Bind: "127.0.0.1", Host: sshPort, Guest: 22}}
	for _, forward := range forwards {
		port, err := usernet.Choose(forward.Host, available)
		if err != nil {
			return 0, nil, err
		}
		chosen[port] = struct{}{}
		resolved = append(resolved, qemu.Forward{Bind: forward.Bind, Host: port, Guest: forward.Guest})
	}
	return sshPort, resolved, nil
}

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

func captureProcess(ctx context.Context, runner execx.Runner, invocation qemu.Invocation, pid int) (ProcessEvidence, error) {
	psPath, err := exec.LookPath("ps")
	if err != nil {
		return ProcessEvidence{}, err
	}
	query := func(field string) (string, error) {
		result, runErr := runner.Run(ctx, psPath, "-p", strconv.Itoa(pid), "-o", field+"=")
		if runErr != nil {
			return "", runErr
		}
		return strings.TrimSpace(string(result.Stdout)), nil
	}
	uidText, err := query("uid")
	if err != nil {
		return ProcessEvidence{}, err
	}
	uid, err := strconv.Atoi(uidText)
	if err != nil {
		return ProcessEvidence{}, fmt.Errorf("parse QEMU uid %q: %w", uidText, err)
	}
	started, err := query("lstart")
	if err != nil {
		return ProcessEvidence{}, err
	}
	executable, err := query("comm")
	if err != nil {
		return ProcessEvidence{}, err
	}
	argv, err := json.Marshal(invocation)
	if err != nil {
		return ProcessEvidence{}, err
	}
	hash := sha256.Sum256(argv)
	return ProcessEvidence{PID: pid, UID: uid, Started: started, Executable: executable, ExpectedArgvHash: hex.EncodeToString(hash[:])}, nil
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid QEMU pidfile %q", strings.TrimSpace(string(data)))
	}
	return pid, nil
}

func validateQMPIdentity(ctx context.Context, client *qmp.Client, socket, name, uuid string) error {
	actualName, err := client.QueryName(ctx, socket)
	if err != nil {
		return err
	}
	actualUUID, err := client.QueryUUID(ctx, socket)
	if err != nil {
		return err
	}
	if actualName.Name != name || !strings.EqualFold(actualUUID.UUID, uuid) {
		return fmt.Errorf("QMP identity mismatch: name=%q uuid=%q", actualName.Name, actualUUID.UUID)
	}
	return nil
}

func waitQMP(ctx context.Context, client *qmp.Client, socket, name, uuid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := validateQMPIdentity(ctx, client, socket, name, uuid); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("QMP identity did not become ready: %w", lastErr)
}

func sshArgs(key, knownHosts string, port uint16, command ...string) []string {
	knownHostsOption, err := openssh.QuoteConfigValue(knownHosts)
	if err != nil {
		return nil
	}
	args := []string{
		"-F", "/dev/null", "-i", key,
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHostsOption,
		"-o", "ConnectTimeout=5",
		"-p", strconv.Itoa(int(port)), "dba@127.0.0.1",
	}
	return append(args, command...)
}

type readyMarker struct {
	Project    string `json:"project"`
	Node       string `json:"node"`
	Generation uint64 `json:"generation"`
	SpecHash   string `json:"spec_hash"`
}

func waitSSHReady(ctx context.Context, runner execx.Runner, sshPath, key, knownHosts string, port uint16, expected readyMarker, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		args := sshArgs(key, knownHosts, port, "cat", "/var/lib/farrow/ready.json")
		if args == nil {
			return "", errors.New("invalid OpenSSH known_hosts path")
		}
		result, err := runner.Run(ctx, sshPath, args...)
		if err == nil {
			var marker readyMarker
			if decodeErr := json.Unmarshal(result.Stdout, &marker); decodeErr == nil && marker == expected {
				return strings.TrimSpace(string(result.Stdout)), nil
			} else if decodeErr != nil {
				lastErr = decodeErr
			} else {
				lastErr = fmt.Errorf("ready marker mismatch: %#v", marker)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return "", fmt.Errorf("SSH/ready marker timeout: %w", lastErr)
}

func startQEMU(ctx context.Context, runner execx.Runner, invocation qemu.Invocation, client *qmp.Client, socket, pidfile, name, uuid string) (int, error) {
	if _, err := runner.Run(ctx, invocation.Binary, invocation.Args...); err != nil {
		return 0, err
	}
	if err := waitQMP(ctx, client, socket, name, uuid, 15*time.Second); err != nil {
		return 0, err
	}
	pid, err := readPID(pidfile)
	if err != nil {
		return 0, err
	}
	if !processRunning(pid) {
		return 0, fmt.Errorf("QEMU pid %d is not running", pid)
	}
	return pid, nil
}

func stopQEMU(ctx context.Context, client *qmp.Client, socket, name, uuid string, pid int) error {
	if err := validateQMPIdentity(ctx, client, socket, name, uuid); err != nil {
		return fmt.Errorf("refuse powerdown without QMP identity: %w", err)
	}
	if err := client.Powerdown(ctx, socket); err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if !processRunning(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err := validateQMPIdentity(ctx, client, socket, name, uuid); err != nil {
		return fmt.Errorf("guest did not power down and QMP identity can no longer be verified: %w", err)
	}
	if err := client.Quit(ctx, socket); err != nil {
		return err
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processRunning(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("QEMU pid %d remained after QMP quit; no signal was sent", pid)
}

func runSSHCheck(ctx context.Context, runner execx.Runner, sshPath, key, knownHosts string, port uint16, command ...string) (string, error) {
	args := sshArgs(key, knownHosts, port, command...)
	if args == nil {
		return "", errors.New("invalid OpenSSH known_hosts path")
	}
	result, err := runner.Run(ctx, sshPath, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func parseDFSize(output string) (int64, error) {
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected df output %q", output)
	}
	size, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse df size from %q: %w", output, err)
	}
	return size, nil
}

func waitNTPSync(ctx context.Context, runner execx.Runner, sshPath, key, knownHosts string, port uint16, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		value, err := runSSHCheck(ctx, runner, sshPath, key, knownHosts, port, "timedatectl", "show", "-p", "NTPSynchronized", "--value")
		if err == nil {
			last = value
			if value == "yes" {
				return value, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return last, fmt.Errorf("guest NTP did not synchronize within %s (last=%q)", timeout, last)
}

func removeRuntimeFiles(socket, pidfile string) error {
	for _, ownedRuntime := range []string{socket, pidfile} {
		if err := os.Remove(ownedRuntime); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stopped runtime artifact %s: %w", ownedRuntime, err)
		}
	}
	return nil
}

func writeEvidence(path string, evidence any) error {
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".evidence-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// QuickSmoke performs one create/boot/stop/start/stop lifecycle and preserves
// every artifact for inspection. It never removes the supplied image.
func QuickSmoke(ctx context.Context, options QuickOptions) (evidence QuickEvidence, returnErr error) {
	evidence.Schema = 1
	evidence.StartedAt = time.Now().UTC()
	evidence.Checks = make(map[string]string)
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
			evidence.event("smoke", "failed", returnErr.Error())
		} else {
			evidence.event("smoke", "passed", "one create/boot/stop/start/stop lifecycle")
		}
		if err := writeEvidence(evidencePath, evidence); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("write evidence: %w", err)
		}
	}()

	imageInfo, err := os.Lstat(options.Image)
	if err != nil || !imageInfo.Mode().IsRegular() {
		return evidence, fmt.Errorf("image must be an existing regular file: %s", options.Image)
	}
	if imageInfo.Mode().Perm()&0o222 != 0 {
		return evidence, fmt.Errorf("M0 base image must be read-only before use: mode %o", imageInfo.Mode().Perm())
	}
	absImage, err := filepath.Abs(options.Image)
	if err != nil {
		return evidence, err
	}
	evidence.ImagePath = absImage
	evidence.ImageSHA256, err = fileSHA256(absImage)
	if err != nil {
		return evidence, err
	}
	if options.ExpectedSHA == "" || !strings.EqualFold(options.ExpectedSHA, evidence.ImageSHA256) {
		return evidence, fmt.Errorf("image SHA-256 %s does not match required %s", evidence.ImageSHA256, options.ExpectedSHA)
	}
	evidence.event("image-digest", "passed", evidence.ImageSHA256)

	profile, err := platform.Native()
	if err != nil {
		return evidence, err
	}
	evidence.HostOS, evidence.HostArch = profile.OS, profile.Arch
	qemuPath, err := platform.FindQEMUBinary(profile, exec.LookPath)
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
	versionResult, err := runner.Run(ctx, qemuPath, "--version")
	if err != nil {
		return evidence, err
	}
	version, err := platform.ParseQEMUVersion(string(versionResult.Stdout) + string(versionResult.Stderr))
	if err != nil {
		return evidence, err
	}
	if !version.AtLeast(profile.MinimumQEMU) {
		return evidence, fmt.Errorf("unsupported QEMU version %s; minimum is %s", version, profile.MinimumQEMU)
	}
	boot := options.Boot
	if boot == "" || boot == "auto" {
		boot = "bios"
		if profile.RequiresUEFI {
			boot = "uefi"
		}
	}
	if boot != "bios" && boot != "uefi" {
		return evidence, fmt.Errorf("unsupported M0 boot mode %q", boot)
	}
	firmware, err := platform.FindFirmwareForBoot(profile, boot)
	if err != nil {
		return evidence, err
	}
	evidence.Boot = boot
	evidence.QEMUPath, evidence.QEMUImgPath = qemuPath, qemuImgPath
	evidence.QEMUVersion = version.String()
	evidence.FirmwareCode, evidence.FirmwareVars = firmware.Code, firmware.Vars

	projectID, err := randomUUID()
	if err != nil {
		return evidence, err
	}
	evidence.ProjectID = projectID
	resolved := spec.Quick(true, true)
	sshPort, forwards, err := choosePorts(resolved.Nodes[0].Forwards)
	if err != nil {
		return evidence, err
	}
	for index := range resolved.Nodes[0].Forwards {
		resolved.Nodes[0].Forwards[index].Host = forwards[index+1].Host
	}
	evidence.SpecHash, err = spec.Hash(resolved)
	if err != nil {
		return evidence, err
	}
	evidence.SSHPort, evidence.Forwards = sshPort, forwards

	keyPath := filepath.Join(options.WorkDir, "id_ed25519")
	if _, err := runner.Run(ctx, sshKeygenPath, "-q", "-t", "ed25519", "-N", "", "-C", "farrow-m0", "-f", keyPath); err != nil {
		return evidence, err
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return evidence, err
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return evidence, err
	}
	knownHosts := filepath.Join(options.WorkDir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		return evidence, err
	}

	diskManager := disk.Manager{QEMUImg: qemuImgPath, Runner: runner}
	rootPath := filepath.Join(options.WorkDir, "root.qcow2")
	if _, err := diskManager.CreateOverlay(ctx, absImage, rootPath, 64*spec.GiB); err != nil {
		return evidence, err
	}
	dataPath := filepath.Join(options.WorkDir, "data.qcow2")
	if _, err := diskManager.CreateBlank(ctx, dataPath, 64*spec.GiB); err != nil {
		return evidence, err
	}
	rootSerial, _ := identity.DiskSerial(projectID, "meta", "root")
	dataSerial, _ := identity.DiskSerial(projectID, "meta", "data")
	mgmtMAC, _ := identity.MAC(projectID, "meta", "management")
	seedFiles, err := cloudinit.Render(cloudinit.Input{
		ProjectID: projectID, Node: "meta", Hostname: "meta", Generation: 1,
		SpecHash: evidence.SpecHash, SSHUser: "dba", PublicKey: strings.TrimSpace(string(publicKey)),
		MgmtMAC: mgmtMAC, Disks: []cloudinit.Disk{{Serial: dataSerial, Mount: "/data", Filesystem: "auto"}},
	})
	if err != nil {
		return evidence, err
	}
	seedPath := filepath.Join(options.WorkDir, "seed.iso")
	if err := cloudinit.BuildISO(seedPath, seedFiles); err != nil {
		return evidence, err
	}
	var qemuFirmware *qemu.Firmware
	if boot == "uefi" {
		nvramPath := filepath.Join(options.WorkDir, "nvram.fd")
		if err := copyExclusive(firmware.Vars, nvramPath, 0o600); err != nil {
			return evidence, err
		}
		qemuFirmware = &qemu.Firmware{Code: firmware.Code, Vars: nvramPath}
	}

	runtimeDir := filepath.Join("/tmp", "farrow-m0-"+strings.ReplaceAll(projectID[:8], "-", ""))
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		return evidence, fmt.Errorf("create short runtime directory: %w", err)
	}
	qmpSocket := filepath.Join(runtimeDir, "qmp.sock")
	pidfile := filepath.Join(runtimeDir, "qemu.pid")
	serialLog := filepath.Join(options.WorkDir, "serial.log")
	invocation, err := qemu.Build(qemu.Config{
		Profile: profile, Binary: qemuPath, Name: "meta", UUID: projectID,
		CPUs: 2, Memory: 4 * spec.GiB,
		Firmware: qemuFirmware,
		Root:     qemu.Disk{Path: rootPath, Serial: rootSerial},
		Data:     []qemu.Disk{{Path: dataPath, Serial: dataSerial}}, Seed: seedPath,
		QMP: qmpSocket, PIDFile: pidfile, SerialLog: serialLog,
		MgmtMAC: mgmtMAC, Forwards: forwards, Detach: true,
	})
	if err != nil {
		return evidence, err
	}
	evidence.Invocation = invocation
	client := &qmp.Client{Timeout: 5 * time.Second}
	pid := 0
	defer func() {
		if pid > 0 && processRunning(pid) {
			if err := validateQMPIdentity(context.Background(), client, qmpSocket, "meta", projectID); err == nil {
				_ = client.Quit(context.Background(), qmpSocket)
			}
		}
	}()

	pid, err = startQEMU(ctx, runner, invocation, client, qmpSocket, pidfile, "meta", projectID)
	if err != nil {
		return evidence, fmt.Errorf("initial QEMU start: %w", err)
	}
	evidence.event("qemu-start-1", "passed", fmt.Sprintf("pid=%d", pid))
	processEvidence, err := captureProcess(ctx, runner, invocation, pid)
	if err != nil {
		return evidence, fmt.Errorf("capture initial QEMU identity: %w", err)
	}
	if processEvidence.UID != os.Getuid() {
		return evidence, fmt.Errorf("QEMU uid %d differs from invoking uid %d", processEvidence.UID, os.Getuid())
	}
	evidence.Processes = append(evidence.Processes, processEvidence)
	expectedReady := readyMarker{Project: projectID, Node: "meta", Generation: 1, SpecHash: evidence.SpecHash}
	ready, err := waitSSHReady(ctx, runner, sshPath, keyPath, knownHosts, sshPort, expectedReady, options.ReadyTimeout)
	if err != nil {
		return evidence, err
	}
	evidence.Checks["ready"] = ready
	for name, command := range map[string][]string{
		"arch":     {"uname", "-m"},
		"data":     {"findmnt", "-n", "-o", "SOURCE,FSTYPE,OPTIONS", "/data"},
		"dns":      {"getent", "hosts", "archive.ubuntu.com"},
		"internet": {"/usr/local/libexec/farrow-network-check"},
	} {
		output, checkErr := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, command...)
		if checkErr != nil {
			return evidence, fmt.Errorf("guest check %s: %w", name, checkErr)
		}
		evidence.Checks[name] = output
	}
	expectedGuestArch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[profile.Arch]
	if evidence.Checks["arch"] != expectedGuestArch {
		return evidence, fmt.Errorf("guest architecture %q does not match native %q", evidence.Checks["arch"], expectedGuestArch)
	}
	rootDF, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, "df", "-B1", "--output=size", "/")
	if err != nil {
		return evidence, fmt.Errorf("root size check: %w", err)
	}
	rootBytes, err := parseDFSize(rootDF)
	if err != nil {
		return evidence, err
	}
	if rootBytes < 60*spec.GiB {
		return evidence, fmt.Errorf("grown root filesystem is too small: %d", rootBytes)
	}
	evidence.Checks["root-bytes"] = strconv.FormatInt(rootBytes, 10)
	dataDF, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, "df", "-B1", "--output=size", "/data")
	if err != nil {
		return evidence, fmt.Errorf("data size check: %w", err)
	}
	dataBytes, err := parseDFSize(dataDF)
	if err != nil {
		return evidence, err
	}
	if dataBytes < 60*spec.GiB {
		return evidence, fmt.Errorf("data filesystem is too small: %d", dataBytes)
	}
	evidence.Checks["data-bytes"] = strconv.FormatInt(dataBytes, 10)
	byID, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, "readlink", "-f", "/dev/disk/by-id/virtio-"+dataSerial)
	if err != nil {
		return evidence, fmt.Errorf("data by-id check failed: %w", err)
	}
	if byID == "" {
		return evidence, errors.New("data by-id resolved to an empty path")
	}
	evidence.Checks["data-by-id"] = byID
	fstab, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, "cat", "/etc/fstab")
	if err != nil {
		return evidence, fmt.Errorf("read fstab: %w", err)
	}
	if !strings.Contains(fstab, "UUID=") || !strings.Contains(fstab, " /data ") || !strings.Contains(fstab, "nofail") {
		return evidence, errors.New("fstab UUID/nofail contract missing")
	}
	evidence.Checks["fstab"] = fstab
	ntp, err := waitNTPSync(ctx, runner, sshPath, keyPath, knownHosts, sshPort, 30*time.Second)
	if err != nil {
		return evidence, err
	}
	evidence.Checks["ntp-synchronized"] = ntp
	if _, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, "sudo", "touch", "/data/farrow-m0-persist"); err != nil {
		return evidence, fmt.Errorf("create data persistence canary: %w", err)
	}
	if err := stopQEMU(ctx, client, qmpSocket, "meta", projectID, pid); err != nil {
		return evidence, fmt.Errorf("initial stop: %w", err)
	}
	evidence.event("qemu-stop-1", "passed", fmt.Sprintf("pid=%d", pid))
	pid = 0
	if err := removeRuntimeFiles(qmpSocket, pidfile); err != nil {
		return evidence, err
	}

	pid, err = startQEMU(ctx, runner, invocation, client, qmpSocket, pidfile, "meta", projectID)
	if err != nil {
		return evidence, fmt.Errorf("second QEMU start: %w", err)
	}
	evidence.event("qemu-start-2", "passed", fmt.Sprintf("pid=%d", pid))
	processEvidence, err = captureProcess(ctx, runner, invocation, pid)
	if err != nil {
		return evidence, fmt.Errorf("capture restarted QEMU identity: %w", err)
	}
	if processEvidence.UID != os.Getuid() {
		return evidence, fmt.Errorf("restarted QEMU uid %d differs from invoking uid %d", processEvidence.UID, os.Getuid())
	}
	evidence.Processes = append(evidence.Processes, processEvidence)
	if _, err := waitSSHReady(ctx, runner, sshPath, keyPath, knownHosts, sshPort, expectedReady, options.ReadyTimeout); err != nil {
		return evidence, fmt.Errorf("ready after restart: %w", err)
	}
	if _, err := runSSHCheck(ctx, runner, sshPath, keyPath, knownHosts, sshPort, "sudo", "test", "-f", "/data/farrow-m0-persist"); err != nil {
		return evidence, fmt.Errorf("data persistence canary missing after restart: %w", err)
	}
	evidence.Checks["data-persistence"] = "passed"
	if err := stopQEMU(ctx, client, qmpSocket, "meta", projectID, pid); err != nil {
		return evidence, fmt.Errorf("final stop: %w", err)
	}
	evidence.event("qemu-stop-2", "passed", fmt.Sprintf("pid=%d", pid))
	pid = 0
	if err := removeRuntimeFiles(qmpSocket, pidfile); err != nil {
		return evidence, err
	}
	if err := os.Remove(runtimeDir); err != nil {
		return evidence, fmt.Errorf("remove empty owned runtime directory: %w", err)
	}
	evidence.Checks["runtime-residue"] = "none"
	return evidence, nil
}
