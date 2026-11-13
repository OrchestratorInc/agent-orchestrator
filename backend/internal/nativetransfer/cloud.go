package nativetransfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A transcript moved to AO Cloud names this machine's working directory and
// Claude project folder with these placeholders. The Cloud worker replaces
// them with its own when it places the transcript, since only it knows them.
const (
	CloudCwdPlaceholder        = "/ao-moved/cwd"
	CloudProjectDirPlaceholder = "/ao-moved/claude-project"
)

// ErrTooLarge reports a transcript over the size the caller can move.
var ErrTooLarge = errors.New("nativetransfer: the conversation is too large to move")

// ClaudeTranscript finds the conversation's transcript and returns its path,
// its canonical session id and its size.
func ClaudeTranscript(ctx context.Context, opts ClaudeExportOptions) (path, id string, size int64, err error) {
	if id, err = canonicalUUID(opts.NativeID); err != nil {
		return "", "", 0, err
	}
	if path, err = locateClaudeTranscript(ctx, opts.ConfigDir, id, opts.Cwd); err != nil {
		return "", "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", 0, err
	}
	return path, id, info.Size(), nil
}

// WriteClaudeTranscriptForCloud writes the conversation's transcript with its
// working directory and project folder replaced by the Cloud placeholders,
// and returns its session id. The sidecar folder (subagent transcripts, saved
// tool output) is not included. The agent must not be running.
func WriteClaudeTranscriptForCloud(ctx context.Context, w io.Writer, opts ClaudeExportOptions, maxBytes int64) (string, error) {
	path, id, size, err := ClaudeTranscript(ctx, opts)
	if err != nil {
		return "", err
	}
	if size > maxBytes {
		return "", fmt.Errorf("%w: %d bytes, over %d", ErrTooLarge, size, maxBytes)
	}
	cwd, _, err := claudeTranscriptFacts(path)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		return "", errors.New("nativetransfer: transcript records no working directory")
	}
	paths, err := newPathMap(map[string]string{cwd: CloudCwdPlaceholder, filepath.Dir(path): CloudProjectDirPlaceholder})
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := rewriteJSONLines(w, f, paths); err != nil {
		return "", err
	}
	return id, nil
}

// WriteClaudeArchiveFromCloud writes an archive ImportClaude places on this
// machine from a transcript an AO Cloud sandbox kept in projectDir. The file
// at transcriptPath must be named <session id>.jsonl. Its sidecar files stay
// in Cloud.
func WriteClaudeArchiveFromCloud(ctx context.Context, w io.Writer, transcriptPath, projectDir string) (Manifest, error) {
	id, err := canonicalUUID(strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl"))
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: formatVersion, Harness: HarnessClaudeCode, NativeID: id, SourceProjectDir: projectDir}
	if m.SourceCwd, m.CLIVersion, err = claudeTranscriptFacts(transcriptPath); err != nil {
		return Manifest{}, err
	}
	if m.SourceCwd == "" {
		return Manifest{}, errors.New("nativetransfer: transcript records no working directory")
	}
	size, sum, err := hashFile(transcriptPath)
	if err != nil {
		return Manifest{}, err
	}
	m.Files = []File{{Path: id + ".jsonl", Size: size, SHA256: sum}}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, writeArchive(ctx, w, m, filepath.Dir(transcriptPath))
}
