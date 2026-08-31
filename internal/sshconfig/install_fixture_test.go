package sshconfig

func Install(home string, entry Entry) (Result, error) {
	return InstallMany(home, []Entry{entry})
}
