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
	flags := flag.NewFlagSet("piglet-m0", flag.ExitOnError)
	image := flags.String("image", "", "absolute path to a checksum-verified native qcow2 image")
	sha256 := flags.String("sha256", "", "required expected image SHA-256")
	workDir := flags.String("work-dir", "", "empty absolute evidence/artifact directory")
	boot := flags.String("boot", "auto", "auto, bios, or uefi")
	readyTimeout := flags.Duration("ready-timeout", 180*time.Second, "guest readiness timeout")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 || *image == "" || *sha256 == "" || *workDir == "" {
		fmt.Fprintln(os.Stderr, "usage: piglet-m0 --image <absolute-qcow2> --sha256 <digest> --work-dir <empty-absolute-dir>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *readyTimeout*2+3*time.Minute)
	defer cancel()
	evidence, err := m0.QuickSmoke(ctx, m0.QuickOptions{Image: *image, ExpectedSHA: *sha256, WorkDir: *workDir, Boot: *boot, ReadyTimeout: *readyTimeout})
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(evidence)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
