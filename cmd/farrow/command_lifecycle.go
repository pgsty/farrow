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
It uses -f first, then local discovery, then the applied specification.`,
		example: `  farrow plan                    # inspect all pending changes
  farrow plan -f pigsty.yml      # compare one explicit desired inventory
  farrow --json plan meta        # inspect one node as structured data`,
	},
	"up": {
		long: `Converge safe additions and starts without disrupting running peers, then
synchronize the default marker-owned SSH configuration from the complete
applied deployment so plain ssh can reach every materialized node.

Up creates missing selected nodes and starts selected nodes that already exist
but are stopped. Start only performs that power-on step and does not refresh SSH
configuration. Definition changes and removed inventory nodes are reported,
never applied implicitly; use recreate or destroy for those transitions.`,
		example: `  farrow up                      # converge the discovered or applied specification
  farrow up meta                 # converge only the meta node
  farrow up -f pigsty.yml --rollback  # remove safe artifacts from failed prepares`,
	},
	"start": {
		long: `Start already-created nodes from the applied deployment state.
Start does not discover or re-read an inventory, create nodes, or refresh the
SSH client configuration; use up for the complete converge-and-configure path.`,
		example: `  farrow start                   # start every stopped node
  farrow start meta --no-wait    # return after process and QMP identity`,
	},
	"stop": {
		long: `Stop running nodes recorded in the applied deployment state.`,
		example: `  farrow stop                    # stop the deployment
  farrow stop meta               # stop one node`,
	},
	"restart": {
		long: `Stop and start existing nodes from applied state without re-reading an inventory.
Use reload when the desired inventory may contain additions or changed fields.`,
		example: `  farrow restart                 # restart the deployment
  farrow restart meta --no-wait  # restart one node without guest-readiness wait`,
	},
	"reload": {
		long: `Stop selected nodes, re-read the desired inventory, and run the safe up
convergence path as one operation. Additions can be created; definition
changes still require an explicit recreate and removed nodes still require an
explicit destroy, and either is refused before any node stops.`,
		example: `  farrow reload                  # re-read the discovered inventory
  farrow reload -f pigsty.yml    # reload from an explicit desired inventory
  farrow reload meta             # reload one selected node`,
	},
	"recreate": {
		long: `Destroy and recreate selected nodes from the desired inventory.

This is the explicit path for node-definition drift. On a terminal Farrow asks
you to type recreate; automation must pass --force. Persistent disks follow
their declared lifecycle policy. A successful recreate also refreshes the
complete default SSH configuration.`,
		example: `  farrow recreate meta           # review and confirm one changed node
  farrow recreate --force meta   # non-interactive recreation
  farrow recreate -f pigsty.yml meta`,
	},
	"status": {
		long: `Show recorded applied state and live runtime identity for selected nodes.
Status works outside the inventory directory and safely converges dead or
QMP-bound interrupted transitions before reporting them.`,
		example: `  farrow status                  # show every node
  farrow status meta             # show one node
  farrow --json status           # stable machine-readable status`,
	},
	"destroy": {
		long: `Destroy selected nodes, or the whole deployment when no selector is given.

Destroy never infers deletion from a missing inventory entry. On a terminal it
asks you to type destroy; automation must pass --force. Persistent disks and
keys are preserved unless their wider deletion flags are explicit. Node destroy
refreshes the default SSH configuration for the remaining deployment; whole
destroy removes that default marker-owned integration.`,
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
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "desired inventory (default: discover locally, then use applied state)")
		command.Flags().StringVarP(&options.Repository, "repo", "r", "", repositoryFlagHelp)
	case "up", "reload":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "desired inventory (default: discover locally, then use applied state)")
		command.Flags().StringVarP(&options.Repository, "repo", "r", "", repositoryFlagHelp)
		command.Flags().BoolVarP(&options.NoWait, "no-wait", "n", false, "return after QMP/process identity without waiting for guest readiness")
		command.Flags().BoolVarP(&options.Rollback, "rollback", "b", false, "remove safe artifacts from nodes that fail to prepare")
	case "start", "restart":
		command.Flags().BoolVarP(&options.NoWait, "no-wait", "n", false, "return after QMP/process identity without waiting for guest readiness")
	case "recreate":
		command.Flags().StringVarP(&options.ConfigPath, "file", "f", "", "desired inventory (default: discover locally, then use applied state)")
		command.Flags().BoolVar(&options.Force, "force", false, "recreate without the interactive confirmation (required without a terminal)")
		command.Flags().BoolVarP(&options.NoWait, "no-wait", "n", false, "return after QMP/process identity without waiting for guest readiness")
	case "destroy":
		command.Flags().BoolVarP(&options.Force, "force", "f", false, "destroy without the interactive confirmation (required without a terminal)")
		command.Flags().BoolVar(&options.DeletePersistent, "delete-persistent", false, "also delete owned persistent data disks")
		command.Flags().BoolVar(&options.Purge, "purge", false, "terminal disposal: also delete persistent disks, keys, and deployment state")
	}
	command.RunE = func(_ *cobra.Command, nodes []string) error {
		return commandError(runLifecycleCommand(name, options, nodes, stdout, stderr))
	}
	return command
}
