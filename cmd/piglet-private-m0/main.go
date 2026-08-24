package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/pgsty/piglet/internal/m0"
)

func main() {
	flags := flag.NewFlagSet("piglet-private-m0", flag.ExitOnError)
	image := flags.String("image", "", "absolute read-only native qcow2")
	sha256 := flags.String("sha256", "", "required expected SHA-256")
	workDir := flags.String("work-dir", "", "new or empty absolute artifact directory")
	restartDaemon := flags.Bool("restart-daemon", true, "on Darwin, restart launchd daemon and verify stream reconnect")
	diagnosticShared := flags.Bool("diagnostic-shared", false, "record all Darwin shared-mode contract failures before returning")
	networkCIDR := flags.String("network-cidr", "10.10.10.0/24", "Darwin M0 diagnostic private /24")
	linuxHelper := flags.String("linux-bridge-helper", "", "optional exact supported qemu-bridge-helper path on Linux")
	readyTimeout := flags.Duration("ready-timeout", 180*time.Second, "per-node guest readiness timeout")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 || *image == "" || *sha256 == "" || *workDir == "" {
		fmt.Fprintln(os.Stderr, "usage: piglet-private-m0 --image <qcow2> --sha256 <digest> --work-dir <dir>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	evidence, err := m0.PrivateSmoke(ctx, m0.PrivateOptions{Image: *image, ExpectedSHA: *sha256, WorkDir: *workDir, ReadyTimeout: *readyTimeout, RestartDaemon: *restartDaemon, LinuxHelper: *linuxHelper, NetworkCIDR: *networkCIDR, DiagnosticShared: *diagnosticShared})
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(evidence)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
