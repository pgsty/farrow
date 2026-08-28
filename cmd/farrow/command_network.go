package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/spf13/cobra"
)

func newNetworkCommand(stdout, stderr io.Writer) *cobra.Command {
	parent := subcommandGroup(
		"network",
		"Inspect and manage the host-global fixed-IP network",
		`Inspect, install, or remove the one host-global fixed-IP network used by the
Farrow deployment. Mutating commands print their privileged plan and require
--yes before applying it.`,
		`  farrow network status
  farrow network install
  farrow network install --yes
  farrow network uninstall --yes`,
		stdout, stderr,
	)

	statusOptions := networkOptions{Action: "status"}
	status := &cobra.Command{
		Use:   "status",
		Short: "Inspect installed network state and readiness",
		Long: `Inspect the platform backend, installed CIDR, ownership, health, and the
read-only preflight findings used before lifecycle mutation.`,
		Example: `  farrow network status
  farrow network status --cidr 10.10.10.0/24
  farrow --json network status`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return commandError(runNetwork(statusOptions, stdout, stderr))
		},
	}
	status.Flags().StringVarP(&statusOptions.CIDR, "cidr", "c", "", "expected host-global RFC1918 IPv4 /24")
	parent.AddCommand(status)

	installOptions := networkOptions{Action: "install", CIDR: subnet.DefaultCIDR, Mode: "host"}
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the host-global fixed-IP network",
		Long: `Plan or install the platform-native fixed-IP backend. On macOS this is
socket_vmnet in host or shared mode; on Linux it is the selected bridge/network
manager backend. Without --yes the command is a read-only plan.`,
		Example: `  farrow network install                    # print the privileged plan
  farrow network install --yes              # apply the default 10.10.10.0/24 plan
  farrow network install --mode shared --yes # macOS shared vmnet mode`,
		Args: cobra.NoArgs,
		PreRunE: func(command *cobra.Command, _ []string) error {
			if err := validateChoice("--mode", installOptions.Mode, "host", "shared"); err != nil {
				return err
			}
			if runtime.GOOS == "linux" && installOptions.Mode != "host" {
				return fmt.Errorf("--mode is a macOS socket_vmnet option; Linux uses farrow0")
			}
			if runtime.GOOS != "darwin" && (command.Flags().Changed("archive") || command.Flags().Changed("interface-id")) {
				return fmt.Errorf("--archive and --interface-id are macOS-only")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return commandError(runNetwork(installOptions, stdout, stderr))
		},
	}
	install.Flags().StringVarP(&installOptions.CIDR, "cidr", "c", installOptions.CIDR, "host-global RFC1918 IPv4 /24")
	install.Flags().StringVar(&installOptions.Mode, "mode", installOptions.Mode, "macOS vmnet mode: host or shared")
	install.Flags().StringVar(&installOptions.Archive, "archive", "", "macOS: pinned socket_vmnet archive")
	install.Flags().StringVar(&installOptions.InterfaceID, "interface-id", "", "macOS: persistent vmnet UUID")
	install.Flags().BoolVar(&installOptions.Apply, "yes", false, "apply the displayed privileged plan without prompting")
	_ = install.RegisterFlagCompletionFunc("mode", enumFlagCompletion("host", "shared"))
	parent.AddCommand(install)

	uninstallOptions := networkOptions{Action: "uninstall"}
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Farrow-owned host networking",
		Long: `Plan or remove only Farrow-owned network state and restore recorded host
settings. The operation refuses while any deployment node is live and changes
nothing without --yes.`,
		Example: `  farrow network uninstall       # inspect the removal plan
  farrow network uninstall --yes # apply after the deployment is stopped`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return commandError(runNetwork(uninstallOptions, stdout, stderr))
		},
	}
	uninstall.Flags().BoolVar(&uninstallOptions.Apply, "yes", false, "apply the displayed privileged plan without prompting")
	parent.AddCommand(uninstall)
	return parent
}
