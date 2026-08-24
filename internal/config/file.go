package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/image"
	"github.com/pgsty/piglet/internal/network/subnet"
	"github.com/pgsty/piglet/internal/spec"
	"go.yaml.in/yaml/v3"
)

const maxConfigBytes = 1 << 20

type Size int64

func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return errors.New("size must be a quoted or plain string with an explicit unit")
	}
	value, err := ParseSize(node.Value)
	if err != nil {
		return err
	}
	*s = Size(value)
	return nil
}

const maxVirtualCPUs = 256

func formatSize(value int64) string {
	for _, unit := range []struct {
		name string
		size int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if value >= unit.size && value%unit.size == 0 {
			return strconv.FormatInt(value/unit.size, 10) + unit.name
		}
	}
	return strconv.FormatInt(value, 10) + "B"
}

func (s Size) MarshalYAML() (any, error) { return formatSize(int64(s)), nil }

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return errors.New("duration must be a string")
	}
	value, err := time.ParseDuration(node.Value)
	if err != nil || value <= 0 {
		return fmt.Errorf("invalid positive duration %q", node.Value)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

type NetworkConfig struct {
	Mode        string `yaml:"mode"`
	CIDR        string `yaml:"cidr,omitempty"`
	HostAddress string `yaml:"host_address,omitempty"`
	DHCPEnd     string `yaml:"dhcp_end,omitempty"`
}

type DefaultsConfig struct {
	Image    string `yaml:"image,omitempty"`
	CPUs     int    `yaml:"cpus,omitempty"`
	Memory   Size   `yaml:"memory,omitempty"`
	RootDisk Size   `yaml:"root_disk,omitempty"`
}

type SSHConfig struct {
	User        string   `yaml:"user,omitempty"`
	WaitTimeout Duration `yaml:"wait_timeout,omitempty"`
}

type StorageConfig struct {
	DataRoot string `yaml:"data_root,omitempty"`
}

type DiskConfig struct {
	Name       string `yaml:"name"`
	Size       Size   `yaml:"size"`
	Mount      string `yaml:"mount"`
	Filesystem string `yaml:"filesystem,omitempty"`
	Persistent bool   `yaml:"persistent,omitempty"`
}

type ForwardConfig struct {
	Bind     string `yaml:"bind,omitempty"`
	Host     uint16 `yaml:"host"`
	Guest    uint16 `yaml:"guest"`
	Protocol string `yaml:"protocol,omitempty"`
}

type NodeConfig struct {
	Name        string          `yaml:"name"`
	Control     bool            `yaml:"control,omitempty"`
	Address     string          `yaml:"address,omitempty"`
	HostAliases []string        `yaml:"host_aliases,omitempty"`
	Image       string          `yaml:"image,omitempty"`
	CPUs        int             `yaml:"cpus,omitempty"`
	Memory      Size            `yaml:"memory,omitempty"`
	RootDisk    Size            `yaml:"root_disk,omitempty"`
	Disks       []DiskConfig    `yaml:"disks,omitempty"`
	Forwards    []ForwardConfig `yaml:"forwards,omitempty"`
}

type File struct {
	Version  int            `yaml:"version"`
	Name     string         `yaml:"name"`
	Arch     string         `yaml:"arch,omitempty"`
	Network  NetworkConfig  `yaml:"network"`
	Defaults DefaultsConfig `yaml:"defaults,omitempty"`
	SSH      SSHConfig      `yaml:"ssh,omitempty"`
	Storage  StorageConfig  `yaml:"storage,omitempty"`
	Nodes    []NodeConfig   `yaml:"nodes"`
}

var (
	dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	sshUser  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	dnsName  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
	diskName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

func safeDiskMount(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return false
	}
	for _, root := range []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/piglet"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return false
		}
	}
	return true
}

func Decode(reader io.Reader) (File, error) {
	limited := io.LimitReader(reader, maxConfigBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return File{}, err
	}
	if len(data) > maxConfigBytes {
		return File{}, errors.New("configuration exceeds 1 MiB limit")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file File
	if err := decoder.Decode(&file); err != nil {
		return File{}, fmt.Errorf("decode strict YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return File{}, errors.New("multiple YAML documents are not supported")
	}
	file.defaults()
	if err := file.Validate(); err != nil {
		return File{}, err
	}
	return file, nil
}

func Load(path string) (File, error) {
	if path == "" || !filepath.IsAbs(path) {
		return File{}, errors.New("configuration path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() {
		return File{}, errors.New("configuration must be a regular non-symlink file")
	}
	handle, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer handle.Close()
	return Decode(handle)
}

func (f *File) defaults() {
	if f.Arch == "" {
		f.Arch = "native"
	}
	if f.Network.Mode == "" {
		f.Network.Mode = "user"
	}
	if f.Defaults.Image == "" {
		f.Defaults.Image = "u24"
	}
	f.Defaults.Image = image.CanonicalAlias(f.Defaults.Image)
	if f.Defaults.CPUs == 0 {
		f.Defaults.CPUs = 1
	}
	if f.Defaults.Memory == 0 {
		f.Defaults.Memory = Size(2 * spec.GiB)
	}
	if f.Defaults.RootDisk == 0 {
		f.Defaults.RootDisk = Size(64 * spec.GiB)
	}
	if f.SSH.User == "" {
		f.SSH.User = "dba"
	}
	if f.SSH.WaitTimeout == 0 {
		f.SSH.WaitTimeout = Duration(180 * time.Second)
	}
	if f.Network.Mode == "private" {
		if f.Network.CIDR == "" {
			f.Network.CIDR = subnet.DefaultCIDR
		}
		if layout, err := subnet.Parse(f.Network.CIDR); err == nil {
			if f.Network.HostAddress == "" {
				f.Network.HostAddress = layout.HostAddress()
			}
			if f.Network.DHCPEnd == "" {
				f.Network.DHCPEnd = layout.DHCPEnd()
			}
		}
	}
	for index := range f.Nodes {
		node := &f.Nodes[index]
		if node.Image == "" {
			node.Image = f.Defaults.Image
		} else {
			node.Image = image.CanonicalAlias(node.Image)
		}
		if node.CPUs == 0 {
			node.CPUs = f.Defaults.CPUs
		}
		if node.Memory == 0 {
			node.Memory = f.Defaults.Memory
		}
		if node.RootDisk == 0 {
			node.RootDisk = f.Defaults.RootDisk
		}
		for forwardIndex := range node.Forwards {
			if node.Forwards[forwardIndex].Bind == "" {
				node.Forwards[forwardIndex].Bind = "127.0.0.1"
			}
			if node.Forwards[forwardIndex].Protocol == "" {
				node.Forwards[forwardIndex].Protocol = "tcp"
			}
		}
		for diskIndex := range node.Disks {
			if node.Disks[diskIndex].Filesystem == "" {
				node.Disks[diskIndex].Filesystem = "auto"
			}
		}
	}
}

func (f *File) Validate() error {
	f.defaults()
	if f.Version != 1 {
		return fmt.Errorf("configuration version must be 1, got %d", f.Version)
	}
	if !dnsLabel.MatchString(f.Name) {
		return fmt.Errorf("invalid project name %q", f.Name)
	}
	if f.Arch != "native" {
		return errors.New("v1 supports arch: native only")
	}
	if f.Network.Mode != "user" && f.Network.Mode != "private" {
		return fmt.Errorf("unsupported network mode %q", f.Network.Mode)
	}
	if !sshUser.MatchString(f.SSH.User) {
		return fmt.Errorf("invalid SSH user %q", f.SSH.User)
	}
	if f.Storage.DataRoot != "" && (!filepath.IsAbs(f.Storage.DataRoot) || filepath.Clean(f.Storage.DataRoot) != f.Storage.DataRoot || filepath.Clean(f.Storage.DataRoot) == "/") {
		return errors.New("storage.data_root must be a clean non-root absolute path")
	}
	if len(f.Nodes) == 0 || len(f.Nodes) > 20 {
		return errors.New("configuration requires 1..20 nodes")
	}
	if f.Network.Mode == "user" && len(f.Nodes) != 1 {
		return errors.New("user network supports exactly one node")
	}
	privateLayout := subnet.Layout{}
	if f.Network.Mode == "private" {
		var err error
		privateLayout, err = subnet.Parse(f.Network.CIDR)
		if err != nil {
			return err
		}
		if f.Network.HostAddress != privateLayout.HostAddress() || f.Network.DHCPEnd != privateLayout.DHCPEnd() {
			return fmt.Errorf("private v1 requires host %s and DHCP end %s for %s", privateLayout.HostAddress(), privateLayout.DHCPEnd(), privateLayout.CIDR())
		}
	}
	names := make(map[string]struct{})
	addresses := make(map[string]struct{})
	allAliases := make(map[string]struct{})
	controls := 0
	for _, node := range f.Nodes {
		if !dnsLabel.MatchString(node.Name) {
			return fmt.Errorf("invalid node name %q", node.Name)
		}
		if _, exists := names[node.Name]; exists {
			return fmt.Errorf("duplicate node name %q", node.Name)
		}
		names[node.Name] = struct{}{}
		allAliases[node.Name] = struct{}{}
	}
	for nodeIndex := range f.Nodes {
		node := &f.Nodes[nodeIndex]
		if node.Control {
			controls++
		}
		if node.CPUs < 1 || node.CPUs > maxVirtualCPUs || int64(node.Memory) < 512<<20 || int64(node.RootDisk) <= 0 {
			return fmt.Errorf("invalid CPU/memory/root resources for node %s", node.Name)
		}
		if f.Network.Mode == "private" {
			if !privateLayout.IsStatic(node.Address) {
				return fmt.Errorf("private node %s address must be in %s-%s", node.Name, privateLayout.StaticStart(), privateLayout.StaticEnd())
			}
			if _, exists := addresses[node.Address]; exists {
				return fmt.Errorf("duplicate private address %s", node.Address)
			}
			addresses[node.Address] = struct{}{}
		} else if node.Address != "" {
			return fmt.Errorf("user-network node %s must not set address", node.Name)
		}
		aliasSeen := make(map[string]struct{})
		for _, alias := range node.HostAliases {
			if !dnsName.MatchString(alias) || strings.Contains(alias, "..") {
				return fmt.Errorf("invalid host alias %q", alias)
			}
			if _, exists := aliasSeen[alias]; exists {
				return fmt.Errorf("duplicate host alias %q", alias)
			}
			if _, exists := allAliases[alias]; exists {
				return fmt.Errorf("host alias %q collides within project", alias)
			}
			aliasSeen[alias] = struct{}{}
			allAliases[alias] = struct{}{}
		}
		diskNames := make(map[string]struct{})
		mounts := make(map[string]struct{})
		for _, disk := range node.Disks {
			if !diskName.MatchString(disk.Name) || disk.Size <= 0 || !safeDiskMount(disk.Mount) {
				return fmt.Errorf("invalid disk %q on node %s", disk.Name, node.Name)
			}
			if disk.Filesystem != "auto" && disk.Filesystem != "xfs" && disk.Filesystem != "ext4" {
				return fmt.Errorf("unsupported filesystem %q", disk.Filesystem)
			}
			if _, exists := diskNames[disk.Name]; exists {
				return fmt.Errorf("duplicate disk name %q", disk.Name)
			}
			if _, exists := mounts[disk.Mount]; exists {
				return fmt.Errorf("duplicate mount %q", disk.Mount)
			}
			diskNames[disk.Name] = struct{}{}
			mounts[disk.Mount] = struct{}{}
		}
		forwardKeys := make(map[string]struct{})
		for _, forward := range node.Forwards {
			if forward.Protocol != "tcp" || net.ParseIP(forward.Bind) == nil || forward.Host == 0 || forward.Guest == 0 {
				return fmt.Errorf("invalid forward on node %s", node.Name)
			}
			key := fmt.Sprintf("%s/%d", forward.Bind, forward.Host)
			if _, exists := forwardKeys[key]; exists {
				return fmt.Errorf("duplicate host forward %s", key)
			}
			forwardKeys[key] = struct{}{}
		}
	}
	if controls > 1 {
		return errors.New("at most one control node is allowed")
	}
	if controls == 0 {
		f.Nodes[0].Control = true
	}
	return nil
}

func (f File) Resolve() (spec.Resolved, error) {
	f.defaults()
	if err := f.Validate(); err != nil {
		return spec.Resolved{}, err
	}
	resolved := spec.Resolved{Schema: 1, Name: f.Name, Image: f.Defaults.Image, Network: f.Network.Mode, SSHUser: f.SSH.User, SSHWaitTimeoutNS: int64(f.SSH.WaitTimeout), DataRoot: f.Storage.DataRoot}
	if f.Network.Mode == "private" {
		resolved.Private = &spec.PrivateNetwork{CIDR: f.Network.CIDR, HostAddress: f.Network.HostAddress, DHCPEnd: f.Network.DHCPEnd}
	}
	for _, source := range f.Nodes {
		node := spec.Node{Name: source.Name, Control: source.Control, Address: source.Address, Aliases: append([]string(nil), source.HostAliases...), CPUs: source.CPUs, Memory: int64(source.Memory), RootDisk: int64(source.RootDisk)}
		if source.Image != f.Defaults.Image {
			node.Image = source.Image
		}
		for _, sourceDisk := range source.Disks {
			filesystem := sourceDisk.Filesystem
			if filesystem == "auto" {
				filesystem = ""
			}
			node.Disks = append(node.Disks, spec.Disk{Name: sourceDisk.Name, Size: int64(sourceDisk.Size), Mount: sourceDisk.Mount, Filesystem: filesystem, Persistent: sourceDisk.Persistent})
		}
		for _, sourceForward := range source.Forwards {
			node.Forwards = append(node.Forwards, spec.Forward{Bind: sourceForward.Bind, Host: sourceForward.Host, Guest: sourceForward.Guest, Protocol: sourceForward.Protocol})
		}
		resolved.Nodes = append(resolved.Nodes, node)
	}
	if resolved.Network == "user" && len(resolved.Nodes) == 1 && resolved.Nodes[0].Image != "" {
		resolved.Image = resolved.Nodes[0].Image
		resolved.Nodes[0].Image = ""
	}
	return resolved, nil
}

// RebasePrivateNetwork moves a resolved private project to one explicit
// host-global /24 while preserving every node's last octet.
func RebasePrivateNetwork(resolved spec.Resolved, targetCIDR string) (spec.Resolved, error) {
	if resolved.Network != "private" || resolved.Private == nil {
		return spec.Resolved{}, errors.New("network CIDR override requires a private resolved spec")
	}
	source, err := subnet.Parse(resolved.Private.CIDR)
	if err != nil {
		return spec.Resolved{}, err
	}
	if resolved.Private.HostAddress != source.HostAddress() || resolved.Private.DHCPEnd != source.DHCPEnd() {
		return spec.Resolved{}, errors.New("resolved private network does not match its /24 layout")
	}
	target, err := subnet.Parse(targetCIDR)
	if err != nil {
		return spec.Resolved{}, err
	}
	result := resolved
	result.Private = &spec.PrivateNetwork{CIDR: target.CIDR(), HostAddress: target.HostAddress(), DHCPEnd: target.DHCPEnd()}
	result.Nodes = append([]spec.Node(nil), resolved.Nodes...)
	for index := range result.Nodes {
		address, err := target.RebaseStatic(result.Nodes[index].Address, source)
		if err != nil {
			return spec.Resolved{}, fmt.Errorf("rebase node %s: %w", result.Nodes[index].Name, err)
		}
		result.Nodes[index].Address = address
	}
	return result, nil
}

func FromResolved(resolved spec.Resolved) (File, error) {
	if resolved.Schema != 1 || len(resolved.Nodes) == 0 {
		return File{}, errors.New("resolved spec is empty or unsupported")
	}
	waitTimeout, err := resolved.SSHWaitTimeout()
	if err != nil {
		return File{}, err
	}
	file := File{Version: 1, Name: resolved.Name, Arch: "native", Network: NetworkConfig{Mode: resolved.Network}, Defaults: DefaultsConfig{Image: resolved.Image}, SSH: SSHConfig{User: resolved.SSHUser, WaitTimeout: Duration(waitTimeout)}, Storage: StorageConfig{DataRoot: resolved.DataRoot}}
	if resolved.Private != nil {
		file.Network.CIDR, file.Network.HostAddress, file.Network.DHCPEnd = resolved.Private.CIDR, resolved.Private.HostAddress, resolved.Private.DHCPEnd
	}
	for _, source := range resolved.Nodes {
		node := NodeConfig{Name: source.Name, Control: source.Control, Address: source.Address, HostAliases: append([]string(nil), source.Aliases...), Image: source.Image, CPUs: source.CPUs, Memory: Size(source.Memory), RootDisk: Size(source.RootDisk)}
		if node.Image == "" {
			node.Image = resolved.Image
		}
		for _, sourceDisk := range source.Disks {
			node.Disks = append(node.Disks, DiskConfig{Name: sourceDisk.Name, Size: Size(sourceDisk.Size), Mount: sourceDisk.Mount, Filesystem: sourceDisk.Filesystem, Persistent: sourceDisk.Persistent})
		}
		for _, sourceForward := range source.Forwards {
			node.Forwards = append(node.Forwards, ForwardConfig{Bind: sourceForward.Bind, Host: sourceForward.Host, Guest: sourceForward.Guest, Protocol: sourceForward.Protocol})
		}
		file.Nodes = append(file.Nodes, node)
	}
	return file, nil
}

func Marshal(file File) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(file); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func SortedNodeNames(file File) []string {
	names := make([]string, 0, len(file.Nodes))
	for _, node := range file.Nodes {
		names = append(names, node.Name)
	}
	sort.Strings(names)
	return names
}
