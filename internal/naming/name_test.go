package naming

import (
	"strings"
	"testing"
)

func TestValidNodeNameContract(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a", "meta", "node-1", strings.Repeat("a", 63)} {
		if !ValidNodeName(name) {
			t.Errorf("valid node name %q was rejected", name)
		}
	}
	for _, name := range []string{"", "Meta", "node_1", "-node", "node-", "node.example", "../node", strings.Repeat("a", 64)} {
		if ValidNodeName(name) {
			t.Errorf("invalid node name %q was accepted", name)
		}
	}
}
