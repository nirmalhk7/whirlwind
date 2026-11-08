package github

import (
	"errors"
	"testing"
)

func TestSSHUndefinedConnectionErrorIsTransient(t *testing.T) {
	if !IsTransient(errors.New("ssh: connect to host github.com port 22: Undefined error: 0\nfatal: Could not read from remote repository.")) {
		t.Fatal("observed SSH transport interruption was not retryable")
	}
	if IsTransient(errors.New("git@github.com: Permission denied (publickey).")) {
		t.Fatal("authentication failure became retryable")
	}
}
