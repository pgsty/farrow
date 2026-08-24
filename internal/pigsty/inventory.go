// Package pigsty implements the narrow, audited integration boundary between
// Piglet-owned VM profiles and Pigsty inventory templates.
package pigsty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pgsty/piglet/internal/network/subnet"
	"github.com/pgsty/piglet/internal/profile"
	"go.yaml.in/yaml/v3"
)

const maxInventoryBytes = 8 << 20

var ipv4Token = regexp.MustCompile(`(?:[0-9]{1,3}\.){3}[0-9]{1,3}`)

// Result is a validated Pigsty inventory rendering. Direct-mode default
// renders remain byte-identical; custom and typed-overlay renders are emitted
// as canonical YAML without source comments.
type Result struct {
	Data           []byte
	SourcePath     string
	Profile        string
	SourceCIDR     string
	TargetCIDR     string
	Matches        int
	Replacements   int
	OverlayChanges int
	TuneChanges    int
	NoProxyChanges int
	InventoryMode  profile.InventoryMode
	UnusedVMNodes  []string
	Scale          int
	SourceSHA256   string
	OutputSHA256   string
}

type semantics struct {
	Matches      int
	Replacements int
	Hosts        map[string]struct{}
	VIPs         map[string]struct{}
	References   map[string]struct{}
	Admins       map[string]struct{}
}

func newSemantics() *semantics {
	return &semantics{
		Hosts: make(map[string]struct{}), VIPs: make(map[string]struct{}),
		References: make(map[string]struct{}), Admins: make(map[string]struct{}),
	}
}

// Render selects the Pigsty inventory reference owned by profileName, applies
// its explicit topology policy, and rebases allowlisted address semantics while
// preserving final octets. The source file is read only.
func Render(sourceRoot, profileName, targetCIDR string) (Result, error) {
	return RenderScaled(sourceRoot, profileName, targetCIDR, profile.DefaultScale)
}

// RenderScaled binds VM resource scaling to inventory tuning. Profiles whose
// control node has fewer than four vCPUs use Pigsty's tiny node/PG templates,
// matching the existing configure policy without unsafe address substitution.
func RenderScaled(sourceRoot, profileName, targetCIDR string, scale int) (Result, error) {
	if sourceRoot == "" || !filepath.IsAbs(sourceRoot) || filepath.Clean(sourceRoot) != sourceRoot {
		return Result{}, errors.New("pigsty source root must be a clean absolute path")
	}
	rootInfo, err := os.Lstat(sourceRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Result{}, fmt.Errorf("pigsty source root must be a real directory: %s", sourceRoot)
	}
	canonicalRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil || canonicalRoot != sourceRoot {
		return Result{}, fmt.Errorf("pigsty source root must not traverse symlinks: %s", sourceRoot)
	}

	file, descriptor, err := profile.LoadWithOverrides(profileName, profile.Overrides{Scale: scale})
	if err != nil {
		return Result{}, err
	}
	resolved, err := file.Resolve()
	if err != nil || resolved.Private == nil || len(resolved.Nodes) == 0 {
		return Result{}, fmt.Errorf("profile %s has no valid private inventory contract", profileName)
	}
	controlAddress := ""
	controlCPUs := 0
	nodeAddresses := make(map[string]string, len(resolved.Nodes))
	for _, node := range resolved.Nodes {
		nodeAddresses[node.Name] = node.Address
		if node.Control {
			controlAddress = node.Address
			controlCPUs = node.CPUs
		}
	}
	if controlAddress == "" {
		return Result{}, fmt.Errorf("profile %s has no control node", profileName)
	}
	for _, unused := range descriptor.InventoryUnusedNodes {
		if _, exists := nodeAddresses[unused]; !exists {
			return Result{}, fmt.Errorf("profile %s declares unknown unused inventory node %s", profileName, unused)
		}
	}

	sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(descriptor.InventoryRef))
	relative, err := filepath.Rel(sourceRoot, sourcePath)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Result{}, errors.New("profile inventory reference escapes the Pigsty source root")
	}
	canonicalSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil || canonicalSource != sourcePath {
		return Result{}, fmt.Errorf("pigsty inventory source must not traverse symlinks: %s", sourcePath)
	}
	data, err := readSource(sourcePath)
	if err != nil {
		return Result{}, err
	}
	document, err := decodeYAML(data)
	if err != nil {
		return Result{}, fmt.Errorf("invalid Pigsty inventory source: %w", err)
	}

	if targetCIDR == "" {
		targetCIDR = subnet.DefaultCIDR
	}
	target, err := subnet.Parse(targetCIDR)
	if err != nil {
		return Result{}, err
	}
	source := subnet.Default()
	overlayChanges := 0
	if descriptor.InventoryMode == profile.InventoryBuildSubset {
		overlayChanges, err = applyBuildSubset(document.Content[0], nodeAddresses, controlAddress, source.Prefix())
		if err != nil {
			return Result{}, fmt.Errorf("apply profile %s inventory overlay: %w", profileName, err)
		}
	}
	tuneChanges := applyTinyTune(document.Content[0], controlCPUs)

	observed := newSemantics()
	if err := rewriteSemantics(document.Content[0], []string{"inventory"}, source.Prefix(), target, observed); err != nil {
		return Result{}, err
	}
	if observed.Matches == 0 {
		return Result{}, fmt.Errorf("pigsty inventory %s contains no allowlisted addresses in %s", sourcePath, source.CIDR())
	}
	if err := validateTopology(profileName, nodeAddresses, descriptor.InventoryUnusedNodes, observed); err != nil {
		return Result{}, err
	}
	noProxyChanges := 0
	if !target.IsDefault() {
		noProxyChanges, err = ensureNoProxy(document.Content[0], target.Prefix())
		if err != nil {
			return Result{}, err
		}
	}

	resultData := append([]byte(nil), data...)
	if !target.IsDefault() || overlayChanges != 0 || tuneChanges != 0 || noProxyChanges != 0 {
		clearComments(document)
		resultData, err = encodeYAML(document)
		if err != nil {
			return Result{}, fmt.Errorf("encode Pigsty inventory: %w", err)
		}
		if _, err := decodeYAML(resultData); err != nil {
			return Result{}, fmt.Errorf("rebased Pigsty inventory is invalid: %w", err)
		}
	}
	if !target.IsDefault() && countPrefixTokens(resultData, source.Prefix()) != 0 {
		return Result{}, errors.New("rebased Pigsty inventory still contains default-subnet address tokens")
	}
	sourceDigest := sha256.Sum256(data)
	outputDigest := sha256.Sum256(resultData)
	return Result{
		Data: resultData, SourcePath: sourcePath, Profile: profileName,
		SourceCIDR: source.CIDR(), TargetCIDR: target.CIDR(), Matches: observed.Matches,
		Replacements: observed.Replacements, OverlayChanges: overlayChanges,
		TuneChanges: tuneChanges, NoProxyChanges: noProxyChanges, InventoryMode: descriptor.InventoryMode,
		UnusedVMNodes: append([]string(nil), descriptor.InventoryUnusedNodes...),
		Scale:         scale,
		SourceSHA256:  hex.EncodeToString(sourceDigest[:]), OutputSHA256: hex.EncodeToString(outputDigest[:]),
	}, nil
}

func readSource(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxInventoryBytes {
		return nil, fmt.Errorf("pigsty inventory source must be a non-empty regular file no larger than %d bytes: %s", maxInventoryBytes, path)
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Pigsty inventory source: %w", err)
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("pigsty inventory source changed during validation")
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxInventoryBytes+1))
	if err != nil || len(data) > maxInventoryBytes {
		return nil, errors.New("read Pigsty inventory source within size limit")
	}
	return data, nil
}

func rewriteSemantics(node *yaml.Node, path []string, source netip.Prefix, target subnet.Layout, observed *semantics) error {
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			originalKey := key.Value
			addresses := sourceAddresses(originalKey, source)
			if len(addresses) != 0 {
				if lastPath(path) != "hosts" || len(addresses) != 1 || originalKey != addresses[0] {
					return fmt.Errorf("unclassified default-subnet address at %s key %q", strings.Join(path, "."), originalKey)
				}
				observed.Matches++
				observed.Hosts[addresses[0]] = struct{}{}
				if !target.IsDefault() {
					parsed, _ := netip.ParseAddr(addresses[0])
					key.Value = target.Address(parsed.As4()[3])
					observed.Replacements++
				}
			}
			if err := rewriteSemantics(value, appendPath(path, originalKey), source, target, observed); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := rewriteSemantics(child, appendPath(path, "[]"), source, target, observed); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		addresses := sourceAddresses(node.Value, source)
		if len(addresses) == 0 {
			return nil
		}
		kind := classifyScalar(path)
		if kind == "" {
			return fmt.Errorf("unclassified default-subnet address at %s: %q", strings.Join(path, "."), node.Value)
		}
		for _, address := range addresses {
			observed.Matches++
			switch kind {
			case "admin":
				observed.Admins[address] = struct{}{}
			case "vip":
				observed.VIPs[address] = struct{}{}
			case "reference":
				observed.References[address] = struct{}{}
			}
		}
		if !target.IsDefault() {
			node.Value, observed.Replacements = rewriteValue(node.Value, source, target, observed.Replacements)
		}
	}
	return nil
}

func classifyScalar(path []string) string {
	key := lastPath(path)
	switch key {
	case "admin_ip":
		return "admin"
	case "pg_vip_address", "vip_address":
		return "vip"
	case "ip":
		if containsPath(path, "servers") {
			return "reference"
		}
	case "pg_host":
		if containsPath(path, "pg_exporters") {
			return "reference"
		}
	case "endpoint":
		if containsPath(path, "infra_portal") {
			return "reference"
		}
	case "replica_of":
		if containsPath(path, "redis_instances") {
			return "reference"
		}
	case "host":
		if containsPath(path, "redis_sentinel_monitor") {
			return "reference"
		}
	case "node_dns_servers", "node_etc_hosts", "node_ntp_servers":
		return "reference"
	case "[]":
		if len(path) >= 2 {
			switch path[len(path)-2] {
			case "node_dns_servers", "node_etc_hosts", "node_ntp_servers":
				return "reference"
			}
		}
	}
	return ""
}

func validateTopology(profileName string, nodeAddresses map[string]string, unused []string, observed *semantics) error {
	expected := make(map[string]struct{}, len(nodeAddresses))
	unusedSet := make(map[string]struct{}, len(unused))
	for _, name := range unused {
		unusedSet[name] = struct{}{}
	}
	for name, address := range nodeAddresses {
		if _, skip := unusedSet[name]; !skip {
			expected[address] = struct{}{}
		}
	}
	if !equalSet(expected, observed.Hosts) {
		return fmt.Errorf("profile %s VM/inventory host mismatch: expected=%v observed=%v", profileName, sortedSet(expected), sortedSet(observed.Hosts))
	}
	for address := range observed.VIPs {
		if _, conflict := observed.Hosts[address]; conflict {
			return fmt.Errorf("profile %s inventory VIP %s collides with a VM host", profileName, address)
		}
	}
	for address := range observed.Admins {
		if _, exists := observed.Hosts[address]; !exists {
			return fmt.Errorf("profile %s inventory admin address %s is not a VM host", profileName, address)
		}
	}
	for address := range observed.References {
		_, host := observed.Hosts[address]
		_, vip := observed.VIPs[address]
		if !host && !vip {
			return fmt.Errorf("profile %s inventory reference %s is neither a VM host nor a declared VIP", profileName, address)
		}
	}
	return nil
}

func applyBuildSubset(root *yaml.Node, nodes map[string]string, control string, source netip.Prefix) (int, error) {
	all, ok := mappingValue(root, "all")
	if !ok || all.Kind != yaml.MappingNode {
		return 0, errors.New("build inventory has no all mapping")
	}
	vars, ok := mappingValue(all, "vars")
	if !ok || vars.Kind != yaml.MappingNode {
		return 0, errors.New("build inventory has no all.vars mapping")
	}
	admin, ok := mappingValue(vars, "admin_ip")
	if !ok || admin.Kind != yaml.ScalarNode {
		return 0, errors.New("build inventory has no scalar all.vars.admin_ip")
	}
	changes := 0
	if admin.Value != control {
		admin.Value = control
		changes++
	}
	children, ok := mappingValue(all, "children")
	if !ok || children.Kind != yaml.MappingNode {
		return 0, errors.New("build inventory has no all.children mapping")
	}
	keep := make(map[string]struct{}, len(nodes))
	for _, address := range nodes {
		keep[address] = struct{}{}
	}
	content := make([]*yaml.Node, 0, len(children.Content))
	for index := 0; index+1 < len(children.Content); index += 2 {
		name, group := children.Content[index].Value, children.Content[index+1]
		hosts, hasHosts := mappingValue(group, "hosts")
		if !hasHosts {
			content = append(content, children.Content[index], group)
			continue
		}
		if hosts.Kind != yaml.MappingNode {
			return 0, fmt.Errorf("build inventory group %s hosts is not a mapping", name)
		}
		if name == "etcd" {
			if len(hosts.Content) != 2 {
				return 0, errors.New("build inventory etcd group must have one host")
			}
			if hosts.Content[0].Value != control {
				hosts.Content[0].Value = control
				changes++
			}
			content = append(content, children.Content[index], group)
			continue
		}
		filtered, filterChanges, err := filterHosts(hosts, keep, name == "infra", source)
		if err != nil {
			return 0, fmt.Errorf("build inventory group %s: %w", name, err)
		}
		changes += filterChanges
		if filtered == 0 {
			changes++
			continue
		}
		content = append(content, children.Content[index], group)
	}
	children.Content = content
	return changes, nil
}

func filterHosts(hosts *yaml.Node, keep map[string]struct{}, renumber bool, source netip.Prefix) (int, int, error) {
	content := make([]*yaml.Node, 0, len(hosts.Content))
	changes, sequence := 0, 0
	for index := 0; index+1 < len(hosts.Content); index += 2 {
		key, value := hosts.Content[index], hosts.Content[index+1]
		address, err := netip.ParseAddr(key.Value)
		if err != nil || !source.Contains(address) {
			return 0, changes, fmt.Errorf("unexpected host key %q", key.Value)
		}
		if _, wanted := keep[key.Value]; !wanted {
			changes++
			continue
		}
		sequence++
		if renumber {
			infraSeq, ok := mappingValue(value, "infra_seq")
			if !ok || infraSeq.Kind != yaml.ScalarNode {
				return 0, changes, fmt.Errorf("infra host %s has no infra_seq", key.Value)
			}
			wanted := strconv.Itoa(sequence)
			if infraSeq.Value != wanted {
				infraSeq.Value, infraSeq.Tag = wanted, "!!int"
				changes++
			}
		}
		content = append(content, key, value)
	}
	hosts.Content = content
	return sequence, changes, nil
}

func applyTinyTune(node *yaml.Node, controlCPUs int) int {
	if controlCPUs >= 4 {
		return 0
	}
	changes := 0
	var walk func(*yaml.Node)
	walk = func(current *yaml.Node) {
		switch current.Kind {
		case yaml.MappingNode:
			for index := 0; index+1 < len(current.Content); index += 2 {
				key, value := current.Content[index].Value, current.Content[index+1]
				if value.Kind == yaml.ScalarNode {
					switch {
					case key == "node_tune" && value.Value == "oltp":
						value.Value = "tiny"
						changes++
					case key == "pg_conf" && value.Value == "oltp.yml":
						value.Value = "tiny.yml"
						changes++
					}
				}
				walk(value)
			}
		case yaml.SequenceNode:
			for _, child := range current.Content {
				walk(child)
			}
		}
	}
	walk(node)
	return changes
}

func ensureNoProxy(node *yaml.Node, target netip.Prefix) (int, error) {
	changes := 0
	var walk func(*yaml.Node, []string) error
	walk = func(current *yaml.Node, path []string) error {
		switch current.Kind {
		case yaml.MappingNode:
			for index := 0; index+1 < len(current.Content); index += 2 {
				key, value := current.Content[index].Value, current.Content[index+1]
				if key == "no_proxy" && containsPath(path, "proxy_env") {
					if value.Kind != yaml.ScalarNode {
						return errors.New("proxy_env.no_proxy must be a scalar")
					}
					covered := false
					for _, item := range strings.Split(value.Value, ",") {
						prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
						if err == nil && prefix.Contains(target.Addr()) && prefix.Bits() <= target.Bits() {
							covered = true
							break
						}
					}
					if !covered {
						if strings.TrimSpace(value.Value) == "" {
							value.Value = target.String()
						} else {
							value.Value = strings.TrimRight(value.Value, " ,") + "," + target.String()
						}
						changes++
					}
				}
				if err := walk(value, appendPath(path, key)); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for _, child := range current.Content {
				if err := walk(child, appendPath(path, "[]")); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return changes, walk(node, []string{"inventory"})
}

func decodeYAML(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("inventory must contain one YAML mapping document")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("inventory must contain exactly one YAML document")
	}
	if err := validateNodes(document.Content[0], "inventory"); err != nil {
		return nil, err
	}
	return &document, nil
}

func validateNodes(node *yaml.Node, context string) error {
	if node == nil {
		return nil
	}
	if node.Anchor != "" || (strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!")) {
		return fmt.Errorf("%s contains YAML anchors or custom tags", context)
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Value == "<<" {
				return fmt.Errorf("%s contains a YAML merge key", context)
			}
			identity := key.Tag + "\x00" + key.Value
			if _, exists := seen[identity]; exists {
				return fmt.Errorf("%s contains duplicate key %q", context, key.Value)
			}
			seen[identity] = struct{}{}
			if err := validateNodes(node.Content[index+1], context+"."+key.Value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if err := validateNodes(child, fmt.Sprintf("%s[%d]", context, index)); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		return errors.New("inventory YAML aliases are not supported at the integration boundary")
	}
	return nil
}

func encodeYAML(document *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document.Content[0]); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func sourceAddresses(value string, source netip.Prefix) []string {
	result := make([]string, 0)
	data := []byte(value)
	for _, location := range ipv4Token.FindAllIndex(data, -1) {
		if !tokenBoundary(data, location[0]-1) || !tokenBoundary(data, location[1]) {
			continue
		}
		text := string(data[location[0]:location[1]])
		address, err := netip.ParseAddr(text)
		if err == nil && address.Is4() && source.Contains(address) {
			result = append(result, text)
		}
	}
	return result
}

func rewriteValue(value string, source netip.Prefix, target subnet.Layout, replacements int) (string, int) {
	data := []byte(value)
	locations := ipv4Token.FindAllIndex(data, -1)
	var output bytes.Buffer
	position := 0
	for _, location := range locations {
		if !tokenBoundary(data, location[0]-1) || !tokenBoundary(data, location[1]) {
			continue
		}
		text := string(data[location[0]:location[1]])
		address, err := netip.ParseAddr(text)
		if err != nil || !address.Is4() || !source.Contains(address) {
			continue
		}
		output.Write(data[position:location[0]])
		output.WriteString(target.Address(address.As4()[3]))
		position = location[1]
		replacements++
	}
	if position == 0 {
		return value, replacements
	}
	output.Write(data[position:])
	return output.String(), replacements
}

func countPrefixTokens(data []byte, prefix netip.Prefix) int {
	count := 0
	for _, location := range ipv4Token.FindAllIndex(data, -1) {
		if !tokenBoundary(data, location[0]-1) || !tokenBoundary(data, location[1]) {
			continue
		}
		address, err := netip.ParseAddr(string(data[location[0]:location[1]]))
		if err == nil && prefix.Contains(address) {
			count++
		}
	}
	return count
}

func tokenBoundary(data []byte, index int) bool {
	if index < 0 || index >= len(data) {
		return true
	}
	value := data[index]
	return !((value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || value == '_' || value == '.')
}

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}

func appendPath(path []string, value string) []string {
	result := make([]string, len(path), len(path)+1)
	copy(result, path)
	return append(result, value)
}

func lastPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

func containsPath(path []string, value string) bool {
	for _, segment := range path {
		if segment == value {
			return true
		}
	}
	return false
}

func clearComments(node *yaml.Node) {
	if node == nil {
		return
	}
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	for _, child := range node.Content {
		clearComments(child)
	}
}

func equalSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, exists := right[value]; !exists {
			return false
		}
	}
	return true
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
