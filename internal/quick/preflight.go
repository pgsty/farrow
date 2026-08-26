package quick

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/hostshare"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

type qemuPreflightEvidence struct {
	Binary      string
	ShareDevice bool
}

func resolvedHasShares(resolved spec.Resolved) bool {
	return len(resolved.Nodes) != 0 && len(resolved.Nodes[0].Shares) != 0
}

func quickQEMUPreflight(ctx context.Context, profile platform.Profile, binary string, shares bool, runner execx.Runner) error {
	if binary == "" {
		return &CapabilityError{Reason: "QEMU invocation has no binary"}
	}
	if runner == nil {
		return &CapabilityError{Reason: "QEMU preflight requires a command runner"}
	}
	result, err := runner.Run(ctx, binary, "--version")
	if err != nil {
		return &CapabilityError{Reason: fmt.Sprintf("probe QEMU version: %v", err)}
	}
	if _, err := platform.ValidateQEMUVersion(profile, string(result.Stdout)+string(result.Stderr)); err != nil {
		return &CapabilityError{Reason: err.Error()}
	}
	if !shares {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err = runner.Run(ctx, binary, "-device", "help")
	if err != nil {
		return &CapabilityError{Reason: fmt.Sprintf("probe QEMU virtio-9p-pci support: %v", err)}
	}
	if err := qemu.ValidateShareDeviceHelp(string(result.Stdout) + "\n" + string(result.Stderr)); err != nil {
		return &CapabilityError{Reason: err.Error()}
	}
	return nil
}

func (m Manager) nativeProfile() (platform.Profile, error) {
	if m.NativeProfile != nil {
		return m.NativeProfile()
	}
	return platform.Native()
}

func (m Manager) lookPath(name string) (string, error) {
	if m.LookPath != nil {
		return m.LookPath(name)
	}
	return exec.LookPath(name)
}

func (m Manager) preflightQEMU(ctx context.Context, binary string, shares bool) (qemuPreflightEvidence, error) {
	profile, err := m.nativeProfile()
	if err != nil {
		return qemuPreflightEvidence{}, &CapabilityError{Reason: err.Error()}
	}
	if err := ctx.Err(); err != nil {
		return qemuPreflightEvidence{}, err
	}
	if err := quickQEMUPreflight(ctx, profile, binary, shares, m.runner()); err != nil {
		return qemuPreflightEvidence{}, err
	}
	return qemuPreflightEvidence{Binary: binary, ShareDevice: shares}, nil
}

func (m Manager) preflightNativeQEMU(ctx context.Context, profile platform.Profile, shares bool) (qemuPreflightEvidence, error) {
	if err := ctx.Err(); err != nil {
		return qemuPreflightEvidence{}, err
	}
	qemuPath, err := m.lookPath(profile.QEMUBinary)
	if err != nil {
		return qemuPreflightEvidence{}, &CapabilityError{Reason: fmt.Sprintf("locate QEMU binary: %v", err)}
	}
	if err := quickQEMUPreflight(ctx, profile, qemuPath, shares, m.runner()); err != nil {
		return qemuPreflightEvidence{}, err
	}
	return qemuPreflightEvidence{Binary: qemuPath, ShareDevice: shares}, nil
}

func validateQEMUPreflight(evidence qemuPreflightEvidence, invocation string, shares bool) error {
	if evidence.Binary == "" || invocation == "" || evidence.Binary != invocation {
		return &CapabilityError{Reason: "QEMU invocation binary changed after capability preflight"}
	}
	if shares && !evidence.ShareDevice {
		return &CapabilityError{Reason: "QEMU virtio-9p-pci support was not preflighted"}
	}
	return nil
}

// upNeedsShareCapability reads any existing spec without creating a project
// marker. A transition involving shares on either side requires device
// evidence before Up may stop or rewrite anything.
func (m Manager) upNeedsShareCapability(requested spec.Resolved, hasOverrides bool) (bool, error) {
	requestedShares := resolvedHasShares(requested)
	projectValue, err := m.openProject(false)
	if err != nil {
		if missingPath(err) {
			return requestedShares, nil
		}
		return false, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil {
		if missingPath(err) {
			return requestedShares, nil
		}
		return false, err
	}
	if !hasOverrides {
		return resolvedHasShares(projectState.Resolved), nil
	}
	return requestedShares || resolvedHasShares(projectState.Resolved), nil
}

// preflightExistingQEMU validates persisted typed state before executing its
// exact invocation binary. The returned evidence lets Restart's later launch
// avoid probing the same binary a second time.
func (m Manager) preflightExistingQEMU(ctx context.Context) (qemuPreflightEvidence, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return qemuPreflightEvidence{}, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), true)
	if err != nil {
		return qemuPreflightEvidence{}, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	projectState, node, err := readConsistent(store, nodeName)
	if err != nil {
		return qemuPreflightEvidence{}, err
	}
	if err := ensureNoPendingTransaction(store, nodeName); err != nil {
		return qemuPreflightEvidence{}, err
	}
	if err := hostshare.Validate(projectValue, projectState.Resolved.Nodes[0].Shares); err != nil {
		return qemuPreflightEvidence{}, err
	}
	return m.preflightQEMU(ctx, node.Invocation.Binary, resolvedHasShares(projectState.Resolved))
}
