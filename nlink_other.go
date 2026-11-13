//go:build !unix

package main

import "io/fs"

// links is how many names (hard links) a file has; 1 where splice can't tell.
func links(fs.FileInfo) uint64 { return 1 }
