// Package sshconfig owns the narrowly marked OpenSSH Include integration.
package sshconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/openssh"
	"golang.org/x/sys/unix"
)

const maxConfigBytes = 1 << 20

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
var identityPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{7,63}$`)
var aliasPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,252}$`)

func safeOpenSSHPath(value string) bool {
	return filepath.IsAbs(value) && !strings.ContainsAny(value, "\r\n\x00$%*?[]")
}

type Entry struct {
	ProjectID  string
	Name       string
	Node       string
	Aliases    []string
	User       string
	Host       string
	Port       uint16
	Identity   string
	KnownHosts string
}

type Result struct {
	Fragment string `json:"fragment"`
	Config   string `json:"config"`
	Changed  bool   `json:"changed"`
	Action   string `json:"action"`
}

func validateEntry(entry Entry) error {
	if !identityPattern.MatchString(entry.ProjectID) || !namePattern.MatchString(entry.Name) || !namePattern.MatchString(entry.Node) || !namePattern.MatchString(entry.User) || strings.ContainsAny(entry.Host, "\r\n\x00$%") || entry.Host == "" || entry.Port == 0 || !safeOpenSSHPath(entry.Identity) || !safeOpenSSHPath(entry.KnownHosts) {
		return errors.New("SSH config entry identity, name, connection, or paths are invalid")
	}
	for _, alias := range entry.Aliases {
		if !aliasPattern.MatchString(alias) {
			return fmt.Errorf("SSH config alias %q is invalid", alias)
		}
	}
	return nil
}

func markers(projectID string) (string, string, string) {
	begin := "# piglet:" + projectID + ":begin"
	end := "# piglet:" + projectID + ":end"
	include := "# piglet:" + projectID + ":include"
	return begin, end, include
}

func render(entries []Entry) (string, error) {
	begin, end, _ := markers(entries[0].ProjectID)
	var output strings.Builder
	output.WriteString(begin)
	output.WriteByte('\n')
	for index, entry := range entries {
		identity, err := openssh.QuoteConfigValue(entry.Identity)
		if err != nil {
			return "", err
		}
		knownHosts, err := openssh.QuoteConfigValue(entry.KnownHosts)
		if err != nil {
			return "", err
		}
		patterns := make([]string, 0, len(entry.Aliases)+1)
		seen := make(map[string]struct{}, len(entry.Aliases)+1)
		for _, pattern := range append([]string{entry.Name + "-" + entry.Node}, entry.Aliases...) {
			if _, exists := seen[pattern]; exists {
				continue
			}
			seen[pattern] = struct{}{}
			patterns = append(patterns, pattern)
		}
		fmt.Fprintf(&output, "Host %s\n  HostName %s\n  User %s\n  Port %d\n  IdentityFile %s\n  UserKnownHostsFile %s\n  IdentitiesOnly yes\n  StrictHostKeyChecking accept-new\n", strings.Join(patterns, " "), entry.Host, entry.User, entry.Port, identity, knownHosts)
		if index != len(entries)-1 {
			output.WriteByte('\n')
		}
	}
	output.WriteString(end)
	output.WriteByte('\n')
	return output.String(), nil
}

func globalInsertOffset(config string) int {
	offset := 0
	for _, line := range strings.SplitAfter(config, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 && (strings.EqualFold(fields[0], "Host") || strings.EqualFold(fields[0], "Match")) {
				return offset
			}
		}
		offset += len(line)
	}
	return len(config)
}

func installGlobalBlock(config, block string) string {
	without := strings.Replace(config, block, "", 1)
	offset := globalInsertOffset(without)
	prefix, suffix := without[:offset], without[offset:]
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + block + suffix
}

func ensureSSHDir(home string) (string, error) {
	if home == "" || !safeOpenSSHPath(home) {
		return "", errors.New("SSH integration home must be absolute")
	}
	homeInfo, err := os.Lstat(home)
	if err != nil {
		return "", errors.New("SSH integration home is not a real directory")
	}
	homeStat, homeStatOK := homeInfo.Sys().(*syscall.Stat_t)
	if !homeInfo.IsDir() || homeInfo.Mode()&os.ModeSymlink != 0 || !homeStatOK || int(homeStat.Uid) != os.Geteuid() {
		return "", errors.New("SSH integration home is not a real directory")
	}
	directory := filepath.Join(home, ".ssh")
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", errors.New("~/.ssh is missing, unsafe, or writable by other users")
	}
	stat, statOK := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || !statOK || int(stat.Uid) != os.Geteuid() {
		return "", errors.New("~/.ssh is missing, unsafe, or writable by other users")
	}
	handle, err := os.Open(directory)
	if err != nil {
		return "", err
	}
	opened, statErr := handle.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = handle.Close()
		return "", errors.New("~/.ssh identity changed while opening")
	}
	if err := handle.Chmod(0o700); err != nil {
		_ = handle.Close()
		return "", err
	}
	if err := handle.Close(); err != nil {
		return "", err
	}
	return directory, nil
}

func acquireLock(directory string) (func(), error) {
	path := filepath.Join(directory, ".piglet.lock")
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = unix.Close(fd)
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 || stat.Mode&0o777 != 0o600 {
		return nil, errors.New("piglet SSH integration lock metadata is unsafe")
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return nil, err
	}
	closeOnError = false
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = unix.Close(fd)
	}, nil
}

func wrongMode(pathname string, exists bool, mode os.FileMode) (bool, error) {
	if !exists {
		return false, nil
	}
	info, err := os.Lstat(pathname)
	if err != nil {
		return false, err
	}
	return info.Mode().Perm() != mode.Perm(), nil
}

func readOptionalRegular(pathname string) ([]byte, bool, error) {
	info, err := os.Lstat(pathname)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	stat, statOK := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxConfigBytes || !statOK || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 {
		return nil, false, errors.New("SSH config target is not a bounded regular non-symlink file")
	}
	handle, err := os.Open(pathname)
	if err != nil {
		return nil, false, err
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, false, errors.New("SSH config target identity changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxConfigBytes+1))
	if err != nil || len(data) > maxConfigBytes {
		return nil, false, errors.New("SSH config exceeds 1 MiB limit")
	}
	return data, true, nil
}

func requireUnchanged(pathname string, expected []byte, expectedExists bool) error {
	actual, exists, err := readOptionalRegular(pathname)
	if err != nil {
		return err
	}
	if exists != expectedExists || !bytes.Equal(actual, expected) {
		return fmt.Errorf("SSH integration target changed after validation: %s", pathname)
	}
	return nil
}

func Install(home string, entry Entry) (Result, error) {
	return InstallMany(home, []Entry{entry})
}

// InstallMany atomically publishes one marker-owned fragment containing every
// node in a project and one marker-owned Include in the user's main config.
func InstallMany(home string, entries []Entry) (Result, error) {
	if len(entries) == 0 {
		return Result{}, errors.New("SSH config install requires at least one entry")
	}
	entry := entries[0]
	seenNodes := make(map[string]struct{}, len(entries))
	for _, candidate := range entries {
		if err := validateEntry(candidate); err != nil {
			return Result{}, err
		}
		if candidate.ProjectID != entry.ProjectID || candidate.Name != entry.Name {
			return Result{}, errors.New("SSH config entries must belong to one project and fragment name")
		}
		if _, exists := seenNodes[candidate.Node]; exists {
			return Result{}, fmt.Errorf("duplicate SSH config node %q", candidate.Node)
		}
		seenNodes[candidate.Node] = struct{}{}
	}
	directory, err := ensureSSHDir(home)
	if err != nil {
		return Result{}, err
	}
	releaseLock, err := acquireLock(directory)
	if err != nil {
		return Result{}, err
	}
	defer releaseLock()
	fragment := filepath.Join(directory, entry.Name+"_config")
	configPath := filepath.Join(directory, "config")
	content, err := render(entries)
	if err != nil {
		return Result{}, err
	}
	begin, end, includeMarker := markers(entry.ProjectID)
	existingFragment, fragmentExists, err := readOptionalRegular(fragment)
	if err != nil {
		return Result{}, err
	}
	if fragmentExists {
		trimmed := strings.TrimSuffix(string(existingFragment), "\n")
		if !strings.HasPrefix(trimmed, begin+"\n") || !strings.HasSuffix(trimmed, "\n"+end) || strings.Count(trimmed, begin) != 1 || strings.Count(trimmed, end) != 1 {
			return Result{}, errors.New("refuse overwrite of SSH fragment without exact Piglet ownership markers")
		}
	}
	fragmentModeChanged, err := wrongMode(fragment, fragmentExists, 0o600)
	if err != nil {
		return Result{}, err
	}
	config, configExists, err := readOptionalRegular(configPath)
	if err != nil {
		return Result{}, err
	}
	configModeChanged, err := wrongMode(configPath, configExists, 0o600)
	if err != nil {
		return Result{}, err
	}
	quotedFragment, err := openssh.QuoteConfigValue(fragment)
	if err != nil {
		return Result{}, err
	}
	includeLine := "Include " + quotedFragment
	configText := string(config)
	block := includeMarker + "\n" + includeLine + "\n"
	markerCount := strings.Count(configText, includeMarker)
	blockCount := strings.Count(configText, block)
	if markerCount > 1 || blockCount > 1 || markerCount != blockCount {
		return Result{}, errors.New("refuse adoption of malformed Piglet SSH Include block")
	}
	if strings.Contains(configText, includeLine) && blockCount == 0 {
		return Result{}, errors.New("refuse adoption of unmarked matching SSH Include")
	}
	fragmentChanged := !fragmentExists || string(existingFragment) != content
	canonicalConfig := installGlobalBlock(configText, block)
	configChanged := canonicalConfig != configText
	configText = canonicalConfig
	fragmentWillWrite := fragmentChanged || fragmentModeChanged
	configWillWrite := configChanged || configModeChanged || !configExists
	if fragmentWillWrite || configWillWrite {
		if err := requireUnchanged(fragment, existingFragment, fragmentExists); err != nil {
			return Result{}, err
		}
		if err := requireUnchanged(configPath, config, configExists); err != nil {
			return Result{}, err
		}
	}
	if fragmentWillWrite {
		if err := fsutil.AtomicWrite(fragment, []byte(content), 0o600); err != nil {
			return Result{}, err
		}
	}
	if configWillWrite {
		if err := requireUnchanged(fragment, []byte(content), true); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: fragmentWillWrite, Action: "install-partial"}, err
		}
		if err := requireUnchanged(configPath, config, configExists); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: fragmentWillWrite, Action: "install-partial"}, err
		}
		if err := fsutil.AtomicWrite(configPath, []byte(configText), 0o600); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: fragmentWillWrite, Action: "install-partial"}, err
		}
	}
	changed := fragmentChanged || fragmentModeChanged || configChanged || configModeChanged || !configExists
	return Result{Fragment: fragment, Config: configPath, Changed: changed, Action: "install"}, nil
}

func Remove(home, projectID, name string) (Result, error) {
	if !identityPattern.MatchString(projectID) || !namePattern.MatchString(name) {
		return Result{}, errors.New("SSH remove project identity or name is invalid")
	}
	directory, err := ensureSSHDir(home)
	if err != nil {
		return Result{}, err
	}
	releaseLock, err := acquireLock(directory)
	if err != nil {
		return Result{}, err
	}
	defer releaseLock()
	fragment := filepath.Join(directory, name+"_config")
	configPath := filepath.Join(directory, "config")
	begin, end, includeMarker := markers(projectID)
	quotedFragment, err := openssh.QuoteConfigValue(fragment)
	if err != nil {
		return Result{}, err
	}
	includeLine := "Include " + quotedFragment
	block := includeMarker + "\n" + includeLine + "\n"
	config, configExists, err := readOptionalRegular(configPath)
	if err != nil {
		return Result{}, err
	}
	fragmentData, fragmentExists, err := readOptionalRegular(fragment)
	if err != nil {
		return Result{}, err
	}
	if fragmentExists {
		trimmed := strings.TrimSuffix(string(fragmentData), "\n")
		if !strings.HasPrefix(trimmed, begin+"\n") || !strings.HasSuffix(trimmed, "\n"+end) || strings.Count(trimmed, begin) != 1 || strings.Count(trimmed, end) != 1 {
			return Result{}, errors.New("refuse removal of SSH fragment without exact Piglet ownership markers")
		}
	}
	configText := string(config)
	if strings.Contains(configText, includeLine) && !strings.Contains(configText, block) {
		return Result{}, errors.New("refuse removal while an unmarked matching SSH Include remains")
	}
	changed := false
	updatedConfig := configText
	configChanged := false
	if configExists && strings.Contains(configText, includeMarker) {
		if strings.Count(string(config), includeMarker) != 1 || strings.Count(string(config), block) != 1 {
			return Result{}, errors.New("refuse removal of malformed Piglet SSH Include block")
		}
		updatedConfig = strings.Replace(configText, block, "", 1)
		configChanged = true
	}
	if configChanged || fragmentExists {
		if err := requireUnchanged(configPath, config, configExists); err != nil {
			return Result{}, err
		}
		if err := requireUnchanged(fragment, fragmentData, fragmentExists); err != nil {
			return Result{}, err
		}
	}
	if configChanged {
		if err := fsutil.AtomicWrite(configPath, []byte(updatedConfig), 0o600); err != nil {
			return Result{}, err
		}
		changed = true
	}
	if fragmentExists {
		if err := requireUnchanged(configPath, []byte(updatedConfig), configExists); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: configChanged, Action: "remove-partial"}, err
		}
		if err := requireUnchanged(fragment, fragmentData, true); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: configChanged, Action: "remove-partial"}, err
		}
		if err := os.Remove(fragment); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: configChanged, Action: "remove-partial"}, err
		}
		if err := fsutil.SyncDir(directory); err != nil {
			return Result{Fragment: fragment, Config: configPath, Changed: true, Action: "remove-partial"}, err
		}
		changed = true
	}
	return Result{Fragment: fragment, Config: configPath, Changed: changed, Action: "remove"}, nil
}
