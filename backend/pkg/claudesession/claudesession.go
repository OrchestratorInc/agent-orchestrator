// Package claudesession copies a Claude Code session for a fork: up to one of
// its messages, into another workspace. The desktop daemon and AO Cloud's
// worker share it, so a forked Claude conversation is cut and moved the same
// way wherever the fork happens.
//
// The copy itself is the Claude Agent SDK's forkSession, run by RunFork. The
// SDK writes the copy beside the source, keyed by the source folder, so
// MoveSessionFile then moves it to the fork folder's key and points every path
// of the original's folder at the fork's. The source session file is never
// modified.
package claudesession

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed fork_session.mjs
var forkScript []byte

var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]`)

// ProjectKey is the folder name, under the Claude config folder's projects,
// that Claude keeps a workspace's sessions in. realPath must have its links
// resolved, as Claude resolves it.
func ProjectKey(realPath string) string {
	return nonAlphanumeric.ReplaceAllString(realPath, "-")
}

// ResolveForkPoint finds the session-file line to cut a fork after: the last
// line of the API message apiMessageID, since one message is written as
// several lines while it streams. It reads the source session's own file and
// returns that line's uuid.
func ResolveForkPoint(ctx context.Context, sessionFile, apiMessageID string) (string, error) {
	file, err := os.Open(sessionFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 256<<20)
	last := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var line struct {
			UUID    string `json:"uuid"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line.Message.ID == apiMessageID && line.UUID != "" {
			last = line.UUID
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if last == "" {
		return "", fmt.Errorf("message %s is not in the session", apiMessageID)
	}
	return last, nil
}

// RunFork copies the session sourceID, kept for the folder dir, with the
// Claude Agent SDK's forkSession, run by node from sdkPath (the SDK's
// sdk.mjs). With upToUUID set, the copy ends at that line (see
// ResolveForkPoint); otherwise it holds the whole session. env must point
// CLAUDE_CONFIG_DIR at the config folder holding the session. It returns the
// copy's session id; the copy is beside the source, under dir's ProjectKey.
func RunFork(ctx context.Context, node, sdkPath string, env []string, sourceID, dir, upToUUID string) (string, error) {
	script, err := os.CreateTemp("", "ao-claude-fork-*.mjs")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(script.Name()) }()
	if _, err := script.Write(forkScript); err != nil {
		_ = script.Close()
		return "", err
	}
	if err := script.Close(); err != nil {
		return "", err
	}
	cmd := aoprocess.CommandContext(ctx, node, script.Name(), sdkPath, sourceID, dir, upToUUID)
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("fork script: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return "", fmt.Errorf("fork script output: %w", err)
	}
	if result.SessionID == "" || result.SessionID == sourceID || strings.ContainsAny(result.SessionID, `/\`) {
		return "", fmt.Errorf("fork script: invalid session id %q", result.SessionID)
	}
	return result.SessionID, nil
}

// MoveSessionFile moves the session file from to to, rewriting every mention
// of a sources folder in it to dst on the way (see RewriteWorkspacePaths). The
// file is written beside to and renamed into place, so to is either whole or
// absent; from is removed once to is in place.
func MoveSessionFile(ctx context.Context, from, to string, sources []string, dst string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".ao-fork-*.jsonl")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	out := bufio.NewWriter(tmp)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			_ = tmp.Close()
			return err
		}
		line := RewriteWorkspacePaths(scanner.Bytes(), sources, dst)
		if _, err := out.Write(append(line, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := out.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), to); err != nil {
		return err
	}
	_ = in.Close()
	return os.Remove(from)
}

// RewriteWorkspacePaths points every mention of a sources folder in one JSONL
// record at dst: its cwd, and the paths in tool inputs and results. Left as
// they are, the fork's agent would read and edit the original's files. A path
// is matched only as a whole folder name, so a sibling such as "fork-test-10"
// beside "fork-test-1" is left alone. The paths are matched as JSON encodes
// them, so the record stays valid.
func RewriteWorkspacePaths(line []byte, sources []string, dst string) []byte {
	to := jsonStringBody(dst)
	for _, src := range sources {
		if src == "" || src == dst {
			continue
		}
		from := jsonStringBody(src)
		var out []byte
		rest := line
		for {
			i := bytes.Index(rest, from)
			if i < 0 {
				break
			}
			end := i + len(from)
			if end < len(rest) && continuesPathName(rest[end]) {
				out = append(out, rest[:end]...)
				rest = rest[end:]
				continue
			}
			out = append(append(out, rest[:i]...), to...)
			rest = rest[end:]
		}
		if out != nil {
			out = append(out, rest...)
			line = out
		}
	}
	return line
}

// continuesPathName reports whether c can continue a folder name, so a path
// followed by it names another folder.
func continuesPathName(c byte) bool {
	return c >= 0x80 || c == '.' || c == '-' || c == '_' ||
		('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// jsonStringBody is s as it appears inside a JSON string.
func jsonStringBody(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimSuffix(bytes.TrimPrefix(bytes.TrimSpace(b.Bytes()), []byte(`"`)), []byte(`"`))
}
