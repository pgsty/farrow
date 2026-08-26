package quick

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func TestListDiscoversCurrentAndUninitializedProjectsWithoutMutation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	workA := filepath.Join(root, "work-a")
	workB := filepath.Join(root, "work-b")
	for _, work := range []string{workA, workB} {
		if err := os.Mkdir(work, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	projectA, err := project.Create(workA, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	projectB, err := project.Create(workB, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolved := spec.Quick(true, true)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := (state.Store{Project: projectA}).WriteProject(state.ProjectState{
		Schema: state.ProjectSchema, FarrowVersion: "test", ProjectID: projectA.Marker.ProjectID,
		SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	beforeB, err := os.ReadDir(projectB.Root)
	if err != nil {
		t.Fatal(err)
	}

	report, err := (Manager{CWD: workA}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != 1 || report.DataRoot != dataRoot || report.CurrentID != projectA.Marker.ProjectID || len(report.Projects) != 2 {
		t.Fatalf("list report = %#v", report)
	}
	currentCount := 0
	for _, listed := range report.Projects {
		if listed.Current {
			currentCount++
			if listed.Name != "quick" || listed.Network != "user" || listed.SpecHash != hash {
				t.Fatalf("current project summary = %#v", listed)
			}
		}
	}
	if currentCount != 1 || report.CurrentRoot() != projectA.Root {
		t.Fatalf("current project selection = %#v", report)
	}
	afterB, err := os.ReadDir(projectB.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeB) != len(afterB) {
		t.Fatalf("read-only list mutated uninitialized project: before=%v after=%v", beforeB, afterB)
	}
}

func TestListReportsEveryPrivateNode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	resolved := spec.Resolved{
		Schema: 1, Name: "private", Image: "u24", Network: "private", SSHUser: "dba",
		Private: &spec.PrivateNetwork{CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8"},
		Nodes: []spec.Node{
			{Name: "meta", Control: true, Address: "10.10.10.10", CPUs: 1, Memory: spec.GiB, RootDisk: 8 * spec.GiB},
			{Name: "node-1", Address: "10.10.10.11", CPUs: 1, Memory: spec.GiB, RootDisk: 8 * spec.GiB},
		},
	}
	hash, _ := spec.Hash(resolved)
	store := state.Store{Project: projectValue}
	now := time.Now().UTC()
	if err := store.WriteProject(state.ProjectState{Schema: state.ProjectSchema, FarrowVersion: "test", ProjectID: projectValue.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for index, definition := range resolved.Nodes {
		nodeDir, _ := projectValue.NodeDir(definition.Name)
		node := state.NodeState{
			Schema: state.NodeSchema, FarrowVersion: "test", ProjectID: projectValue.Marker.ProjectID,
			Node: definition.Name, VMUUID: projectValue.Marker.ProjectID, Phase: state.Stopped, Generation: 1, SpecHash: hash,
			Image:    state.Image{Alias: "u24", Release: "test", Digest: "digest", VirtualSize: spec.GiB},
			RootDisk: filepath.Join(nodeDir, "root.qcow2"), Seed: filepath.Join(nodeDir, "seed.iso"), SSHPort: uint16(2222 + index),
			Runtime:    state.RuntimePaths{Directory: filepath.Join(root, "runtime", definition.Name), QMP: filepath.Join(root, "runtime", definition.Name, "qmp.sock"), PIDFile: filepath.Join(root, "runtime", definition.Name, "qemu.pid")},
			Invocation: qemu.Invocation{Binary: "/usr/bin/qemu-system", Args: []string{"-name", definition.Name}}, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.WriteNode(node); err != nil {
			t.Fatal(err)
		}
	}
	report, err := (Manager{CWD: work}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Projects) != 1 || len(report.Projects[0].Nodes) != 2 || report.Projects[0].Nodes[0].Name != "meta" || report.Projects[0].Nodes[1].Name != "node-1" {
		t.Fatalf("private list report = %#v", report)
	}
}
