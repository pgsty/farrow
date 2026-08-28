package main

import (
	"io"

	"github.com/spf13/cobra"
)

func newImageCommand(stdout, stderr io.Writer) *cobra.Command {
	parent := subcommandGroup(
		"image",
		"Manage image catalogs and the local image cache",
		`Inspect signed image metadata, populate or prune the verified local cache,
import local qcow2 files, and manage the active signed catalog.`,
		`  farrow image list
  farrow image info u24
  farrow image pull u24
  farrow image prune --dry-run`,
		stdout, stderr,
	)
	parent.Aliases = []string{"images", "im"}
	listOptions := imageOptions{Action: "list"}
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available images",
		Long: `Refresh the configured signed catalog when available, then list catalog
and registered local image entries for the native host architecture.`,
		Example: `  farrow image list
  farrow image list --repo https://mirror.example/farrow
  farrow --json image list`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return commandError(runImage(listOptions, stdout, stderr))
		},
	}
	list.Flags().StringVarP(&listOptions.Repository, "repo", "r", "", repositoryFlagHelp)
	parent.AddCommand(list)

	for _, action := range []string{"info", "pull"} {
		action := action
		options := imageOptions{Action: action}
		short := "Show image metadata and cache state"
		if action == "pull" {
			short = "Download and verify an image"
		}
		long := `Show signed metadata, native-architecture selection, and verified local
cache state for one image alias.`
		example := "  farrow image info u24\n  farrow --json image info u24"
		if action == "pull" {
			long = `Download one native-architecture image from the configured repository or
immutable upstream source, then verify its digest and qcow2 metadata before
activation.`
			example = "  farrow image pull u24\n  farrow image pull u24 --repo /srv/farrow-mirror"
		}
		command := &cobra.Command{
			Use:               action + " <alias>",
			Short:             short,
			Long:              long,
			Example:           example,
			Args:              cobra.ExactArgs(1),
			ValidArgsFunction: imageAliasCompletion,
			RunE: func(_ *cobra.Command, arguments []string) error {
				options.Alias = arguments[0]
				return commandError(runImage(options, stdout, stderr))
			},
		}
		if action == "info" {
			command.Aliases = []string{"in"}
		} else {
			command.Aliases = []string{"p"}
		}
		command.Flags().StringVarP(&options.Repository, "repo", "r", "", repositoryFlagHelp)
		parent.AddCommand(command)
	}

	pruneOptions := imageOptions{Action: "prune"}
	prune := &cobra.Command{
		Use:     "prune",
		Aliases: []string{"pr"},
		Short:   "Remove unreferenced images and stale staging files",
		Long: `Find cache entries not referenced by the applied deployment plus stale
staging files. The default and --dry-run are read-only; deletion requires
--yes.`,
		Example: `  farrow image prune             # inspect candidates
  farrow image prune --dry-run   # explicit read-only scan
  farrow image prune --yes       # delete the displayed candidates`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return commandError(runImage(pruneOptions, stdout, stderr))
		},
	}
	prune.Flags().BoolVarP(&pruneOptions.DryRun, "dry-run", "d", false, "show unreferenced images and stale staging files without deleting")
	prune.Flags().BoolVarP(&pruneOptions.Apply, "yes", "y", false, "delete the displayed unreferenced images and stale staging files")
	prune.MarkFlagsMutuallyExclusive("dry-run", "yes")
	parent.AddCommand(prune)

	syncOptions := imageOptions{Action: "sync"}
	syncCommand := &cobra.Command{
		Use:     "sync <url|path>",
		Aliases: []string{"sy"},
		Short:   "Activate a signed image catalog",
		Long: `Fetch or read a catalog and its detached signature, verify it against the
production key set, enforce the version high-water mark, and activate it
atomically.`,
		Example: `  farrow image sync https://mirror.example/farrow/catalog.json
  farrow image sync /srv/farrow/catalog.json
  farrow image sync --allow-downgrade /srv/recovery/catalog.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, arguments []string) error {
			syncOptions.Source = arguments[0]
			return commandError(runImage(syncOptions, stdout, stderr))
		},
	}
	syncCommand.Flags().BoolVar(&syncOptions.AllowDowngrade, "allow-downgrade", false, "allow activation below the catalog high-water mark")
	parent.AddCommand(syncCommand)

	resetOptions := imageOptions{Action: "reset-manifest"}
	reset := &cobra.Command{
		Use:   "reset-manifest",
		Short: "Restore the embedded bootstrap catalog",
		Long: `Reactivate the catalog embedded in this Farrow binary while preserving the
signed-catalog high-water mark used to prevent silent downgrade.`,
		Example: `  farrow image reset-manifest
  farrow --json image reset-manifest`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return commandError(runImage(resetOptions, stdout, stderr))
		},
	}
	parent.AddCommand(reset)

	importOptions := imageOptions{Action: "import"}
	importCommand := &cobra.Command{
		Use:     "import <path>",
		Aliases: []string{"i"},
		Short:   "Import and verify a local qcow2 image",
		Long: `Copy a regular local image into the private cache, verify an optional SHA-256,
and inspect its qcow2 metadata. Supplying --name registers an immutable local-
prefixed alias and therefore also requires --boot and --source-user.`,
		Example: `  farrow image import ./base.qcow2
  farrow image import ./base.qcow2 --sha256 <digest>
  farrow image import ./base.qcow2 --name local-dev --boot uefi --source-user dba`,
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if importOptions.Boot == "" {
				return nil
			}
			return validateChoice("--boot", importOptions.Boot, "bios", "uefi")
		},
		RunE: func(_ *cobra.Command, arguments []string) error {
			importOptions.Path = arguments[0]
			return commandError(runImage(importOptions, stdout, stderr))
		},
	}
	importCommand.Flags().StringVarP(&importOptions.ExpectedSHA256, "sha256", "s", "", "optional expected SHA-256")
	importCommand.Flags().StringVarP(&importOptions.Name, "name", "n", "", "optional immutable local- prefixed alias")
	importCommand.Flags().StringVarP(&importOptions.Boot, "boot", "b", "", "required with --name: bios or uefi")
	importCommand.Flags().StringVarP(&importOptions.SourceUser, "source-user", "u", "", "required with --name: source image login user")
	importCommand.MarkFlagsRequiredTogether("name", "boot", "source-user")
	_ = importCommand.RegisterFlagCompletionFunc("boot", enumFlagCompletion("bios", "uefi"))
	parent.AddCommand(importCommand)
	return parent
}
