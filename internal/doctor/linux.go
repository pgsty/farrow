package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	linuxnet "github.com/pgsty/farrow/internal/network/linux"
	"github.com/pgsty/farrow/internal/platform"
)

func parseLinuxFamily(content string) linuxnet.Family {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "debian") || strings.Contains(lower, "ubuntu") {
		return linuxnet.Debian
	}
	if strings.Contains(lower, "rhel") || strings.Contains(lower, "fedora") || strings.Contains(lower, "rocky") || strings.Contains(lower, "almalinux") || strings.Contains(lower, "centos") {
		return linuxnet.RPM
	}
	return ""
}

func unixMode(info os.FileInfo) uint32 {
	mode := uint32(info.Mode().Perm())
	if info.Mode()&os.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if info.Mode()&os.ModeSetgid != 0 {
		mode |= 0o2000
	}
	return mode
}

func helperCheck(pathname string, family linuxnet.Family) Check {
	info, err := os.Lstat(pathname)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Check{Name: "bridge-helper", Status: Error, Evidence: "qemu-bridge-helper is missing or unsafe", Fix: "install the distribution QEMU bridge helper; do not substitute an arbitrary executable"}
	}
	statistics, ok := info.Sys().(*syscall.Stat_t)
	if !ok || statistics.Uid != 0 {
		return Check{Name: "bridge-helper", Status: Error, Evidence: pathname + " is not root-owned", Fix: "restore the distribution package helper ownership"}
	}
	mode := unixMode(info)
	groupName := strconv.FormatUint(uint64(statistics.Gid), 10)
	if group, lookupErr := user.LookupGroupId(groupName); lookupErr == nil {
		groupName = group.Name
	}
	switch family {
	case linuxnet.Debian:
		if mode == 0o4750 {
			if platform.CurrentProcessInGroup(statistics.Gid) {
				return Check{Name: "bridge-helper", Status: OK, Evidence: fmt.Sprintf("%s root:%s mode=%04o and executable by the invoking user", pathname, groupName, mode)}
			}
			return Check{Name: "bridge-helper", Status: Error, Evidence: fmt.Sprintf("%s is root:%s mode=4750 but the invoking user is not in group %s", pathname, groupName, groupName), Fix: "uninstall the Farrow network as its owner, then reinstall it for this user"}
		}
		primaryGroup := strconv.FormatInt(int64(os.Getgid()), 10)
		if group, lookupErr := user.LookupGroupId(primaryGroup); lookupErr == nil {
			primaryGroup = group.Name
		}
		return Check{Name: "bridge-helper", Status: Warn, Evidence: fmt.Sprintf("%s root:%s mode=%04o requires reversible dpkg-statoverride", pathname, groupName, mode), Fix: fmt.Sprintf("network install must prove no non-Farrow override before applying root:%s 4750", primaryGroup)}
	case linuxnet.RPM:
		if mode == 0o4755 {
			return Check{Name: "bridge-helper", Status: Warn, Evidence: fmt.Sprintf("%s root:%s mode=4755 permits every local user to request an allowed bridge attach", pathname, groupName)}
		}
		return Check{Name: "bridge-helper", Status: Error, Evidence: fmt.Sprintf("unsupported RPM helper mode %04o", mode), Fix: "restore the distribution helper; Farrow does not mutate RPM helper permissions"}
	default:
		return Check{Name: "bridge-helper", Status: Error, Evidence: "unsupported Linux distribution family for private helper policy"}
	}
}

func protectedLinuxStateDir() bool {
	info, err := os.Lstat("/var/lib/farrow")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return false
	}
	statistics, ok := info.Sys().(*syscall.Stat_t)
	return ok && statistics.Uid == 0
}

func (p Probe) linuxPrivateChecks(ctx context.Context) []Check {
	checks := make([]Check, 0, 5)
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil || len(osRelease) > 64<<10 {
		checks = append(checks, Check{Name: "linux-family", Status: Error, Evidence: "cannot read bounded /etc/os-release"})
		return checks
	}
	family := parseLinuxFamily(string(osRelease))
	if family == "" {
		checks = append(checks, Check{Name: "linux-family", Status: Error, Evidence: "distribution is outside Debian/RPM v1 support boundary"})
	} else {
		checks = append(checks, Check{Name: "linux-family", Status: OK, Evidence: string(family)})
	}

	systemctl, systemctlErr := p.lookPath("systemctl")
	if systemctlErr != nil {
		checks = append(checks, Check{Name: "linux-network-owner", Status: Error, Evidence: "systemctl not found", Fix: "Linux private networking requires systemd"})
	} else {
		serviceActive := func(name string) bool {
			result, runErr := p.runner().Run(ctx, systemctl, "is-active", name)
			return runErr == nil && strings.TrimSpace(string(result.Stdout)) == "active"
		}
		switch {
		case serviceActive("NetworkManager.service"):
			checks = append(checks, Check{Name: "linux-network-owner", Status: OK, Evidence: "NetworkManager owns the host network (nmcli backend)"})
		case serviceActive("systemd-networkd.service"):
			checks = append(checks, Check{Name: "linux-network-owner", Status: OK, Evidence: "systemd-networkd owns the host network"})
		default:
			checks = append(checks, Check{Name: "linux-network-owner", Status: Error, Evidence: "neither NetworkManager nor systemd-networkd is active", Fix: "activate the distribution network manager; farrow follows the active owner"})
		}
	}

	helperPath := ""
	for _, candidate := range []string{"/usr/lib/qemu/qemu-bridge-helper", "/usr/libexec/qemu-bridge-helper"} {
		if _, err := os.Lstat(candidate); err == nil {
			helperPath = candidate
			break
		}
	}
	if helperPath == "" {
		checks = append(checks, Check{Name: "bridge-helper", Status: Error, Evidence: "no supported qemu-bridge-helper path found"})
	} else {
		checks = append(checks, helperCheck(helperPath, family))
	}

	ipBinary, ipErr := p.lookPath("ip")
	stateExists := false
	stateProtected := false
	if stateInfo, stateErr := os.Lstat(linuxnet.StatePath); stateErr == nil && stateInfo.Mode().IsRegular() {
		stateExists = true
	} else if errors.Is(stateErr, os.ErrPermission) && protectedLinuxStateDir() {
		stateExists = true
		stateProtected = true
	}
	if ipErr != nil {
		checks = append(checks, Check{Name: "private-bridge", Status: Error, Evidence: "ip command not found"})
	} else {
		bridgeResult, bridgeErr := p.runner().Run(ctx, ipBinary, "-json", "link", "show", "dev", linuxnet.BridgeName)
		bridgeExists := bridgeErr == nil && len(strings.TrimSpace(string(bridgeResult.Stdout))) > 2
		if bridgeExists && !stateExists {
			checks = append(checks, Check{Name: "private-bridge", Status: Error, Evidence: "farrow0 exists without the root-owned Farrow manifest", Fix: "do not adopt or overwrite the existing bridge"})
		} else if bridgeExists && stateProtected {
			checks = append(checks, Check{Name: "private-bridge", Status: Warn, Evidence: "farrow0 exists and its mode-0700 ownership state is intentionally not inspectable by this user", Fix: "run the same read-only status command as root to verify network.json metadata"})
		} else if bridgeExists {
			checks = append(checks, Check{Name: "private-bridge", Status: OK, Evidence: "farrow0 and ownership manifest exist; install/status must verify exact metadata"})
		} else {
			checks = append(checks, Check{Name: "private-bridge", Status: OK, Evidence: "farrow0 is absent"})
		}
	}
	return checks
}
