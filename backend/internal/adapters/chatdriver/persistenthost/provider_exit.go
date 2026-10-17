package persistenthost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// maxStderrTail bounds how much provider stderr a host retains. Only the end
// of a crash log is needed to name its cause.
const maxStderrTail = 8 << 10

// ProviderExit records how a hosted provider ended on its own. A daemon whose
// handshake the provider never answered reads it to explain the disconnect.
type ProviderExit struct {
	// State is the process state, such as "exit status 1" or "signal: abort trap".
	State      string    `json:"state"`
	StderrTail string    `json:"stderrTail,omitempty"`
	ExitedAt   time.Time `json:"exitedAt"`
}

// tailBuffer keeps the last maxStderrTail bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if excess := len(b.buf) - maxStderrTail; excess > 0 {
		b.buf = append(b.buf[:0], b.buf[excess:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func providerExitPath(dataDir, sessionID string) (string, error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "provider-exit.json"), nil
}

func writeProviderExit(dataDir, sessionID string, exit ProviderExit) error {
	path, err := providerExitPath(dataDir, sessionID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(exit)
	if err != nil {
		return err
	}
	tmp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func removeProviderExit(dataDir, sessionID string) {
	if path, err := providerExitPath(dataDir, sessionID); err == nil {
		_ = os.Remove(path)
	}
}

func readProviderExit(dataDir, sessionID string) (ProviderExit, bool) {
	path, err := providerExitPath(dataDir, sessionID)
	if err != nil {
		return ProviderExit{}, false
	}
	b, err := os.ReadFile(path) //nolint:gosec // AO-owned path derived from validated session id.
	if err != nil {
		return ProviderExit{}, false
	}
	var exit ProviderExit
	if json.Unmarshal(b, &exit) != nil || exit.State == "" {
		return ProviderExit{}, false
	}
	return exit, true
}

// WaitProviderExit returns the exit record of the provider most recently
// hosted for sessionID. The host publishes the record before it removes its
// descriptor, so a host that is gone without one has nothing to report. The
// wait covers a client that sees the disconnect before the host has reaped
// the provider.
func WaitProviderExit(ctx context.Context, dataDir, sessionID string) (ProviderExit, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exit, ok := readProviderExit(dataDir, sessionID); ok {
			return exit, true
		}
		if _, err := readDescriptor(dataDir, sessionID); errors.Is(err, os.ErrNotExist) {
			return readProviderExit(dataDir, sessionID)
		}
		select {
		case <-ctx.Done():
			return ProviderExit{}, false
		case <-ticker.C:
		}
	}
}
