package ptyregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Entry struct {
	SessionID    string `json:"sessionId"`
	PtyHostPID   int    `json:"ptyHostPid"`
	PipePath     string `json:"pipePath"`
	LaunchID     string `json:"launchId,omitempty"`
	RegisteredAt string `json:"registeredAt"`
}

var pidAlive = defaultPidAlive

var rewriteRegistry = writeRaw

var ErrRegistryMalformed = errors.New("conpty pty registry malformed")

const UnresolvedPipePath = "ao-conpty://startup-unresolved"

var overrideDir string

var registryMu sync.Mutex

func SetRunFilePath(path string) {
	if path == "" {
		overrideDir = ""
		return
	}
	overrideDir = filepath.Dir(path)
}

func registryFile() (string, error) {
	if overrideDir != "" {
		return filepath.Join(overrideDir, "windows-pty-hosts.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ao", "windows-pty-hosts.json"), nil
}

func readRaw(ctx context.Context) ([]Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	path, err := registryFile()
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, err
	}
	var parsed []json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrRegistryMalformed, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	out := make([]Entry, 0, len(parsed))
	for _, raw := range parsed {
		if err := ctx.Err(); err != nil {
			return out, false, err
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return out, false, fmt.Errorf("%w: %w", ErrRegistryMalformed, err)
		}
		if e.SessionID == "" || e.PtyHostPID < 0 || e.PipePath == "" || (e.PtyHostPID == 0 && e.PipePath != UnresolvedPipePath) {
			return out, false, fmt.Errorf("%w: entry has invalid sessionId, ptyHostPid, or pipePath", ErrRegistryMalformed)
		}
		out = append(out, e)
	}
	return out, true, nil
}

func writeRaw(ctx context.Context, entries []Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := registryFile()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(entries) == 0 {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "pty-hosts-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := ctx.Err(); err != nil {
		_ = tmp.Close()
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func Register(ctx context.Context, entry Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	all, complete, err := scanLocked(ctx)
	if err != nil || !complete {
		return errors.Join(err, errors.New("conpty pty registry scan incomplete"))
	}
	next := make([]Entry, 0)
	for _, e := range all {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.SessionID != entry.SessionID {
			next = append(next, e)
		}
	}
	next = append(next, entry)
	return writeRaw(ctx, next)
}

func Unregister(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	all, complete, err := scanLocked(ctx)
	if err != nil || !complete {
		return errors.Join(err, errors.New("conpty pty registry scan incomplete"))
	}
	next := make([]Entry, 0, len(all))
	for _, e := range all {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.SessionID != sessionID {
			next = append(next, e)
		}
	}
	if len(next) == len(all) {
		return nil
	}
	return writeRaw(ctx, next)
}

func Scan(ctx context.Context) (entries []Entry, complete bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return scanLocked(ctx)
}

func scanLocked(ctx context.Context) (entries []Entry, complete bool, err error) {
	all, complete, err := readRaw(ctx)
	if err != nil || !complete {
		return all, false, err
	}
	live := make([]Entry, 0, len(all))
	for _, e := range all {
		if err := ctx.Err(); err != nil {
			return live, false, err
		}
		if e.PtyHostPID == 0 && e.PipePath == UnresolvedPipePath {
			live = append(live, e)
		} else if pidAlive(e.PtyHostPID) {
			live = append(live, e)
		}
		if err := ctx.Err(); err != nil {
			return live, false, err
		}
	}
	if len(live) != len(all) {
		if err := ctx.Err(); err != nil {
			return live, false, err
		}
		if err := rewriteRegistry(ctx, live); err != nil {
			return live, false, err
		}
		if err := ctx.Err(); err != nil {
			return live, false, err
		}
	}
	return live, true, nil
}

func List(ctx context.Context) ([]Entry, error) {
	entries, _, err := Scan(ctx)
	return entries, err
}

func Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeRaw(ctx, nil)
}
