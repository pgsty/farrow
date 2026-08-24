package process

import (
	"testing"

	"github.com/pgsty/piglet/internal/qemu"
)

func TestHashInvocationStable(t *testing.T) {
	t.Parallel()
	invocation := qemu.Invocation{Binary: "/opt/qemu", Args: []string{"-name", "meta"}}
	first, err := HashInvocation(invocation)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := HashInvocation(invocation)
	if first != second || len(first) != 64 {
		t.Fatalf("hashes = %q %q", first, second)
	}
	invocation.Args = append(invocation.Args, "-S")
	third, _ := HashInvocation(invocation)
	if third == first {
		t.Fatal("argv change did not change hash")
	}
}
