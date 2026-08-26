package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/pgsty/farrow/internal/profile"
)

const commandWords = "init validate plan doctor up start stop restart recreate status list ssh exec provision ssh-config hosts logs repair destroy image project network pigsty debug completion version help"
const globalOutputWords = "--json --yaml --verbose"

func runCompletion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: farrow completion bash|zsh|fish")
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
	structured := structuredOutput(stdout, false)
	target := stdout
	var script strings.Builder
	if structured {
		target = &script
	}
	switch args[0] {
	case "bash":
		fmt.Fprintf(target, `_farrow_complete() {
  local cur prev
  COMPREPLY=()
  cur=${COMP_WORDS[COMP_CWORD]}
  prev=${COMP_WORDS[COMP_CWORD-1]}
  if [[ ${cur} == -* ]]; then
    COMPREPLY=($(compgen -W %q -- "${cur}"))
  elif [[ ${COMP_CWORD} -eq 1 ]]; then
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
  elif [[ ${COMP_WORDS[1]} == network && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W "preflight status install uninstall" -- "${cur}"))
  elif [[ ${COMP_WORDS[1]} == pigsty && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=($(compgen -W "inventory" -- "${cur}"))
  fi
}
complete -F _farrow_complete farrow
`, globalOutputWords, commandWords, profileWords)
	case "zsh":
		fmt.Fprintf(target, `#compdef farrow
_farrow() {
  local -a commands
  commands=(%s)
  if [[ ${words[CURRENT]} == -* ]]; then
    _values 'output option' %s
  elif (( CURRENT == 2 )); then
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
  elif [[ ${words[2]} == network && ${CURRENT} == 3 ]]; then
    _values 'network command' preflight status install uninstall
  elif [[ ${words[2]} == pigsty && ${CURRENT} == 3 ]]; then
    _values 'pigsty command' inventory
  fi
}
_farrow "$@"
`, commandWords, globalOutputWords, profileWords)
	case "fish":
		for _, line := range []string{
			"complete -c farrow -f",
			"complete -c farrow -l json -d 'emit JSON'",
			"complete -c farrow -l yaml -d 'emit YAML'",
			"complete -c farrow -l verbose -d 'show detailed diagnostics'",
			"complete -c farrow -n '__fish_use_subcommand' -a '" + commandWords + "'",
			"complete -c farrow -n '__fish_seen_subcommand_from init' -a '" + profileWords + "'",
			"complete -c farrow -n '__fish_seen_subcommand_from image' -a 'list info pull prune import sync reset-manifest'",
			"complete -c farrow -n '__fish_seen_subcommand_from hosts' -a 'install uninstall'",
			"complete -c farrow -n '__fish_seen_subcommand_from project' -a 'purge-keys upgrade-state'",
			"complete -c farrow -n '__fish_seen_subcommand_from debug' -a 'bundle'",
			"complete -c farrow -n '__fish_seen_subcommand_from network' -a 'preflight status install uninstall'",
			"complete -c farrow -n '__fish_seen_subcommand_from pigsty' -a 'inventory'",
		} {
			fmt.Fprintln(target, line)
		}
	default:
		fmt.Fprintf(stderr, "unsupported completion shell %q\n", args[0])
		return exitUsage
	}
	if structured {
		return encodeJSON(stdout, stderr, struct {
			Shell  string `json:"shell"`
			Script string `json:"script"`
		}{Shell: args[0], Script: script.String()})
	}
	return exitOK
}
