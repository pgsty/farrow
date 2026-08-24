package diagnostics

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/doctor"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/qemu"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

func readBundle(t *testing.T, pathname string) (map[string][]byte, map[string]int64) {
	t.Helper()
	handle, err := os.Open(pathname)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	gzipReader, err := gzip.NewReader(handle)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	modes := make(map[string]int64)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = data
		modes[header.Name] = header.Mode
	}
	return files, modes
}

func TestBundleCollectsAllPrivateNodesWithoutSeedOrKeys(t *testing.T) {
	t.Parallel()
	canary := "PIGLET_PRIVATE_BUNDLE_CANARY_91ac"
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
	now := time.Now().UTC()
	store := state.Store{Project: projectValue}
	if err := store.WriteProject(state.ProjectState{Schema: state.ProjectSchema, PigletVersion: "test", ProjectID: projectValue.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for index, definition := range resolved.Nodes {
		nodeDir, err := projectValue.EnsureNodeDir(definition.Name)
		if err != nil {
			t.Fatal(err)
		}
		node := state.NodeState{
			Schema: state.NodeSchema, PigletVersion: "test", ProjectID: projectValue.Marker.ProjectID, Node: definition.Name,
			VMUUID: projectValue.Marker.ProjectID, Phase: state.Stopped, Generation: 1, SpecHash: hash,
			Image:    state.Image{Alias: "u24", Release: "test", Digest: "digest", VirtualSize: spec.GiB},
			RootDisk: filepath.Join(nodeDir, "root.qcow2"), Seed: filepath.Join(nodeDir, "seed.iso"), SSHPort: uint16(2222 + index),
			Runtime:    state.RuntimePaths{Directory: filepath.Join(root, "runtime", definition.Name), QMP: filepath.Join(root, "runtime", definition.Name, "qmp.sock"), PIDFile: filepath.Join(root, "runtime", definition.Name, "qemu.pid")},
			Invocation: qemu.Invocation{Binary: "/usr/bin/qemu", Args: []string{"-name", definition.Name}}, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.WriteNode(node); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nodeDir, "serial.log"), []byte("password="+canary+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, excluded := range []string{"seed.iso", "root.qcow2"} {
			if err := os.WriteFile(filepath.Join(nodeDir, excluded), []byte(canary), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519"), []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := (Builder{CWD: work, Version: "test", Doctor: func(context.Context) doctor.Report { return doctor.Report{} }, Host: func(context.Context) ([]BundleFile, []string) { return nil, nil }}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, file := range plan.Files {
		names[file.Name] = true
		if bytes.Contains(file.Data, []byte(canary)) {
			t.Fatalf("private bundle leaked canary in %s", file.Name)
		}
		if strings.Contains(file.Name, "seed.iso") || strings.Contains(file.Name, "root.qcow2") || strings.Contains(file.Name, "id_ed25519") {
			t.Fatalf("private bundle included forbidden artifact %s", file.Name)
		}
	}
	for _, wanted := range []string{"state/nodes/meta/state.json", "state/nodes/node-1/state.json", "logs/meta/serial.log", "logs/node-1/serial.log"} {
		if !names[wanted] {
			t.Errorf("private bundle missing %s", wanted)
		}
	}
}

func TestBundleStrictAllowlistAndSecretCanary(t *testing.T) {
	t.Parallel()
	canary := "PIGLET_SECRET_CANARY_26bc3b17"
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(workDir, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "piglet.yaml"), []byte("version: 1\n# token: "+canary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectValue.Root, "resolved.json"), []byte(`{"name":"test","client_secret":"`+canary+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeDir, err := projectValue.EnsureNodeDir("meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "serial.log"), []byte("Authorization: Bearer "+canary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"seed.iso", "root.qcow2", "data.qcow2"} {
		if err := os.WriteFile(filepath.Join(nodeDir, excluded), []byte(canary), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keyDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "id_ed25519"), []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}

	fixedTime := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	builder := Builder{
		CWD: workDir, Version: "test", Now: func() time.Time { return fixedTime },
		Doctor: func(context.Context) doctor.Report {
			return doctor.Report{OS: "test", Arch: "test", Checks: []doctor.Check{{Name: "canary", Status: doctor.Warn, Evidence: "password=" + canary}}}
		},
		Host: func(context.Context) ([]BundleFile, []string) {
			return []BundleFile{{Name: "host/test.txt", Data: []byte("--token=" + canary)}}, nil
		},
	}
	plan, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) < 6 || plan.SuggestedName != "piglet-debug-"+projectValue.Marker.ProjectID[:8]+"-20260823T120000Z.tar.gz" {
		t.Fatalf("unexpected bundle plan: %#v", plan)
	}
	for _, file := range plan.Files {
		if bytes.Contains(file.Data, []byte(canary)) {
			t.Fatalf("planned entry %s leaked canary", file.Name)
		}
		for _, forbidden := range []string{"seed.iso", "root.qcow2", "data.qcow2", "id_ed25519", "known_hosts"} {
			if file.Name == forbidden || strings.HasSuffix(file.Name, "/"+forbidden) {
				t.Fatalf("excluded artifact entered plan: %s", file.Name)
			}
		}
	}

	firstPath := filepath.Join(root, "first.tar.gz")
	first, err := WriteBundle(firstPath, plan)
	if err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(root, "second.tar.gz")
	second, err := WriteBundle(secondPath, plan)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || first.Size != second.Size || first.FileCount != len(plan.Files) {
		t.Fatalf("bundle output is not deterministic: %#v %#v", first, second)
	}
	if _, err := WriteBundle(firstPath, plan); err == nil {
		t.Fatal("existing bundle was overwritten")
	}
	info, err := os.Lstat(firstPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode = %v, %v", info, err)
	}
	archiveFiles, modes := readBundle(t, firstPath)
	if len(archiveFiles) != len(plan.Files) {
		t.Fatalf("archive entries = %d, plan = %d", len(archiveFiles), len(plan.Files))
	}
	for name, data := range archiveFiles {
		if bytes.Contains(data, []byte(canary)) {
			t.Fatalf("archive entry %s leaked canary", name)
		}
		if modes[name] != 0o600 {
			t.Fatalf("archive entry %s mode = %o", name, modes[name])
		}
	}
}
