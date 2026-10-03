package actions

import "golang.org/x/sys/unix"

const targetedExclusiveRenameSupported = true

func targetedRenameExclusive(fd int, from, to string) error {
	return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE)
}
