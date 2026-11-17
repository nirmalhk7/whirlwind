package process

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestParityDarwinExitingProcessDoesNotBlockUnrelatedCleanup(t *testing.T) {
	var row unix.KinfoProc
	row.Proc.P_pid = 123
	row.Proc.P_stat = 1
	row.Proc.P_flag = 0x00002000 // P_WEXIT in the installed macOS SDK sys/proc.h.
	if !darwinIdentity(row).Zombie {
		t.Fatal("kernel-marked exiting process treated as runnable; procargs lookup blocks all cleanup")
	}
	row.Proc.P_flag = 0
	if darwinIdentity(row).Zombie {
		t.Fatal("live process hidden from ownership verification")
	}
}
