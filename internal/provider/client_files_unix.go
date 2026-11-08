//go:build darwin || linux

package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Open each path component relative to a pinned directory descriptor. No symbolic
// link may be followed after the caller's scope/permission decision, including
// links swapped into ancestors while permission approval is pending.
func openACPFile(root, relative string, write bool) (*os.File, error) {
	if !filepath.IsLocal(relative) || relative == "." {
		return nil, errors.New("ACP file requires a local regular path")
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	fd, err := openACPParent(root, parts[:len(parts)-1], write)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	return openACPLeaf(root, relative, parts[len(parts)-1], fd, write)
}

func openACPParent(root string, parts []string, write bool) (int, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range parts {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if write && errors.Is(err, unix.ENOENT) {
			if err = unix.Mkdirat(fd, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, err
			}
			next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, nil
}

func openACPLeaf(root, relative, leaf string, fd int, write bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if write {
		flags = unix.O_WRONLY | unix.O_CREAT | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	}
	fileFD, err := unix.Openat(fd, leaf, flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fileFD), filepath.Join(root, relative))
	var stat unix.Stat_t
	if err = unix.Fstat(fileFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || (write && stat.Nlink != 1) {
		_ = f.Close()
		return nil, errors.New("ACP requires a regular file without writable hardlink aliases")
	}
	if write {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}
