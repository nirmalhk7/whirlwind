package dashboard

import "golang.org/x/sys/unix"

const (
	tailGetTermios = unix.TCGETS
	tailSetTermios = unix.TCSETS
)
