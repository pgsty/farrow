package platform

import "testing"

func TestProcessGroupAccessIncludesRootPrimaryAndSupplementaryGroups(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		euid, egid int
		groups     []int
		target     uint32
		wantAccess bool
	}{
		{name: "root", euid: 0, egid: 0, target: 100, wantAccess: true},
		{name: "primary", euid: 1000, egid: 100, target: 100, wantAccess: true},
		{name: "supplementary", euid: 1000, egid: 100, groups: []int{27, 999}, target: 999, wantAccess: true},
		{name: "unrelated", euid: 1000, egid: 100, groups: []int{27, 999}, target: 998, wantAccess: false},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := processInGroup(test.euid, test.egid, test.groups, test.target); got != test.wantAccess {
				t.Fatalf("processInGroup() = %t, want %t", got, test.wantAccess)
			}
		})
	}
}
