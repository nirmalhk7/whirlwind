package process

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func kernelProcesses() ([]inspectedProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	processes := []inspectedProcess{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		process, owned, err := linuxProcess(pid)
		if err != nil {
			return nil, err
		}
		if owned {
			processes = append(processes, process)
		}
	}
	return processes, nil
}

func kernelIdentity(pid int) (Identity, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return Identity{}, err
	}
	return linuxIdentity(pid, string(raw))
}

func linuxIdentity(pid int, raw string) (Identity, error) {
	close := strings.LastIndex(raw, ")")
	if close < 0 {
		return Identity{}, errors.New("invalid kernel process stat")
	}
	fields := strings.Fields(raw[close+1:])
	if len(fields) < 20 {
		return Identity{}, errors.New("incomplete kernel process stat")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return Identity{}, err
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, Parent: parent, Group: group, Started: fields[19], Zombie: fields[0] == "Z" || fields[0] == "X"}, nil
}

func linuxProcess(pid int) (inspectedProcess, bool, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return inspectedProcess{}, false, nil
	}
	if err != nil {
		return inspectedProcess{}, false, err
	}
	return linuxProcessInfo(pid, dir, info)
}

func linuxProcessInfo(pid int, dir string, info os.FileInfo) (inspectedProcess, bool, error) {
	ownedUser := int(info.Sys().(*syscall.Stat_t).Uid) == os.Geteuid()
	identity, err := kernelIdentity(pid)
	if os.IsNotExist(err) {
		return inspectedProcess{}, false, nil
	}
	if err != nil {
		return inspectedProcess{}, false, err
	}
	process := inspectedProcess{Identity: identity}
	if identity.Zombie || !ownedUser {
		return process, true, nil
	}
	return linuxProcessEnvironment(pid, dir, identity)
}

func linuxProcessEnvironment(pid int, dir string, identity Identity) (inspectedProcess, bool, error) {
	process := inspectedProcess{Identity: identity}
	raw, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		current, lookup := kernelIdentity(pid)
		if os.IsNotExist(lookup) || current.Zombie || lookup == nil && current.Started != identity.Started {
			return inspectedProcess{}, false, nil
		}
		return inspectedProcess{}, false, inspectFailure(pid, err)
	}
	process.Token, process.Registry, err = processOwnership(strings.Split(string(raw), "\x00"))
	return process, true, err
}
