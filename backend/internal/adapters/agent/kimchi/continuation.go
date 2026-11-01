package kimchi

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
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const kimchiCodingAgentDirEnv = "KIMCHI_CODING_AGENT_DIR"

var _ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)

// kimchiSessionIDPattern mirrors Kimchi's own assertValidSessionId
// (@kimchi-dev/cli): non-empty, only alphanumerics, '-', '_', '.', beginning
// and ending with an alphanumeric. It accepts the uuidv7 ids Kimchi mints while
// rejecting anything with a path separator or a leading dot, so a stored native
// id can never escape the sessions directory.
var kimchiSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// NativeSessionConfigDir returns Kimchi's session-state root. A project
// environment may explicitly select KIMCHI_CODING_AGENT_DIR; otherwise Kimchi
// uses ~/.config/kimchi/harness.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, kimchiCodingAgentDirEnv, filepath.Join(".config", "kimchi", "harness"))
	if err != nil {
		return "", fmt.Errorf("kimchi: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession uses Kimchi's local JSONL transcript as authoritative
// backing state for kimchi-acp's session/load. Kimchi stores one
// <time>_<session-id>.jsonl below a per-cwd directory in <config>/sessions.
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

// LocateTranscript finds Kimchi's provider-owned JSONL by native session id.
// kimchi-acp resolves a session file by id within the cwd-keyed directory, but
// AO must find the original across every cwd folder, so this walks the whole
// sessions tree rather than depending on the lossy dashed-path directory name.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID, err := canonicalKimchiSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	sessionsDir := filepath.Join(configDir, "sessions")
	suffix := "_" + sessionID + ".jsonl"
	bare := sessionID + ".jsonl"
	var found string
	err = filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// One unreadable folder elsewhere in the tree does not hide this
			// conversation, as the importer skips it too.
			if path != sessionsDir && errors.Is(walkErr, fs.ErrPermission) {
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			name := entry.Name()
			if name == bare || strings.HasSuffix(name, suffix) {
				found = path
				return fs.SkipAll
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("kimchi: scan transcripts: %w", err)
	}
	return found, found != "", nil
}

// relocateKimchiImportedSession places a copy of the native transcript where
// kimchi-acp's session/load will look when launched in launchCWD, with the
// stored working directory rewritten to launchCWD so the loader accepts it. It
// returns the path the provider will read.
//
// Kimchi's ACP loader computes its session directory as
// sessions/--<launch-cwd-dashed>--/ from the launch cwd verbatim (no symlink
// resolution) and, on a match, rejects the file unless header.cwd equals the
// launch cwd exactly. When no file matches it silently starts an empty session
// with the same id — so a missing or misplaced copy looks like a successful
// resume with no history. This writes the copy to the exact lookup path, with
// header.cwd rewritten to launchCWD, and verifies it carries that identity, a
// single session record and a real conversation before returning, so that
// silent empty load cannot happen.
//
// The original is only read, never modified, and an existing copy is never
// overwritten: the relocation runs once, before the first imported resume
// (requireNativeHistory && NeedsImportResume), so a copy already present at the
// launch cwd is AO's and is kept.
//
// This is the harness-local half of the import relocation contract (folder
// encoder, cwd rewrite, single-session-record check). It is wired into the
// shared ports.AgentImportedSessionRelocator / importcopy.CopyOnce surface once
// that contract is published in codex/session-import-build; until then it is a
// self-contained implementation exercised by the adapter's tests.
func relocateKimchiImportedSession(ctx context.Context, configDir, nativeID, transcriptPath, launchCWD string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sessionID, err := canonicalKimchiSessionID(nativeID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(configDir) == "" {
		return "", errors.New("kimchi: no config dir for native resume")
	}
	if transcriptPath == "" {
		return "", errors.New("kimchi: no native transcript for native resume")
	}
	if launchCWD == "" || !filepath.IsAbs(launchCWD) {
		return "", fmt.Errorf("kimchi: launch directory must be absolute, got %q", launchCWD)
	}
	// The folder and the header.cwd both come from the exact launch cwd AO's ACP
	// driver sends (ports.ChatStartConfig.WorkspacePath), encoded verbatim:
	// Kimchi resolves neither, so resolving here would place the copy where the
	// loader never looks and reintroduce the silent empty load on symlinked
	// paths.
	destDir := filepath.Join(configDir, "sessions", encodeKimchiCwdDir(launchCWD))

	// Idempotent: never overwrite a copy already at the launch cwd. It is AO's
	// and may hold turns added since the import.
	if existing, ok := resolveKimchiSessionByID(destDir, sessionID); ok {
		return existing, nil
	}

	dest := filepath.Join(destDir, filepath.Base(transcriptPath))
	if err := writeKimchiResumeCopy(ctx, transcriptPath, dest, launchCWD); err != nil {
		return "", err
	}

	// Verify the copy landed at the loader's lookup path with the exact identity
	// and working directory it checks, and holds a real conversation.
	final, ok := resolveKimchiSessionByID(destDir, sessionID)
	if !ok {
		return "", fmt.Errorf("kimchi: resume copy was not written to %s", destDir)
	}
	match, err := kimchiHeaderMatches(final, sessionID, launchCWD)
	if err != nil {
		return "", err
	}
	if !match {
		return "", fmt.Errorf("kimchi: resume copy %s does not carry the requested identity and working directory", final)
	}
	if !kimchiHasConversation(final) {
		return "", fmt.Errorf("kimchi: resume copy %s has no conversation to replay", final)
	}
	return final, nil
}

// encodeKimchiCwdDir reproduces kimchi-acp's encodeCwdDir exactly: strip a
// single leading slash or backslash, map every '/', '\\' and ':' to '-', and
// wrap in '--'. It operates on the raw launch cwd with no symlink resolution,
// matching the loader, so the folder AO writes to is the folder Kimchi reads.
func encodeKimchiCwdDir(cwd string) string {
	if len(cwd) > 0 && (cwd[0] == '/' || cwd[0] == '\\') {
		cwd = cwd[1:]
	}
	return "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cwd) + "--"
}

// resolveKimchiSessionByID mirrors kimchi-acp's resolveSessionPathById: a file
// named exactly <id>.jsonl or ending in _<id>.jsonl inside dir.
func resolveKimchiSessionByID(dir, sessionID string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	bare := sessionID + ".jsonl"
	suffix := "_" + sessionID + ".jsonl"
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == bare || strings.HasSuffix(name, suffix) {
			return filepath.Join(dir, name), true
		}
	}
	return "", false
}

type kimchiSessionHeader struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	CWD  string `json:"cwd"`
}

// kimchiHeaderMatches reports whether path's first session header carries the
// given id and working directory. The loader checks header.cwd === launch cwd
// with exact string equality, so this compares verbatim.
func kimchiHeaderMatches(path, sessionID, cwd string) (bool, error) {
	header, err := readKimchiHeader(path)
	if err != nil {
		return false, err
	}
	if header == nil {
		return false, nil
	}
	return header.ID == sessionID && header.CWD == cwd, nil
}

// readKimchiHeader returns the first session header record, or nil when the
// file has none. It reads only the first non-empty line, which is always the
// header in a Kimchi transcript.
func readKimchiHeader(path string) (*kimchiSessionHeader, error) {
	f, err := os.Open(path) //nolint:gosec // provider-owned path resolved from the configured root
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var header kimchiSessionHeader
			if jsonErr := json.Unmarshal(line, &header); jsonErr != nil || header.Type != "session" {
				return nil, nil
			}
			return &header, nil
		}
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// kimchiHasConversation reports whether path holds at least one user or
// assistant message record, so an empty or header-only file is not mistaken
// for replayable history.
func kimchiHasConversation(path string) bool {
	f, err := os.Open(path) //nolint:gosec // provider-owned path resolved from the configured root
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var rec struct {
				Type    string `json:"type"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Type == "message" && (rec.Message.Role == "user" || rec.Message.Role == "assistant") {
				return true
			}
		}
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return false
		}
	}
}

// writeKimchiResumeCopy streams source to dest, rewriting the first session
// header's cwd to cwd and copying every other record verbatim. It writes to a
// temporary file in the destination directory and renames it into place, mode
// 0600, so the original is never modified and a partial copy is never visible.
func writeKimchiResumeCopy(ctx context.Context, source, dest, cwd string) error {
	in, err := os.Open(source) //nolint:gosec // provider-owned path resolved from the configured root
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".ao-kimchi-resume-*.jsonl")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	bw := bufio.NewWriterSize(tmp, 256<<10)
	br := bufio.NewReaderSize(in, 256<<10)
	rewrote := false
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			out := line
			if !rewrote {
				if rewritten, ok, err := rewriteKimchiHeaderCwd(line, cwd); err != nil {
					return err
				} else if ok {
					out, rewrote = rewritten, true
				}
			}
			if _, err := bw.Write(out); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if !rewrote {
		return errors.New("kimchi: source transcript has no session header to rewrite")
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// rewriteKimchiHeaderCwd rewrites the cwd of a session header line, preserving
// every other field. ok is false for a line that is not a session header, so
// the caller keeps scanning. The trailing newline, if any, is preserved.
func rewriteKimchiHeaderCwd(line []byte, cwd string) ([]byte, bool, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, false, nil //nolint:nilerr // a non-JSON line is not the header
	}
	if typ, ok := fields["type"]; !ok || string(typ) != `"session"` {
		return nil, false, nil
	}
	encoded, err := json.Marshal(cwd)
	if err != nil {
		return nil, false, err
	}
	fields["cwd"] = encoded
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return nil, false, err
	}
	if len(line) > 0 && line[len(line)-1] == '\n' {
		rewritten = append(rewritten, '\n')
	}
	return rewritten, true, nil
}

// canonicalKimchiSessionID validates a native session id against Kimchi's own
// rule and returns it unchanged. The rule forbids path separators, so the id is
// safe to join into a filesystem path.
func canonicalKimchiSessionID(value string) (string, error) {
	id := strings.TrimSpace(value)
	if !kimchiSessionIDPattern.MatchString(id) {
		return "", fmt.Errorf("kimchi: invalid native session id %q", value)
	}
	return id, nil
}
