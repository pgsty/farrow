// Package linux defines the reversible root-owned Linux private bridge plan.
// It does not execute privileged operations.
package linux

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pgsty/farrow/internal/network/subnet"
)

const (
	BridgeName         = "farrow0"
	QEMUConfigDir      = "/etc/qemu"
	BridgeConfPath     = "/etc/qemu/bridge.conf"
	NetDevPath         = "/etc/systemd/network/80-farrow0.netdev"
	NetworkPath        = "/etc/systemd/network/80-farrow0.network"
	NetworkManagerPath = "/etc/NetworkManager/conf.d/90-farrow-unmanaged.conf"
	TmpfilesPath       = "/etc/tmpfiles.d/farrow.conf"
	StatePath          = "/var/lib/farrow/network.json"
	LeaseRoot          = "/run/farrow"
	LeaseLockPath      = "/run/farrow/private-lease.lock"
	markerBegin        = "# BEGIN FARROW MANAGED: farrow0"
	markerEnd          = "# END FARROW MANAGED: farrow0"
)

var NetworkdUnitNames = []string{
	"systemd-networkd.service",
	"systemd-networkd.socket",
	"systemd-network-generator.service",
	"systemd-networkd-wait-online.service",
}

type Family string

const (
	Debian Family = "debian"
	RPM    Family = "rpm"
)

type Override struct {
	Owner string `json:"owner"`
	Group string `json:"group"`
	Mode  string `json:"mode"`
}

type UnitState struct {
	LoadState     string `json:"load_state"`
	UnitFileState string `json:"unit_file_state"`
	ActiveState   string `json:"active_state"`
	SubState      string `json:"sub_state"`
}

// NetworkdLink is the bounded link identity used to prove that starting an
// inactive systemd-networkd cannot claim a pre-existing non-Farrow link. The
// planned farrow0 identity is included even when the bridge does not yet exist
// so an earlier, unowned .network file cannot win systemd's first-match rule.
type NetworkdLink struct {
	Name             string   `json:"name"`
	AlternativeNames []string `json:"alternative_names,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	Type             string   `json:"type"`
	FarrowOwned      bool     `json:"farrow_owned,omitempty"`
}

// NetworkdConfiguration records an effective .network or .netdev identity and
// the supported positive match predicates from .network files. A predicate is
// retained only when Farrow can parse it exactly; unsupported predicates can
// never be used as safety proof.
type NetworkdConfiguration struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	MatchNames []string `json:"match_names,omitempty"`
	MatchTypes []string `json:"match_types,omitempty"`
}

type NetworkdActivationConflict struct {
	Path   string `json:"path"`
	Link   string `json:"link,omitempty"`
	Reason string `json:"reason"`
}

// NetworkdActivationSafety is populated only by pre-mutation discovery when
// systemd-networkd is inactive. Conflicts are deliberately typed so the plan
// can fail closed without starting networkd and attempting post-hoc repair.
type NetworkdActivationSafety struct {
	Checked        bool                         `json:"checked"`
	Links          []NetworkdLink               `json:"links"`
	Configurations []NetworkdConfiguration      `json:"configurations,omitempty"`
	Conflicts      []NetworkdActivationConflict `json:"conflicts,omitempty"`
}

type PathState struct {
	Existed bool   `json:"existed"`
	Owner   string `json:"owner,omitempty"`
	Group   string `json:"group,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type HelperFacts struct {
	Path         string
	OwnerUID     int
	Group        string
	Mode         uint32
	Regular      bool
	Symlink      bool
	ParentSafe   bool
	PackageOwned bool
	Override     *Override
}

type Facts struct {
	Family               Family
	Systemd              bool
	NetworkdActive       bool
	NetworkdUnits        map[string]UnitState
	NetworkdActivation   *NetworkdActivationSafety
	NetworkManagerActive bool
	BridgeExists         bool
	BridgeOwned          bool
	BridgeConf           string
	BridgeConfState      PathState
	QEMUConfigDirExisted bool
	Helper               HelperFacts
	ExistingManifest     *Manifest
}

type Config struct {
	CIDR        string
	HostAddress string
	DHCPEnd     string
}

func ConfigForCIDR(cidr string) (Config, error) {
	layout, err := subnet.Parse(cidr)
	if err != nil {
		return Config{}, err
	}
	return Config{CIDR: layout.CIDR(), HostAddress: layout.HostAddress(), DHCPEnd: layout.DHCPEnd()}, nil
}

type File struct {
	Path    string `json:"path"`
	Owner   string `json:"owner"`
	Mode    string `json:"mode"`
	Content string `json:"content"`
}

type Directory struct {
	Path  string `json:"path"`
	Owner string `json:"owner"`
	Mode  string `json:"mode"`
}

type Command struct {
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
}

type Manifest struct {
	Schema             int                  `json:"schema"`
	Family             Family               `json:"family"`
	Bridge             string               `json:"bridge"`
	CIDR               string               `json:"cidr"`
	HostAddress        string               `json:"host_address"`
	DHCPEnd            string               `json:"dhcp_end"`
	HelperPath         string               `json:"helper_path"`
	OriginalHelper     Override             `json:"original_helper"`
	OriginalBridgeConf string               `json:"original_bridge_conf"`
	OriginalBridgePath PathState            `json:"original_bridge_path"`
	QEMUConfigCreated  bool                 `json:"qemu_config_created"`
	NetworkdUnits      map[string]UnitState `json:"networkd_units"`
	AppliedOverride    *Override            `json:"applied_override,omitempty"`
	Files              map[string]string    `json:"files"`
	NetworkManager     bool                 `json:"network_manager"`
	LeaseRoot          string               `json:"lease_root"`
}

type Plan struct {
	Manifest           Manifest                  `json:"manifest"`
	NetworkdActivation *NetworkdActivationSafety `json:"networkd_activation,omitempty"`
	Directories        []Directory               `json:"directories"`
	Files              []File                    `json:"files"`
	Commands           []Command                 `json:"commands"`
	Phases             []CommandPhase            `json:"phases"`
	Warnings           []string                  `json:"warnings,omitempty"`
}

type CommandPhase struct {
	Name     string    `json:"name"`
	Commands []Command `json:"commands"`
}

func validateConfig(config Config) error {
	layout, err := subnet.Parse(config.CIDR)
	if err != nil {
		return err
	}
	if config.HostAddress != layout.HostAddress() || config.DHCPEnd != layout.DHCPEnd() {
		return fmt.Errorf("linux private network %s requires host %s and DHCP end %s", layout.CIDR(), layout.HostAddress(), layout.DHCPEnd())
	}
	return nil
}

func validateNetworkdUnits(units map[string]UnitState) error {
	if len(units) != len(NetworkdUnitNames) {
		return errors.New("networkd prestate must contain the four exact units")
	}
	for _, name := range NetworkdUnitNames {
		state, ok := units[name]
		if !ok || state.LoadState != "loaded" || state.SubState == "" {
			return fmt.Errorf("networkd unit %s has incomplete prestate", name)
		}
		if state.UnitFileState != "enabled" && state.UnitFileState != "disabled" {
			return fmt.Errorf("networkd unit %s has unsupported unit-file state %q", name, state.UnitFileState)
		}
		if state.ActiveState != "active" && state.ActiveState != "inactive" {
			return fmt.Errorf("networkd unit %s has unsupported active state %q", name, state.ActiveState)
		}
	}
	return nil
}

func validateNetworkdActivationSafety(safety *NetworkdActivationSafety) error {
	if safety == nil || !safety.Checked {
		return errors.New("refuse to start inactive systemd-networkd without a pre-mutation activation safety proof")
	}
	if len(safety.Links) == 0 {
		return errors.New("networkd activation safety proof contains no link inventory")
	}
	plannedBridge := false
	seenLinks := make(map[string]struct{}, len(safety.Links))
	for _, link := range safety.Links {
		if link.Name == "" || link.Type == "" {
			return errors.New("networkd activation safety proof contains an incomplete link identity")
		}
		if _, exists := seenLinks[link.Name]; exists {
			return fmt.Errorf("networkd activation safety proof repeats link %s", link.Name)
		}
		seenLinks[link.Name] = struct{}{}
		if link.Name == BridgeName && link.FarrowOwned {
			plannedBridge = true
		}
	}
	if !plannedBridge {
		return errors.New("networkd activation safety proof omits the planned Farrow bridge identity")
	}
	for _, configuration := range safety.Configurations {
		if configuration.Path == "" || (configuration.Kind != "network" && configuration.Kind != "netdev") {
			return errors.New("networkd activation safety proof contains an invalid configuration identity")
		}
		if configuration.Kind == "netdev" {
			return fmt.Errorf("refuse to start inactive systemd-networkd: %s may create or change a virtual link", configuration.Path)
		}
		for _, patterns := range [][]string{configuration.MatchNames, configuration.MatchTypes} {
			for _, pattern := range patterns {
				if !supportedMatchPattern(pattern) {
					return fmt.Errorf("networkd activation safety proof contains an unsupported match pattern in %s", configuration.Path)
				}
			}
		}
		for _, link := range safety.Links {
			if configurationCouldMatch(configuration, link) {
				return fmt.Errorf("refuse to start inactive systemd-networkd: %s could affect link %s", configuration.Path, link.Name)
			}
		}
	}
	if len(safety.Conflicts) != 0 {
		conflict := safety.Conflicts[0]
		if conflict.Path == "" || conflict.Reason == "" {
			return errors.New("networkd activation safety proof contains an incomplete conflict")
		}
		if conflict.Link != "" {
			return fmt.Errorf("refuse to start inactive systemd-networkd: %s could affect link %s: %s", conflict.Path, conflict.Link, conflict.Reason)
		}
		return fmt.Errorf("refuse to start inactive systemd-networkd: %s: %s", conflict.Path, conflict.Reason)
	}
	return nil
}

func copyUnitStates(source map[string]UnitState) map[string]UnitState {
	result := make(map[string]UnitState, len(source))
	for name, state := range source {
		result[name] = state
	}
	return result
}

func validateBridgePathState(state PathState) error {
	if !state.Existed {
		if state.Owner != "" || state.Group != "" || state.Mode != "" {
			return errors.New("absent bridge.conf path has unexpected metadata")
		}
		return nil
	}
	if state.Owner != "root" || state.Group == "" || state.Mode == "" {
		return errors.New("existing bridge.conf must have root ownership metadata")
	}
	return nil
}

func managedBridgeBlock() string {
	return markerBegin + "\nallow " + BridgeName + "\n" + markerEnd + "\n"
}

func ReconcileBridgeConf(existing string, install bool) (string, error) {
	beginCount := strings.Count(existing, markerBegin)
	endCount := strings.Count(existing, markerEnd)
	if beginCount != endCount || beginCount > 1 {
		return "", errors.New("qemu bridge.conf has malformed or duplicate Farrow markers")
	}
	block := managedBridgeBlock()
	if beginCount == 1 {
		if !strings.Contains(existing, block) {
			return "", errors.New("qemu bridge.conf Farrow block was modified")
		}
		if install {
			return existing, nil
		}
		return strings.Replace(existing, block, "", 1), nil
	}
	if !install {
		return existing, nil
	}
	for _, line := range strings.Split(existing, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "allow" && fields[1] == BridgeName {
			return "", errors.New("refuse adoption of unmarked allow farrow0 bridge rule")
		}
	}
	result := existing
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if result != "" && !strings.HasSuffix(result, "\n\n") {
		result += "\n"
	}
	return result + block, nil
}

func fileDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func validateHelper(facts Facts) (*Override, []Command, []string, error) {
	helper := facts.Helper
	if (helper.Path != "/usr/lib/qemu/qemu-bridge-helper" && helper.Path != "/usr/libexec/qemu-bridge-helper") || !filepath.IsAbs(helper.Path) || helper.OwnerUID != 0 || !helper.Regular || helper.Symlink || !helper.ParentSafe || !helper.PackageOwned {
		return nil, nil, nil, errors.New("qemu-bridge-helper must be the package-owned regular non-symlink file below safe root-owned parents")
	}
	switch facts.Family {
	case Debian:
		desired := &Override{Owner: "root", Group: "kvm", Mode: "4750"}
		if helper.Override != nil {
			if facts.ExistingManifest == nil || facts.ExistingManifest.AppliedOverride == nil || *helper.Override != *facts.ExistingManifest.AppliedOverride {
				return nil, nil, nil, errors.New("refuse Debian helper mutation with a non-Farrow dpkg-statoverride")
			}
			return desired, nil, nil, nil
		}
		if helper.Group == "kvm" && helper.Mode == 0o4750 {
			return nil, nil, nil, nil
		}
		command := Command{Binary: "/usr/bin/dpkg-statoverride", Args: []string{"--update", "--add", "root", "kvm", "4750", helper.Path}}
		return desired, []Command{command}, nil, nil
	case RPM:
		if helper.Mode != 0o4755 {
			return nil, nil, nil, fmt.Errorf("RPM helper mode is %04o, want distribution-owned 4755", helper.Mode)
		}
		return nil, nil, []string{"RPM qemu-bridge-helper mode 4755 permits every local user to request an allowed bridge attach"}, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported linux family %q", facts.Family)
	}
}

func NewInstallPlan(facts Facts, config Config) (Plan, error) {
	if err := validateConfig(config); err != nil {
		return Plan{}, err
	}
	if !facts.Systemd {
		return Plan{}, errors.New("linux private v1 requires systemd")
	}
	if err := validateNetworkdUnits(facts.NetworkdUnits); err != nil {
		return Plan{}, err
	}
	if err := validateBridgePathState(facts.BridgeConfState); err != nil {
		return Plan{}, err
	}
	if facts.BridgeExists && !facts.BridgeOwned {
		return Plan{}, errors.New("refuse adoption of existing unowned farrow0 bridge")
	}
	serviceState := facts.NetworkdUnits["systemd-networkd.service"]
	if serviceState.ActiveState != "active" {
		if err := validateNetworkdActivationSafety(facts.NetworkdActivation); err != nil {
			return Plan{}, err
		}
	}
	bridgeConf, err := ReconcileBridgeConf(facts.BridgeConf, true)
	if err != nil {
		return Plan{}, err
	}
	appliedOverride, helperCommands, warnings, err := validateHelper(facts)
	if err != nil {
		return Plan{}, err
	}
	netdev := "[NetDev]\nName=farrow0\nKind=bridge\n"
	network := fmt.Sprintf("[Match]\nName=farrow0\n\n[Network]\nAddress=%s/%s\nConfigureWithoutCarrier=yes\nLinkLocalAddressing=no\nIPv6AcceptRA=no\n\n[Link]\nRequiredForOnline=no\n", config.HostAddress, strings.Split(config.CIDR, "/")[1])
	files := []File{
		{Path: BridgeConfPath, Owner: "root:root", Mode: "0644", Content: bridgeConf},
		{Path: NetDevPath, Owner: "root:root", Mode: "0644", Content: netdev},
		{Path: NetworkPath, Owner: "root:root", Mode: "0644", Content: network},
		{Path: TmpfilesPath, Owner: "root:root", Mode: "0644", Content: "d /run/farrow 1777 root root -\n"},
		{Path: LeaseLockPath, Owner: "root:root", Mode: "0666", Content: ""},
	}
	if facts.NetworkManagerActive {
		files = append(files, File{Path: NetworkManagerPath, Owner: "root:root", Mode: "0644", Content: "[keyfile]\nunmanaged-devices=interface-name:farrow0\n"})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	originalHelper := Override{Owner: "root", Group: facts.Helper.Group, Mode: fmt.Sprintf("%04o", facts.Helper.Mode)}
	originalBridgeConf := facts.BridgeConf
	originalBridgePath := facts.BridgeConfState
	qemuConfigCreated := !facts.QEMUConfigDirExisted
	originalUnits := copyUnitStates(facts.NetworkdUnits)
	if facts.ExistingManifest != nil {
		if err := validateManifest(*facts.ExistingManifest); err != nil {
			return Plan{}, fmt.Errorf("existing Linux network manifest: %w", err)
		}
		existing := facts.ExistingManifest
		if existing.Family != facts.Family || existing.CIDR != config.CIDR || existing.HostAddress != config.HostAddress || existing.DHCPEnd != config.DHCPEnd || existing.HelperPath != facts.Helper.Path || existing.NetworkManager != facts.NetworkManagerActive {
			return Plan{}, errors.New("existing Linux network manifest does not match requested install")
		}
		originalHelper = existing.OriginalHelper
		originalBridgeConf = existing.OriginalBridgeConf
		originalBridgePath = existing.OriginalBridgePath
		qemuConfigCreated = existing.QEMUConfigCreated
		originalUnits = copyUnitStates(existing.NetworkdUnits)
		appliedOverride = existing.AppliedOverride
	}
	manifest := Manifest{
		Schema: 1, Family: facts.Family, Bridge: BridgeName, CIDR: config.CIDR,
		HostAddress: config.HostAddress, DHCPEnd: config.DHCPEnd, HelperPath: facts.Helper.Path,
		OriginalHelper: originalHelper, OriginalBridgeConf: originalBridgeConf, OriginalBridgePath: originalBridgePath,
		QEMUConfigCreated: qemuConfigCreated, NetworkdUnits: originalUnits,
		AppliedOverride: appliedOverride, Files: make(map[string]string), NetworkManager: facts.NetworkManagerActive, LeaseRoot: LeaseRoot,
	}
	for _, file := range files {
		manifest.Files[file.Path] = fileDigest(file.Content)
	}
	stateBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Plan{}, err
	}
	stateContent := string(append(stateBytes, '\n'))
	files = append(files, File{Path: StatePath, Owner: "root:root", Mode: "0600", Content: stateContent})
	phases := make([]CommandPhase, 0, 4)
	if facts.NetworkManagerActive {
		phases = append(phases, CommandPhase{Name: "network-manager-unmanaged-before-bridge", Commands: []Command{{Binary: "/usr/bin/nmcli", Args: []string{"general", "reload"}}}})
	}
	activate := make([]Command, 0, 3)
	if serviceState.ActiveState != "active" {
		activate = append(activate, Command{Binary: "/usr/bin/systemctl", Args: []string{"start", "systemd-networkd.service"}})
	}
	activate = append(activate, Command{Binary: "/usr/bin/networkctl", Args: []string{"reload"}}, Command{Binary: "/usr/bin/networkctl", Args: []string{"reconfigure", BridgeName}})
	phases = append(phases, CommandPhase{Name: "activate-bridge", Commands: activate})
	privilege := append([]Command(nil), helperCommands...)
	privilege = append(privilege, Command{Binary: "/usr/bin/systemd-tmpfiles", Args: []string{"--create", TmpfilesPath}})
	phases = append(phases, CommandPhase{Name: "helper-and-runtime-boundary", Commands: privilege})
	if serviceState.UnitFileState != "enabled" {
		phases = append(phases, CommandPhase{Name: "persist-after-attach-verification", Commands: []Command{{Binary: "/usr/bin/systemctl", Args: []string{"enable", "systemd-networkd.service"}}}})
	}
	commands := make([]Command, 0)
	for _, phase := range phases {
		commands = append(commands, phase.Commands...)
	}
	directories := []Directory{{Path: "/var/lib/farrow", Owner: "root:root", Mode: "0700"}, {Path: LeaseRoot, Owner: "root:root", Mode: "1777"}}
	if !facts.QEMUConfigDirExisted {
		directories = append([]Directory{{Path: QEMUConfigDir, Owner: "root:root", Mode: "0755"}}, directories...)
	}
	return Plan{Manifest: manifest, NetworkdActivation: facts.NetworkdActivation, Directories: directories, Files: files, Commands: commands, Phases: phases, Warnings: warnings}, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != 1 || manifest.Bridge != BridgeName || manifest.LeaseRoot != LeaseRoot || (manifest.Family != Debian && manifest.Family != RPM) || (manifest.HelperPath != "/usr/lib/qemu/qemu-bridge-helper" && manifest.HelperPath != "/usr/libexec/qemu-bridge-helper") {
		return errors.New("linux network ownership manifest identity is invalid")
	}
	if err := validateConfig(Config{CIDR: manifest.CIDR, HostAddress: manifest.HostAddress, DHCPEnd: manifest.DHCPEnd}); err != nil {
		return err
	}
	if manifest.OriginalHelper.Owner != "root" || manifest.OriginalHelper.Group == "" || len(manifest.OriginalHelper.Mode) != 4 {
		return errors.New("linux network manifest lacks original helper state")
	}
	if err := validateBridgePathState(manifest.OriginalBridgePath); err != nil {
		return err
	}
	if err := validateNetworkdUnits(manifest.NetworkdUnits); err != nil {
		return err
	}
	allowed := map[string]struct{}{BridgeConfPath: {}, NetDevPath: {}, NetworkPath: {}, TmpfilesPath: {}, LeaseLockPath: {}}
	if manifest.NetworkManager {
		allowed[NetworkManagerPath] = struct{}{}
	}
	if len(manifest.Files) != len(allowed) {
		return errors.New("linux network manifest file allowlist size is invalid")
	}
	for pathname, digest := range manifest.Files {
		if _, ok := allowed[pathname]; !ok || len(digest) != 64 {
			return errors.New("linux network manifest file path/digest is invalid")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return err
		}
	}
	return nil
}

func StrictManifest(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return Manifest{}, errors.New("linux network manifest size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("linux network manifest has trailing JSON data")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

type UninstallFacts struct {
	LeaseActive     bool
	BridgeMembers   []string
	CurrentFiles    map[string]string
	CurrentHelper   Override
	CurrentOverride *Override
}

type UninstallPlan struct {
	RestoreFiles      []File         `json:"restore_files"`
	RemoveFiles       []string       `json:"remove_files"`
	RemoveDirectories []string       `json:"remove_directories"`
	Commands          []Command      `json:"commands"`
	Phases            []CommandPhase `json:"phases"`
}

func restoreNetworkdCommands(units map[string]UnitState) []Command {
	commands := []Command{{Binary: "/usr/bin/systemctl", Args: []string{"disable", "systemd-networkd.service"}}}
	if units["systemd-networkd.service"].UnitFileState == "enabled" {
		commands = append(commands, Command{Binary: "/usr/bin/systemctl", Args: []string{"enable", "systemd-networkd.service"}})
	}
	for _, name := range NetworkdUnitNames[1:] {
		verb := "disable"
		if units[name].UnitFileState == "enabled" {
			verb = "enable"
		}
		commands = append(commands, Command{Binary: "/usr/bin/systemctl", Args: []string{verb, name}})
	}
	for _, name := range NetworkdUnitNames {
		verb := "stop"
		if units[name].ActiveState == "active" {
			verb = "start"
		}
		commands = append(commands, Command{Binary: "/usr/bin/systemctl", Args: []string{verb, name}})
	}
	return commands
}

func NewUninstallPlan(manifest Manifest, facts UninstallFacts) (UninstallPlan, error) {
	if err := validateManifest(manifest); err != nil {
		return UninstallPlan{}, err
	}
	if facts.LeaseActive {
		return UninstallPlan{}, errors.New("refuse Linux network uninstall while a private lease is active")
	}
	if len(facts.BridgeMembers) > 0 {
		return UninstallPlan{}, fmt.Errorf("refuse Linux network uninstall while farrow0 has members: %v", facts.BridgeMembers)
	}
	for pathname, expectedDigest := range manifest.Files {
		content, ok := facts.CurrentFiles[pathname]
		if !ok || fileDigest(content) != expectedDigest {
			return UninstallPlan{}, fmt.Errorf("owned Linux network file changed or missing: %s", pathname)
		}
	}
	phases := []CommandPhase{{Name: "delete-bridge-before-owned-files", Commands: []Command{{Binary: "/usr/bin/networkctl", Args: []string{"delete", BridgeName}}}}}
	helperCommands := make([]Command, 0, 3)
	if manifest.AppliedOverride != nil {
		if facts.CurrentOverride == nil || *facts.CurrentOverride != *manifest.AppliedOverride || facts.CurrentHelper != *manifest.AppliedOverride {
			return UninstallPlan{}, errors.New("qemu-bridge-helper override/current state no longer matches Farrow manifest")
		}
		helperCommands = append(helperCommands,
			Command{Binary: "/usr/bin/dpkg-statoverride", Args: []string{"--remove", manifest.HelperPath}},
			Command{Binary: "/bin/chown", Args: []string{manifest.OriginalHelper.Owner + ":" + manifest.OriginalHelper.Group, manifest.HelperPath}},
			Command{Binary: "/bin/chmod", Args: []string{manifest.OriginalHelper.Mode, manifest.HelperPath}},
		)
	}
	if len(helperCommands) > 0 {
		phases = append(phases, CommandPhase{Name: "restore-helper-after-vm-detach", Commands: helperCommands})
	}
	phases = append(phases, CommandPhase{Name: "reload-after-network-file-removal", Commands: []Command{{Binary: "/usr/bin/networkctl", Args: []string{"reload"}}}})
	if manifest.NetworkManager {
		phases = append(phases, CommandPhase{Name: "reload-network-manager-after-dropin-removal", Commands: []Command{{Binary: "/usr/bin/nmcli", Args: []string{"general", "reload"}}}})
	}
	phases = append(phases, CommandPhase{Name: "restore-networkd-prestate", Commands: restoreNetworkdCommands(manifest.NetworkdUnits)})
	removeFiles := make([]string, 0, len(manifest.Files)+1)
	for _, pathname := range []string{NetDevPath, NetworkPath, NetworkManagerPath, TmpfilesPath, LeaseLockPath} {
		if _, owned := manifest.Files[pathname]; owned {
			removeFiles = append(removeFiles, pathname)
		}
	}
	restoreFiles := make([]File, 0, 1)
	if manifest.OriginalBridgePath.Existed {
		restoreFiles = append(restoreFiles, File{Path: BridgeConfPath, Owner: manifest.OriginalBridgePath.Owner + ":" + manifest.OriginalBridgePath.Group, Mode: manifest.OriginalBridgePath.Mode, Content: manifest.OriginalBridgeConf})
	} else {
		removeFiles = append(removeFiles, BridgeConfPath)
	}
	removeFiles = append(removeFiles, StatePath)
	removeDirectories := []string{LeaseRoot, "/var/lib/farrow"}
	if manifest.QEMUConfigCreated {
		removeDirectories = append(removeDirectories, QEMUConfigDir)
	}
	commands := make([]Command, 0)
	for _, phase := range phases {
		commands = append(commands, phase.Commands...)
	}
	return UninstallPlan{
		RestoreFiles: restoreFiles, RemoveFiles: removeFiles, RemoveDirectories: removeDirectories, Commands: commands, Phases: phases,
	}, nil
}
