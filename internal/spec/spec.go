// Package spec contains the typed resolved model used by the M0 quick slice.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const GiB int64 = 1024 * 1024 * 1024

type Forward struct {
	Bind     string `json:"bind"`
	Host     uint16 `json:"host"`
	Guest    uint16 `json:"guest"`
	Protocol string `json:"protocol"`
}

type Disk struct {
	Name       string `json:"name"`
	Size       int64  `json:"size_bytes"`
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem,omitempty"`
	Persistent bool   `json:"persistent"`
}

type Node struct {
	Name     string    `json:"name"`
	Control  bool      `json:"control"`
	Address  string    `json:"address,omitempty"`
	Image    string    `json:"image,omitempty"`
	Aliases  []string  `json:"host_aliases,omitempty"`
	CPUs     int       `json:"cpus"`
	Memory   int64     `json:"memory_bytes"`
	RootDisk int64     `json:"root_disk_bytes"`
	Disks    []Disk    `json:"disks,omitempty"`
	Forwards []Forward `json:"forwards,omitempty"`
}

type PrivateNetwork struct {
	CIDR        string `json:"cidr"`
	HostAddress string `json:"host_address"`
	DHCPEnd     string `json:"dhcp_end"`
}

type Resolved struct {
	Schema           int             `json:"schema"`
	Name             string          `json:"name"`
	Image            string          `json:"image"`
	Network          string          `json:"network"`
	Private          *PrivateNetwork `json:"private,omitempty"`
	SSHUser          string          `json:"ssh_user"`
	SSHWaitTimeoutNS int64           `json:"ssh_wait_timeout_ns,omitempty"`
	DataRoot         string          `json:"data_root,omitempty"`
	Nodes            []Node          `json:"nodes"`
}

const DefaultSSHWaitTimeout = 180 * time.Second

// SSHWaitTimeout accepts old resolved/state documents that predate the field
// by applying the original 180-second default. Negative values fail closed.
func (r Resolved) SSHWaitTimeout() (time.Duration, error) {
	if r.SSHWaitTimeoutNS == 0 {
		return DefaultSSHWaitTimeout, nil
	}
	if r.SSHWaitTimeoutNS < 0 {
		return 0, errors.New("resolved SSH wait timeout must be positive")
	}
	return time.Duration(r.SSHWaitTimeoutNS), nil
}

// Quick returns the terminal semantics from the active execution prompt. It
// intentionally differs from migrated Pigsty profile disk defaults.
func Quick(withDataDisk, withDefaultForwards bool) Resolved {
	node := Node{
		Name:     "meta",
		Control:  true,
		CPUs:     2,
		Memory:   4 * GiB,
		RootDisk: 64 * GiB,
	}
	if withDataDisk {
		node.Disks = []Disk{{Name: "data", Size: 64 * GiB, Mount: "/data"}}
	}
	if withDefaultForwards {
		node.Forwards = []Forward{
			{Bind: "127.0.0.1", Host: 15432, Guest: 5432, Protocol: "tcp"},
			{Bind: "127.0.0.1", Host: 13000, Guest: 3000, Protocol: "tcp"},
			{Bind: "127.0.0.1", Host: 18080, Guest: 80, Protocol: "tcp"},
			{Bind: "127.0.0.1", Host: 18443, Guest: 443, Protocol: "tcp"},
		}
	}
	return Resolved{Schema: 1, Name: "quick", Image: "u24", Network: "user", SSHUser: "dba", SSHWaitTimeoutNS: int64(DefaultSSHWaitTimeout), Nodes: []Node{node}}
}

// CanonicalJSON relies only on structs and slices; there are no unordered map
// keys in the M0 model.
func CanonicalJSON(value Resolved) ([]byte, error) { return json.Marshal(value) }

func Hash(value Resolved) (string, error) {
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
