//go:build darwin || linux

package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Reap kills registered orphan process groups before removing their recovery records.
func Reap(root string) error {
	files, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		if err = reapRegistration(filepath.Join(root, file.Name())); err != nil {
			return err
		}
	}
	return nil
}
func reapRegistration(path string) error {
	registration, err := readRegistration(path)
	if err != nil {
		return err
	}
	groups, err := registeredGroups(registration.Token)
	if err != nil {
		return err
	}
	if err = killGroups(groups); err != nil {
		return err
	}
	return os.Remove(path)
}
func readRegistration(path string) (registration, error) {
	var record registration
	body, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if json.Unmarshal(body, &record) != nil || record.PID < 0 || record.PID == 1 || len(record.Token) != 32 {
		return record, errors.New("invalid orphan process registration")
	}
	return record, nil
}
func registeredGroups(token string) (map[int]bool, error) {
	output, err := exec.Command("ps", "eww", "-A", "-o", "pid=", "-o", "pgid=", "-o", "command=").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot reconcile process registry: %w", err)
	}
	groups := map[int]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		group, err := registeredGroup(line, token)
		if err != nil {
			return nil, err
		}
		if group != 0 {
			groups[group] = true
		}
	}
	return groups, nil
}
func registeredGroup(line, token string) (int, error) {
	if !strings.Contains(line, processTokenPrefix+token) {
		return 0, nil
	}
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, nil
	}
	group, err := strconv.Atoi(fields[1])
	if err != nil || group <= 1 {
		return 0, errors.New("invalid orphan process group")
	}
	return group, nil
}
func killGroups(groups map[int]bool) error {
	for group := range groups {
		if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
	}
	return nil
}
