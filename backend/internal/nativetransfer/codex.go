package nativetransfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
)

// HarnessCodex is the harness id for Codex.
const HarnessCodex = "codex"

// ErrSegmented reports a Codex thread continued in segment files
// (rollout-<time>-<id>_<segment>.jsonl). A move carries one rollout file, so
// it refuses these rather than drop the later turns.
var ErrSegmented = errors.New("nativetransfer: the Codex conversation continues in segment files, which a move does not carry")

// CodexOptions selects a Codex conversation.
type CodexOptions struct {
	// Home is Codex's home folder (CODEX_HOME, or ~/.codex).
	Home     string
	NativeID string
}

// CodexRollout finds the conversation's rollout under <home>/sessions, plain
// or zstd-compressed as Codex leaves older ones, and returns its path,
// canonical id and size on disk.
func CodexRollout(ctx context.Context, opts CodexOptions) (path, id string, size int64, err error) {
	if id, err = canonicalUUID(opts.NativeID); err != nil {
		return "", "", 0, err
	}
	if path, err = findCodexRollout(ctx, opts.Home, id); err != nil {
		return "", "", 0, err
	}
	if path == "" {
		return "", "", 0, fmt.Errorf("nativetransfer: no Codex rollout for %s: %w", id, fs.ErrNotExist)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", 0, err
	}
	return path, id, info.Size(), nil
}

// WriteCodexRolloutForCloud writes the conversation's rollout, uncompressed,
// with its working directory replaced by CloudCwdPlaceholder, and returns its
// id. The agent must not be running.
func WriteCodexRolloutForCloud(ctx context.Context, w io.Writer, opts CodexOptions, maxBytes int64) (string, error) {
	path, id, _, err := CodexRollout(ctx, opts)
	if err != nil {
		return "", err
	}
	data, err := readCodexRollout(path, maxBytes)
	if err != nil {
		return "", err
	}
	meta, err := codexSessionMeta(data)
	if err != nil {
		return "", err
	}
	if meta.ID != id {
		return "", fmt.Errorf("nativetransfer: rollout %s records conversation %s", path, meta.ID)
	}
	paths, err := cloudPaths(map[string]string{meta.Cwd: CloudCwdPlaceholder})
	if err != nil {
		return "", err
	}
	if _, err := rewriteJSONLines(w, bytes.NewReader(data), paths); err != nil {
		return "", err
	}
	return id, nil
}

// CodexImportOptions says where to place a moved Codex conversation.
type CodexImportOptions struct {
	Home string
	// Cwd is the working directory the agent resumes in.
	Cwd string
}

// ImportCodex places the rollout at rolloutPath, from AO Cloud, under
// <home>/sessions with its working directory replaced by opts.Cwd, and
// returns its id. A rollout this machine already has for the conversation is
// replaced at its path, the one Codex's thread index names. Otherwise it gets
// the dated path and file name Codex gives a new rollout: Codex refuses to
// resume any other name.
func ImportCodex(ctx context.Context, rolloutPath string, opts CodexImportOptions) (string, error) {
	data, err := os.ReadFile(rolloutPath)
	if err != nil {
		return "", err
	}
	meta, err := codexSessionMeta(data)
	if err != nil {
		return "", err
	}
	id, err := canonicalUUID(meta.ID)
	if err != nil {
		return "", err
	}
	cwd, err := realDir(opts.Cwd)
	if err != nil {
		return "", err
	}
	paths, err := newPathMap(map[string]string{meta.Cwd: cwd})
	if err != nil {
		return "", err
	}
	target, err := findCodexRollout(ctx, opts.Home, id)
	if err != nil {
		return "", err
	}
	target = strings.TrimSuffix(target, ".zst")
	if target == "" {
		started, err := time.Parse(time.RFC3339Nano, meta.Timestamp)
		if err != nil {
			started = time.Now()
		}
		started = started.UTC()
		target = filepath.Join(opts.Home, "sessions", started.Format("2006/01/02"),
			"rollout-"+started.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(target), ".nativetransfer-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(out.Name()) }()
	_, err = rewriteJSONLines(out, bytes.NewReader(data), paths)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(out.Name(), target)
	}
	if err != nil {
		return "", fmt.Errorf("nativetransfer: place Codex rollout: %w", err)
	}
	// Codex reads a compressed copy beside the path its index names, so an
	// older compressed copy would shadow the one just placed.
	if err := os.Remove(target + ".zst"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return id, nil
}

type codexMeta struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

// codexSessionMeta reads the session_meta record a rollout starts with.
func codexSessionMeta(data []byte) (codexMeta, error) {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	var record struct {
		Type    string    `json:"type"`
		Payload codexMeta `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil || record.Type != "session_meta" {
		return codexMeta{}, errors.New("nativetransfer: the rollout does not start with its session_meta record")
	}
	if record.Payload.Cwd == "" {
		return codexMeta{}, errors.New("nativetransfer: rollout records no working directory")
	}
	return record.Payload, nil
}

// findCodexRollout returns the path of id's rollout under <home>/sessions,
// preferring a plain one, or "" when there is none. A thread with segment
// files is ErrSegmented.
func findCodexRollout(ctx context.Context, home, id string) (string, error) {
	sessions := filepath.Join(home, "sessions")
	// An account home links its sessions to the computer's own; WalkDir does
	// not descend into a root that is itself a link.
	if resolved, err := filepath.EvalSymlinks(sessions); err == nil {
		sessions = resolved
	}
	var plain, compressed string
	segmented := false
	err := filepath.WalkDir(sessions, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() || nativeconfig.CodexRolloutThread(p) != id {
			return nil
		}
		switch name := entry.Name(); {
		case strings.Contains(name, id+"_"):
			segmented = true
			return fs.SkipAll
		case strings.HasSuffix(name, ".zst"):
			compressed = p
		default:
			plain = p
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if segmented {
		return "", ErrSegmented
	}
	if plain != "" {
		return plain, nil
	}
	return compressed, nil
}

// readCodexRollout reads a rollout, decompressing a .zst one, up to maxBytes.
func readCodexRollout(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var r io.Reader = bufio.NewReader(f)
	if strings.HasSuffix(path, ".zst") {
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		r = zr
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: over %d bytes", ErrTooLarge, maxBytes)
	}
	return data, nil
}
