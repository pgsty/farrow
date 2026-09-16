package cloudinit

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const SetupVersion = "20260916.2"

// RetryScript updates only Farrow's guest setup helpers. This lets existing
// VMs use current recovery behavior without cloud-init reset or recreation.
// Private keys never travel in the generated shell command.
func RetryScript(input Input, stages ...string) (string, error) {
	if input.PrivateKey != "" {
		return "", errors.New("guest setup retry must not contain a private key")
	}
	if err := validateInput(input); err != nil {
		return "", err
	}
	ready, err := renderReadyScript(input)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(`set -euo pipefail
install -d -m 0755 /var/lib/farrow
exec 9>/var/lib/farrow/finalize.lock
flock -n 9 || { echo 'guest setup is already running; retry shortly' >&2; exit 1; }
temporary=$(mktemp -d /usr/local/libexec/.farrow-setup.XXXXXX)
trap 'rm -rf -- "${temporary}"' EXIT
`)
	write := func(name, script string) {
		fmt.Fprintf(&out, "printf '%%s' %s | base64 -d >\"${temporary}/%s\"\nchmod 0755 \"${temporary}/%s\"\nmv -f -- \"${temporary}/%s\" /usr/local/libexec/%s\n",
			base64.StdEncoding.EncodeToString([]byte(script)), name, name, name, name)
	}
	write("farrow-warning", renderWarningScript())
	write("farrow-identity-contract", renderIdentityContractScript(input.SSHUser))
	write("farrow-init-disks", renderDiskScript(input.Disks))
	write("farrow-hosts", RenderHostsScript(input.Hosts))
	if len(input.Shares) != 0 {
		write("farrow-init-shares", renderShareScript(input.SSHUser, input.Shares))
	}
	if input.Private != nil {
		write("farrow-private-contract", renderPrivateContractScript(*input.Private))
	}
	if input.Control {
		write("farrow-install-control-ssh", renderControlSSHInstallScript(input.SSHUser))
	}
	write("farrow-ready", ready)
	write("farrow-finalize", renderFinalizeScript(input.Control, input.Private != nil, len(input.Shares) != 0))
	out.WriteString("exec 9>&-\ntimeout --kill-after=5s 140s /usr/local/libexec/farrow-finalize")
	for _, stage := range stages {
		switch stage {
		case "hosts", "data-disks", "shares", "control-ssh", "private-network", "ready":
			out.WriteString(" " + stage)
		default:
			return "", fmt.Errorf("unknown guest setup stage %q", stage)
		}
	}
	out.WriteString("\n")
	return out.String(), nil
}
