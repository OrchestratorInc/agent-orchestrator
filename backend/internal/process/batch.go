package process

import (
	"path/filepath"
	"strings"
)

// IsWindowsBatchFile reports whether path names a Windows batch script. Windows
// cannot start a `.cmd` or `.bat` directly: CreateProcess hands the file to
// cmd.exe, which caps its `/c` input line near 8,191 characters. A program
// launched through a batch shim therefore tolerates a far shorter command line
// than the 32,767 characters CreateProcess allows for a native executable, and
// its arguments additionally pass through cmd.exe quoting.
func IsWindowsBatchFile(path string) bool {
	if path == "" {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}
