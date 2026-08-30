package process

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

func processStarted(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", fmt.Errorf("read Linux process start identity: %w", err)
	}
	return parseProcStatStart(data)
}

func processArgv(pid int) ([]string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, fmt.Errorf("read Linux process argv: %w", err)
	}
	return parseNULArgv(data)
}

func parseNULArgv(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, errors.New("process argv bytes are empty")
	}
	if data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	parts := bytes.Split(data, []byte{0})
	argv := make([]string, len(parts))
	for index, part := range parts {
		argv[index] = string(part)
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("process argv has no executable")
	}
	return argv, nil
}
