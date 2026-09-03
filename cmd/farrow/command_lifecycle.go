package main

import (
	"io"

	"github.com/spf13/cobra"
)

type lifecycleHelp struct {
	long    string
	example string
}

var lifecycleHelpByName = map[string]lifecycleHelp{
	"plan": {
		long: `Compare the desired inventory with the applied deployment and classify each
selected node as create, recreate, missing, or unchanged. Plan is read-only.
It uses -f first, then local discovery, then the last applied inventory.`,
		example: `  farrow plan                    # inspect all pending changes
  farrow plan -f pigsty.yml      # compare one explicit desired inventory
  farrow --json plan meta        # inspect one node as structured data`,
	},
	"up": {
		long: `Create missing nodes, start stopped ones, and refresh the SSH client
configuration so plain ssh reaches every node. Running nodes are left alone.

Changed node definitions and nodes removed from the inventory are reported,
never applied: use recreate or destroy for those.`,
		example: `  farrow up                      # converge the discovered or last applied inventory
  farrow up meta                 # converge only the meta node
  farrow up --mirror             # use the China official repository for downloads
  farrow up -f pigsty.yml --rollback  # remove safe artifacts from failed prepares`,
	},
	"start": {
		long: `Start stopped nodes from the applied deployment state and wait for each
guest to become ready. Start does not read an inventory, create nodes, or
refresh the SSH client configuration.`,
		example: `  farrow start                   # start every stopped node
  farrow start meta --no-wait    # return once QEMU is running`,
	},
	"stop": {
		long: `Stop running nodes recorded in the applied deployment state.`,
		example: `  farrow stop                    # stop the deployment
  farrow stop meta               # stop one node`,
	},
	"restart": {
		long: `Stop and start existing nodes from the applied state without re-reading an
inventory. Use reload when the inventory may contain new nodes.`,
		example: `  farrow restart                 # restart the deployment
  farrow restart meta --no-wait  # restart one node without waiting for the guest`,
	},
	"reload": {
		long: `Stop the selected nodes, re-read the inventory, and run up. New nodes are
created; changed definitions still need recreate and removed nodes still need
destroy, and either is refused before anything stops.`,
		example: `  farrow reload                  # re-read the discovered inventory
  farrow reload -f pigsty.yml    # reload from an explicit desired inventory
  farrow reload --mirror         # use the China official repository for downloads
  farrow reload meta             # reload one selected node`,
	},
	"recreate": {
		long: `Destroy and recreate selected nodes from the desired inventory.

This is the explicit path for node-definition drift. On a terminal Farrow asks
you to type recreate; automation must pass --force. Persistent disks follow
their declared lifecycle policy. A successful recreate also refreshes the SSH
client configuration.`,
		example: `  farrow recreate meta           # review and confirm one changed node
  farrow recreate --force meta   # non-interactive recreation
  farrow recreate --mirror meta  # use the China official repository for downloads
  farrow recreate -f pigsty.yml meta`,
	},
	"status": {
		long: `Show recorded applied state and live runtime identity for selected nodes.
Status works outside the inventory directory and repairs state left by an
interrupted operation before reporting it.`,
		example: `  farrow status                  # show every node
  farrow status meta             # show one node
  farrow --json status           # stable machine-readable status`,
	},
	"destroy": {
		long: `Destroy selected nodes, or the whole deployment when no selector is given.

Destroy never infers deletion from a missing inventory entry. On a terminal it
asks you to type destroy; automation must pass --force. Persistent disks and
keys are preserved unless their wider deletion flags are explicit. Removing
nodes refreshes the SSH client configuration for the remaining deployment;
destroying the whole deployment also removes the SSH client configuration
Farrow installed.`,
		example: `  farrow destroy meta            # review and confirm removal of one node
  farrow destroy --force         # remove the deployment, preserving persistent data
  farrow destroy --force --purge # terminal disposal; image cache remains`,
	},
}

func newLifecycleCommand(name, short string, stdout, stderr io.Writer) *cobra.Command {
	help := lifecycleHelpByName[name]
	command := &cobra.Command{
		Use:               name + " [node...]",
		Short:             short,
		Long:              help.long,
		Example:           help.example,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: nodeCompletion(name == "plan" || name == "up" || name == "reload" || name == "recreate", false),
	}
	switch name {
	case "plan":
		command.Aliases = []string{"pl"}
	case "recreate":
		command.Aliases = []string{"rc"}
	case "status":
		command.Aliases = []string{"st"}
	case "destroy":
		command.Aliases = []string{"de"}
	}
	options := lifecycleOptions{}
	switch name {
	case "plan":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "desired inventory; defaults to the discovered inventory, then the applied state")
		command.Flags().StringVarP(&options.Repository, "repo", "r", "", repositoryFlagHelp)
	case "up", "reload":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "desired inventory; defaults to the discovered inventory, then the applied state")
		command.Flags().StringVarP(&options.Repository, "repo", "r", "", repositoryFlagHelp)
		command.Flags().BoolVar(&options.Mirror, "mirror", false, mirrorFlagHelp)
		command.Flags().BoolVarP(&options.NoWait, "no-wait", "n", false, "return once QEMU is running, without waiting for the guest to boot")
		command.Flags().BoolVar(&options.Rollback, "rollback", false, "remove safe artifacts from nodes that fail to prepare")
		noFileCompletions(command, "mirror", "no-wait", "rollback")
	case "start", "restart":
		command.Flags().BoolVarP(&options.NoWait, "no-wait", "n", false, "return once QEMU is running, without waiting for the guest to boot")
		noFileCompletions(command, "no-wait")
	case "recreate":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "desired inventory; defaults to the discovered inventory, then the applied state")
		command.Flags().StringVarP(&options.Repository, "repo", "r", "", repositoryFlagHelp)
		command.Flags().BoolVar(&options.Mirror, "mirror", false, mirrorFlagHelp)
		command.Flags().BoolVar(&options.Force, "force", false, "recreate without the interactive confirmation (required without a terminal)")
		command.Flags().BoolVarP(&options.NoWait, "no-wait", "n", false, "return once QEMU is running, without waiting for the guest to boot")
		noFileCompletions(command, "mirror", "force", "no-wait")
	case "destroy":
		command.Flags().BoolVar(&options.Force, "force", false, "destroy without the interactive confirmation (required without a terminal)")
		command.Flags().BoolVar(&options.DeletePersistent, "delete-persistent", false, "also delete owned persistent data disks")
		command.Flags().BoolVar(&options.Purge, "purge", false, "terminal disposal: also delete persistent disks, keys, and deployment state")
		noFileCompletions(command, "force", "delete-persistent", "purge")
	}
	command.RunE = func(command *cobra.Command, nodes []string) error {
		outcome, err := runLifecycleCommand(command.Context(), name, options, nodes, stderr)
		if err != nil {
			return err
		}
		return collectCommandOutcome(command.Context(), outcome)
	}
	return command
}
