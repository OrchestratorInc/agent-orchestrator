package notificationoutbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EpochFileName, inside the worker data directory, holds the epoch of the
// worker currently running there. A coding agent outlives its worker on a
// provider that keeps the agent across a wake (Freestyle), so the
// AO_CLOUD_WORKER_EPOCH in its environment is the epoch it was launched under,
// not the current one. Its hooks read this file instead, so the events they
// queue are stamped for the worker that will deliver them.
const EpochFileName = "worker-epoch"

// WriteWorkerEpoch records epoch as the current worker's, atomically.
func WriteWorkerEpoch(dataDir string, epoch int64) error {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" || epoch <= 0 {
		return fmt.Errorf("worker epoch file needs a data directory and a positive epoch")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create worker data directory: %w", err)
	}
	temporary, err := os.CreateTemp(dataDir, "."+EpochFileName+"-*")
	if err != nil {
		return fmt.Errorf("create worker epoch file: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(strconv.FormatInt(epoch, 10) + "\n"); err != nil {
		temporary.Close()
		return fmt.Errorf("write worker epoch file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write worker epoch file: %w", err)
	}
	// Readable by the agent, which may run as another user in the same group.
	if err := os.Chmod(temporary.Name(), 0o644); err != nil {
		return fmt.Errorf("secure worker epoch file: %w", err)
	}
	if err := os.Rename(temporary.Name(), filepath.Join(dataDir, EpochFileName)); err != nil {
		return fmt.Errorf("install worker epoch file: %w", err)
	}
	return nil
}

// CurrentWorkerEpoch returns the epoch recorded in dataDir, or fallback when
// none is recorded (an older worker that does not write the file).
func CurrentWorkerEpoch(dataDir string, fallback int64) int64 {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return fallback
	}
	data, err := os.ReadFile(filepath.Join(dataDir, EpochFileName))
	if err != nil {
		return fallback
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || epoch <= 0 {
		return fallback
	}
	return epoch
}
