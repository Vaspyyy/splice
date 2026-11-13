//go:build unix

package main

import (
	"io/fs"
	"syscall"
)

// links is how many names (hard links) a file has.
func links(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
