//go:build !linux && !darwin

package main

import "io/fs"

// fileChangeID is empty where the platform reports no inode and status change
// time; a scan then compares size and modification time only.
func fileChangeID(fs.FileInfo) string {
	return ""
}
