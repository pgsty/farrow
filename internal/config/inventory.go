package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/pgsty/farrow/internal/spec"
	"go.yaml.in/yaml/v3"
)

// The configuration is a Pigsty-compatible Ansible inventory. Farrow reads
// exactly the vm_* namespace plus a short whitelist of native Pigsty
// variables; everything else in the file is opaque and never validated.
//
// Strictness is inverted at the namespace boundary: unknown vm_* keys, wrong
// types, template expressions, and group-level conflicts are hard errors,
// while the surrounding Pigsty parameters are ignored entirely.

const maxInventoryBytes = 4 << 20

// InventoryProjectName is the fixed resolved-spec name for inventory-defined
// projects. Naming from file content or directory would move the drift hash
// when the file or directory is renamed, so the name is deliberately constant;
// project identity lives in the workspace marker.
const InventoryProjectName = "farrow"

const (
	defaultCPU      = 2
	defaultMemMiB   = 4096
	defaultDiskGiB  = 64
	defaultDataGiB  = 128
	defaultSSHUser  = "dba"
	defaultAdminUID = 88
)

var defaultImage = image.EmbeddedCatalog().Defaults.Image

var knownVMKeys = map[string]struct{}{
	"vm_skip": {}, "vm_image": {}, "vm_arch": {}, "vm_cpu": {}, "vm_mem": {},
	"vm_disk": {}, "vm_disks": {}, "vm_alias": {}, "vm_shares": {},
}

var derivedDiskName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// varSource is one contribution layer for a host: all.vars (depth 0), each
// group on the path to the host (depth = nesting level), and host vars
// (maximal depth). Deeper wins; two different values at the same depth are a
// conflict the user must resolve at host level.
type varSource struct {
	origin string
	depth  int
	vars   map[string]*yaml.Node
}

type inventoryHost struct {
	address string
	sources []varSource
}

func isMapping(node *yaml.Node) bool { return node != nil && node.Kind == yaml.MappingNode }

func resolveAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

func mappingEntries(node *yaml.Node) ([][2]*yaml.Node, error) {
	node = resolveAlias(node)
	if node == nil || node.Kind == 0 || node.Tag == "!!null" {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a YAML mapping at line %d", node.Line)
	}
	entries := make([][2]*yaml.Node, 0, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		entries = append(entries, [2]*yaml.Node{node.Content[index], resolveAlias(node.Content[index+1])})
	}
	return entries, nil
}

func mappingLookup(node *yaml.Node, key string) *yaml.Node {
	entries, err := mappingEntries(node)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry[0].Value == key {
			return entry[1]
		}
	}
	return nil
}

func varsOf(node *yaml.Node, origin string) (map[string]*yaml.Node, error) {
	entries, err := mappingEntries(node)
	if err != nil {
		return nil, fmt.Errorf("%s vars: %w", origin, err)
	}
	result := make(map[string]*yaml.Node, len(entries))
	for _, entry := range entries {
		key := entry[0].Value
		if strings.HasPrefix(key, "vm_") {
			if _, known := knownVMKeys[key]; !known {
				return nil, fmt.Errorf("unknown farrow variable %q in %s; the vm_* namespace is strict", key, origin)
			}
		}
		result[key] = entry[1]
	}
	return result, nil
}

func decodeAny(node *yaml.Node) (any, error) {
	var value any
	if err := node.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func containsTemplate(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "{{")
	case []any:
		for _, item := range typed {
			if containsTemplate(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsTemplate(item) {
				return true
			}
		}
	}
	return false
}

// lookup resolves one variable for a host across its contribution layers.
// It returns the winning YAML node, the origin it came from, and whether the
// key was found anywhere.
func (host inventoryHost) lookup(key string) (*yaml.Node, string, bool, error) {
	bestDepth := -1
	var winner *yaml.Node
	origin := ""
	var conflictOrigins []string
	for _, source := range host.sources {
		node, present := source.vars[key]
		if !present {
			continue
		}
		switch {
		case source.depth > bestDepth:
			bestDepth = source.depth
			winner = node
			origin = source.origin
			conflictOrigins = conflictOrigins[:0]
		case source.depth == bestDepth:
			currentValue, currentErr := decodeAny(winner)
			candidateValue, candidateErr := decodeAny(node)
			if currentErr != nil || candidateErr != nil || !reflect.DeepEqual(currentValue, candidateValue) {
				conflictOrigins = append(conflictOrigins, source.origin)
			}
		}
	}
	if winner == nil {
		return nil, "", false, nil
	}
	if len(conflictOrigins) != 0 {
		return nil, "", false, fmt.Errorf("host %s inherits conflicting values for %q from %s and %s; set it at host level", host.address, key, origin, strings.Join(conflictOrigins, ", "))
	}
	value, err := decodeAny(winner)
	if err != nil {
		return nil, "", false, fmt.Errorf("host %s variable %q: %w", host.address, key, err)
	}
	if containsTemplate(value) {
		return nil, "", false, fmt.Errorf("host %s variable %q contains a template expression; farrow reads literal values only", host.address, key)
	}
	return winner, origin, true, nil
}

func (host inventoryHost) lookupString(key string) (string, bool, error) {
	node, origin, found, err := host.lookup(key)
	if err != nil || !found {
		return "", found, err
	}
	var value string
	if decodeErr := node.Decode(&value); decodeErr != nil {
		return "", true, fmt.Errorf("host %s variable %q from %s must be a string", host.address, key, origin)
	}
	return value, true, nil
}

func (host inventoryHost) lookupInt(key string) (int64, bool, error) {
	node, origin, found, err := host.lookup(key)
	if err != nil || !found {
		return 0, found, err
	}
	var value int64
	if decodeErr := node.Decode(&value); decodeErr != nil {
		return 0, true, fmt.Errorf("host %s variable %q from %s must be an integer", host.address, key, origin)
	}
	return value, true, nil
}

func (host inventoryHost) lookupBool(key string) (bool, bool, error) {
	node, origin, found, err := host.lookup(key)
	if err != nil || !found {
		return false, found, err
	}
	var value bool
	if decodeErr := node.Decode(&value); decodeErr != nil {
		return false, true, fmt.Errorf("host %s variable %q from %s must be a boolean", host.address, key, origin)
	}
	return value, true, nil
}

// lookupSize accepts a bare integer scaled by unitMultiplier (MiB for memory,
// GiB for disks) or a string with an explicit unit.
func (host inventoryHost) lookupSize(key string, unitMultiplier int64) (int64, bool, error) {
	node, origin, found, err := host.lookup(key)
	if err != nil || !found {
		return 0, found, err
	}
	var integer int64
	if decodeErr := node.Decode(&integer); decodeErr == nil {
		if integer <= 0 {
			return 0, true, fmt.Errorf("host %s variable %q must be positive", host.address, key)
		}
		return integer * unitMultiplier, true, nil
	}
	var text string
	if decodeErr := node.Decode(&text); decodeErr != nil {
		return 0, true, fmt.Errorf("host %s variable %q from %s must be an integer or a size string", host.address, key, origin)
	}
	value, parseErr := ParseSize(text)
	if parseErr != nil {
		return 0, true, fmt.Errorf("host %s variable %q: %w", host.address, key, parseErr)
	}
	return value, true, nil
}

func (host inventoryHost) lookupStringList(key string) ([]string, bool, error) {
	node, origin, found, err := host.lookup(key)
	if err != nil || !found {
		return nil, found, err
	}
	var value []string
	if decodeErr := node.Decode(&value); decodeErr != nil {
		return nil, true, fmt.Errorf("host %s variable %q from %s must be a list of strings", host.address, key, origin)
	}
	return value, true, nil
}

func diskNameForPath(path string) (string, error) {
	name := strings.ReplaceAll(strings.Trim(path, "/"), "/", "-")
	if !derivedDiskName.MatchString(name) {
		return "", fmt.Errorf("disk mount %q does not derive a safe disk identity; use a short lowercase path such as /data", path)
	}
	return name, nil
}

func diskSizeBytes(host, path string, size any) (int64, error) {
	switch typed := size.(type) {
	case nil:
		return defaultDataGiB * spec.GiB, nil
	case int:
		if typed <= 0 {
			return 0, fmt.Errorf("host %s disk %s size must be positive", host, path)
		}
		return int64(typed) * spec.GiB, nil
	case int64:
		if typed <= 0 {
			return 0, fmt.Errorf("host %s disk %s size must be positive", host, path)
		}
		return typed * spec.GiB, nil
	case string:
		value, err := ParseSize(typed)
		if err != nil {
			return 0, fmt.Errorf("host %s disk %s: %w", host, path, err)
		}
		return value, nil
	default:
		return 0, fmt.Errorf("host %s disk %s size must be an integer (GiB) or a size string", host, path)
	}
}

func (host inventoryHost) lookupDisks() ([]DiskConfig, bool, error) {
	node, origin, found, err := host.lookup("vm_disks")
	if err != nil || !found {
		return nil, found, err
	}
	var entries []map[string]any
	if decodeErr := node.Decode(&entries); decodeErr != nil {
		return nil, true, fmt.Errorf("host %s vm_disks from %s must be a list of {path, size, fs, persistent} entries", host.address, origin)
	}
	disks := make([]DiskConfig, 0, len(entries))
	for _, entry := range entries {
		for key := range entry {
			switch key {
			case "path", "size", "fs", "persistent":
			default:
				return nil, true, fmt.Errorf("host %s vm_disks entry has unknown key %q", host.address, key)
			}
		}
		path, _ := entry["path"].(string)
		if path == "" {
			return nil, true, fmt.Errorf("host %s vm_disks entry requires an absolute path", host.address)
		}
		name, nameErr := diskNameForPath(path)
		if nameErr != nil {
			return nil, true, fmt.Errorf("host %s: %w", host.address, nameErr)
		}
		size, sizeErr := diskSizeBytes(host.address, path, entry["size"])
		if sizeErr != nil {
			return nil, true, sizeErr
		}
		filesystem := "xfs"
		if value, present := entry["fs"]; present {
			text, ok := value.(string)
			if !ok || (text != "xfs" && text != "ext4") {
				return nil, true, fmt.Errorf("host %s disk %s fs must be xfs or ext4", host.address, path)
			}
			filesystem = text
		}
		persistent := false
		if value, present := entry["persistent"]; present {
			flag, ok := value.(bool)
			if !ok {
				return nil, true, fmt.Errorf("host %s disk %s persistent must be a boolean", host.address, path)
			}
			persistent = flag
		}
		disks = append(disks, DiskConfig{Name: name, Size: Size(size), Mount: path, Filesystem: filesystem, Persistent: persistent})
	}
	return disks, true, nil
}

func (host inventoryHost) lookupShares() ([]ShareConfig, bool, error) {
	node, origin, found, err := host.lookup("vm_shares")
	if err != nil || !found {
		return nil, found, err
	}
	var entries []map[string]any
	if decodeErr := node.Decode(&entries); decodeErr != nil {
		return nil, true, fmt.Errorf("host %s vm_shares from %s must be a list of {host, guest, readonly} entries", host.address, origin)
	}
	shares := make([]ShareConfig, 0, len(entries))
	for _, entry := range entries {
		for key := range entry {
			switch key {
			case "host", "guest", "readonly":
			default:
				return nil, true, fmt.Errorf("host %s vm_shares entry has unknown key %q", host.address, key)
			}
		}
		hostPath, _ := entry["host"].(string)
		guestPath, _ := entry["guest"].(string)
		if hostPath == "" || guestPath == "" {
			return nil, true, fmt.Errorf("host %s vm_shares entry requires host and guest paths", host.address)
		}
		share := ShareConfig{Host: hostPath, Guest: guestPath}
		if value, present := entry["readonly"]; present {
			flag, ok := value.(bool)
			if !ok {
				return nil, true, fmt.Errorf("host %s share %s readonly must be a boolean", host.address, guestPath)
			}
			share.Readonly = shareReadonlyYAML(flag)
		}
		shares = append(shares, share)
	}
	return shares, true, nil
}

// nodeName resolves the VM name: explicit nodename, then the Pigsty
// node_id_from_pg convention (<pg_cluster>-<pg_seq>), then node-<last octet>.
func (host inventoryHost) nodeName() (string, error) {
	if name, found, err := host.lookupString("nodename"); err != nil {
		return "", err
	} else if found && name != "" {
		return name, nil
	}
	cluster, clusterFound, clusterErr := host.lookupString("pg_cluster")
	if clusterErr != nil {
		return "", fmt.Errorf("%v (set nodename explicitly to name this VM)", clusterErr)
	}
	sequence, sequenceFound, sequenceErr := host.lookupInt("pg_seq")
	if sequenceErr != nil {
		return "", fmt.Errorf("%v (set nodename explicitly to name this VM)", sequenceErr)
	}
	if clusterFound && sequenceFound && cluster != "" {
		return fmt.Sprintf("%s-%d", cluster, sequence), nil
	}
	address, err := netip.ParseAddr(host.address)
	if err != nil || !address.Is4() {
		return "", fmt.Errorf("inventory host key %q must be an IPv4 address", host.address)
	}
	octets := address.As4()
	return fmt.Sprintf("node-%d", octets[3]), nil
}

func collectHosts(group *yaml.Node, origin string, depth int, inherited []varSource, hosts *[]inventoryHost, index map[string]int) error {
	groupVars, err := varsOf(mappingLookup(group, "vars"), origin)
	if err != nil {
		return err
	}
	sources := inherited
	if len(groupVars) != 0 {
		sources = append(append([]varSource(nil), inherited...), varSource{origin: origin, depth: depth, vars: groupVars})
	}
	hostEntries, err := mappingEntries(mappingLookup(group, "hosts"))
	if err != nil {
		return fmt.Errorf("%s hosts: %w", origin, err)
	}
	for _, entry := range hostEntries {
		address := entry[0].Value
		if parsed, parseErr := netip.ParseAddr(address); parseErr != nil || !parsed.Is4() {
			return fmt.Errorf("%s host key %q must be an IPv4 address", origin, address)
		}
		hostVars, err := varsOf(entry[1], origin+" host "+address)
		if err != nil {
			return err
		}
		position, exists := index[address]
		if !exists {
			*hosts = append(*hosts, inventoryHost{address: address})
			position = len(*hosts) - 1
			index[address] = position
		}
		host := &(*hosts)[position]
		host.sources = append(host.sources, sources...)
		if len(hostVars) != 0 {
			host.sources = append(host.sources, varSource{origin: origin + " host " + address, depth: 1 << 20, vars: hostVars})
		}
	}
	childEntries, err := mappingEntries(mappingLookup(group, "children"))
	if err != nil {
		return fmt.Errorf("%s children: %w", origin, err)
	}
	for _, child := range childEntries {
		if err := collectHosts(child[1], "group "+child[0].Value, depth+1, sources, hosts, index); err != nil {
			return err
		}
	}
	return nil
}

func dedupeVarSources(host inventoryHost) inventoryHost {
	seen := make(map[string]struct{}, len(host.sources))
	deduped := make([]varSource, 0, len(host.sources))
	for _, source := range host.sources {
		key := fmt.Sprintf("%s\x00%d", source.origin, source.depth)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, source)
	}
	host.sources = deduped
	return host
}

func inventoryCIDR(hosts []inventoryHost) (subnet.Layout, error) {
	prefixes := make(map[string]struct{})
	for _, host := range hosts {
		address, err := netip.ParseAddr(host.address)
		if err != nil || !address.Is4() {
			return subnet.Layout{}, fmt.Errorf("inventory host key %q must be an IPv4 address", host.address)
		}
		octets := address.As4()
		prefixes[fmt.Sprintf("%d.%d.%d.0/24", octets[0], octets[1], octets[2])] = struct{}{}
	}
	if len(prefixes) != 1 {
		list := make([]string, 0, len(prefixes))
		for prefix := range prefixes {
			list = append(list, prefix)
		}
		sort.Strings(list)
		return subnet.Layout{}, fmt.Errorf("all managed hosts must share one /24; inventory spans %s", strings.Join(list, ", "))
	}
	for prefix := range prefixes {
		return subnet.Parse(prefix)
	}
	return subnet.Layout{}, errors.New("inventory has no managed hosts")
}

// ParseInventory reads a Pigsty-compatible inventory document and returns the
// equivalent internal configuration.
func ParseInventory(data []byte) (File, error) {
	if len(data) > maxInventoryBytes {
		return File{}, errors.New("inventory exceeds the 4 MiB limit")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return File{}, fmt.Errorf("decode inventory YAML: %w", err)
	}
	if len(document.Content) != 1 {
		return File{}, errors.New("inventory must be exactly one YAML document")
	}
	root := document.Content[0]
	if !isMapping(root) {
		return File{}, errors.New("inventory root must be a mapping with an all: group")
	}
	all := mappingLookup(root, "all")
	if all == nil {
		return File{}, errors.New("inventory has no all: group")
	}
	globalVars, err := varsOf(mappingLookup(all, "vars"), "all.vars")
	if err != nil {
		return File{}, err
	}
	hosts := make([]inventoryHost, 0)
	index := make(map[string]int)
	inherited := []varSource{}
	if len(globalVars) != 0 {
		inherited = append(inherited, varSource{origin: "all.vars", depth: 0, vars: globalVars})
	}
	if err := collectHosts(all, "all", 0, inherited, &hosts, index); err != nil {
		return File{}, err
	}
	if len(hosts) == 0 {
		return File{}, errors.New("inventory defines no hosts")
	}

	adminIP := ""
	if node, present := globalVars["admin_ip"]; present {
		if err := node.Decode(&adminIP); err != nil {
			return File{}, errors.New("all.vars admin_ip must be a string")
		}
	}

	managed := make([]inventoryHost, 0, len(hosts))
	for _, host := range hosts {
		host = dedupeVarSources(host)
		skip, _, err := host.lookupBool("vm_skip")
		if err != nil {
			return File{}, err
		}
		if skip {
			continue
		}
		managed = append(managed, host)
	}
	if len(managed) == 0 {
		return File{}, errors.New("inventory has no managed hosts; every host sets vm_skip: true")
	}
	layout, err := inventoryCIDR(managed)
	if err != nil {
		return File{}, err
	}

	file := File{
		Version: 1,
		Name:    InventoryProjectName,
		Arch:    "native",
		Network: NetworkConfig{Mode: "private", CIDR: layout.CIDR(), HostAddress: layout.HostAddress(), DHCPEnd: layout.DHCPEnd()},
	}

	sshUser := ""
	sshUserOwner := ""
	vmArch := ""
	vmArchOwner := ""
	vmArchHosts := 0
	controlIndex := -1
	for _, host := range managed {
		name, err := host.nodeName()
		if err != nil {
			return File{}, err
		}
		node := NodeConfig{Name: name, Address: host.address}

		imageAlias := defaultImage
		if value, found, err := host.lookupString("vm_image"); err != nil {
			return File{}, err
		} else if found {
			imageAlias = value
		}
		node.Image, err = image.CanonicalReference(imageAlias)
		if err != nil {
			return File{}, fmt.Errorf("host %s vm_image: %w", host.address, err)
		}

		if value, found, err := host.lookupString("vm_arch"); err != nil {
			return File{}, err
		} else if found {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "native" && value != "amd64" && value != "arm64" {
				return File{}, fmt.Errorf("host %s vm_arch must be native, amd64, or arm64", host.address)
			}
			vmArchHosts++
			if vmArchHosts == 1 {
				vmArch, vmArchOwner = value, host.address
			} else if vmArch != value {
				return File{}, fmt.Errorf("hosts %s and %s declare different vm_arch values; farrow uses one guest architecture per deployment", vmArchOwner, host.address)
			}
		}

		node.CPUs = defaultCPU
		if value, found, err := host.lookupInt("vm_cpu"); err != nil {
			return File{}, err
		} else if found {
			if value < 1 || value > maxVirtualCPUs {
				return File{}, fmt.Errorf("host %s vm_cpu must be 1..%d", host.address, maxVirtualCPUs)
			}
			node.CPUs = int(value)
		}

		node.Memory = Size(defaultMemMiB << 20)
		if value, found, err := host.lookupSize("vm_mem", 1<<20); err != nil {
			return File{}, err
		} else if found {
			node.Memory = Size(value)
		}

		node.RootDisk = Size(defaultDiskGiB * spec.GiB)
		if value, found, err := host.lookupSize("vm_disk", spec.GiB); err != nil {
			return File{}, err
		} else if found {
			node.RootDisk = Size(value)
		}

		if disks, found, err := host.lookupDisks(); err != nil {
			return File{}, err
		} else if found {
			node.Disks = disks
		} else {
			node.Disks = []DiskConfig{{Name: "data", Size: Size(defaultDataGiB * spec.GiB), Mount: "/data", Filesystem: "xfs"}}
		}

		if aliases, found, err := host.lookupStringList("vm_alias"); err != nil {
			return File{}, err
		} else if found {
			node.HostAliases = aliases
		}

		if shares, found, err := host.lookupShares(); err != nil {
			return File{}, err
		} else if found {
			node.Shares = shares
		}

		user := defaultSSHUser
		if value, found, err := host.lookupString("node_admin_username"); err != nil {
			return File{}, err
		} else if found && value != "" {
			user = value
		}
		if sshUser == "" {
			sshUser, sshUserOwner = user, host.address
		} else if sshUser != user {
			return File{}, fmt.Errorf("hosts %s and %s declare different node_admin_username values; farrow v1 uses one login user per deployment", sshUserOwner, host.address)
		}
		if value, found, err := host.lookupInt("node_admin_uid"); err != nil {
			return File{}, err
		} else if found && user == defaultSSHUser && value != defaultAdminUID {
			return File{}, fmt.Errorf("host %s sets node_admin_uid=%d; farrow provisions %s with the fixed UID %d", host.address, value, defaultSSHUser, defaultAdminUID)
		}

		if host.address == adminIP {
			controlIndex = len(file.Nodes)
		}
		file.Nodes = append(file.Nodes, node)
	}
	if controlIndex < 0 {
		controlIndex = 0
	}
	file.Nodes[controlIndex].Control = true
	file.SSH.User = sshUser
	if vmArchHosts != 0 {
		if vmArchHosts != len(managed) {
			return File{}, errors.New("vm_arch must resolve on every managed host; define it once in all.vars")
		}
		file.Arch = vmArch
	}

	if err := file.Validate(); err != nil {
		return File{}, err
	}
	return file, nil
}

// DetectFormat classifies raw configuration bytes without fully parsing them.
func DetectFormat(data []byte) (string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(bytes.TrimSpace(data), &document); err != nil {
		return "", fmt.Errorf("decode configuration YAML: %w", err)
	}
	if len(document.Content) == 0 {
		return "", errors.New("configuration is empty")
	}
	root := document.Content[0]
	if !isMapping(root) {
		return "", errors.New("configuration root must be a YAML mapping")
	}
	if mappingLookup(root, "all") != nil {
		return "inventory", nil
	}
	if mappingLookup(root, "version") != nil && mappingLookup(root, "nodes") != nil {
		return "legacy", nil
	}
	return "", errors.New("unrecognized configuration: expected a Pigsty-compatible inventory with a top-level all: group")
}
