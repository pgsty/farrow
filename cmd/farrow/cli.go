package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/config"
	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/pgsty/farrow/internal/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type commandRunner func([]string, io.Writer, io.Writer) int

type commandExitError struct{ code int }

func (err commandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", err.code)
}

func commandError(code int) error {
	if code == exitOK {
		return nil
	}
	return commandExitError{code: code}
}

// legacyArguments is the narrow compatibility seam between Cobra's public
// command surface and the existing operation implementations. Cobra owns
// command discovery, help, validation, completion, and flag parsing. The
// operation functions still receive canonical GNU-style flags while they are
// incrementally split out of the original main package.
func legacyArguments(command *cobra.Command, positional []string) []string {
	arguments := make([]string, 0, command.Flags().NFlag()+len(positional))
	command.Flags().Visit(func(option *pflag.Flag) {
		switch option.Name {
		case "json", "yaml", "verbose":
			return
		}
		legacyName := option.Name
		if option.Name == "file" {
			legacyName = "f"
		}
		if option.Value.Type() == "stringArray" {
			values, err := command.Flags().GetStringArray(option.Name)
			if err == nil {
				for _, value := range values {
					arguments = append(arguments, "--"+legacyName+"="+value)
				}
				return
			}
		}
		arguments = append(arguments, "--"+legacyName+"="+option.Value.String())
	})
	return append(arguments, positional...)
}

func bindOperation(command *cobra.Command, stdout, stderr io.Writer, runner commandRunner) {
	command.RunE = func(command *cobra.Command, arguments []string) error {
		return commandError(runner(legacyArguments(command, arguments), stdout, stderr))
	}
}

func helpBeforeSeparator(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--" {
			return false
		}
		if argument == "-h" || argument == "--help" {
			return true
		}
	}
	return false
}

func rawOperation(use, short string, stdout, stderr io.Writer, runner commandRunner) *cobra.Command {
	command := &cobra.Command{
		Use:                   use,
		Short:                 short,
		DisableFlagParsing:    true,
		DisableFlagsInUseLine: true,
	}
	command.RunE = func(command *cobra.Command, arguments []string) error {
		if helpBeforeSeparator(arguments) {
			return command.Help()
		}
		return commandError(runner(arguments, stdout, stderr))
	}
	return command
}

func addConfigFlag(command *cobra.Command) {
	command.Flags().StringP("file", "f", "", "declarative configuration file")
}

func addRepositoryFlag(command *cobra.Command) {
	command.Flags().String("repo", "", "image repository URL or absolute local directory")
}

func templateCompletion(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	values := make([]string, 0)
	for _, name := range config.TemplateNames() {
		if strings.HasPrefix(name, prefix) {
			values = append(values, name)
		}
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}

func subcommandGroup(use, short string, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			message := fmt.Sprintf("%s requires a subcommand", command.CommandPath())
			if structuredOutput(stdout, false) {
				if code := emitCommandFailure(stdout, stderr, false, "usage", message, ""); code != exitOK {
					return commandError(code)
				}
				errorf(stderr, "%s", message)
				return commandExitError{code: exitUsage}
			}
			errorf(stderr, "%s", message)
			fmt.Fprintf(stderr, "usage: %s\n", command.UseLine())
			return commandExitError{code: exitUsage}
		},
	}
}

func newSetupCommand(stdout, stderr io.Writer) *cobra.Command {
	options := setupCLIOptions{Mode: "host"}
	command := &cobra.Command{
		Use:               "setup [meta|dual|trio|full]",
		Short:             "Prepare the host and lab for first use",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: templateCompletion,
	}
	command.Flags().StringVarP(&options.FilePath, "file", "f", "", "existing configuration to prepare")
	command.Flags().StringVar(&options.NetworkCIDR, "network-cidr", "", "generated lab: host-global RFC1918 IPv4 /24")
	command.Flags().StringVar(&options.Repo, "repo", "", "repository URL or absolute directory mirroring images and socket_vmnet")
	command.Flags().StringVar(&options.Mode, "mode", options.Mode, "Darwin private network mode: host or shared")
	command.Flags().BoolVar(&options.DryRun, "dry-run", false, "show the resolved setup plan without changing anything")
	command.Flags().BoolVar(&options.Yes, "yes", false, "accept the one-time setup plan (required without a terminal)")
	command.RunE = func(command *cobra.Command, arguments []string) error {
		profileName := ""
		if len(arguments) == 1 {
			profileName = arguments[0]
		}
		options.ModeExplicit = command.Flags().Changed("mode")
		return commandError(runSetupCommand(profileName, options, stdout, stderr))
	}
	return command
}

func newInitCommand(stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:               "init [meta|dual|trio|full]",
		Short:             "Write a lab configuration (Pigsty-compatible inventory)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: templateCompletion,
	}
	command.Flags().String("network-cidr", "", "rebase the template to an RFC1918 IPv4 /24")
	command.Flags().StringP("output", "o", "", "write to this path instead of ./pigsty.yml; '-' writes to stdout")
	command.Flags().Bool("force", false, "overwrite an existing configuration file")
	bindOperation(command, stdout, stderr, runInit)
	return command
}

func newValidateCommand(stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{Use: "validate", Short: "Validate and resolve a Farrow configuration", Args: cobra.NoArgs}
	addConfigFlag(command)
	bindOperation(command, stdout, stderr, runValidate)
	return command
}

func newLifecycleCommand(name, short string, stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{Use: name + " [node...]", Short: short}
	if name == "stop" {
		command.Aliases = []string{"halt"}
	}
	options := lifecycleOptions{}
	switch name {
	case "plan":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "declarative configuration file")
		command.Flags().StringVar(&options.Repository, "repo", "", "image repository URL or absolute local directory")
	case "up", "reload":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "declarative configuration file")
		command.Flags().StringVar(&options.Repository, "repo", "", "image repository URL or absolute local directory")
		command.Flags().BoolVar(&options.NoWait, "no-wait", false, "return after QMP/process identity without waiting for guest readiness")
		command.Flags().BoolVar(&options.Rollback, "rollback", false, "remove safe artifacts from nodes that fail to prepare")
	case "start", "restart":
		command.Flags().BoolVar(&options.NoWait, "no-wait", false, "return after QMP/process identity without waiting for guest readiness")
	case "recreate":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "declarative configuration file")
		command.Flags().BoolVar(&options.Force, "force", false, "confirm destructive recreation")
		command.Flags().BoolVar(&options.NoWait, "no-wait", false, "return after QMP/process identity without waiting for guest readiness")
	case "destroy":
		command.Flags().BoolVar(&options.Force, "force", false, "confirm destruction")
		command.Flags().BoolVar(&options.DeletePersistent, "delete-persistent", false, "also delete owned persistent data disks")
		command.Flags().BoolVar(&options.Purge, "purge", false, "terminal disposal: also delete persistent disks, keys, the registration, and the workspace marker")
	}
	command.RunE = func(_ *cobra.Command, nodes []string) error {
		return commandError(runLifecycleCommand(name, options, nodes, stdout, stderr))
	}
	return command
}

func newProvisionCommand(stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{Use: "provision [node...]", Short: "Run a local Bash script in selected guests"}
	command.Flags().String("script", "", "local Bash script to stream to each selected guest")
	command.Flags().Bool("sudo", false, "run the guest script through sudo -n")
	command.Flags().Int("parallel", 1, "bounded node concurrency, 1..4")
	command.Flags().Duration("timeout", time.Hour, "hard deadline for the operation, maximum 24h")
	bindOperation(command, stdout, stderr, runProvision)
	return command
}

func newSSHConfigCommand(stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "ssh-config [node...]",
		Short: "Print, install, or remove OpenSSH configuration",
		Long:  "Print a project SSH fragment, or explicitly install/remove its marker-owned global Include.",
	}
	command.Flags().Bool("install", false, "install a marker-owned Include and project fragment")
	command.Flags().Bool("remove", false, "remove this project's marker-owned Include and fragment")
	command.Flags().String("name", "farrow", "SSH Host and fragment prefix")
	bindOperation(command, stdout, stderr, runSSHConfig)
	return command
}

func defaultSSHShortcutName() (string, error) {
	return "farrow", nil
}

func sshShortcutArguments(name string, nodes []string) ([]string, error) {
	if name == "" {
		var err error
		name, err = defaultSSHShortcutName()
		if err != nil {
			return nil, err
		}
	}
	if !sshconfig.ValidName(name) {
		return nil, fmt.Errorf("invalid SSH config name %q", name)
	}
	arguments := []string{"--install", "--name", name}
	return append(arguments, nodes...), nil
}

func newSSHShortcutCommand(stdout, stderr io.Writer) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "ss [node...]",
		Short: "Install this project's SSH config",
		Long:  "Shortcut for 'farrow ssh-config --install'. The default prefix is the current project directory name.",
		RunE: func(_ *cobra.Command, nodes []string) error {
			arguments, err := sshShortcutArguments(name, nodes)
			if err != nil {
				return err
			}
			return commandError(runSSHConfig(arguments, stdout, stderr))
		},
	}
	command.Flags().StringVar(&name, "name", "", "SSH prefix (default: current project directory name)")
	return command
}

func newLogsCommand(stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{Use: "logs [node]", Short: "Read or follow project logs", Args: cobra.MaximumNArgs(1)}
	command.Flags().String("source", "serial", "log source: serial, qemu, or events")
	command.Flags().BoolP("follow", "f", false, "continue streaming appended log data")
	bindOperation(command, stdout, stderr, runLogs)
	return command
}

func addApplyFlag(command *cobra.Command) {
	command.Flags().Bool("yes", false, "apply the displayed privileged plan")
}

func newHostsCommand(stdout, stderr io.Writer) *cobra.Command {
	parent := subcommandGroup("hosts", "Manage project entries in the host hosts file", stdout, stderr)
	for _, action := range []string{"install", "uninstall"} {
		action := action
		short := "Install project host entries"
		if action == "uninstall" {
			short = "Remove project host entries"
		}
		command := &cobra.Command{Use: action, Short: short, Args: cobra.NoArgs}
		addApplyFlag(command)
		bindOperation(command, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
			return runHosts(append([]string{action}, arguments...), stdout, stderr)
		})
		parent.AddCommand(command)
	}
	return parent
}

func newImageCommand(stdout, stderr io.Writer) *cobra.Command {
	parent := subcommandGroup("image", "Manage image catalogs and the local image cache", stdout, stderr)
	list := &cobra.Command{Use: "list", Short: "List available images", Args: cobra.NoArgs}
	addRepositoryFlag(list)
	bindOperation(list, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runImage(append([]string{"list"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(list)

	for _, action := range []string{"info", "pull"} {
		action := action
		short := "Show image metadata and cache state"
		if action == "pull" {
			short = "Download and verify an image"
		}
		command := &cobra.Command{Use: action + " <alias>", Short: short, Args: cobra.ExactArgs(1)}
		addRepositoryFlag(command)
		bindOperation(command, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
			return runImage(append([]string{action}, arguments...), stdout, stderr)
		})
		parent.AddCommand(command)
	}

	prune := &cobra.Command{Use: "prune", Short: "Remove unreferenced images and stale staging files", Args: cobra.NoArgs}
	prune.Flags().Bool("dry-run", false, "show unreferenced images and stale staging files without deleting")
	prune.Flags().Bool("yes", false, "delete the displayed unreferenced images and stale staging files")
	bindOperation(prune, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runImage(append([]string{"prune"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(prune)

	syncCommand := &cobra.Command{Use: "sync <url|path>", Short: "Activate a signed image catalog", Args: cobra.ExactArgs(1)}
	syncCommand.Flags().Bool("allow-downgrade", false, "allow activation below the catalog high-water mark")
	bindOperation(syncCommand, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runImage(append([]string{"sync"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(syncCommand)

	reset := &cobra.Command{Use: "reset-manifest", Short: "Restore the embedded bootstrap catalog", Args: cobra.NoArgs}
	bindOperation(reset, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runImage(append([]string{"reset-manifest"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(reset)

	importCommand := &cobra.Command{Use: "import <path>", Short: "Import and verify a local qcow2 image", Args: cobra.ExactArgs(1)}
	importCommand.Flags().String("sha256", "", "optional expected SHA-256")
	importCommand.Flags().String("name", "", "optional immutable local- prefixed alias")
	importCommand.Flags().String("boot", "", "required with --name: bios or uefi")
	importCommand.Flags().String("source-user", "", "required with --name: source image login user")
	bindOperation(importCommand, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runImage(append([]string{"import"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(importCommand)
	return parent
}

func newNetworkCommand(stdout, stderr io.Writer) *cobra.Command {
	parent := subcommandGroup("network", "Inspect and manage host-global private networking", stdout, stderr)

	preflight := &cobra.Command{Use: "preflight", Short: "Check a private-network plan", Args: cobra.NoArgs}
	preflight.Flags().String("cidr", "", "candidate host-global RFC1918 IPv4 /24")
	preflight.Flags().StringP("file", "f", "", "private configuration whose node addresses should be probed")
	bindOperation(preflight, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runNetwork(append([]string{"preflight"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(preflight)

	status := &cobra.Command{Use: "status", Short: "Inspect installed network and lease state", Args: cobra.NoArgs}
	status.Flags().String("cidr", "", "expected host-global RFC1918 IPv4 /24")
	bindOperation(status, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runNetwork(append([]string{"status"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(status)

	install := &cobra.Command{Use: "install", Short: "Install the host-global private network", Args: cobra.NoArgs}
	install.Flags().String("cidr", "10.10.10.0/24", "host-global RFC1918 IPv4 /24")
	install.Flags().String("mode", "host", "Darwin vmnet mode: host or shared")
	install.Flags().String("archive", "", "Darwin: pinned socket_vmnet archive")
	install.Flags().String("interface-id", "", "Darwin: persistent vmnet UUID")
	addApplyFlag(install)
	bindOperation(install, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runNetwork(append([]string{"install"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(install)

	uninstall := &cobra.Command{Use: "uninstall", Short: "Remove Farrow-owned host networking", Args: cobra.NoArgs}
	addApplyFlag(uninstall)
	bindOperation(uninstall, stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runNetwork(append([]string{"uninstall"}, arguments...), stdout, stderr)
	})
	parent.AddCommand(uninstall)
	return parent
}

func runVersionCommand(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) != 0 {
		return exitUsage
	}
	if structuredOutput(stdout, false) {
		return encodeJSON(stdout, stderr, struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Built   string `json:"built"`
			OS      string `json:"os"`
			Arch    string `json:"arch"`
		}{Name: "farrow", Version: version.Version, Commit: version.Commit, Built: version.Date, OS: runtime.GOOS, Arch: runtime.GOARCH})
	}
	fmt.Fprintf(stdout, "farrow %s (commit %s, built %s, %s/%s)\n", version.Version, version.Commit, version.Date, runtime.GOOS, runtime.GOARCH)
	return exitOK
}

func newCompletionCommand(root *cobra.Command, stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(_ *cobra.Command, arguments []string) error {
			var script bytes.Buffer
			var err error
			switch arguments[0] {
			case "bash":
				err = root.GenBashCompletion(&script)
			case "zsh":
				err = root.GenZshCompletion(&script)
			case "fish":
				err = root.GenFishCompletion(&script, true)
			case "powershell":
				err = root.GenPowerShellCompletion(&script)
			default:
				return fmt.Errorf("unsupported completion shell %q; expected bash, zsh, fish, or powershell", arguments[0])
			}
			if err != nil {
				return err
			}
			if structuredOutput(stdout, false) {
				return commandError(encodeJSON(stdout, stderr, struct {
					Shell  string `json:"shell"`
					Script string `json:"script"`
				}{Shell: arguments[0], Script: script.String()}))
			}
			_, err = io.Copy(stdout, &script)
			return err
		},
	}
	return command
}

func commandSettings(stdout io.Writer) *viper.Viper {
	if state := outputContextFrom(stdout); state != nil && state.settings != nil {
		return state.settings
	}
	settings := viper.New()
	settings.SetDefault("output.format", string(outputText))
	settings.SetDefault("output.verbose", false)
	return settings
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	settings := commandSettings(stdout)
	root := &cobra.Command{
		Use:   "farrow",
		Short: "Run reproducible local Linux virtual-machine labs",
		Long:  "Farrow boots a Pigsty-compatible inventory into fixed-IP QEMU virtual machines, managed from the current project directory.",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if structuredOutput(stdout, false) {
				if code := emitCommandFailure(stdout, stderr, false, "usage", "farrow requires a command", ""); code != exitOK {
					return commandError(code)
				}
				errorf(stderr, "farrow requires a command")
				return commandExitError{code: exitUsage}
			}
			if err := command.Help(); err != nil {
				return err
			}
			return commandExitError{code: exitUsage}
		},
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableAutoGenTag:     true,
		DisableFlagsInUseLine: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return err })
	root.PersistentFlags().Bool("json", false, "emit JSON output")
	root.PersistentFlags().Bool("yaml", false, "emit YAML output")
	root.PersistentFlags().Bool("verbose", false, "emit detailed diagnostics to stderr")
	_ = settings.BindPFlag("output.json", root.PersistentFlags().Lookup("json"))
	_ = settings.BindPFlag("output.yaml", root.PersistentFlags().Lookup("yaml"))
	_ = settings.BindPFlag("output.verbose_flag", root.PersistentFlags().Lookup("verbose"))

	root.AddGroup(
		&cobra.Group{ID: "project", Title: "Project Setup:"},
		&cobra.Group{ID: "lifecycle", Title: "Lifecycle:"},
		&cobra.Group{ID: "access", Title: "Guest Access:"},
		&cobra.Group{ID: "images", Title: "Images:"},
		&cobra.Group{ID: "host", Title: "Host Management:"},
		&cobra.Group{ID: "advanced", Title: "Advanced:"},
	)

	setup := newSetupCommand(stdout, stderr)
	setup.GroupID = "project"
	initCommand := newInitCommand(stdout, stderr)
	initCommand.GroupID = "project"
	validate := newValidateCommand(stdout, stderr)
	validate.GroupID = "project"
	root.AddCommand(setup, initCommand, validate)

	for _, item := range []struct{ name, short string }{
		{"plan", "Show the changes required to reach desired state"},
		{"up", "Create or converge selected virtual machines"},
		{"start", "Start selected stopped virtual machines"},
		{"stop", "Stop selected running virtual machines"},
		{"restart", "Restart selected virtual machines"},
		{"reload", "Stop, re-read the configuration, and converge (halt + up)"},
		{"recreate", "Destroy and recreate selected virtual machines"},
		{"status", "Show current project state"},
		{"destroy", "Destroy the project, or remove selected nodes from it"},
	} {
		command := newLifecycleCommand(item.name, item.short, stdout, stderr)
		command.GroupID = "lifecycle"
		root.AddCommand(command)
	}

	ssh := rawOperation("ssh [node] [-- command [args...]]", "Open SSH or run a command in a guest", stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runSSH("ssh", arguments, stdout, stderr)
	})
	ssh.GroupID = "access"
	execCommand := rawOperation("exec [node] -- command [args...]", "Run a command in a guest", stdout, stderr, func(arguments []string, stdout, stderr io.Writer) int {
		return runSSH("exec", arguments, stdout, stderr)
	})
	execCommand.GroupID = "access"
	provision := newProvisionCommand(stdout, stderr)
	provision.GroupID = "access"
	sshConfig := newSSHConfigCommand(stdout, stderr)
	sshConfig.GroupID = "access"
	ss := newSSHShortcutCommand(stdout, stderr)
	ss.GroupID = "access"
	hosts := newHostsCommand(stdout, stderr)
	hosts.GroupID = "access"
	logs := newLogsCommand(stdout, stderr)
	logs.GroupID = "access"
	root.AddCommand(ssh, execCommand, provision, sshConfig, ss, hosts, logs)

	images := newImageCommand(stdout, stderr)
	images.GroupID = "images"
	root.AddCommand(images)

	doctor := &cobra.Command{Use: "doctor", Short: "Check host capabilities", Args: cobra.NoArgs, GroupID: "host"}
	bindOperation(doctor, stdout, stderr, runDoctor)
	network := newNetworkCommand(stdout, stderr)
	network.GroupID = "host"
	root.AddCommand(doctor, network)

	versionCommand := &cobra.Command{Use: "version", Short: "Print build version", Args: cobra.NoArgs, GroupID: "advanced"}
	bindOperation(versionCommand, stdout, stderr, runVersionCommand)
	completion := newCompletionCommand(root, stdout, stderr)
	completion.GroupID = "advanced"
	root.AddCommand(completion, versionCommand)
	return root
}

func executeCLI(arguments []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(arguments)
	executed, err := root.ExecuteC()
	if err == nil {
		return exitOK
	}
	var coded commandExitError
	if errors.As(err, &coded) {
		return coded.code
	}
	_ = emitCommandFailure(stdout, stderr, false, "usage", err.Error(), "")
	errorf(stderr, "%v", err)
	if executed == nil {
		executed = root
	}
	fmt.Fprintf(stderr, "usage: %s\n", executed.UseLine())
	return exitUsage
}
