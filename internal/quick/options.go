package quick

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/spec"
)

type Options struct {
	Image             string
	CPUs              int
	Memory            int64
	RootDisk          int64
	DataDisk          int64
	NoDataDisk        bool
	NoDefaultForwards bool
	Forwards          []spec.Forward
}

func (o Options) HasOverrides() bool {
	return o.Image != "" || o.CPUs != 0 || o.Memory != 0 || o.RootDisk != 0 || o.DataDisk != 0 || o.NoDataDisk || o.NoDefaultForwards || len(o.Forwards) > 0
}

func (o Options) Resolve() (spec.Resolved, error) {
	if o.NoDataDisk && o.DataDisk != 0 {
		return spec.Resolved{}, errors.New("--no-data-disk conflicts with --data-disk")
	}
	resolved := spec.Quick(!o.NoDataDisk, !o.NoDefaultForwards)
	if o.Image != "" {
		resolved.Image = image.CanonicalAlias(o.Image)
	}
	node := &resolved.Nodes[0]
	if o.CPUs != 0 {
		if o.CPUs < 1 || o.CPUs > 64 {
			return spec.Resolved{}, errors.New("quick CPUs must be in range 1..64")
		}
		node.CPUs = o.CPUs
	}
	if o.Memory != 0 {
		if o.Memory < 512<<20 {
			return spec.Resolved{}, errors.New("quick memory must be at least 512MiB")
		}
		node.Memory = o.Memory
	}
	if o.RootDisk != 0 {
		node.RootDisk = o.RootDisk
	}
	if o.DataDisk != 0 {
		if len(node.Disks) == 0 {
			node.Disks = []spec.Disk{{Name: "data", Mount: "/data"}}
		}
		node.Disks[0].Size = o.DataDisk
	}
	node.Forwards = append(node.Forwards, o.Forwards...)
	seen := make(map[string]struct{})
	for _, forward := range node.Forwards {
		if forward.Protocol != "tcp" || forward.Bind == "" || forward.Host == 0 || forward.Guest == 0 {
			return spec.Resolved{}, errors.New("quick forward is incomplete or non-TCP")
		}
		key := fmt.Sprintf("%s/%d", forward.Bind, forward.Host)
		if _, exists := seen[key]; exists {
			return spec.Resolved{}, fmt.Errorf("duplicate quick forward %s", key)
		}
		seen[key] = struct{}{}
	}
	return resolved, nil
}

type DriftError struct {
	Action string
	Before spec.Resolved
	After  spec.Resolved
}

type CapabilityError struct{ Reason string }

func (e *CapabilityError) Error() string { return "host capability unavailable: " + e.Reason }

func (e *DriftError) Error() string { return "quick state drift: " + e.Action + " required" }

func classifyDrift(before, after spec.Resolved) string {
	if before.Name != after.Name || before.Image != after.Image || before.Network != after.Network || !reflect.DeepEqual(before.Private, after.Private) || before.SSHUser != after.SSHUser || len(before.Nodes) != len(after.Nodes) {
		return "recreate"
	}
	action := "no-op"
	for nodeIndex := range before.Nodes {
		oldNode, newNode := before.Nodes[nodeIndex], after.Nodes[nodeIndex]
		if oldNode.Name != newNode.Name || oldNode.Control != newNode.Control || oldNode.Address != newNode.Address || oldNode.Image != newNode.Image || !reflect.DeepEqual(oldNode.Aliases, newNode.Aliases) {
			return "recreate"
		}
		if newNode.RootDisk < oldNode.RootDisk || len(newNode.Disks) < len(oldNode.Disks) {
			return "explicit destructive recreate"
		}
		if len(newNode.Disks) != len(oldNode.Disks) {
			action = "stop"
		}
		for index := range oldNode.Disks {
			if newNode.Disks[index].Name != oldNode.Disks[index].Name || newNode.Disks[index].Mount != oldNode.Disks[index].Mount || newNode.Disks[index].Filesystem != oldNode.Disks[index].Filesystem || newNode.Disks[index].Persistent != oldNode.Disks[index].Persistent || newNode.Disks[index].Size < oldNode.Disks[index].Size {
				return "explicit destructive recreate"
			}
			if newNode.Disks[index].Size != oldNode.Disks[index].Size {
				action = "stop"
			}
		}
		if newNode.RootDisk != oldNode.RootDisk {
			action = "stop"
		}
		if action == "no-op" && (newNode.CPUs != oldNode.CPUs || newNode.Memory != oldNode.Memory || !reflect.DeepEqual(newNode.Forwards, oldNode.Forwards) || !reflect.DeepEqual(newNode.Shares, oldNode.Shares)) {
			action = "restart"
		}
	}
	if action != "no-op" {
		return action
	}
	return "reconcile"
}

func materializeExistingPorts(desired, existing spec.Resolved) spec.Resolved {
	desired.Nodes[0].Forwards = spec.ReuseMaterializedForwardPorts(desired.Nodes[0].Forwards, existing.Nodes[0].Forwards)
	return desired
}

func compareDesired(existing, desired spec.Resolved) error {
	if reflect.DeepEqual(existing, desired) {
		return nil
	}
	return &DriftError{Action: classifyDrift(existing, desired), Before: existing, After: desired}
}

func ApplyOptions(resolved spec.Resolved, options Options) (spec.Resolved, error) {
	if len(resolved.Nodes) != 1 {
		return spec.Resolved{}, errors.New("quick CLI overrides require exactly one node")
	}
	if options.NoDefaultForwards {
		return spec.Resolved{}, errors.New("--no-default-forwards is only meaningful without declarative YAML")
	}
	if options.NoDataDisk && options.DataDisk != 0 {
		return spec.Resolved{}, errors.New("--no-data-disk conflicts with --data-disk")
	}
	node := &resolved.Nodes[0]
	if options.Image != "" {
		resolved.Image = image.CanonicalAlias(options.Image)
		node.Image = ""
	}
	if options.CPUs != 0 {
		node.CPUs = options.CPUs
	}
	if options.Memory != 0 {
		node.Memory = options.Memory
	}
	if options.RootDisk != 0 {
		node.RootDisk = options.RootDisk
	}
	if options.NoDataDisk {
		node.Disks = nil
	} else if options.DataDisk != 0 {
		if len(node.Disks) == 0 {
			node.Disks = []spec.Disk{{Name: "data", Mount: "/data"}}
		}
		node.Disks[0].Size = options.DataDisk
	}
	node.Forwards = append(node.Forwards, options.Forwards...)
	if node.CPUs < 1 || node.CPUs > 64 || node.Memory < 512<<20 || node.RootDisk <= 0 || len(node.Disks) > 1 {
		return spec.Resolved{}, errors.New("declarative quick overrides produce invalid resources")
	}
	return resolved, nil
}
