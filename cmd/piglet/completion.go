package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/pgsty/piglet/internal/profile"
)

const commandWords = "init validate plan doctor up start stop restart recreate status list ssh exec ssh-config hosts logs repair destroy image project network pigsty debug completion version help"

func runCompletion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: piglet completion bash|zsh|fish")
		return exitUsage
	}
	descriptors, err := profile.List()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitIntegrity
	}
	profileNames := []string{"quick"}
	for _, descriptor := range descriptors {
		profileNames = append(profileNames, descriptor.Name)
	}
	profileWords := strings.Join(profileNames, " ")
	switch args[0] {
	case "bash":
		fmt.Fprintf(stdout, `_piglet_complete() {
  local cur prev
  COMPREPLY=()
  cur=${COMP_WORDS[COMP_CWORD]}
  prev=${COMP_WORDS[COMP_CWORD-1]}
  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=($(compgen -W %q -- "${cur}"))
  elif [[ ${COMP_WORDS[1]} == init && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W %q -- "${cur}"))
  elif [[ ${COMP_WORDS[1]} == image && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W "list info pull prune import sync reset-manifest" -- "${cur}"))
  elif [[ ${COMP_WORDS[1]} == hosts && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W "install uninstall" -- "${cur}"))
  elif [[ ${COMP_WORDS[1]} == project && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W "purge-keys upgrade-state" -- "${cur}"))
  elif [[ ${COMP_WORDS[1]} == debug && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W "bundle" -- "${cur}"))
  fi
}
complete -F _piglet_complete piglet
`, commandWords, profileWords)
	case "zsh":
		fmt.Fprintf(stdout, `#compdef piglet
_piglet() {
  local -a commands
  commands=(%s)
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  elif [[ ${words[2]} == init && ${CURRENT} == 3 ]]; then
    _values 'profile' %s
  elif [[ ${words[2]} == image && ${CURRENT} == 3 ]]; then
    _values 'image command' list info pull prune import sync reset-manifest
  elif [[ ${words[2]} == hosts && ${CURRENT} == 3 ]]; then
    _values 'hosts command' install uninstall
  elif [[ ${words[2]} == project && ${CURRENT} == 3 ]]; then
    _values 'project command' purge-keys upgrade-state
  elif [[ ${words[2]} == debug && ${CURRENT} == 3 ]]; then
    _values 'debug command' bundle
  fi
}
_piglet "$@"
`, commandWords, profileWords)
	case "fish":
		for _, line := range []string{
			"complete -c piglet -f",
			"complete -c piglet -n '__fish_use_subcommand' -a '" + commandWords + "'",
			"complete -c piglet -n '__fish_seen_subcommand_from init' -a '" + profileWords + "'",
			"complete -c piglet -n '__fish_seen_subcommand_from image' -a 'list info pull prune import sync reset-manifest'",
			"complete -c piglet -n '__fish_seen_subcommand_from hosts' -a 'install uninstall'",
			"complete -c piglet -n '__fish_seen_subcommand_from project' -a 'purge-keys upgrade-state'",
			"complete -c piglet -n '__fish_seen_subcommand_from debug' -a 'bundle'",
		} {
			fmt.Fprintln(stdout, line)
		}
	default:
		fmt.Fprintf(stderr, "unsupported completion shell %q\n", args[0])
		return exitUsage
	}
	return exitOK
}
