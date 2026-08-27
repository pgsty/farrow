package private

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/pgsty/farrow/internal/cloudinit"
	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/spec"
)

type SeedInput struct {
	PublicKey  string
	PrivateKey string
	SpecHashes map[string]string
	Generation map[string]uint64
}

func prefixLength(cidr string) (int, error) {
	_, networkValue, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, err
	}
	ones, bits := networkValue.Mask.Size()
	if bits != 32 {
		return 0, errors.New("private seed requires IPv4 network")
	}
	return ones, nil
}

func projectHosts(resolved spec.Resolved) []cloudinit.Host {
	hosts := make([]cloudinit.Host, 0, len(resolved.Nodes)*2)
	for _, node := range resolved.Nodes {
		hosts = append(hosts, cloudinit.Host{Name: node.Name, Address: node.Address})
		for _, alias := range node.Aliases {
			hosts = append(hosts, cloudinit.Host{Name: alias, Address: node.Address})
		}
	}
	return hosts
}

func cloudDisks(node spec.Node) ([]cloudinit.Disk, error) {
	result := make([]cloudinit.Disk, 0, len(node.Disks))
	for _, disk := range node.Disks {
		serial, err := identity.DiskSerial(node.Name, disk.Name)
		if err != nil {
			return nil, err
		}
		filesystem := disk.Filesystem
		if filesystem == "" {
			filesystem = "auto"
		}
		result = append(result, cloudinit.Disk{Serial: serial, Mount: disk.Mount, Filesystem: filesystem})
	}
	return result, nil
}

func RenderSeeds(resolved spec.Resolved, plan Plan, input SeedInput) (map[string]cloudinit.Files, error) {
	if err := validateResolved(resolved); err != nil {
		return nil, err
	}
	if len(plan.Nodes) != len(resolved.Nodes) || strings.TrimSpace(input.PublicKey) == "" {
		return nil, errors.New("private seed plan or public key is incomplete")
	}
	for _, nodeSpec := range resolved.Nodes {
		if len(input.SpecHashes[nodeSpec.Name]) != 64 {
			return nil, fmt.Errorf("private seed node hash missing for node %s", nodeSpec.Name)
		}
	}
	prefix, err := prefixLength(resolved.Private.CIDR)
	if err != nil {
		return nil, err
	}
	hosts := projectHosts(resolved)
	result := make(map[string]cloudinit.Files, len(resolved.Nodes))
	for _, nodeSpec := range resolved.Nodes {
		nodePlan, ok := plan.Node(nodeSpec.Name)
		if !ok || nodePlan.Address != nodeSpec.Address {
			return nil, fmt.Errorf("private seed plan missing node %s", nodeSpec.Name)
		}
		generation := input.Generation[nodeSpec.Name]
		if generation == 0 {
			return nil, fmt.Errorf("private seed generation missing for node %s", nodeSpec.Name)
		}
		disks, err := cloudDisks(nodeSpec)
		if err != nil {
			return nil, err
		}
		privateKey := ""
		if nodeSpec.Control && len(resolved.Nodes) > 1 {
			privateKey = input.PrivateKey
			if privateKey == "" {
				return nil, errors.New("private control seed requires the deployment private key")
			}
		}
		files, err := cloudinit.Render(cloudinit.Input{
			Node: nodeSpec.Name, Hostname: nodeSpec.Name,
			Generation: generation, SpecHash: input.SpecHashes[nodeSpec.Name], SSHUser: resolved.SSHUser,
			PublicKey: strings.TrimSpace(input.PublicKey), PrivateKey: privateKey,
			Control: nodeSpec.Control, MgmtMAC: nodePlan.ManagementMAC,
			Private: &cloudinit.PrivateNetwork{MAC: nodePlan.PrivateMAC, Address: nodeSpec.Address, Prefix: prefix, HostAddress: resolved.Private.HostAddress},
			Hosts:   hosts, Disks: disks, Shares: privateCloudShares(nodeSpec.Shares),
		})
		if err != nil {
			return nil, fmt.Errorf("render private seed for %s: %w", nodeSpec.Name, err)
		}
		result[nodeSpec.Name] = files
	}
	return result, nil
}
