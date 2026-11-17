//go:build darwin

package process

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// macOS's /usr/bin/git is an xcrun shim that looks up the active developer
// directory on every call before git itself starts. The daemon runs git many
// times per turn, so it runs the binary the shim would pick.
var (
	findRealGit sync.Once
	realGit     atomic.Pointer[string]
)

func executable(name string) string {
	if name != "git" {
		return name
	}
	// Discovery runs once in the background so no caller waits for xcrun;
	// calls before it finishes use the shim.
	findRealGit.Do(func() {
		go func() {
			path := resolveRealGit()
			realGit.Store(&path)
		}()
	})
	if path := realGit.Load(); path != nil {
		return gitPath(name, *path)
	}
	return name
}

// resolveRealGit returns the binary the xcrun shim runs, or "" when git on PATH
// is not the shim or xcrun cannot find git.
func resolveRealGit() string {
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
}

// gitPath returns resolved while it exists. A removed or switched Xcode leaves the
// shim to find git again.
func gitPath(name, resolved string) string {
	if resolved != "" {
		if _, err := os.Stat(resolved); err == nil {
			return resolved
		}
	}
	return name
}
