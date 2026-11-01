package qwen

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const qwenHomeEnv = "QWEN_HOME"

// qwenNativeSessionID matches a Qwen chat id: the <id>.jsonl file name, which is
// a UUID and is globally unique across projects.
var qwenNativeSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// qwenNonAlnum mirrors Qwen Code's sanitizeCwd exactly: it replaces every
// non-alphanumeric byte of the cwd with a dash to name the project folder
// (so "/Users/x/.ao" becomes "-Users-x--ao"). Pinned by continuation_test.go.
var qwenNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentNativeSessionResumePreparer = (*Plugin)(nil)
)

// sanitizeQwenCwd encodes a working directory the way Qwen Code's Storage does
// when it names a project folder: `cwd.replace(/[^a-zA-Z0-9]/g, "-")`.
func sanitizeQwenCwd(cwd string) string {
	return qwenNonAlnum.ReplaceAllString(cwd, "-")
}

// resolveQwenCwd returns the path Qwen keys its project folder by. Qwen resolves
// symlinks before encoding, so AO does too (e.g. /tmp -> /private/tmp on macOS).
func resolveQwenCwd(cwd string) string {
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		return resolved
	}
	return filepath.Clean(cwd)
}

// qwenChatPath is the transcript Qwen reads when it runs in cwd:
// <configDir>/projects/<sanitizeCwd(resolved cwd)>/chats/<id>.jsonl.
func qwenChatPath(configDir, cwd, sessionID string) string {
	return filepath.Join(configDir, "projects", sanitizeQwenCwd(resolveQwenCwd(cwd)), "chats", sessionID+".jsonl")
}

// NativeSessionConfigDir returns the Qwen Code state root used by a launch:
// QWEN_HOME when set, otherwise ~/.qwen.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, qwenHomeEnv, ".qwen")
	if err != nil {
		return "", fmt.Errorf("qwen: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession reports whether the imported conversation still has its
// local transcript, the authoritative backing state for a resume.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, ok, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// LocateTranscript finds a Qwen chat's JSONL without depending on the lossy
// project-folder name: the <id>.jsonl file name is a globally unique UUID, so
// scanning each project's chats directory is deterministic.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID := strings.TrimSpace(ref.NativeSessionID)
	if !qwenNativeSessionID.MatchString(sessionID) {
		return "", false, fmt.Errorf("qwen: invalid native session id")
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	projectsDir := filepath.Join(configDir, "projects")
	projects, err := os.ReadDir(projectsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("qwen: read projects: %w", err)
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(projectsDir, project.Name(), "chats", sessionID+".jsonl")
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			return path, true, nil
		case err == nil, errors.Is(err, os.ErrNotExist):
			continue
		default:
			return "", false, fmt.Errorf("qwen: stat transcript: %w", err)
		}
	}
	return "", false, nil
}

// PrepareNativeSessionResume copies the imported chat to the project folder Qwen
// will read when it runs in cwd, rewriting the cwd on every record so Qwen loads
// the history instead of reporting the session missing. The original is never
// touched, and an existing copy is left in place because it holds AO's own turns
// after the first resume.
func (p *Plugin) PrepareNativeSessionResume(ctx context.Context, ref ports.NativeSessionRef, cwd, priorCwd string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(ref.NativeSessionID)
	if !qwenNativeSessionID.MatchString(sessionID) {
		return "", fmt.Errorf("qwen: invalid native session id")
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", fmt.Errorf("qwen: missing config dir")
	}
	if strings.TrimSpace(cwd) == "" || !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("qwen: resume working directory must be absolute")
	}
	resolvedCWD := resolveQwenCwd(cwd)
	target := qwenChatPath(configDir, cwd, sessionID)
	// First open writes the copy; later opens at the same cwd keep it, since it
	// now carries the turns AO added after the resume.
	if info, err := os.Stat(target); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("qwen: resume target is not a regular file: %s", target)
		}
		return target, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("qwen: stat resume target: %w", err)
	}
	// Choose what to copy from. When a previous open already prepared a copy
	// (priorCwd) and the launch cwd has since drifted, that prior copy holds AO's
	// appended turns and is authoritative; the untouched original is only a
	// fallback when the prior copy is gone. Never pick by newest mtime: a
	// lock-less provider the user kept running could make the original newest.
	source := ""
	if strings.TrimSpace(priorCwd) != "" {
		if prior := qwenChatPath(configDir, priorCwd, sessionID); prior != target {
			if info, err := os.Stat(prior); err == nil && info.Mode().IsRegular() {
				source = prior
			}
		}
	}
	if source == "" {
		located, ok, err := p.LocateTranscript(ctx, ref)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("qwen: source transcript for %s not found under %s", sessionID, configDir)
		}
		source = located
	}
	if source == target {
		// The chat already lives where Qwen will read it (same folder); nothing to do.
		return target, nil
	}
	if err := copyQwenTranscriptRewritingCWD(ctx, source, target, resolvedCWD); err != nil {
		return "", err
	}
	return target, nil
}

// copyQwenTranscriptRewritingCWD streams src to dst line by line, replacing the
// top-level "cwd" field of each JSON record with newCWD, then atomically renames
// the temp file into place at mode 0600. The source is only read.
func copyQwenTranscriptRewritingCWD(ctx context.Context, src, dst, newCWD string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("qwen: open source transcript: %w", err)
	}
	defer func() { _ = in.Close() }()

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("qwen: create project chats dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ao-import-*.jsonl.tmp")
	if err != nil {
		return fmt.Errorf("qwen: create temp transcript: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("qwen: chmod temp transcript: %w", err)
	}

	newCWDJSON, err := json.Marshal(newCWD)
	if err != nil {
		return err
	}
	reader := bufio.NewReaderSize(in, 256<<10)
	writer := bufio.NewWriterSize(tmp, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			rewritten := rewriteQwenCWDLine(line, newCWDJSON)
			if _, err := writer.Write(rewritten); err != nil {
				return fmt.Errorf("qwen: write transcript: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("qwen: read source transcript: %w", readErr)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("qwen: flush transcript: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("qwen: sync transcript: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("qwen: close transcript: %w", err)
	}
	cleanup = false
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("qwen: place transcript: %w", err)
	}
	return nil
}

// rewriteQwenCWDLine replaces only the top-level "cwd" value of one JSONL record,
// preserving every other field and the trailing newline. A line that is not a
// JSON object, or has no cwd, is copied through unchanged so the copy stays a
// faithful replica apart from the folder the chat resumes in.
func rewriteQwenCWDLine(line, newCWDJSON []byte) []byte {
	trimmed := strings.TrimRight(string(line), "\r\n")
	if trimmed == "" {
		return line
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &fields) != nil {
		return line
	}
	if _, ok := fields["cwd"]; !ok {
		return line
	}
	fields["cwd"] = append(json.RawMessage(nil), newCWDJSON...)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return line
	}
	suffix := line[len(trimmed):]
	return append(encoded, suffix...)
}
