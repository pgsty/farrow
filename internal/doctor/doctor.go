// Package doctor performs read-only host capability checks.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/platform"
)

type Status string

const (
	OK    Status = "ok"
	Warn  Status = "warn"
	Error Status = "error"
)

type Check struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Evidence string `json:"evidence"`
	Fix      string `json:"fix,omitempty"`
}

type Report struct {
	OS     string  `json:"os"`
	Arch   string  `json:"arch"`
	Tier   string  `json:"tier"`
	Checks []Check `json:"checks"`
}

func (r Report) HasErrors() bool {
	for _, check := range r.Checks {
		if check.Status == Error {
			return true
		}
	}
	return false
}

type Probe struct {
	Runner           execx.Runner
	LookPath         func(string) (string, error)
	AcceleratorSmoke func(context.Context, string, platform.Profile) (string, error)
	CWD              string
}

func (p Probe) acceleratorSmoke(ctx context.Context, qemuPath string, profile platform.Profile) (string, error) {
	if p.AcceleratorSmoke != nil {
		return p.AcceleratorSmoke(ctx, qemuPath, profile)
	}
	return acceleratorSmoke(ctx, qemuPath, profile)
}

func (p Probe) runner() execx.Runner {
	if p.Runner != nil {
		return p.Runner
	}
	return execx.OSRunner{Timeout: 10 * time.Second}
}

func (p Probe) lookPath(name string) (string, error) {
	if p.LookPath != nil {
		return p.LookPath(name)
	}
	return exec.LookPath(name)
}

func qemuInstallFix(goos string) string {
	if goos == "darwin" {
		return "brew install qemu"
	}
	return "install the distribution QEMU system and qemu-utils/qemu-img packages"
}

func combined(result execx.Result) string {
	return strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
}

func (p Probe) capability(ctx context.Context, binary, name string, args []string, needles ...string) Check {
	result, err := p.runner().Run(ctx, binary, args...)
	if err != nil {
		return Check{Name: name, Status: Error, Evidence: err.Error(), Fix: "inspect the exact QEMU binary and its help output"}
	}
	output := combined(result)
	missing := make([]string, 0)
	for _, needle := range needles {
		if !strings.Contains(output, needle) {
			missing = append(missing, needle)
		}
	}
	if len(missing) > 0 {
		return Check{Name: name, Status: Error, Evidence: "missing: " + strings.Join(missing, ", "), Fix: "use a QEMU build exposing all required native devices/options"}
	}
	return Check{Name: name, Status: OK, Evidence: "found " + strings.Join(needles, ", ")}
}

// Run performs no installation or host mutation.
func (p Probe) Run(ctx context.Context) Report {
	report := Report{OS: runtime.GOOS, Arch: runtime.GOARCH}
	profile, err := platform.Native()
	if err != nil {
		report.Tier = platform.Unsupported.String()
		report.Checks = append(report.Checks, Check{Name: "host", Status: Error, Evidence: err.Error()})
		return report
	}
	report.Tier = profile.Tier.String()
	report.Checks = append(report.Checks, Check{Name: "host", Status: OK, Evidence: fmt.Sprintf("%s/%s %s", profile.OS, profile.Arch, profile.Tier)})

	qemuPath, err := p.lookPath(profile.QEMUBinary)
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "qemu", Status: Error, Evidence: profile.QEMUBinary + " not found", Fix: qemuInstallFix(profile.OS)})
	} else {
		report.Checks = append(report.Checks, Check{Name: "qemu-path", Status: OK, Evidence: qemuPath})
		qemuVersion := ""
		result, versionErr := p.runner().Run(ctx, qemuPath, "--version")
		if versionErr != nil {
			report.Checks = append(report.Checks, Check{Name: "qemu-version", Status: Error, Evidence: versionErr.Error()})
		} else if version, parseErr := platform.ParseQEMUVersion(combined(result)); parseErr != nil {
			report.Checks = append(report.Checks, Check{Name: "qemu-version", Status: Error, Evidence: parseErr.Error()})
		} else if !version.AtLeast(profile.MinimumQEMU) {
			report.Checks = append(report.Checks, Check{Name: "qemu-version", Status: Error, Evidence: version.String() + " is below " + profile.MinimumQEMU.String(), Fix: qemuInstallFix(profile.OS)})
		} else {
			qemuVersion = version.String()
			report.Checks = append(report.Checks, Check{Name: "qemu-version", Status: OK, Evidence: qemuVersion})
		}
		var staticChecks []Check
		var cacheKey capabilityKey
		cacheWritable := false
		if qemuVersion != "" {
			if key, keyErr := capabilityKeyFor(qemuPath, qemuVersion); keyErr != nil {
				report.Checks = append(report.Checks, Check{Name: "capability-cache", Status: Warn, Evidence: keyErr.Error()})
			} else if cached, hit, cacheErr := p.loadCapabilityCache(key); cacheErr != nil {
				report.Checks = append(report.Checks, Check{Name: "capability-cache", Status: Warn, Evidence: cacheErr.Error(), Fix: "inspect or remove only the unsafe per-user capability cache"})
			} else if hit {
				staticChecks = cached
				report.Checks = append(report.Checks, Check{Name: "capability-cache", Status: OK, Evidence: "hit for exact QEMU path, size, mtime, and version"})
			} else {
				cacheKey, cacheWritable = key, true
			}
		}
		if len(staticChecks) == 0 {
			staticChecks = []Check{
				p.capability(ctx, qemuPath, "accelerator", []string{"-accel", "help"}, profile.Accelerator),
				p.capability(ctx, qemuPath, "machine", []string{"-machine", "help"}, profile.Machine),
				p.capability(ctx, qemuPath, "cpu", []string{"-cpu", "help"}, profile.CPU),
				p.capability(ctx, qemuPath, "devices", []string{"-device", "help"}, "virtio-blk-pci", "virtio-net-pci", "virtio-scsi-pci"),
				p.capability(ctx, qemuPath, "netdev", []string{"-machine", "none", "-netdev", "help"}, "user"),
			}
			if cacheWritable {
				if writeErr := p.writeCapabilityCache(cacheKey, staticChecks); writeErr != nil {
					report.Checks = append(report.Checks, Check{Name: "capability-cache", Status: Warn, Evidence: writeErr.Error()})
				} else {
					report.Checks = append(report.Checks, Check{Name: "capability-cache", Status: OK, Evidence: "refreshed after exact QEMU path, size, mtime, or version miss"})
				}
			}
		}
		report.Checks = append(report.Checks, staticChecks...)
		if evidence, smokeErr := p.acceleratorSmoke(ctx, qemuPath, profile); smokeErr != nil {
			report.Checks = append(report.Checks, Check{Name: profile.Accelerator + "-real-smoke", Status: Error, Evidence: smokeErr.Error(), Fix: "verify the native accelerator entitlement/device and retry doctor"})
		} else {
			report.Checks = append(report.Checks, Check{Name: profile.Accelerator + "-real-smoke", Status: OK, Evidence: evidence})
		}
	}

	qemuImgPath, err := p.lookPath("qemu-img")
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "qemu-img", Status: Error, Evidence: "qemu-img not found", Fix: qemuInstallFix(profile.OS)})
	} else {
		result, versionErr := p.runner().Run(ctx, qemuImgPath, "--version")
		if versionErr != nil {
			report.Checks = append(report.Checks, Check{Name: "qemu-img", Status: Error, Evidence: versionErr.Error()})
		} else {
			report.Checks = append(report.Checks, Check{Name: "qemu-img", Status: OK, Evidence: qemuImgPath + ": " + strings.Split(combined(result), "\n")[0]})
		}
	}

	if profile.RequiresUEFI {
		firmware, firmwareErr := platform.FindFirmware(profile)
		if firmwareErr != nil {
			report.Checks = append(report.Checks, Check{Name: "firmware", Status: Error, Evidence: "no matching arm64 UEFI code/vars pair found", Fix: qemuInstallFix(profile.OS)})
		} else {
			report.Checks = append(report.Checks, Check{Name: "firmware", Status: OK, Evidence: firmware.Code + " + " + firmware.Vars})
		}
	}

	if profile.OS == "linux" {
		file, openErr := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if openErr != nil {
			report.Checks = append(report.Checks, Check{Name: "kvm", Status: Error, Evidence: openErr.Error(), Fix: "enable KVM and grant the invoking user read/write access to /dev/kvm"})
		} else {
			_ = file.Close()
			report.Checks = append(report.Checks, Check{Name: "kvm", Status: OK, Evidence: "/dev/kvm is readable and writable"})
		}
	}

	if sshPath, sshErr := p.lookPath("ssh"); sshErr != nil {
		report.Checks = append(report.Checks, Check{Name: "ssh", Status: Error, Evidence: "OpenSSH client not found", Fix: "install an OpenSSH client"})
	} else {
		report.Checks = append(report.Checks, Check{Name: "ssh", Status: OK, Evidence: filepath.Clean(sshPath)})
	}
	report.Checks = append(report.Checks, p.projectChecks()...)
	if profile.OS == "darwin" {
		report.Checks = append(report.Checks, p.networkPreflightChecks(ctx, profile)...)
	} else if profile.OS == "linux" {
		report.Checks = append(report.Checks, p.linuxPrivateChecks(ctx)...)
		report.Checks = append(report.Checks, p.networkPreflightChecks(ctx, profile)...)
	}
	return report
}
