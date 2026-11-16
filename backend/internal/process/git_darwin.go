//go:build darwin

package process

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// macOS's /usr/bin/git is an xcrun shim that looks up the active developer
// directory on every call before git itself starts. The daemon runs git many
// times per turn, so it runs the binary the shim would pick.
var realGit = sync.OnceValue(func() string {
	path, err := exec.LookPath("git")
	if err != nil || path != "/usr/bin/git" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/xcrun", "--find", "git").Output()
	if err != nil {
		return ""
	}
	if found := strings.TrimSpace(string(out)); found != path {
		return found
	}
	return ""
})

func executable(name string) string {
	if name != "git" {
		return name
	}
	// A removed or switched Xcode leaves the shim to find git again.
	if path := realGit(); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return name
}
