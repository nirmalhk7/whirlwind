package process

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	identity, err := kernelIdentity(pid)
	if os.IsNotExist(err) {
		return inspectedProcess{}, false, nil
	}
	if err != nil {
		return inspectedProcess{}, false, err
	}
	process := inspectedProcess{Identity: identity}
	if identity.Zombie || pid <= 1 {
		return process, true, nil
	}
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return linuxUnreadableOwnership(identity, err)
	}
	// Linux may change the owner of /proc/<pid> when a same-user process
	// becomes non-dumpable. Its kernel credential row still reports its UIDs.
	ownedUser, err := linuxProcessUser(string(status), os.Geteuid())
	if err != nil || !ownedUser {
		return process, true, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return linuxUnreadableOwnership(identity, err)
	}
	current, err := kernelIdentity(pid)
	if os.IsNotExist(err) || current.Zombie || err == nil && current.Started != identity.Started {
		return inspectedProcess{}, false, nil
	}
	if err != nil {
		return inspectedProcess{}, false, err
	}
	process.Identity = current
	process.Token, process.Registry, err = processOwnership(strings.Split(string(raw), "\x00"))
	return process, true, err
}

func linuxProcessUser(status string, uid int) (bool, error) {
	for line := range strings.SplitSeq(status, "\n") {
		if raw, ok := strings.CutPrefix(line, "Uid:"); ok {
			fields := strings.Fields(raw)
			if len(fields) != 4 {
				break
			}
			var match bool
			for _, value := range fields {
				id, err := strconv.Atoi(value)
				if err != nil || id < 0 {
					return false, errors.New("invalid kernel process UID")
				}
				match = match || id == uid
			}
			return match, nil
		}
	}
	return false, errors.New("missing kernel process UIDs")
}

func linuxUnreadableOwnership(identity Identity, err error) (inspectedProcess, bool, error) {
	current, lookup := kernelIdentity(identity.PID)
	if os.IsNotExist(lookup) || current.Zombie || lookup == nil && current.Started != identity.Started {
		return inspectedProcess{}, false, nil
	}
	if lookup == nil && errors.Is(err, os.ErrPermission) {
		return inspectedProcess{Identity: current, OwnershipErr: inspectFailure(identity.PID, err)}, true, nil
	}
	return inspectedProcess{}, false, inspectFailure(identity.PID, err)
}
