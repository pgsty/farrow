package spec

// Quick remains test-only; keep Network "user" for legacy-state diagnostics.
func Quick(withDataDisk, withDefaultForwards bool) Resolved {
	node := Node{Name: "meta", Control: true, CPUs: 2, Memory: 4 * GiB, RootDisk: 64 * GiB}
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
