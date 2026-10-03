//go:build !darwin && !linux

package actions

import "golang.org/x/sys/unix"

const targetedExclusiveRenameSupported = false

// Refuse rather than fall back to rename-over-existing on unsupported systems.
func targetedRenameExclusive(fd int, from, to string) error {
	return unix.ENOTSUP
}
