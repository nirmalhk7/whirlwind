package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const parityStateIdentity = "whirlwind-parity-v1\n"

func prepareFreshState(directory string) error {
	marker := filepath.Join(directory, "parity-v1.state")
	info, err := os.Lstat(marker)
	if err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("parity state marker must be a regular file")
		}
		content, err := os.ReadFile(marker)
		if err != nil {
			return err
		}
		if string(content) != parityStateIdentity {
			return errors.New("unrecognized application state identity")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(filepath.Join(directory, "whirlwind.db")); err == nil {
		return fmt.Errorf("state directory predates parity-v1; select a fresh directory such as %s; existing database remains untouched", filepath.Join(directory, "parity-v1"))
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return prepareFreshState(directory)
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(parityStateIdentity)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
