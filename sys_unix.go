//go:build unix

package main

import (
	"io/fs"
	"syscall"
)

// newMode is the mode of a new file: 0666 without the umask's bits, as
// any program creating a file would get.
func newMode() fs.FileMode {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return 0o666 &^ fs.FileMode(m)
}

// links is how many names (hard links) a file has.
func links(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
