package quick

import (
	"context"
	"testing"

	"github.com/pgsty/piglet/internal/spec"
)

func TestResolvedSSHUserAcceptsCustomAndFallsBackForOldState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "custom", input: "operator_1", want: "operator_1"},
		{name: "old state", want: "dba"},
		{name: "option injection", input: "-oProxyCommand=bad", wantErr: true},
		{name: "newline", input: "operator\nbad", wantErr: true},
		{name: "too long", input: "abcdefghijklmnopqrstuvwxyzabcdefg", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolvedSSHUser(test.input)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("resolvedSSHUser(%q) = %q, %v; want %q, error=%t", test.input, got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestConnectionStateUsesResolvedSSHUser(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "custom", input: "operator", want: "operator"},
		{name: "old state fallback", want: "dba"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReconcileFixture(t)
			projectState := fixture.projectState
			projectState.Resolved.SSHUser = test.input
			hash, err := spec.Hash(projectState.Resolved)
			if err != nil {
				t.Fatal(err)
			}
			projectState.SpecHash = hash
			node := fixture.node
			node.SpecHash = hash
			node.Invocation, err = buildInvocation(fixture.store.Project, projectState, node)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.WriteProject(projectState); err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.WriteNode(node); err != nil {
				t.Fatal(err)
			}

			connection, _, err := fixture.manager.connectionState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if connection.User != test.want {
				t.Fatalf("connection user = %q, want %q", connection.User, test.want)
			}
		})
	}
}

func TestStatusUsesResolvedSSHUser(t *testing.T) {
	fixture := newReconcileFixture(t)
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "custom", input: "operator", want: "operator"},
		{name: "old state fallback", want: "dba"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := statusFrom(fixture.store.Project, fixture.node, test.input, "test")
			if status.SSHUser != test.want {
				t.Fatalf("status SSH user = %q, want %q", status.SSHUser, test.want)
			}
		})
	}
}
