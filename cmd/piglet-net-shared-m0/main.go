// piglet-net-shared-m0 installs the pinned Darwin socket_vmnet shared-mode
// plan for native M0 diagnostics. It is intentionally not part of release
// packaging; the public Piglet CLI continues to reject shared mode.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/pgsty/piglet/internal/execx"
	darwinnet "github.com/pgsty/piglet/internal/network/darwin"
)

type sudoRunner struct{ base execx.Runner }

func (r sudoRunner) Run(ctx context.Context, binary string, args ...string) (execx.Result, error) {
	if r.base == nil {
		return execx.Result{}, errors.New("sudo runner has no base runner")
	}
	sudoArgs := append([]string{"-n", "--", binary}, args...)
	return r.base.Run(ctx, "/usr/bin/sudo", sudoArgs...)
}

func main() {
	flags := flag.NewFlagSet("piglet-net-shared-m0", flag.ExitOnError)
	archive := flags.String("archive", "", "absolute pinned socket_vmnet archive")
	interfaceID := flags.String("interface-id", "", "persistent vmnet interface UUID")
	apply := flags.Bool("yes", false, "apply the displayed privileged plan")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 || *archive == "" || *interfaceID == "" {
		fmt.Fprintln(os.Stderr, "usage: piglet-net-shared-m0 --archive <absolute-tar.gz> --interface-id <uuid> [--yes] [--json]")
		os.Exit(2)
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "piglet-net-shared-m0 requires native Darwin")
		os.Exit(3)
	}
	base := execx.OSRunner{Timeout: 2 * time.Minute, OutputLimit: 1 << 20}
	executor := darwinnet.Executor{User: base, Root: sudoRunner{base: base}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, err := executor.InstallMode(ctx, *archive, *interfaceID, runtime.GOARCH, "shared", *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(7)
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("action: %s\napplied: %t\n", report.Action, report.Applied)
}
