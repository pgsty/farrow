package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	darwinnet "github.com/pgsty/farrow/internal/network/darwin"
)

type output struct {
	StagingDir  string                       `json:"staging_dir"`
	Release     darwinnet.Release            `json:"release"`
	Archive     darwinnet.ArchiveInfo        `json:"archive"`
	State       darwinnet.NetworkState       `json:"state"`
	Directories map[string]directoryMetadata `json:"directories"`
	Targets     map[string]targetMetadata    `json:"targets"`
	Args        []string                     `json:"daemon_args"`
}

type targetMetadata struct {
	Source string `json:"source"`
	Owner  string `json:"owner"`
	Mode   string `json:"mode"`
}

type directoryMetadata struct {
	Owner string `json:"owner"`
	Mode  string `json:"mode"`
}

func main() {
	flags := flag.NewFlagSet("farrow-net-stage", flag.ExitOnError)
	archive := flags.String("archive", "", "absolute pinned socket_vmnet archive path")
	staging := flags.String("staging", "", "new or empty absolute staging directory")
	interfaceID := flags.String("interface-id", "", "persistent vmnet interface UUID")
	mode := flags.String("mode", "host", "diagnostic vmnet mode: host or shared")
	cidr := flags.String("cidr", "10.10.10.0/24", "diagnostic host-global RFC1918 /24")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 || *archive == "" || *staging == "" || *interfaceID == "" {
		fmt.Fprintln(os.Stderr, "usage: farrow-net-stage --archive <absolute-tar.gz> --staging <absolute-empty-dir> --interface-id <uuid> [--mode host|shared] [--cidr RFC1918/24]")
		os.Exit(2)
	}
	archiveInfo, err := darwinnet.VerifyArchive(*archive, runtime.GOARCH)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(7)
	}
	if err := darwinnet.ExtractVerifiedBinaries(*archive, runtime.GOARCH, *staging); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(7)
	}
	plan, err := darwinnet.NewInstallPlanModeNetwork(runtime.GOARCH, *interfaceID, *mode, *cidr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	state, err := plan.StateJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	plist, err := plan.Plist()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stateSource := filepath.Join(*staging, "network.json")
	plistSource := filepath.Join(*staging, "io.pgsty.farrow.vmnet.plist")
	if err := os.WriteFile(stateSource, state, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(plistSource, plist, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result := output{
		StagingDir: *staging, Release: plan.Release, Archive: archiveInfo, State: plan.State,
		Directories: map[string]directoryMetadata{
			darwinnet.InstallRoot:                           {Owner: "root:wheel", Mode: "0755"},
			filepath.Join(darwinnet.InstallRoot, "libexec"): {Owner: "root:wheel", Mode: "0755"},
			darwinnet.StateDir:                              {Owner: "root:wheel", Mode: "0700"},
			darwinnet.LogDir:                                {Owner: "root:wheel", Mode: "0755"},
			darwinnet.LeaseRoot:                             {Owner: "root:wheel", Mode: "1777"},
		},
		Targets: map[string]targetMetadata{
			darwinnet.DaemonPath: {Source: filepath.Join(*staging, "socket_vmnet"), Owner: "root:wheel", Mode: "0755"},
			darwinnet.ClientPath: {Source: filepath.Join(*staging, "socket_vmnet_client"), Owner: "root:wheel", Mode: "0755"},
			darwinnet.PlistPath:  {Source: plistSource, Owner: "root:wheel", Mode: "0644"},
			darwinnet.StatePath:  {Source: stateSource, Owner: "root:wheel", Mode: "0600"},
		},
		Args: plan.Args,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
