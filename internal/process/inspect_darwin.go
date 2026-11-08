package process

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func kernelProcesses() ([]inspectedProcess, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	processes := make([]inspectedProcess, 0, len(rows))
	for _, row := range rows {
		process := darwinIdentity(row)
		if !process.Zombie && int(row.Eproc.Ucred.Uid) == os.Geteuid() {
			process.Token, process.Registry, err = darwinOwnership(process.Identity)
			if err != nil {
				return nil, err
			}
		}
		processes = append(processes, process)
	}
	return processes, nil
}

func darwinIdentity(row unix.KinfoProc) inspectedProcess {
	return inspectedProcess{Identity: Identity{PID: int(row.Proc.P_pid), Parent: int(row.Eproc.Ppid), Group: int(row.Eproc.Pgid), Started: fmt.Sprintf("%d:%d", row.Proc.P_starttime.Sec, row.Proc.P_starttime.Usec), Zombie: row.Proc.P_stat == 5}}
}

func kernelIdentity(pid int) (Identity, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return Identity{}, err
	}
	if len(rows) == 0 {
		return Identity{}, os.ErrNotExist
	}
	if len(rows) != 1 {
		return Identity{}, errors.New("ambiguous kernel process identity")
	}
	return darwinIdentity(rows[0]).Identity, nil
}

func darwinOwnership(identity Identity) (string, string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", identity.PID)
	if err != nil {
		current, lookup := kernelIdentity(identity.PID)
		if os.IsNotExist(lookup) || errors.Is(lookup, unix.ESRCH) || current.Zombie || lookup == nil && current.Started != identity.Started {
			return "", "", nil
		}
		return "", "", inspectFailure(identity.PID, err)
	}
	environment, err := darwinEnvironment(raw)
	if err != nil {
		return "", "", inspectFailure(identity.PID, err)
	}
	return processOwnership(environment)
}

// KERN_PROCARGS2 is argc, executable path, NUL padding, argc NUL-terminated
// arguments, then NUL-terminated environment entries. Tokens in argv are data.
func darwinEnvironment(raw []byte) ([]string, error) {
	if len(raw) < 4 {
		return nil, errors.New("incomplete kernel process arguments")
	}
	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	if argc <= 0 {
		return nil, errors.New("invalid kernel argument count")
	}
	rest := raw[4:]
	index := bytes.IndexByte(rest, 0)
	if index < 0 {
		return nil, errors.New("incomplete kernel executable path")
	}
	rest = bytes.TrimLeft(rest[index+1:], "\x00")
	for range argc {
		index = bytes.IndexByte(rest, 0)
		if index < 0 {
			return nil, errors.New("incomplete kernel argument list")
		}
		rest = rest[index+1:]
	}
	environment := []string{}
	for _, value := range strings.Split(string(rest), "\x00") {
		if value == "" {
			break
		}
		environment = append(environment, value)
	}
	return environment, nil
}
