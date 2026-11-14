//go:build !windows

package browserruntime

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
)

// Darwin's sockaddr_un.sun_path is 104 bytes including the trailing NUL. Keep
// the address at or below 103 bytes so the same path is valid on macOS and
// Linux regardless of the user's home/data-directory length.
const maxUnixSocketPathBytes = 103

var runtimeAliasPattern = regexp.MustCompile(`^ao-brd-(\d+)-[0-9a-f]{16}$`)

// Listen creates the local daemon-to-Electron browser bridge listener.
func Listen(runFilePath string) (net.Listener, string, error) {
	return ListenNamed(runFilePath, "browser")
}

// ListenNamed creates a browser bridge listener whose socket is named after
// the runtime it serves, so several runtimes can share one run directory.
func ListenNamed(runFilePath, name string) (net.Listener, string, error) {
	aliasRoot := os.TempDir()
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		// os.TempDir can inherit an arbitrarily long TMPDIR. /tmp is the stable,
		// short spelling needed for sockaddr_un (including on macOS, where it is
		// normally a symlink to /private/tmp).
		aliasRoot = "/tmp"
	}
	return listenUnix(runFilePath, aliasRoot, name+".sock")
}

func listenUnix(runFilePath, aliasRoot, socketName string) (net.Listener, string, error) {
	runtimeDir, err := filepath.Abs(filepath.Dir(runFilePath))
	if err != nil {
		return nil, "", fmt.Errorf("resolve browser runtime directory: %w", err)
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create browser runtime directory: %w", err)
	}
	cleanupStaleRuntimeAliases(aliasRoot, runtimeDir, unixProcessAlive)

	aliasPath, err := createRuntimeAlias(aliasRoot, runtimeDir)
	if err != nil {
		return nil, "", err
	}
	sockPath := filepath.Join(aliasPath, socketName)
	if len([]byte(sockPath)) > maxUnixSocketPathBytes {
		_ = os.Remove(aliasPath)
		return nil, "", fmt.Errorf("browser runtime socket path is %d bytes; maximum is %d", len([]byte(sockPath)), maxUnixSocketPathBytes)
	}
	_ = os.Remove(filepath.Join(runtimeDir, socketName))
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = os.Remove(aliasPath)
		return nil, "", err
	}
	wrapped := &cleanupUnixListener{Listener: ln, socketPath: sockPath, aliasPath: aliasPath}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = wrapped.Close()
		return nil, "", err
	}
	return wrapped, sockPath, nil
}

func createRuntimeAlias(root, runtimeDir string) (string, error) {
	for range 16 {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return "", fmt.Errorf("generate browser runtime alias: %w", err)
		}
		aliasPath := filepath.Join(root, fmt.Sprintf("ao-brd-%d-%s", os.Getpid(), hex.EncodeToString(random)))
		if err := os.Symlink(runtimeDir, aliasPath); err == nil {
			return aliasPath, nil
		} else if !os.IsExist(err) {
			return "", fmt.Errorf("create browser runtime alias: %w", err)
		}
	}
	return "", fmt.Errorf("create browser runtime alias: exhausted random names")
}

func cleanupStaleRuntimeAliases(root, runtimeDir string, processAlive func(int) bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		match := runtimeAliasPattern.FindStringSubmatch(entry.Name())
		if len(match) != 2 || entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid <= 0 || processAlive(pid) {
			continue
		}
		aliasPath := filepath.Join(root, entry.Name())
		target, err := os.Readlink(aliasPath)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		target, err = filepath.Abs(target)
		if err != nil || filepath.Clean(target) != runtimeDir {
			continue
		}
		_ = os.Remove(aliasPath)
	}
}

func unixProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

type cleanupUnixListener struct {
	net.Listener
	socketPath string
	aliasPath  string
	once       sync.Once
}

func (l *cleanupUnixListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() {
		_ = os.Remove(l.socketPath)
		_ = os.Remove(l.aliasPath)
	})
	return err
}
