//go:build windows

package jcode

import "os"

// Windows directories carry ACLs rather than a uid; the per-user temp and AO
// data dirs are already private to the user.
func ownedByCurrentUser(os.FileInfo) bool { return true }
