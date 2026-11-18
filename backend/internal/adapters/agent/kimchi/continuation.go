package kimchi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const kimchiCodingAgentDirEnv = "KIMCHI_CODING_AGENT_DIR"

var _ ports.AgentImportedSessionRelocator = (*Plugin)(nil)
var _ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
var _ ports.AgentNativeSessionProber = (*Plugin)(nil)
var _ ports.AgentTranscriptLocator = (*Plugin)(nil)

// kimchiSessionIDPattern mirrors Kimchi's own assertValidSessionId
// (@kimchi-dev/cli): non-empty, only alphanumerics, '-', '_', '.', beginning
// and ending with an alphanumeric. It accepts the uuidv7 ids Kimchi mints while
// rejecting anything with a path separator or a leading dot, so a stored native
// id can never escape the sessions directory.
var kimchiSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// NativeSessionConfigDir models Kimchi's src/entry.ts: it resets both
// KIMCHI_CODING_AGENT_DIR and PI_CODING_AGENT_DIR to homedir/.config/kimchi/harness.
// A different requested root cannot be honored and must not silently resume an
// empty conversation. This inspects the child environment without changing it.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	homeKey := "HOME"
	if runtime.GOOS == "windows" {
		homeKey = "USERPROFILE"
	}
	home, set := kimchiEnvValue(env, homeKey)
	if !set {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("kimchi: resolve native home: %w", err)
		}
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", errors.New("kimchi: native home must be an absolute directory")
	}
	dir := filepath.Join(home, ".config", "kimchi", "harness")
	if requested, _ := kimchiEnvValue(env, kimchiCodingAgentDirEnv); requested != "" && !sameKimchiRoot(requested, dir) {
		return "", fmt.Errorf("kimchi: this runtime ignores KIMCHI_CODING_AGENT_DIR; requested root %q differs from its native home root %q", requested, dir)
	}
	return dir, nil
}

func kimchiEnvValue(env map[string]string, key string) (string, bool) {
	if runtime.GOOS == "windows" {
		for name, value := range env {
			if strings.EqualFold(name, key) {
				return value, true
			}
		}
	} else if value, ok := env[key]; ok {
		return value, true
	}
	return os.LookupEnv(key)
}

func sameKimchiRoot(left, right string) bool {
	if !filepath.IsAbs(left) || !filepath.IsAbs(right) {
		return false
	}
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	l, lerr := os.Stat(left)
	r, rerr := os.Stat(right)
	return lerr == nil && rerr == nil && l.IsDir() && r.IsDir() && os.SameFile(l, r)
}

// ProbeNativeSession uses Kimchi's local JSONL transcript as authoritative
// backing state for kimchi-acp's session/load. Kimchi stores one
// <time>_<session-id>.jsonl below a per-cwd directory in <config>/sessions.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	return nativeconfig.ProbeTranscript(ctx, ref, p.LocateTranscript)
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
	suffix := "_" + sessionID + ".jsonl"
	bare := sessionID + ".jsonl"
	path, found, err := nativeconfig.FindFile(ctx, filepath.Join(configDir, "sessions"), func(name string) bool {
		return name == bare || strings.HasSuffix(name, suffix)
	})
	if err != nil {
		return "", false, fmt.Errorf("kimchi: scan transcripts: %w", err)
	}
	return path, found, nil
}

// RelocateImportedSession prepares the exact cwd-keyed file Kimchi's ACP loader
// reads. ResumeCwd persistence and first-open admission belong to the manager.
func (p *Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	_, err := relocateKimchiImportedSession(ctx, r.ConfigDir, r.NativeSessionID, r.TranscriptPath, r.LaunchCWD)
	return err
}

// relocateKimchiImportedSession keeps the native basename and identity, rewriting
// only the session record's cwd. Kimchi encodes ACP params.cwd verbatim:
// resolving a symlink here makes session/load silently open an empty chat.
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
	nativeRoot, err := New().NativeSessionConfigDir(ctx, map[string]string{kimchiCodingAgentDirEnv: ""})
	if err != nil {
		return "", err
	}
	if !sameKimchiRoot(configDir, nativeRoot) {
		return "", fmt.Errorf("kimchi: cannot import from root %q; this runtime only loads sessions from native home root %q", configDir, nativeRoot)
	}
	if transcriptPath == "" {
		return "", errors.New("kimchi: no native transcript for native resume")
	}
	if launchCWD == "" || !filepath.IsAbs(launchCWD) {
		return "", fmt.Errorf("kimchi: launch directory must be absolute, got %q", launchCWD)
	}
	name := filepath.Base(transcriptPath)
	if name != sessionID+".jsonl" && !strings.HasSuffix(name, "_"+sessionID+".jsonl") {
		return "", errors.New("kimchi: transcript basename does not match native session id")
	}
	destDir := filepath.Join(configDir, "sessions", encodeKimchiCwdDir(launchCWD))
	dest := filepath.Join(destDir, name)
	if err := checkKimchiLookup(destDir, sessionID, dest); err != nil {
		return "", err
	}
	records := kimchiRecords{sessionID: sessionID}
	rewrite := func(line []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		session, err := records.observe(line)
		if err != nil {
			return nil, err
		}
		if session {
			return importcopy.SetStringField(line, "cwd", launchCWD)
		}
		return line, nil
	}
	check := func() error {
		if err := records.complete(); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := importcopy.CopyOnce(transcriptPath, dest, rewrite, check); err != nil {
		return "", fmt.Errorf("kimchi: prepare resume copy: %w", err)
	}
	// Existing copies may hold AO turns, but must still be valid native input.
	if err := validateKimchiResumeCopy(ctx, dest, sessionID, launchCWD); err != nil {
		return "", err
	}
	if err := checkKimchiLookup(destDir, sessionID, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// checkKimchiLookup rejects another matching basename: Kimchi picks the first
// file matching an id, so a valid copy alone does not prove it will be loaded.
func checkKimchiLookup(dir, sessionID, dest string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == sessionID+".jsonl" || strings.HasSuffix(name, "_"+sessionID+".jsonl") {
			if filepath.Join(dir, name) != dest || !entry.Type().IsRegular() {
				return fmt.Errorf("kimchi: ambiguous or non-regular resume path %s", filepath.Join(dir, name))
			}
		}
	}
	return nil
}

func validateKimchiResumeCopy(ctx context.Context, path, sessionID, cwd string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("kimchi: resume copy %s is not a regular file", path)
	}
	f, err := os.Open(path) //nolint:gosec // exact provider lookup path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReaderSize(f, 256<<10)
	records := kimchiRecords{sessionID: sessionID}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			session, err := records.observe(line)
			if err != nil {
				return fmt.Errorf("kimchi: invalid resume copy: %w", err)
			}
			if recordCWD, _ := importcopy.StringField(line, "cwd"); session && recordCWD != cwd {
				return errors.New("kimchi: resume copy session record does not match the raw launch cwd")
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := records.complete(); err != nil {
		return fmt.Errorf("kimchi: invalid resume copy: %w", err)
	}
	return nil
}

// kimchiRecords applies the rules a transcript must meet for kimchi-acp to
// replay it, one record at a time: the first record is the only session
// record and carries the native id, and a user or assistant message follows.
type kimchiRecords struct {
	sessionID    string
	sessions     int
	conversation bool
}

// observe checks the next record and reports whether it is the session record.
func (k *kimchiRecords) observe(line []byte) (bool, error) {
	var rec struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Message struct {
			Role string `json:"role"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &rec); err != nil {
		return false, fmt.Errorf("kimchi: invalid transcript record: %w", err)
	}
	if k.sessions == 0 && rec.Type != "session" {
		return false, errors.New("kimchi: first transcript record must be a session")
	}
	if rec.Type == "session" {
		k.sessions++
		if k.sessions != 1 || rec.ID != k.sessionID {
			return false, errors.New("kimchi: expected exactly one session record with the native id")
		}
		return true, nil
	}
	k.conversation = k.conversation || rec.Type == "message" && (rec.Message.Role == "user" || rec.Message.Role == "assistant")
	return false, nil
}

// complete reports whether every record seen made a replayable transcript.
func (k *kimchiRecords) complete() error {
	if k.sessions != 1 || !k.conversation {
		return errors.New("kimchi: transcript needs exactly one session record and conversation history to replay")
	}
	return nil
}

// encodeKimchiCwdDir reproduces kimchi-acp's encodeCwdDir exactly: strip a
// single leading slash or backslash, map every '/', '\\' and ':' to '-', and
// wrap in '--'. It operates on the raw launch cwd with no symlink resolution,
// matching the loader, so the folder AO writes to is the folder Kimchi reads.
func encodeKimchiCwdDir(cwd string) string {
	if cwd != "" && (cwd[0] == '/' || cwd[0] == '\\') {
		cwd = cwd[1:]
	}
	return "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cwd) + "--"
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
