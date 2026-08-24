package diagnostics

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/doctor"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/lease"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/state"
)

const maxCollectedFileBytes = 4 << 20

type BundleFile struct {
	Name string `json:"name"`
	Size int    `json:"size"`
	Data []byte `json:"-"`
}

type BundlePlan struct {
	Schema        int          `json:"schema"`
	ProjectID     string       `json:"project_id"`
	GeneratedAt   time.Time    `json:"generated_at"`
	SuggestedName string       `json:"suggested_name"`
	Files         []BundleFile `json:"files"`
	Skipped       []string     `json:"skipped,omitempty"`
}

type BundleResult struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	FileCount int    `json:"file_count"`
}

type Builder struct {
	CWD     string
	Version string
	Runner  execx.Runner
	Now     func() time.Time
	Doctor  func(context.Context) doctor.Report
	Host    func(context.Context) ([]BundleFile, []string)
}

func (b Builder) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

func (b Builder) runner() execx.Runner {
	if b.Runner != nil {
		return b.Runner
	}
	return execx.OSRunner{Timeout: 10 * time.Second, OutputLimit: 1 << 20}
}

func (b Builder) doctor(ctx context.Context) doctor.Report {
	if b.Doctor != nil {
		return b.Doctor(ctx)
	}
	return (doctor.Probe{Runner: b.runner()}).Run(ctx)
}

func safeBundleName(name string) bool {
	clean := path.Clean(name)
	return name != "" && clean == name && clean != "." && !path.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, "../")
}

func addFile(files *[]BundleFile, name string, data []byte) error {
	if !safeBundleName(name) {
		return fmt.Errorf("unsafe diagnostic bundle entry %q", name)
	}
	clone := append([]byte(nil), data...)
	*files = append(*files, BundleFile{Name: name, Size: len(clone), Data: clone})
	return nil
}

func marshalRedacted(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return RedactJSON(append(data, '\n')), nil
}

func readDiagnostic(pathname string, tail bool) ([]byte, error) {
	info, err := os.Lstat(pathname)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("not a regular non-symlink file")
	}
	handle, err := os.Open(pathname)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	prefix := []byte(nil)
	if info.Size() > maxCollectedFileBytes {
		if !tail {
			return nil, fmt.Errorf("file exceeds %d-byte diagnostic limit", maxCollectedFileBytes)
		}
		if _, err := handle.Seek(-maxCollectedFileBytes, io.SeekEnd); err != nil {
			return nil, err
		}
		prefix = []byte(fmt.Sprintf("[piglet: log truncated to final %d bytes]\n", maxCollectedFileBytes))
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxCollectedFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCollectedFileBytes {
		return nil, fmt.Errorf("file exceeds %d-byte diagnostic limit", maxCollectedFileBytes)
	}
	return append(prefix, data...), nil
}

func collectPath(files *[]BundleFile, skipped *[]string, entryName, pathname string, jsonFile, tail bool) {
	data, err := readDiagnostic(pathname, tail)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		*skipped = append(*skipped, fmt.Sprintf("%s: %v", entryName, err))
		return
	}
	if jsonFile {
		data = RedactJSON(data)
	} else {
		data = RedactText(data)
	}
	if err := addFile(files, entryName, data); err != nil {
		*skipped = append(*skipped, fmt.Sprintf("%s: %v", entryName, err))
	}
}

func (b Builder) defaultHost(ctx context.Context) ([]BundleFile, []string) {
	commands := []struct {
		name   string
		binary string
		args   []string
	}{
		{"host/uname.txt", "uname", []string{"-a"}},
	}
	if runtime.GOOS == "darwin" {
		commands = append(commands,
			struct {
				name   string
				binary string
				args   []string
			}{"host/sw-vers.txt", "sw_vers", nil},
			struct {
				name   string
				binary string
				args   []string
			}{"host/routes.txt", "netstat", []string{"-rn", "-f", "inet"}},
			struct {
				name   string
				binary string
				args   []string
			}{"host/interfaces.txt", "ifconfig", nil},
		)
	} else if runtime.GOOS == "linux" {
		commands = append(commands,
			struct {
				name   string
				binary string
				args   []string
			}{"host/routes.txt", "ip", []string{"route", "show"}},
			struct {
				name   string
				binary string
				args   []string
			}{"host/interfaces.txt", "ip", []string{"address", "show"}},
		)
	}
	files := make([]BundleFile, 0, len(commands))
	skipped := make([]string, 0)
	for _, command := range commands {
		binary, err := exec.LookPath(command.binary)
		if err != nil {
			skipped = append(skipped, command.name+": binary not found")
			continue
		}
		result, runErr := b.runner().Run(ctx, binary, command.args...)
		data := append(append([]byte(nil), result.Stdout...), result.Stderr...)
		if runErr != nil {
			data = append(data, []byte("\n[piglet: "+runErr.Error()+"]\n")...)
		}
		_ = addFile(&files, command.name, RedactText(data))
	}
	return files, skipped
}

func (b Builder) Build(ctx context.Context) (BundlePlan, error) {
	workDir := b.CWD
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return BundlePlan{}, err
		}
	}
	projectValue, err := project.Open(workDir)
	if err != nil {
		return BundlePlan{}, err
	}
	generatedAt := b.now()
	plan := BundlePlan{
		Schema: 1, ProjectID: projectValue.Marker.ProjectID, GeneratedAt: generatedAt,
		SuggestedName: fmt.Sprintf("piglet-debug-%s-%s.tar.gz", projectValue.Marker.ProjectID[:8], generatedAt.Format("20060102T150405Z")),
	}
	versionData, err := marshalRedacted(map[string]any{
		"piglet": b.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
	})
	if err != nil {
		return BundlePlan{}, err
	}
	if err := addFile(&plan.Files, "version.json", versionData); err != nil {
		return BundlePlan{}, err
	}
	doctorData, err := marshalRedacted(b.doctor(ctx))
	if err != nil {
		return BundlePlan{}, err
	}
	if err := addFile(&plan.Files, "doctor.json", doctorData); err != nil {
		return BundlePlan{}, err
	}

	collectPath(&plan.Files, &plan.Skipped, "source/piglet.yaml", filepath.Join(projectValue.WorkDir, "piglet.yaml"), false, false)
	collectPath(&plan.Files, &plan.Skipped, "state/project.json", filepath.Join(projectValue.Root, "project.json"), true, false)
	collectPath(&plan.Files, &plan.Skipped, "state/resolved.json", filepath.Join(projectValue.Root, "resolved.json"), true, false)
	collectPath(&plan.Files, &plan.Skipped, "logs/project/events.jsonl", filepath.Join(projectValue.Root, "events.jsonl"), false, true)
	nodeNames := []string{"meta"}
	stateStore := state.Store{Project: projectValue}
	if projectState, readErr := stateStore.ReadProject(); readErr == nil && len(projectState.Resolved.Nodes) > 0 {
		nodeNames = make([]string, 0, len(projectState.Resolved.Nodes))
		for _, node := range projectState.Resolved.Nodes {
			nodeNames = append(nodeNames, node.Name)
		}
	}
	for _, nodeName := range nodeNames {
		nodeDir, nodeDirErr := projectValue.NodeDir(nodeName)
		if nodeDirErr != nil {
			plan.Skipped = append(plan.Skipped, "state/nodes/"+nodeName+": "+nodeDirErr.Error())
			continue
		}
		collectPath(&plan.Files, &plan.Skipped, "state/nodes/"+nodeName+"/state.json", filepath.Join(nodeDir, "state.json"), true, false)
		collectPath(&plan.Files, &plan.Skipped, "state/nodes/"+nodeName+"/transaction.json", filepath.Join(nodeDir, "transaction.json"), true, false)
		collectPath(&plan.Files, &plan.Skipped, "state/nodes/"+nodeName+"/private-prepare.json", filepath.Join(nodeDir, "private-prepare.json"), true, false)
		for _, logName := range []string{"qemu.log", "serial.log", "events.jsonl"} {
			collectPath(&plan.Files, &plan.Skipped, "logs/"+nodeName+"/"+logName, filepath.Join(nodeDir, logName), false, true)
		}
		if node, readErr := stateStore.ReadNode(nodeName); readErr == nil {
			display := execx.Display(node.Invocation.Binary, node.Invocation.Args...) + "\n"
			if err := addFile(&plan.Files, "state/nodes/"+nodeName+"/qemu-display.txt", RedactText([]byte(display))); err != nil {
				return BundlePlan{}, err
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			plan.Skipped = append(plan.Skipped, "state/nodes/"+nodeName+"/qemu-display.txt: "+readErr.Error())
		}
	}

	networkCandidates := []struct {
		name string
		path string
	}{
		{"network/global.json", "/private/var/db/piglet/network.json"},
	}
	if leaseRoot, leaseErr := lease.DefaultRoot(); leaseErr == nil {
		networkCandidates = append(networkCandidates, struct {
			name string
			path string
		}{"network/private-lease.json", filepath.Join(leaseRoot, "private-lease.json")})
	}
	for _, candidate := range networkCandidates {
		collectPath(&plan.Files, &plan.Skipped, candidate.name, candidate.path, true, false)
	}
	hostFiles, hostSkipped := b.defaultHost(ctx)
	if b.Host != nil {
		hostFiles, hostSkipped = b.Host(ctx)
	}
	for _, file := range hostFiles {
		if err := addFile(&plan.Files, file.Name, RedactText(file.Data)); err != nil {
			return BundlePlan{}, err
		}
	}
	plan.Skipped = append(plan.Skipped, hostSkipped...)

	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Name < plan.Files[j].Name })
	sort.Strings(plan.Skipped)
	manifestData, err := marshalRedacted(map[string]any{
		"schema": 1, "project_id": plan.ProjectID, "generated_at": plan.GeneratedAt,
		"included": plan.Files,
		"always_excluded": []string{
			"project private/public keys and known_hosts", "seed.iso and all cloud-init contents",
			"root/data qcow2 disks", "process environment and arbitrary user files",
		},
		"skipped":   plan.Skipped,
		"redaction": "best-effort content redaction follows a strict collection allowlist; review before sharing",
	})
	if err != nil {
		return BundlePlan{}, err
	}
	if err := addFile(&plan.Files, "manifest.json", manifestData); err != nil {
		return BundlePlan{}, err
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Name < plan.Files[j].Name })
	return plan, nil
}

func closeWriters(tarWriter *tar.Writer, gzipWriter *gzip.Writer) error {
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		return err
	}
	return gzipWriter.Close()
}

func writeArchive(output io.Writer, digest hash.Hash, plan BundlePlan) error {
	gzipWriter, err := gzip.NewWriterLevel(io.MultiWriter(output, digest), gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	tarWriter := tar.NewWriter(gzipWriter)
	prefix := "piglet-debug-" + plan.ProjectID[:8]
	for _, file := range plan.Files {
		if !safeBundleName(file.Name) || len(file.Data) != file.Size {
			_ = closeWriters(tarWriter, gzipWriter)
			return fmt.Errorf("invalid planned bundle entry %q", file.Name)
		}
		header := &tar.Header{
			Name: path.Join(prefix, file.Name), Mode: 0o600, Size: int64(len(file.Data)),
			ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			_ = closeWriters(tarWriter, gzipWriter)
			return err
		}
		if _, err := tarWriter.Write(file.Data); err != nil {
			_ = closeWriters(tarWriter, gzipWriter)
			return err
		}
	}
	return closeWriters(tarWriter, gzipWriter)
}

func WriteBundle(outputPath string, plan BundlePlan) (BundleResult, error) {
	if outputPath == "" || !filepath.IsAbs(outputPath) || len(plan.ProjectID) < 8 || len(plan.Files) == 0 {
		return BundleResult{}, errors.New("absolute output path and non-empty valid bundle plan are required")
	}
	parent := filepath.Dir(outputPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return BundleResult{}, errors.New("bundle output parent must be a real directory")
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return BundleResult{}, errors.New("refuse to overwrite existing debug bundle")
	} else if !errors.Is(err, os.ErrNotExist) {
		return BundleResult{}, err
	}
	temporary, err := os.CreateTemp(parent, ".piglet-debug-*.partial")
	if err != nil {
		return BundleResult{}, err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return BundleResult{}, err
	}
	digest := sha256.New()
	if err := writeArchive(temporary, digest, plan); err != nil {
		return BundleResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return BundleResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return BundleResult{}, err
	}
	if err := os.Link(temporaryPath, outputPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return BundleResult{}, errors.New("refuse to overwrite existing debug bundle")
		}
		return BundleResult{}, err
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(outputPath)
		return BundleResult{}, err
	}
	published = true
	if err := fsutil.SyncDir(parent); err != nil {
		return BundleResult{}, err
	}
	info, err := os.Lstat(outputPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return BundleResult{}, errors.New("published debug bundle has unexpected type or mode")
	}
	return BundleResult{Path: outputPath, SHA256: hex.EncodeToString(digest.Sum(nil)), Size: info.Size(), FileCount: len(plan.Files)}, nil
}
