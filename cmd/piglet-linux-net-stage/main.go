// piglet-linux-net-stage performs read-only host discovery and writes an
// unprivileged staging tree. It never installs files or changes host services.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/pgsty/piglet/internal/execx"
	linuxnet "github.com/pgsty/piglet/internal/network/linux"
)

func main() {
	flags := flag.NewFlagSet("piglet-linux-net-stage", flag.ExitOnError)
	staging := flags.String("staging", "", "new or empty absolute staging directory")
	cidr := flags.String("cidr", "10.10.10.0/24", "diagnostic host-global RFC1918 /24")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 || *staging == "" {
		fmt.Fprintln(os.Stderr, "usage: piglet-linux-net-stage --staging <absolute-empty-dir> [--cidr RFC1918/24]")
		os.Exit(2)
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "piglet-linux-net-stage requires native Linux")
		os.Exit(3)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner := execx.OSRunner{Timeout: 10 * time.Second, OutputLimit: 1 << 20}
	facts, err := linuxnet.DiscoverFacts(ctx, runner, runner)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	config, err := linuxnet.ConfigForCIDR(*cidr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	plan, err := linuxnet.NewInstallPlan(facts, config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	result, err := linuxnet.StageInstallPlan(plan, *staging)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
