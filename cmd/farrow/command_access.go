package main

import (
	"fmt"
	"io"
	"time"

	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/spf13/cobra"
)

func newProvisionCommand(stdout, stderr io.Writer) *cobra.Command {
	options := provisionOptions{Parallelism: 1, Timeout: time.Hour}
	command := &cobra.Command{
		Use:   "provision [node...]",
		Short: "Run a local Bash script in selected guests",
		Long: `Stream one bounded local Bash script to selected running guests over the
verified deployment SSH connection. Execution is serial by default, audited by
script digest, and never uploads the script as a persistent guest file.`,
		Example: `  farrow provision --script ./bootstrap.sh
  farrow provision --script ./check.sh meta node-1
  farrow provision --script ./admin.sh --sudo --parallel 2 --timeout 30m`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: nodeCompletion(false, false),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.Parallelism < 1 || options.Parallelism > 4 {
				return fmt.Errorf("--parallel must be between 1 and 4, got %d", options.Parallelism)
			}
			if options.Timeout <= 0 || options.Timeout > 24*time.Hour {
				return fmt.Errorf("--timeout must be greater than zero and no more than 24h, got %s", options.Timeout)
			}
			return nil
		},
		RunE: func(_ *cobra.Command, nodes []string) error {
			return commandError(runProvision(options, nodes, stdout, stderr))
		},
	}
	command.Flags().StringVar(&options.ScriptPath, "script", "", "local Bash script to stream to each selected guest")
	command.Flags().BoolVar(&options.Sudo, "sudo", false, "run the guest script through sudo -n")
	command.Flags().IntVar(&options.Parallelism, "parallel", options.Parallelism, "bounded node concurrency, 1..4")
	command.Flags().DurationVar(&options.Timeout, "timeout", options.Timeout, "hard deadline for the operation, maximum 24h")
	_ = command.MarkFlagRequired("script")
	return command
}

func newSSHConfigCommand(stdout, stderr io.Writer) *cobra.Command {
	options := sshConfigOptions{Name: "farrow"}
	command := &cobra.Command{
		Use:   "ssh-config [node...]",
		Short: "Print, install, or remove OpenSSH configuration",
		Long: `Print the deployment OpenSSH fragment, install it through one marker-owned
Include, or remove only that owned fragment. Printing and installation can be
limited to selected nodes; removal is state-independent and accepts no nodes.`,
		Example: `  farrow ssh-config                       # print the fragment
  farrow ssh-config --install meta        # install aliases for one node
  farrow ssh-config --install --name lab  # use lab-* as the prefixed aliases
  farrow ssh-config --remove --name lab   # remove only the owned lab fragment`,
		Args: func(_ *cobra.Command, arguments []string) error {
			if options.Remove && len(arguments) != 0 {
				return fmt.Errorf("--remove does not accept node selectors")
			}
			return nil
		},
		ValidArgsFunction: nodeCompletion(false, false),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if !sshconfig.ValidName(options.Name) {
				return fmt.Errorf("invalid SSH config name %q", options.Name)
			}
			return nil
		},
		RunE: func(_ *cobra.Command, nodes []string) error {
			return commandError(runSSHConfig(options, nodes, stdout, stderr))
		},
	}
	command.Flags().BoolVar(&options.Install, "install", false, "install a marker-owned Include and deployment fragment")
	command.Flags().BoolVar(&options.Remove, "remove", false, "remove the marker-owned Include and fragment")
	command.Flags().StringVar(&options.Name, "name", options.Name, "SSH Host and fragment prefix")
	command.MarkFlagsMutuallyExclusive("install", "remove")
	return command
}

func sshShortcutOptions(name string) (sshConfigOptions, error) {
	if name == "" {
		name = "farrow"
	}
	if !sshconfig.ValidName(name) {
		return sshConfigOptions{}, fmt.Errorf("invalid SSH config name %q", name)
	}
	return sshConfigOptions{Install: true, Name: name}, nil
}

func newSSHShortcutCommand(stdout, stderr io.Writer) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "ss [node...]",
		Short: "Install the deployment SSH config",
		Long: `Shortcut for 'farrow ssh-config --install'. Aliases answer bare node
names, and the installed fragment prefix defaults to 'farrow'.`,
		Example: `  farrow ss              # make every node available to plain ssh
  farrow ss meta         # install aliases for one node
  farrow ss --name lab   # use lab-* as the prefixed aliases`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: nodeCompletion(false, false),
		RunE: func(_ *cobra.Command, nodes []string) error {
			options, err := sshShortcutOptions(name)
			if err != nil {
				return err
			}
			return commandError(runSSHConfig(options, nodes, stdout, stderr))
		},
	}
	command.Flags().StringVar(&name, "name", "", "SSH fragment prefix (default: farrow)")
	return command
}

func newLogsCommand(stdout, stderr io.Writer) *cobra.Command {
	options := logOptions{Source: "serial"}
	command := &cobra.Command{
		Use:   "logs [node]",
		Short: "Read or follow deployment logs",
		Long: `Read the selected node's serial, QEMU, or Farrow event log. With --follow,
text mode streams bytes and structured modes emit a record stream (NDJSON for
JSON) so stdout remains machine-readable.`,
		Example: `  farrow logs meta                   # read the serial log
  farrow logs meta --source qemu     # read QEMU diagnostics
  farrow logs meta --source events -f
  farrow --json logs meta --follow   # stream structured records`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: nodeCompletion(false, true),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateChoice("--source", options.Source, "serial", "qemu", "events")
		},
		RunE: func(_ *cobra.Command, arguments []string) error {
			node := ""
			if len(arguments) == 1 {
				node = arguments[0]
			}
			return commandError(runLogs(options, node, stdout, stderr))
		},
	}
	command.Flags().StringVar(&options.Source, "source", options.Source, "log source: serial, qemu, or events")
	command.Flags().BoolVarP(&options.Follow, "follow", "f", false, "continue streaming appended log data")
	_ = command.RegisterFlagCompletionFunc("source", enumFlagCompletion("serial", "qemu", "events"))
	return command
}

func newHostsCommand(stdout, stderr io.Writer) *cobra.Command {
	parent := subcommandGroup(
		"hosts",
		"Manage deployment entries in the host hosts file",
		`Publish or remove the deployment's marker-owned /etc/hosts block through the
digest-matched Farrow helper. Both operations show their exact privileged plan
before applying it.`,
		`  farrow hosts install         # show the proposed /etc/hosts block
  farrow hosts install --yes   # apply it
  farrow hosts uninstall --yes # remove only the Farrow-owned block`,
		stdout, stderr,
	)
	for _, action := range []string{"install", "uninstall"} {
		action := action
		apply := false
		short := "Install deployment host entries"
		if action == "uninstall" {
			short = "Remove deployment host entries"
		}
		example := "  farrow hosts " + action + "\n  farrow hosts " + action + " --yes"
		command := &cobra.Command{
			Use:     action,
			Short:   short,
			Long:    short + " through the digest-matched helper. Without --yes, print the exact privileged plan and change nothing.",
			Example: example,
			Args:    cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				return commandError(runHosts(action, apply, stdout, stderr))
			},
		}
		command.Flags().BoolVar(&apply, "yes", false, "apply the displayed privileged plan without prompting")
		parent.AddCommand(command)
	}
	return parent
}
