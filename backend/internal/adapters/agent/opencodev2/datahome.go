package opencodev2

import (
	"os"
	"path/filepath"
	"strings"
)

const dataHomeDirName = "opencode-v2-home"

// DataHome returns the XDG_DATA_HOME OpenCode 2 is launched with. OpenCode 1 and
// 2 would otherwise write the same <data home>/opencode/opencode.db, so v2 gets
// a sibling home next to the user's own data home; v2 then stores its database
// and sessions under <data home>/opencode-v2-home/opencode. OpenCode 1 keeps the
// user's existing location untouched.
func DataHome() (string, bool) {
	parent := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if filepath.Base(parent) == dataHomeDirName {
		return parent, true
	}
	if parent == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		parent = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(parent, dataHomeDirName), true
}
