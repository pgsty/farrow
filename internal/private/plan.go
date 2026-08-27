// Package private builds the deterministic node and lease intent shared by
// Darwin socket_vmnet and Linux bridge runtimes.
package private

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/pgsty/farrow/internal/runtimepath"
	"github.com/pgsty/farrow/internal/spec"
)

var nodePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type UUIDSource func() (string, error)

type NodePlan struct {
	Name          string             `json:"name"`
	Control       bool               `json:"control"`
	Address       string             `json:"address"`
	Aliases       []string           `json:"aliases,omitempty"`
	ManagementMAC string             `json:"management_mac"`
	PrivateMAC    string             `json:"private_mac"`
	VMUUID        string             `json:"vm_uuid"`
	Runtime       lease.RuntimePaths `json:"runtime"`
}

type Plan struct {
	ProjectID string      `json:"project_id"`
	Control   string      `json:"control"`
	Nodes     []NodePlan  `json:"nodes"`
	Lease     lease.Lease `json:"lease"`
}

func validateResolved(value spec.Resolved) error {
	if value.Schema != 1 || value.Network != "private" || value.Private == nil || len(value.Nodes) < 1 || len(value.Nodes) > 20 || value.SSHUser == "" {
		return errors.New("private plan requires schema 1, private network, and 1..20 nodes")
	}
	if _, err := value.SSHWaitTimeout(); err != nil {
		return err
	}
	layout, err := subnet.Parse(value.Private.CIDR)
	if err != nil || value.Private.HostAddress != layout.HostAddress() || value.Private.DHCPEnd != layout.DHCPEnd() {
		return errors.New("private plan network contract is invalid")
	}
	controls := 0
	names := make(map[string]struct{})
	addresses := make(map[string]struct{})
	for _, node := range value.Nodes {
		if !nodePattern.MatchString(node.Name) || !layout.IsStatic(node.Address) || node.CPUs < 1 || node.Memory < 512<<20 || node.RootDisk <= 0 {
			return fmt.Errorf("private node %q identity, address, or resources are invalid", node.Name)
		}
		if _, duplicate := names[node.Name]; duplicate {
			return fmt.Errorf("duplicate private node %q", node.Name)
		}
		if _, duplicate := addresses[node.Address]; duplicate {
			return fmt.Errorf("duplicate private address %q", node.Address)
		}
		names[node.Name] = struct{}{}
		addresses[node.Address] = struct{}{}
		if node.Control {
			controls++
		}
	}
	if controls != 1 {
		return fmt.Errorf("private plan requires exactly one control node, got %d", controls)
	}
	return nil
}

func runtimePaths(node string, uid int) (lease.RuntimePaths, error) {
	directory, err := runtimepath.Directory(node, uid)
	if err != nil {
		return lease.RuntimePaths{}, err
	}
	return lease.RuntimePaths{Directory: directory, QMP: filepath.Join(directory, "qmp.sock"), PIDFile: filepath.Join(directory, "qemu.pid")}, nil
}

func existingUUIDs(existing *lease.Lease, projectID string, ownerUID int) (map[string]string, error) {
	result := make(map[string]string)
	if existing == nil {
		return result, nil
	}
	if existing.ProjectID != projectID || existing.OwnerUID != ownerUID {
		return nil, errors.New("existing private lease does not belong to this project/owner")
	}
	for _, node := range existing.Nodes {
		result[node.Name] = node.VMUUID
	}
	return result, nil
}

func Build(resolved spec.Resolved, projectID string, ownerUID int, existing *lease.Lease, source UUIDSource) (Plan, error) {
	if err := validateResolved(resolved); err != nil {
		return Plan{}, err
	}
	if !identity.ValidUUID(projectID) || ownerUID < 0 {
		return Plan{}, errors.New("private plan project UUID or owner UID is invalid")
	}
	if source == nil {
		source = identity.NewUUID
	}
	knownUUIDs, err := existingUUIDs(existing, projectID, ownerUID)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{ProjectID: projectID, Nodes: make([]NodePlan, 0, len(resolved.Nodes))}
	leaseValue := lease.Lease{
		ProjectID: projectID, OwnerUID: ownerUID, CIDR: resolved.Private.CIDR,
		HostAddress: resolved.Private.HostAddress, DHCPEnd: resolved.Private.DHCPEnd,
		Nodes: make([]lease.Node, 0, len(resolved.Nodes)),
	}
	for _, node := range resolved.Nodes {
		vmUUID := knownUUIDs[node.Name]
		if vmUUID == "" {
			vmUUID, err = source()
			if err != nil {
				return Plan{}, err
			}
		}
		if !identity.ValidUUID(vmUUID) {
			return Plan{}, fmt.Errorf("private node %s VM UUID is invalid", node.Name)
		}
		managementMAC, err := identity.MAC(node.Address, identity.NICManagement)
		if err != nil {
			return Plan{}, err
		}
		privateMAC, err := identity.MAC(node.Address, identity.NICPrivate)
		if err != nil {
			return Plan{}, err
		}
		runtimeValue, err := runtimePaths(node.Name, ownerUID)
		if err != nil {
			return Plan{}, fmt.Errorf("private node %s runtime path: %w", node.Name, err)
		}
		nodePlan := NodePlan{
			Name: node.Name, Control: node.Control, Address: node.Address,
			Aliases: append([]string(nil), node.Aliases...), ManagementMAC: managementMAC,
			PrivateMAC: privateMAC, VMUUID: vmUUID, Runtime: runtimeValue,
		}
		plan.Nodes = append(plan.Nodes, nodePlan)
		leaseValue.Nodes = append(leaseValue.Nodes, lease.Node{
			Name: node.Name, Address: node.Address, ManagementMAC: managementMAC,
			PrivateMAC: privateMAC, VMUUID: vmUUID, Phase: lease.Reserved,
		})
		if node.Control {
			plan.Control = node.Name
		}
	}
	sort.Slice(leaseValue.Nodes, func(i, j int) bool { return leaseValue.Nodes[i].Name < leaseValue.Nodes[j].Name })
	validationLease := leaseValue
	validationLease.Schema = lease.Schema
	validationLease.Generation = 1
	validationLease.CreatedAt = time.Unix(1, 0).UTC()
	validationLease.UpdatedAt = validationLease.CreatedAt
	if err := lease.Validate(validationLease); err != nil {
		return Plan{}, err
	}
	plan.Lease = leaseValue
	return plan, nil
}

func (plan Plan) Node(name string) (NodePlan, bool) {
	for _, node := range plan.Nodes {
		if strings.EqualFold(node.Name, name) {
			return node, true
		}
	}
	return NodePlan{}, false
}
