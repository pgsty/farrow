package quick

import (
	"testing"

	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func TestQuickDiskRecordsPropagateResolvedFilesystem(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		configured string
		want       string
	}{
		{name: "legacy empty defaults to auto", want: "auto"},
		{name: "explicit auto", configured: "auto", want: "auto"},
		{name: "explicit xfs", configured: "xfs", want: "xfs"},
		{name: "explicit ext4", configured: "ext4", want: "ext4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := spec.Disk{Name: "data", Size: 64 * spec.GiB, Mount: "/data", Filesystem: test.configured, Persistent: true}
			persisted, cloud := quickDiskRecords(definition, "/managed/data.qcow2", "abcdefghijklmnopqrst")
			if persisted.RequestedFilesystem != test.want || persisted.ActualFilesystem != "" {
				t.Fatalf("persisted filesystem state = requested %q actual %q, want requested %q actual unknown", persisted.RequestedFilesystem, persisted.ActualFilesystem, test.want)
			}
			if cloud.Filesystem != test.want || cloud.Serial != persisted.Serial || cloud.Mount != persisted.Mount {
				t.Fatalf("cloud-init disk = %#v, persisted = %#v", cloud, persisted)
			}
		})
	}
}

func TestDesiredDataStatePreservesObservedFilesystem(t *testing.T) {
	t.Parallel()
	fixture := newReconcileFixture(t)
	desired := fixture.projectState.Resolved
	desired.Nodes[0].Disks[0].Size += spec.GiB
	current := fixture.node.DataDisks
	current[0].ActualFilesystem = "xfs"

	got, err := desiredDataState(fixture.store.Project, desired, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestedFilesystem != "auto" || got[0].ActualFilesystem != "xfs" {
		t.Fatalf("reconciled filesystem state = %#v", got)
	}
	if got[0].Persistent != current[0].Persistent || got[0].Path != current[0].Path {
		t.Fatalf("reconciled disk identity changed: before=%#v after=%#v", current[0], got[0])
	}
}

func TestDesiredDataStateLeavesLegacyActualFilesystemUnknown(t *testing.T) {
	t.Parallel()
	fixture := newReconcileFixture(t)
	current := append([]state.DataDisk(nil), fixture.node.DataDisks...)
	current[0].RequestedFilesystem = ""
	current[0].ActualFilesystem = ""

	got, err := desiredDataState(fixture.store.Project, fixture.projectState.Resolved, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestedFilesystem != "auto" || got[0].ActualFilesystem != "" {
		t.Fatalf("legacy filesystem state was not upgraded in memory safely: %#v", got)
	}
}
