package settingsfile

import (
	"bytes"
	"errors"
	"os"
	"whirlwind/internal/store"
)

func (s *Source) persistDocument(doc Document, commit func() error) error {
	next, err := validatedEncoding(doc)
	if err != nil {
		return err
	}
	previous := append([]byte{}, s.raw...)
	if err = s.writeCurrent(next); err != nil {
		return err
	}
	if err = s.commitDocument(commit, next, previous); err != nil {
		return err
	}
	s.raw = next
	s.doc = doc
	return nil
}

func validatedEncoding(doc Document) ([]byte, error) {
	if err := validate(doc); err != nil {
		return nil, err
	}
	return encode(doc)
}

func (s *Source) writeCurrent(next []byte) error {
	current, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, s.raw) {
		return store.ErrConflict
	}
	return atomicWrite(s.path, next)
}

func (s *Source) commitDocument(commit func() error, next, previous []byte) error {
	if err := commit(); err != nil {
		current, readErr := os.ReadFile(s.path)
		if readErr == nil && bytes.Equal(current, next) {
			return errors.Join(err, atomicWrite(s.path, previous))
		}
		return err
	}
	return nil
}
