package runfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// Takeover reclaims the exact daemon record observed by the desktop. Unknown
// ownership blocks replacement; a reused PID permits metadata cleanup only.
func Takeover(ctx context.Context, path, expectedSHA256 string) error {
	return takeover(ctx, path, expectedSHA256, processalive.CheckOwnership, signalDaemon)
}

func takeover(ctx context.Context, path, expectedSHA256 string, check func(context.Context, int, string) processalive.Ownership, signal func(int) error) error {
	if digest, err := hex.DecodeString(expectedSHA256); err != nil || len(digest) != sha256.Size {
		return errors.New("daemon takeover requires the observed run-file SHA-256")
	}
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	contents, err := readExpected(path, expectedSHA256)
	if err != nil {
		return err
	}
	var info Info
	if err := json.Unmarshal(contents, &info); err != nil || info.PID <= 0 {
		return errors.New("daemon owner record is invalid; replacement blocked")
	}
	switch check(ctx, info.PID, info.ProcessStartID) {
	case processalive.OwnershipGone, processalive.OwnershipReused:
		return removeExpected(path, expectedSHA256)
	case processalive.OwnershipMatching:
		// Signal only the daemon. Its detached session hosts must survive restart.
		if err := signal(info.PID); err != nil {
			ownership := check(ctx, info.PID, info.ProcessStartID)
			if ownership == processalive.OwnershipGone || ownership == processalive.OwnershipReused {
				return removeExpected(path, expectedSHA256)
			}
			return fmt.Errorf("signal verified daemon: %w", err)
		}
	default:
		return errors.New("daemon process ownership is unknown; replacement blocked")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for verified daemon to stop: %w", ctx.Err())
		case <-ticker.C:
			ownership := check(ctx, info.PID, info.ProcessStartID)
			if ownership == processalive.OwnershipGone || ownership == processalive.OwnershipReused {
				return removeExpected(path, expectedSHA256)
			}
			// A transient exit probe failure is not proof of death; retry until the deadline.
		}
	}
}

func readExpected(path, expectedSHA256 string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read observed daemon owner: %w", err)
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, errors.New("daemon owner changed; replacement blocked")
	}
	return contents, nil
}

func removeExpected(path, expectedSHA256 string) error {
	if _, err := readExpected(path, expectedSHA256); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return remove(path)
}
