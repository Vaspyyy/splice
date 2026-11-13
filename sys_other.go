//go:build !unix

package main

import "io/fs"

// newMode is the mode of a new file.
func newMode() fs.FileMode { return 0o644 }

// links is how many names (hard links) a file has; 1 where splice can't tell.
func links(fs.FileInfo) uint64 { return 1 }
