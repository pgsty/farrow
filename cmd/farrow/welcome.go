package main

import "io"

// The bare command is a small map of the next few actions. --help remains
// the complete reference, and missing-command exit semantics stay unchanged.
func printWelcome(out io.Writer) {
	bestEffortln(out, "Farrow · local Linux virtual machines")
	bestEffortln(out)
	if resolved, err := currentDeploymentResolved(); err == nil && len(resolved.Nodes) > 0 {
		textField(out, 10, "connect", "farrow ssh")
		textField(out, 10, "inspect", "farrow status")
		textField(out, 10, "continue", "farrow up")
		textField(out, 10, "stop", "farrow stop")
	} else {
		textField(out, 10, "start", "farrow up")
		textField(out, 10, "customize", "farrow init")
		textField(out, 10, "preview", "farrow plan")
	}
	textField(out, 10, "help", "farrow --help")
}
