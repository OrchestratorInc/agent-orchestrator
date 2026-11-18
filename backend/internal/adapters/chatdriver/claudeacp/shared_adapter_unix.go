//go:build unix

package claudeacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ensureSharedAdapter starts acp-server.mjs on socket unless one already
// listens there. The server runs in its own session so it outlives a daemon
// restart, and exits by itself once its last chat disconnects.
func ensureSharedAdapter(ctx context.Context, socket string, rt runtimeLaunch, env map[string]string) error {
	if adapterListening(socket) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(socket+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // AO-owned path under the data dir.
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	// Another launch may hold the lock while its server starts; wait for it
	// without ignoring ctx.
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if adapterListening(socket) {
		return nil
	}
	logFile, err := os.OpenFile(socket+".log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // AO-owned path under the data dir.
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	cmd := aoprocess.Command(rt.command, rt.server, socket) //nolint:gosec // packaged runtime paths.
	cmd.Dir = filepath.Dir(socket)
	cmd.Env = processenv.Merge(env)
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if adapterListening(socket) {
			return nil
		}
	}
	return errors.New("shared Claude adapter did not start listening")
}
