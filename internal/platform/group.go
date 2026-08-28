package platform

import "os"

func processInGroup(euid, egid int, groups []int, target uint32) bool {
	if euid == 0 || (egid >= 0 && uint32(egid) == target) {
		return true
	}
	for _, gid := range groups {
		if gid >= 0 && uint32(gid) == target {
			return true
		}
	}
	return false
}

// CurrentProcessInGroup reports whether the current process can execute a
// root-owned, group-executable helper. Root is accepted because owner execute
// permission applies independently of its supplementary group list.
func CurrentProcessInGroup(target uint32) bool {
	groups, err := os.Getgroups()
	if err != nil {
		groups = nil
	}
	return processInGroup(os.Geteuid(), os.Getegid(), groups, target)
}
