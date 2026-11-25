//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package dashboard

import "golang.org/x/sys/unix"

const (
	tailGetTermios = unix.TIOCGETA
	tailSetTermios = unix.TIOCSETA
)
