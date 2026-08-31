package main

import (
	"bytes"
	"fmt"
	"io"
	"runtime"

	"github.com/pgsty/farrow/internal/version"
	"github.com/spf13/cobra"
)

type versionResult struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func runVersionCommand() commandOutcome {
	result := versionResult{Name: "farrow", Version: version.Version, Commit: version.Commit, Built: version.Date, OS: runtime.GOOS, Arch: runtime.GOARCH}
	return commandOutcome{
		payload: result,
		text: func(writer io.Writer) error {
			_, err := fmt.Fprintf(writer, "farrow %s (commit %s, built %s, %s/%s)\n", result.Version, result.Commit, result.Built, result.OS, result.Arch)
			return err
		},
	}
}

func newCompletionCommand(root *cobra.Command, stdout, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion",
		Long: `Generate a shell completion script containing commands, scoped flags,
templates, image aliases, and best-effort node-name completion.`,
		Example: `  source <(farrow completion bash)
  farrow completion zsh > "${fpath[1]}/_farrow"
  farrow completion fish > ~/.config/fish/completions/farrow.fish`,
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
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
				errorf(stderr, "generate %s completion: %v", arguments[0], err)
				return commandError(exitRuntime)
			}
			if structuredOutput(stdout) {
				return commandError(encodeJSON(stdout, stderr, struct {
					Shell  string `json:"shell"`
					Script string `json:"script"`
				}{Shell: arguments[0], Script: script.String()}))
			}
			if _, err = io.Copy(stdout, &script); err != nil {
				errorf(stderr, "write %s completion: %v", arguments[0], err)
				return commandError(exitRuntime)
			}
			return nil
		},
	}
	return command
}
