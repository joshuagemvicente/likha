//go:build darwin || linux

package providers

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openPrivateFile(path string, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags = unix.O_RDWR | unix.O_CREAT | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	}
	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("credential storage must be a regular file")
	}
	var stat unix.Stat_t
	if err == nil {
		err = unix.Fstat(fd, &stat)
	}
	if err == nil && stat.Uid != uint32(os.Geteuid()) {
		err = fmt.Errorf("credential storage must be owned by the current user")
	}
	if err == nil && info.Mode().Perm() != 0600 {
		err = f.Chmod(0600)
	}
	if err != nil {
		f.Close()
		return nil, &os.PathError{Op: "protect", Path: path, Err: err}
	}
	return f, nil
}

func tryCredentialFileLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return err == nil, err
}
