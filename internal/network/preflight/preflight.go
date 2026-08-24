// Package preflight provides one typed private-network eligibility report used
// by the standalone CLI, installers, and lifecycle gates.
package preflight

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/pgsty/piglet/internal/network/subnet"
)

type Purpose string

const (
	Inspect Purpose = "inspect"
	Install Purpose = "install"
	Use     Purpose = "use"
)

type Severity string

const (
	OK      Severity = "ok"
	Warning Severity = "warning"
	Error   Severity = "error"
)

type ErrorClass string

const (
	Capability ErrorClass = "capability"
	State      ErrorClass = "state"
	Resource   ErrorClass = "resource"
	Integrity  ErrorClass = "integrity"
)

type Finding struct {
	Code     string     `json:"code"`
	Severity Severity   `json:"severity"`
	Class    ErrorClass `json:"class,omitempty"`
	Subject  string     `json:"subject,omitempty"`
	Evidence string     `json:"evidence"`
	Fix      string     `json:"fix,omitempty"`
}

type Installation struct {
	Status      string `json:"status"`
	Mode        string `json:"mode,omitempty"`
	Family      string `json:"family,omitempty"`
	CIDR        string `json:"cidr,omitempty"`
	HostAddress string `json:"host_address,omitempty"`
	Interface   string `json:"interface,omitempty"`
	HelperPath  string `json:"helper_path,omitempty"`
	Healthy     bool   `json:"healthy"`
	Problem     string `json:"problem,omitempty"`
}

type Route struct {
	Prefix    netip.Prefix
	Interface string
	Kind      string
	Evidence  string
}

type InterfaceAddress struct {
	Address   netip.Addr
	Prefix    netip.Prefix
	Interface string
	Evidence  string
}

type Snapshot struct {
	Installation Installation
	Routes       []Route
	Interfaces   []InterfaceAddress
	Addresses    map[string]string
	SharingBusy  string
	Problems     []string
}

type Request struct {
	OS        string
	Arch      string
	Purpose   Purpose
	Layout    subnet.Layout
	Addresses []string
}

type Report struct {
	Schema       int          `json:"schema"`
	OS           string       `json:"os"`
	Arch         string       `json:"arch"`
	Purpose      Purpose      `json:"purpose"`
	CIDR         string       `json:"cidr"`
	HostAddress  string       `json:"host_address"`
	DHCPEnd      string       `json:"dhcp_end"`
	Default      bool         `json:"default"`
	Installation Installation `json:"installation"`
	Findings     []Finding    `json:"findings"`
	Ready        bool         `json:"ready"`
	ExitCode     int          `json:"exit_code"`
}

func overlap(left, right netip.Prefix) bool {
	return left.IsValid() && right.IsValid() && (left.Contains(right.Addr()) || right.Contains(left.Addr()))
}

func add(report *Report, finding Finding) {
	report.Findings = append(report.Findings, finding)
}

func Evaluate(request Request, snapshot Snapshot) Report {
	report := Report{
		Schema: 1, OS: request.OS, Arch: request.Arch, Purpose: request.Purpose,
		CIDR: request.Layout.CIDR(), HostAddress: request.Layout.HostAddress(), DHCPEnd: request.Layout.DHCPEnd(), Default: request.Layout.IsDefault(),
		Installation: snapshot.Installation,
		Findings:     make([]Finding, 0),
	}
	if warning := request.Layout.Warning(); warning != "" {
		add(&report, Finding{Code: "network.non_default", Severity: Warning, Evidence: warning, Fix: "keep the installed network, every project spec, and every node on this same /24"})
	}
	for _, problem := range snapshot.Problems {
		add(&report, Finding{Code: "probe.incomplete", Severity: Error, Class: Capability, Evidence: problem, Fix: "restore the required route/interface inspection tools and retry"})
	}
	if snapshot.SharingBusy != "" {
		add(&report, Finding{Code: "vmnet.sharing_service_busy", Severity: Error, Class: Resource, Evidence: snapshot.SharingBusy, Fix: "stop the conflicting VM/sharing service or choose one explicit non-conflicting global /24"})
	}
	installed := snapshot.Installation.Status != "" && snapshot.Installation.Status != "absent"
	installedExact := installed && snapshot.Installation.CIDR == request.Layout.CIDR()
	if snapshot.Installation.Status == "partial" || snapshot.Installation.Status == "invalid" {
		add(&report, Finding{Code: "installation.integrity", Severity: Error, Class: Integrity, Evidence: snapshot.Installation.Problem, Fix: "inspect the exact Piglet ownership manifest; do not adopt or broadly delete unknown paths"})
	} else if installed {
		if snapshot.Installation.CIDR != request.Layout.CIDR() {
			add(&report, Finding{Code: "installation.network_mismatch", Severity: Error, Class: State, Evidence: fmt.Sprintf("installed=%s requested=%s", snapshot.Installation.CIDR, request.Layout.CIDR()), Fix: "stop all private projects, uninstall the owned network, then install the requested global /24"})
		}
		if snapshot.Installation.CIDR == request.Layout.CIDR() && !snapshot.Installation.Healthy {
			add(&report, Finding{Code: "installation.not_ready", Severity: Error, Class: Capability, Evidence: snapshot.Installation.Problem, Fix: "repair or reinstall the owned backend; a listening socket without the host address/route is not ready"})
		}
	} else if request.Purpose == Use {
		add(&report, Finding{Code: "installation.absent", Severity: Error, Class: Capability, Evidence: "private network backend is not installed", Fix: "run piglet network preflight, then network install after reviewing its privileged plan"})
	} else if request.Purpose == Inspect {
		add(&report, Finding{Code: "installation.absent", Severity: Warning, Evidence: "private network backend is not installed; the requested subnet can still be checked for eligibility"})
	}

	exactOwnedRoute := false
	for _, route := range snapshot.Routes {
		if !overlap(route.Prefix, request.Layout.Prefix()) {
			continue
		}
		host, _ := netip.ParseAddr(request.Layout.HostAddress())
		localHostRoute := installedExact && route.Interface == "lo0" && route.Prefix.Bits() == 32 && route.Prefix.Contains(host)
		if request.OS == "linux" {
			linuxLocal := route.Kind == "local" && (route.Interface == "lo" || route.Interface == snapshot.Installation.Interface)
			fixtureLocal := route.Kind == "" && route.Interface == "lo"
			localHostRoute = installedExact && route.Prefix.Bits() == 32 && route.Prefix.Contains(host) && (linuxLocal || fixtureLocal)
		}
		connectedKind := route.Kind == "" || route.Kind == "unicast" || route.Kind == "route"
		connected := installedExact && connectedKind && snapshot.Installation.Interface != "" && route.Interface == snapshot.Installation.Interface && route.Prefix.Bits() == request.Layout.Prefix().Bits() && route.Prefix.Masked() == request.Layout.Prefix()
		if connected {
			exactOwnedRoute = true
		}
		owned := localHostRoute || connected
		if !owned {
			add(&report, Finding{Code: "route.overlap", Severity: Error, Class: Resource, Subject: route.Interface, Evidence: route.Evidence, Fix: "remove the conflicting VPN/VM/route or choose one explicit non-conflicting global /24"})
		}
	}
	if installedExact && !exactOwnedRoute {
		add(&report, Finding{Code: "installation.route_missing", Severity: Error, Class: Capability, Subject: snapshot.Installation.Interface, Evidence: fmt.Sprintf("installed backend has no exact owned route for %s on %s", request.Layout.CIDR(), snapshot.Installation.Interface), Fix: "repair or reinstall the owned backend before starting private VMs"})
	}
	for _, address := range snapshot.Interfaces {
		if !overlap(address.Prefix, request.Layout.Prefix()) {
			continue
		}
		host, _ := netip.ParseAddr(request.Layout.HostAddress())
		owned := installedExact && snapshot.Installation.Interface != "" && address.Interface == snapshot.Installation.Interface && address.Address == host && address.Prefix.Bits() == request.Layout.Prefix().Bits() && address.Prefix.Masked() == request.Layout.Prefix()
		if !owned {
			add(&report, Finding{Code: "interface.overlap", Severity: Error, Class: Resource, Subject: address.Interface, Evidence: address.Evidence, Fix: "remove the conflicting virtual/VPN interface or choose one explicit non-conflicting global /24"})
		}
	}
	for _, address := range request.Addresses {
		if evidence := snapshot.Addresses[address]; evidence != "" {
			add(&report, Finding{Code: "address.in_use", Severity: Error, Class: Resource, Subject: address, Evidence: evidence, Fix: "stop the conflicting VM/service or select another static suffix before creating project state"})
		}
	}

	sort.SliceStable(report.Findings, func(i, j int) bool {
		if report.Findings[i].Code == report.Findings[j].Code {
			return report.Findings[i].Subject < report.Findings[j].Subject
		}
		return report.Findings[i].Code < report.Findings[j].Code
	})
	report.Ready = true
	priority := map[ErrorClass]int{Capability: 3, State: 4, Resource: 6, Integrity: 7}
	for _, finding := range report.Findings {
		if finding.Severity != Error {
			continue
		}
		report.Ready = false
		if priority[finding.Class] > report.ExitCode {
			report.ExitCode = priority[finding.Class]
		}
	}
	return report
}
