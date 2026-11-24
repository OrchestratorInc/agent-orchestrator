package stakpak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

// launchRecord is what AO knows about one Stakpak launch: the worktree and the
// time it started, and on resume the conversation it continues. Stakpak mints
// a session id only when the first message is sent, so a fresh launch has no
// id until its first turn.
type launchRecord struct {
	Workspace  string    `json:"workspace"`
	LaunchedAt time.Time `json:"launchedAt"`
	NativeID   string    `json:"nativeId,omitempty"`
	// PromptDigest fingerprints the standing instructions this launch gave
	// Stakpak, which stores them in the conversation.
	PromptDigest string `json:"promptDigest,omitempty"`
	// ResolvedID is the conversation a fresh launch started, once found in
	// Stakpak's session list, so later lookups need no list.
	ResolvedID string `json:"resolvedId,omitempty"`
	// Profile is STAKPAK_PROFILE as the launch environment set it, which picks
	// the store that holds the conversation. nil means the launch environment
	// left it to the daemon's. Nothing else from that environment is kept: it
	// can hold API keys.
	Profile *string `json:"profile,omitempty"`
}

// launchProfile takes the STAKPAK_PROFILE selector from a launch environment.
func launchProfile(env map[string]string) *string {
	if profile, ok := env["STAKPAK_PROFILE"]; ok {
		return &profile
	}
	return nil
}

// restoreEnv is the environment a restore's conversation check runs in: the
// restore's own, with the launch's recorded profile when the caller set none.
// AO's update-when-idle probe passes no environment, and a project-level
// STAKPAK_PROFILE would otherwise send it to the daemon's store.
func restoreEnv(env map[string]string, previous launchRecord) map[string]string {
	if _, set := env["STAKPAK_PROFILE"]; set || previous.Profile == nil {
		return env
	}
	out := make(map[string]string, len(env)+1)
	for key, value := range env {
		out[key] = value
	}
	out["STAKPAK_PROFILE"] = *previous.Profile
	return out
}

// rememberLaunchEnv keeps, in memory only, the environment a session was
// launched or resumed with; PreLaunch is its only caller. Never written to
// disk: it can hold API keys, such as a remote profile's STAKPAK_API_KEY set
// only in the project environment.
func (p *Plugin) rememberLaunchEnv(sessionID string, env map[string]string) {
	if strings.TrimSpace(sessionID) == "" || env == nil {
		return
	}
	kept := make(map[string]string, len(env))
	for key, value := range env {
		kept[key] = value
	}
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	if p.launchEnvs == nil {
		p.launchEnvs = map[string]map[string]string{}
	}
	p.launchEnvs[sessionID] = kept
}

// sessionEnv is the environment every Stakpak command about a session runs
// in. An environment the caller passes wins: a restore's or update-when-idle
// probe's describes the process about to start, a preflight's its target. A
// caller that passes none (SessionInfo) gets the one this process launched or
// resumed the session with; ResolveNativeSessionID prefers that one too (see
// there). Either way the launch's recorded profile applies when the
// environment names none.
func (p *Plugin) sessionEnv(sessionID string, env map[string]string, record launchRecord) map[string]string {
	if env == nil {
		env = p.keptEnv(sessionID)
	}
	return restoreEnv(env, record)
}

// matches reports whether this launch's conversation is nativeID: the one it
// resumed, or the one a fresh launch started (resolved later from the list).
// A fresh launch whose conversation is not yet resolved may be any.
func (r launchRecord) matches(nativeID string) bool {
	switch {
	case r.NativeID != "":
		return r.NativeID == nativeID
	case r.ResolvedID != "":
		return r.ResolvedID == nativeID
	}
	return true
}

// sessionClockSkew tolerates Stakpak stamping a session slightly before AO
// recorded the launch.
const sessionClockSkew = 2 * time.Second

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

func domainSessionID(id string) domain.SessionID { return domain.SessionID(id) }

func launchRecordPath(dataDir, sessionID string) string {
	return filepath.Join(sessionDir(dataDir, sessionID), "launch.json")
}

func writeLaunchRecord(dataDir, sessionID string, record launchRecord) error {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sessionDir(dataDir, sessionID), 0o700); err != nil {
		return fmt.Errorf("stakpak: create session dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(launchRecordPath(dataDir, sessionID), data, 0o600); err != nil {
		return fmt.Errorf("stakpak: record launch: %w", err)
	}
	return nil
}

func readLaunchRecord(dataDir, sessionID string) (launchRecord, bool, error) {
	data, err := os.ReadFile(launchRecordPath(dataDir, sessionID)) //nolint:gosec // AO-owned path under the data dir
	if errors.Is(err, os.ErrNotExist) {
		return launchRecord{}, false, nil
	}
	if err != nil {
		return launchRecord{}, false, err
	}
	var record launchRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return launchRecord{}, false, fmt.Errorf("stakpak: read launch record: %w", err)
	}
	return record, true, nil
}

// nativeSession is one row of `stakpak sessions list --json`.
type nativeSession struct {
	ID        string    `json:"id"`
	Cwd       string    `json:"cwd"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// sessionListLimit bounds the newest-first scan. A launch's conversation is
// among Stakpak's most recent sessions while AO is still observing it.
const sessionListLimit = 100

// listSessions reads Stakpak's own session list. It honours the active
// profile, so it reads the local store or the Stakpak API exactly as the TUI
// does.
func (p *Plugin) listSessions(ctx context.Context, env map[string]string) ([]nativeSession, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := process.CommandContext(ctx, binary, "sessions", "list", "--json", "--limit", fmt.Sprint(sessionListLimit))
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("stakpak: list sessions: %w", err)
	}
	return parseSessionList(out)
}

func parseSessionList(out []byte) ([]nativeSession, error) {
	var list struct {
		Sessions []nativeSession `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("stakpak: parse session list: %w", err)
	}
	return list.Sessions, nil
}

// ResolveNativeSessionID names the Stakpak conversation of the current launch:
// the one it resumed, else the newest conversation Stakpak started in the
// same worktree after the launch. ok is false before the first turn.
//
// The activity observer calls this on every state change, and the list can be
// a Stakpak API call, so it runs only until the id is known: a resumed
// launch already has it, and a fresh launch records it once found. A
// conversation the user starts with /new inside the TUI is therefore not
// followed.
func (p *Plugin) ResolveNativeSessionID(ctx context.Context, cfg ports.NativeSessionResolveConfig) (string, bool, error) {
	// The running process's own launch environment wins: AO rebuilds cfg.Env
	// from the current project and account, which may name keys rotated since
	// the launch. It is used only when nothing is kept, after a restart.
	env := p.keptEnv(string(cfg.SessionID))
	if env == nil {
		env = cfg.Env
	}
	return p.resolveNativeSessionID(ctx, cfg.DataDir, string(cfg.SessionID), env)
}

// keptEnv is the environment this process launched or resumed the session
// with, or nil.
func (p *Plugin) keptEnv(sessionID string) map[string]string {
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	return p.launchEnvs[sessionID]
}

// resolveNativeSessionID lists sessions in sessionEnv(env): a restore passes
// its own environment, which still holds project-only credentials after a
// daemon restart has emptied the in-memory one.
func (p *Plugin) resolveNativeSessionID(ctx context.Context, dataDir, sessionID string, env map[string]string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	record, ok, err := readLaunchRecord(dataDir, sessionID)
	if err != nil || !ok {
		return "", false, err
	}
	for _, known := range []string{record.ResolvedID, record.NativeID} {
		if validSessionID(known) {
			return known, true, nil
		}
	}
	sessions, err := p.listSessions(ctx, p.sessionEnv(sessionID, env, record))
	if err != nil {
		return "", false, err
	}
	id, found := matchLaunch(record, sessions)
	if !found {
		return "", false, nil
	}
	stored, err := p.recordResolvedID(dataDir, sessionID, record, id)
	if err != nil || !stored {
		return "", false, err
	}
	return id, true, nil
}

// rememberDataDir keeps the AO data dir for conversationEnv.
func (p *Plugin) rememberDataDir(dataDir string) {
	if strings.TrimSpace(dataDir) == "" {
		return
	}
	p.recordMu.Lock()
	p.dataDir = dataDir
	p.recordMu.Unlock()
}

// conversationEnv is sessionEnv for a SessionRef. AO's restore asks again
// whether the conversation exists with a ref that carries no data dir, so the
// record is read from the data dir the plugin kept.
func (p *Plugin) conversationEnv(ref ports.SessionRef, env map[string]string) (map[string]string, error) {
	if strings.TrimSpace(ref.ID) == "" {
		return env, nil
	}
	// The environment is used, never kept: an account move's preflight asks
	// with the target account's credentials while the session still runs on
	// its own.
	dataDir := ref.DataDir
	if strings.TrimSpace(dataDir) == "" {
		p.recordMu.Lock()
		dataDir = p.dataDir
		p.recordMu.Unlock()
	}
	var record launchRecord
	if strings.TrimSpace(dataDir) != "" {
		read, _, err := readLaunchRecord(dataDir, ref.ID)
		if err != nil {
			return nil, err
		}
		record = read
	}
	return p.sessionEnv(ref.ID, env, record), nil
}

// saveLaunchRecord writes a launch's record under recordMu.
func (p *Plugin) saveLaunchRecord(dataDir, sessionID string, record launchRecord) error {
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	return writeLaunchRecord(dataDir, sessionID, record)
}

// recordResolvedID stores the conversation the launch seen started. stored is
// false when a newer launch replaced the record while the session list was
// read: that id belongs to the earlier launch, so nothing is recorded.
func (p *Plugin) recordResolvedID(dataDir, sessionID string, seen launchRecord, id string) (bool, error) {
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	current, ok, err := readLaunchRecord(dataDir, sessionID)
	if err != nil || !ok || !current.LaunchedAt.Equal(seen.LaunchedAt) || current.NativeID != seen.NativeID {
		return false, err
	}
	current.ResolvedID = id
	if err := writeLaunchRecord(dataDir, sessionID, current); err != nil {
		return false, err
	}
	return true, nil
}

// matchLaunch picks the newest conversation Stakpak started in the launch's
// worktree since the launch, from a newest-first list.
func matchLaunch(record launchRecord, sessions []nativeSession) (string, bool) {
	workspace := canonicalPath(record.Workspace)
	var newest nativeSession
	for _, s := range sessions {
		if !validSessionID(s.ID) || workspace == "" || canonicalPath(s.Cwd) != workspace {
			continue
		}
		if s.CreatedAt.Before(record.LaunchedAt.Add(-sessionClockSkew)) {
			continue
		}
		if newest.ID == "" || s.CreatedAt.After(newest.CreatedAt) {
			newest = s
		}
	}
	return newest.ID, newest.ID != ""
}

// canonicalPath resolves symlinks: Stakpak records the process's real cwd
// (/private/var/... on macOS), while AO may hold the /var/... spelling.
func canonicalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// NativeConversationExists reports whether Stakpak still has the
// conversation, so a restore never resumes an id Stakpak would reject:
// `stakpak --session <missing>` exits instead of starting the TUI.
func (p *Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	nativeID = strings.TrimSpace(nativeID)
	if !validSessionID(nativeID) {
		return false, nil
	}
	env, err := p.conversationEnv(ref, env)
	if err != nil {
		return false, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := process.CommandContext(ctx, binary, "sessions", "show", nativeID, "--limit", "1")
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return sessionShowResult(nativeID, string(out), err)
}

// sessionShowResult reads `stakpak sessions show`: it prints the session's
// "ID:" line on success. A missing conversation fails with "Session <id> not
// found" from the local store, or with the Stakpak API's not-found message or
// "API error 404 ..." on a remote profile. Any other failure stays an error.
func sessionShowResult(nativeID, out string, err error) (bool, error) {
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "ID:" && fields[1] == nativeID {
				return true, nil
			}
		}
		return false, fmt.Errorf("stakpak: sessions show %s printed no matching ID", nativeID)
	}
	lower := strings.ToLower(out)
	missing := strings.Contains(lower, "not found") || strings.Contains(out, "API error 404")
	// "Profile '<name>' not found in configuration" (an empty or deleted
	// STAKPAK_PROFILE) is a profile Stakpak cannot load, not a missing
	// conversation: a restore must fail rather than discard the history.
	if missing && !strings.Contains(lower, "not found in configuration") {
		return false, nil
	}
	return false, fmt.Errorf("stakpak: sessions show %s: %w", nativeID, err)
}
