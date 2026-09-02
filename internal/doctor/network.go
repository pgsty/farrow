package doctor

import (
	"context"
	"fmt"

	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/state"
)

func readableNetworkInstallation(status string) bool {
	return status == "exact" || status == "protected"
}

// networkProbeAddresses returns addresses whose current use would be a real
// eligibility conflict. Addresses reserved by the applied deployment are not
// candidates: a running deployment is expected to answer on them, while
// lifecycle start/up performs its own stricter check before reusing a stopped
// or newly added node address.
func networkProbeAddresses(layout subnet.Layout) []string {
	addresses := layout.StaticAddresses()
	dataRoot, err := state.ResolveDataRoot()
	if err != nil {
		return addresses
	}
	deployment, err := (state.Store{Root: dataRoot}).ReadDeployment()
	if err != nil || deployment.Resolved.Network != "private" || deployment.Resolved.Private == nil || deployment.Resolved.Private.CIDR != layout.CIDR() {
		return addresses
	}
	reserved := make(map[string]struct{}, len(deployment.Resolved.Nodes))
	for _, node := range deployment.Resolved.Nodes {
		reserved[node.Address] = struct{}{}
	}
	eligible := make([]string, 0, len(addresses)-len(reserved))
	for _, address := range addresses {
		if _, exists := reserved[address]; !exists {
			eligible = append(eligible, address)
		}
	}
	return eligible
}

func (p Probe) networkPreflightChecks(ctx context.Context, profile platform.Profile) []Check {
	layout := subnet.Default()
	report := netpreflight.Run(ctx, netpreflight.Request{OS: profile.OS, Arch: profile.Arch, Purpose: netpreflight.Inspect, Layout: layout, Addresses: networkProbeAddresses(layout)}, netpreflight.Probe{Runner: p.runner()})
	if readableNetworkInstallation(report.Installation.Status) && report.Installation.CIDR != "" && report.Installation.CIDR != layout.CIDR() {
		if installed, err := subnet.Parse(report.Installation.CIDR); err == nil {
			report = netpreflight.Run(ctx, netpreflight.Request{OS: profile.OS, Arch: profile.Arch, Purpose: netpreflight.Inspect, Layout: installed, Addresses: networkProbeAddresses(installed)}, netpreflight.Probe{Runner: p.runner()})
		}
	}
	checks := make([]Check, 0, len(report.Findings)+1)
	if len(report.Findings) == 0 {
		checks = append(checks, Check{Name: "network-preflight", Status: OK, Evidence: fmt.Sprintf("%s ready (%s mode on %s)", report.CIDR, report.Installation.Mode, report.Installation.Interface)})
		return checks
	}
	for _, finding := range report.Findings {
		status := Warn
		if finding.Severity == netpreflight.Error {
			status = Error
		} else if finding.Severity == netpreflight.OK {
			status = OK
		}
		checks = append(checks, Check{Name: "network-" + finding.Code, Status: status, Evidence: finding.Evidence, Fix: finding.Fix})
	}
	return checks
}
