package quick

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

type Plan struct {
	ProjectExists bool           `json:"project_exists"`
	Action        string         `json:"action"`
	Destructive   bool           `json:"destructive"`
	Before        *spec.Resolved `json:"before,omitempty"`
	After         spec.Resolved  `json:"after"`
	SpecHash      string         `json:"spec_hash"`
}

func (m Manager) Plan(ctx context.Context, options Options) (Plan, error) {
	desired, err := options.Resolve()
	if err != nil {
		return Plan{}, err
	}
	return m.planDesired(ctx, desired, options.HasOverrides())
}

func (m Manager) PlanResolved(ctx context.Context, desired spec.Resolved) (Plan, error) {
	m.ConfiguredDataRoot = desired.DataRoot
	return m.planDesired(ctx, desired, true)
}

func (m Manager) planDesired(ctx context.Context, desired spec.Resolved, authoritative bool) (Plan, error) {
	_ = ctx
	if _, err := desired.SSHWaitTimeout(); err != nil {
		return Plan{}, err
	}
	workDir, err := m.workDir()
	if err != nil {
		return Plan{}, err
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			dataRoot, rootErr := project.ResolveDataRootWithConfig(workDir, m.ConfiguredDataRoot, nil)
			if rootErr != nil {
				return Plan{}, rootErr
			}
			desired.DataRoot = dataRoot
			hash, hashErr := spec.Hash(desired)
			return Plan{Action: "create", After: desired, SpecHash: hash}, hashErr
		}
		return Plan{}, err
	}
	desired.DataRoot = projectValue.DataRoot
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			hash, hashErr := spec.Hash(desired)
			return Plan{ProjectExists: true, Action: "create", After: desired, SpecHash: hash}, hashErr
		}
		return Plan{}, err
	}
	if !authoritative {
		desired = projectState.Resolved
	} else {
		desired = materializeExistingPorts(desired, projectState.Resolved)
	}
	action := "no-op"
	destructive := false
	if err := compareDesired(projectState.Resolved, desired); err != nil {
		var drift *DriftError
		if !errors.As(err, &drift) {
			return Plan{}, err
		}
		action = drift.Action
		destructive = strings.Contains(action, "destructive") || action == "recreate"
	}
	hash, err := spec.Hash(desired)
	if err != nil {
		return Plan{}, err
	}
	before := projectState.Resolved
	return Plan{ProjectExists: true, Action: action, Destructive: destructive, Before: &before, After: desired, SpecHash: hash}, nil
}

func (m Manager) Resolved() (spec.Resolved, error) {
	workDir, err := m.workDir()
	if err != nil {
		return spec.Resolved{}, err
	}
	projectValue, err := project.Open(workDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return spec.Quick(true, true), nil
		}
		return spec.Resolved{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return spec.Quick(true, true), nil
		}
		return spec.Resolved{}, err
	}
	return projectState.Resolved, nil
}
