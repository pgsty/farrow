package darwin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/pgsty/farrow/internal/network/subnet"
)

const (
	InstallRoot         = "/opt/farrow"
	DaemonPath          = "/opt/farrow/libexec/socket_vmnet"
	ClientPath          = "/opt/farrow/libexec/socket_vmnet_client"
	HostsHelperPath     = "/opt/farrow/libexec/farrow-hosts-helper"
	PlistPath           = "/Library/LaunchDaemons/io.pgsty.farrow.vmnet.plist"
	SocketPath          = "/private/var/run/farrow-vmnet.sock"
	PIDPath             = "/private/var/run/farrow-vmnet.pid"
	LeaseRoot           = "/private/var/run/farrow"
	StateDir            = "/private/var/db/farrow"
	StatePath           = "/private/var/db/farrow/network.json"
	InterfaceStatePath  = "/private/var/db/farrow/network-interface.json"
	InterfaceMarkerDir  = "/Library/Application Support/io.pgsty.farrow"
	InterfaceMarkerPath = "/Library/Application Support/io.pgsty.farrow/network-interface.json"
	LogDir              = "/var/log/farrow-vmnet"
	ServiceID           = "io.pgsty.farrow.vmnet"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var bsdInterfacePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,14}$`)

type NetworkState struct {
	Schema      int    `json:"schema"`
	Release     string `json:"release"`
	Arch        string `json:"arch"`
	ArchiveSHA  string `json:"archive_sha256"`
	Mode        string `json:"mode"`
	CIDR        string `json:"cidr"`
	HostAddress string `json:"host_address"`
	DHCPEnd     string `json:"dhcp_end"`
	InterfaceID string `json:"interface_id"`
}

// InterfaceMarker is deliberately public and non-secret. Its protected twin
// at InterfaceStatePath prevents a merely plausible pre-existing BSD
// interface from being treated as Farrow-owned.
type InterfaceMarker struct {
	Schema      int    `json:"schema"`
	InterfaceID string `json:"interface_id"`
	CIDR        string `json:"cidr"`
	HostAddress string `json:"host_address"`
	BSDName     string `json:"bsd_name"`
}

type InstallPlan struct {
	Release   Release
	State     NetworkState
	Interface InterfaceMarker
	Args      []string
}

func StrictInterfaceMarker(data []byte) (InterfaceMarker, error) {
	if len(data) == 0 || len(data) > 1<<16 {
		return InterfaceMarker{}, errors.New("darwin interface marker size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker InterfaceMarker
	if err := decoder.Decode(&marker); err != nil {
		return InterfaceMarker{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return InterfaceMarker{}, errors.New("darwin interface marker has trailing JSON data")
	}
	layout, layoutErr := subnet.Parse(marker.CIDR)
	if marker.Schema != 1 || !uuidPattern.MatchString(marker.InterfaceID) ||
		layoutErr != nil || marker.HostAddress != layout.HostAddress() ||
		!bsdInterfacePattern.MatchString(marker.BSDName) {
		return InterfaceMarker{}, errors.New("darwin interface marker contract is invalid")
	}
	canonical, err := marshalInterfaceMarker(marker)
	if err != nil || !bytes.Equal(data, canonical) {
		return InterfaceMarker{}, errors.New("darwin interface marker bytes are not canonical")
	}
	return marker, nil
}

func StrictNetworkState(data []byte) (NetworkState, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return NetworkState{}, errors.New("darwin network state size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state NetworkState
	if err := decoder.Decode(&state); err != nil {
		return NetworkState{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return NetworkState{}, errors.New("darwin network state has trailing JSON data")
	}
	layout, layoutErr := subnet.Parse(state.CIDR)
	if state.Schema != 1 || state.Release != ReleaseVersion || (state.Arch != "arm64" && state.Arch != "amd64") || (state.Mode != "host" && state.Mode != "shared") || layoutErr != nil || state.HostAddress != layout.HostAddress() || state.DHCPEnd != layout.DHCPEnd() || !uuidPattern.MatchString(state.InterfaceID) {
		return NetworkState{}, errors.New("darwin network state contract is invalid")
	}
	release, err := PinnedRelease(state.Arch)
	if err != nil || state.ArchiveSHA != release.SHA256 {
		return NetworkState{}, errors.New("darwin network state release digest is invalid")
	}
	return state, nil
}

func NewInstallPlan(arch, interfaceID string) (InstallPlan, error) {
	return NewInstallPlanMode(arch, interfaceID, "host")
}

func NewInstallPlanMode(arch, interfaceID, mode string) (InstallPlan, error) {
	return NewInstallPlanModeNetwork(arch, interfaceID, mode, subnet.DefaultCIDR)
}

func NewInstallPlanModeNetwork(arch, interfaceID, mode, cidr string) (InstallPlan, error) {
	if !uuidPattern.MatchString(interfaceID) {
		return InstallPlan{}, fmt.Errorf("invalid persistent interface UUID %q", interfaceID)
	}
	release, err := PinnedRelease(arch)
	if err != nil {
		return InstallPlan{}, err
	}
	if mode != "host" && mode != "shared" {
		return InstallPlan{}, fmt.Errorf("unsupported Darwin vmnet mode %q", mode)
	}
	layout, err := subnet.Parse(cidr)
	if err != nil {
		return InstallPlan{}, err
	}
	state := NetworkState{
		Schema: 1, Release: release.Version, Arch: arch, ArchiveSHA: release.SHA256,
		Mode: mode, CIDR: layout.CIDR(), HostAddress: layout.HostAddress(),
		DHCPEnd: layout.DHCPEnd(), InterfaceID: interfaceID,
	}
	args := []string{
		DaemonPath,
		"--vmnet-mode=" + mode,
		"--vmnet-gateway=" + layout.HostAddress(),
		"--vmnet-dhcp-end=" + layout.DHCPEnd(),
		"--vmnet-mask=255.255.255.0",
		"--vmnet-interface-id=" + interfaceID,
		"--socket-group=staff",
		"--pidfile=" + PIDPath,
		SocketPath,
	}
	return InstallPlan{Release: release, State: state, Args: args}, nil
}

func (p InstallPlan) WithBSDInterface(name string) (InstallPlan, error) {
	marker := InterfaceMarker{
		Schema: 1, InterfaceID: p.State.InterfaceID, CIDR: p.State.CIDR,
		HostAddress: p.State.HostAddress, BSDName: name,
	}
	data, err := marshalInterfaceMarker(marker)
	if err != nil {
		return InstallPlan{}, err
	}
	parsed, err := StrictInterfaceMarker(data)
	if err != nil || parsed != marker {
		return InstallPlan{}, errors.New("observed Darwin BSD interface identity is invalid")
	}
	p.Interface = marker
	return p, nil
}

func marshalInterfaceMarker(marker InterfaceMarker) ([]byte, error) {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (p InstallPlan) InterfaceJSON() ([]byte, error) {
	if p.Interface.InterfaceID != p.State.InterfaceID || p.Interface.CIDR != p.State.CIDR ||
		p.Interface.HostAddress != p.State.HostAddress {
		return nil, errors.New("darwin interface marker differs from the protected network plan")
	}
	data, err := marshalInterfaceMarker(p.Interface)
	if err != nil {
		return nil, err
	}
	parsed, err := StrictInterfaceMarker(data)
	if err != nil || parsed != p.Interface {
		return nil, errors.New("darwin interface marker plan is incomplete")
	}
	return data, nil
}

func bindInterfaceEvidence(plan InstallPlan, protected, public []byte) (InstallPlan, error) {
	protectedMarker, err := StrictInterfaceMarker(protected)
	if err != nil {
		return InstallPlan{}, err
	}
	publicMarker, err := StrictInterfaceMarker(public)
	if err != nil || publicMarker != protectedMarker {
		return InstallPlan{}, errors.New("public Darwin interface marker differs from protected interface state")
	}
	plan, err = plan.WithBSDInterface(protectedMarker.BSDName)
	if err != nil || plan.Interface != protectedMarker {
		return InstallPlan{}, errors.New("installed Darwin interface identity does not reproduce the pinned plan")
	}
	expected, err := plan.InterfaceJSON()
	if err != nil || !bytes.Equal(protected, expected) || !bytes.Equal(public, expected) {
		return InstallPlan{}, errors.New("installed Darwin interface identity bytes differ from the pinned plan")
	}
	return plan, nil
}

func (p InstallPlan) StateJSON() ([]byte, error) {
	if p.State.Schema != 1 {
		return nil, errors.New("network state plan is incomplete")
	}
	data, err := json.MarshalIndent(p.State, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func xmlEscape(value string) string {
	replacements := map[rune]string{'&': "&amp;", '<': "&lt;", '>': "&gt;", '"': "&quot;", 39: "&apos;"}
	result := ""
	for _, char := range value {
		if replacement, ok := replacements[char]; ok {
			result += replacement
		} else {
			result += string(char)
		}
	}
	return result
}

func (p InstallPlan) Plist() ([]byte, error) {
	if len(p.Args) == 0 || p.Args[0] != DaemonPath {
		return nil, errors.New("launchd plan is incomplete")
	}
	arguments := ""
	for _, argument := range p.Args {
		arguments += "\n      <string>" + xmlEscape(argument) + "</string>"
	}
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + ServiceID + `</string>
  <key>ProgramArguments</key>
  <array>` + arguments + `
  </array>
  <key>UserName</key>
  <string>root</string>
  <key>GroupName</key>
  <string>wheel</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>` + LogDir + `/stdout.log</string>
  <key>StandardErrorPath</key>
  <string>` + LogDir + `/stderr.log</string>
</dict>
</plist>
`
	return []byte(content), nil
}
