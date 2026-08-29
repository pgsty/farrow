package config

import (
	"fmt"
	"strings"

	"github.com/pgsty/farrow/internal/network/subnet"
)

// Built-in templates are deliberately generic topologies — nodes, addresses,
// and vm_* knobs only. Pigsty-specific service topology belongs to Pigsty's
// own conf templates, which carry the same vm_* variables.

type templateNode struct {
	LastOctet int
	Name      string
}

var templateProfiles = map[string][]templateNode{
	"meta": {{10, "meta"}},
	"dual": {{10, "meta"}, {11, "node-1"}},
	"trio": {{10, "meta"}, {11, "node-1"}, {12, "node-2"}},
	"full": {{10, "meta"}, {11, "node-1"}, {12, "node-2"}, {13, "node-3"}},
}

// TemplateNames lists the built-in lab templates in size order.
func TemplateNames() []string { return []string{"meta", "dual", "trio", "full"} }

func ValidTemplate(name string) bool {
	_, exists := templateProfiles[name]
	return exists
}

// Template renders a built-in lab as a Pigsty-compatible inventory. The
// optional cidr rebases the /24 while keeping each node's last octet.
func Template(name, cidr string) ([]byte, error) {
	nodes, exists := templateProfiles[name]
	if !exists {
		return nil, fmt.Errorf("unknown lab template %q; available: %s", name, strings.Join(TemplateNames(), ", "))
	}
	if cidr == "" {
		cidr = subnet.DefaultCIDR
	}
	layout, err := subnet.Parse(cidr)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(layout.CIDR(), ".0/24")
	var output strings.Builder
	fmt.Fprintf(&output, "---\n")
	fmt.Fprintf(&output, "# Farrow lab %q: %d fixed-IP node(s) on %s.\n", name, len(nodes), layout.CIDR())
	fmt.Fprintf(&output, "#\n")
	fmt.Fprintf(&output, "# This file is a Pigsty-compatible Ansible inventory. Farrow reads the\n")
	fmt.Fprintf(&output, "# vm_* variables plus nodename/admin_ip, pg_cluster/pg_seq, and the\n")
	fmt.Fprintf(&output, "# node_admin identity fields. Other variables stay opaque to farrow.\n")
	fmt.Fprintf(&output, "#\n")
	fmt.Fprintf(&output, "# Per-host or group-level knobs, all optional:\n")
	fmt.Fprintf(&output, "#   vm_arch: arm64                          native, amd64, or arm64 (deployment-wide)\n")
	fmt.Fprintf(&output, "#   vm_cpu: 2                                cores\n")
	fmt.Fprintf(&output, "#   vm_mem: 4096                             memory (MiB, or \"8GiB\")\n")
	fmt.Fprintf(&output, "#   vm_disk: 64                              root disk (GiB)\n")
	fmt.Fprintf(&output, "#   vm_image: %-6s                         image family, channel, or image@version selector\n", defaultImage)
	fmt.Fprintf(&output, "#   vm_version: 9.7                         newest matching numeric version prefix\n")
	fmt.Fprintf(&output, "#   vm_disks: [{ path: /data, size: 128 }]   extra data disks\n")
	fmt.Fprintf(&output, "#   vm_alias: [i.pigsty]                     /etc/hosts aliases\n")
	fmt.Fprintf(&output, "#   vm_skip: true                            host not managed by farrow\n")
	fmt.Fprintf(&output, "all:\n")
	fmt.Fprintf(&output, "  vars:\n")
	fmt.Fprintf(&output, "    admin_ip: %s.%d\n", prefix, nodes[0].LastOctet)
	fmt.Fprintf(&output, "  children:\n")
	fmt.Fprintf(&output, "    nodes:\n")
	fmt.Fprintf(&output, "      hosts:\n")
	for _, node := range nodes {
		fmt.Fprintf(&output, "        %s.%d: { nodename: %s }\n", prefix, node.LastOctet, node.Name)
	}
	data := []byte(output.String())
	if _, err := ParseInventory(data); err != nil {
		return nil, fmt.Errorf("internal template %q does not round-trip: %w", name, err)
	}
	return data, nil
}
