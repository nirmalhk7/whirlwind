//go:build darwin || linux

package process

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const registryEnvironment = "WHIRLWIND_PROCESS_REGISTRY"

var ErrCleanup = errors.New("process cleanup could not be verified")

// Identity comes from kernel process metadata. Arguments and environment values
// are deliberately excluded from reports so inspection cannot disclose secrets.
type Identity struct {
	PID     int    `json:"pid"`
	Parent  int    `json:"parent_pid"`
	Group   int    `json:"group_id"`
	Started string `json:"started"`
	Zombie  bool   `json:"zombie,omitempty"`
}

type inspectedProcess struct {
	Identity
	Token    string
	Registry string
}

type Report struct {
	Clean         bool       `json:"clean"`
	Registrations int        `json:"registrations"`
	Processes     []Identity `json:"processes"`
}

// IdentityAt returns the kernel identity for one process without exposing its arguments or environment.
func IdentityAt(pid int) (Identity, error) {
	if pid <= 1 {
		return Identity{}, errors.New("unsafe process identity")
	}
	return kernelIdentity(pid)
}

func validToken(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func processOwnership(environment []string) (string, string, error) {
	var token, registry string
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "WHIRLWIND_PROCESS_TOKEN":
			if token != "" {
				return "", "", errors.New("ambiguous process ownership token")
			}
			token = value
		case registryEnvironment:
			registry = value
		}
	}
	if !validToken(token) {
		token = ""
	}
	return token, registry, nil
}

func inspectFailure(pid int, err error) error {
	return fmt.Errorf("cannot inspect owned-user process %d: %w", pid, err)
}

// Processes may exit or exec while the kernel table is being read. Retry an
// incomplete census, but never turn an inspection error into an empty census.
func processSnapshot() ([]inspectedProcess, error) {
	deadline := time.Now().Add(time.Second)
	for {
		processes, err := kernelProcesses()
		if err == nil || !time.Now().Before(deadline) {
			return processes, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func liveProcesses(record registration, processes []inspectedProcess) []Identity {
	selected := selectOwnedProcesses(record, processes)
	includeDescendants(selected, processes)
	live := []Identity{}
	for _, process := range processes {
		if selected[process.PID] && !process.Zombie && process.PID != os.Getpid() {
			live = append(live, process.Identity)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].PID < live[j].PID })
	return live
}

func selectOwnedProcesses(record registration, processes []inspectedProcess) map[int]bool {
	rootReused := reusedProcessRoot(record, processes)
	selected := map[int]bool{}
	for _, process := range processes {
		owned := process.Token == record.Token
		// A live group retains its original leader's numeric identity. Never use
		// a reused PID as authority to kill a different process group.
		group := record.Started != "" && !rootReused && process.Group == record.PID
		if owned || group {
			selected[process.PID] = true
		}
	}
	return selected
}

func reusedProcessRoot(record registration, processes []inspectedProcess) bool {
	for _, process := range processes {
		if process.PID == record.PID && record.Started != "" && process.Started != record.Started {
			return true
		}
	}
	return false
}

func includeDescendants(selected map[int]bool, processes []inspectedProcess) {
	for changed := true; changed; {
		changed = false
		for _, process := range processes {
			if !selected[process.PID] && selected[process.Parent] {
				selected[process.PID], changed = true, true
			}
		}
	}
}
