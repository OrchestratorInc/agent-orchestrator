package persistenthost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Packaged hosts avoid initializing the daemon's CLI, database, and services.
func hostExecutable(exe string, args []string) (string, []string, error) {
	name := filepath.Base(exe)
	if name != "ao" && name != "ao.exe" {
		return exe, args, nil
	}
	helper := filepath.Join(filepath.Dir(exe), "ao-chat-host"+filepath.Ext(name))
	info, err := os.Stat(helper)
	if errors.Is(err, os.ErrNotExist) {
		return exe, args, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read chat host executable: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return "", nil, fmt.Errorf("chat host is not executable: %s", helper)
	}
	return helper, args[1:], nil
}
