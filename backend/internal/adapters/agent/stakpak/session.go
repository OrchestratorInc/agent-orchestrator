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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
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
}

// matches reports whether this launch's conversation is nativeID: the one it
// resumed, or the one a fresh launch started (resolved later from the list).
func (r launchRecord) matches(nativeID string) bool {
	return r.NativeID == "" || r.NativeID == nativeID
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
func (p *Plugin) ResolveNativeSessionID(ctx context.Context, cfg ports.NativeSessionResolveConfig) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	record, ok, err := readLaunchRecord(cfg.DataDir, string(cfg.SessionID))
	if err != nil || !ok {
		return "", false, err
	}
	sessions, err := p.listSessions(ctx, nil)
	if err != nil {
		return "", false, err
	}
	id, found := matchLaunch(record, sessions)
	return id, found, nil
}

// matchLaunch picks the conversation for a launch from a newest-first list. A
// conversation started in the TUI after a resume (/new) supersedes the one it
// resumed, so the resumed id is only the fallback.
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
	if newest.ID != "" {
		return newest.ID, true
	}
	if validSessionID(record.NativeID) {
		return record.NativeID, true
	}
	return "", false
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
func (p *Plugin) NativeConversationExists(ctx context.Context, _ ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	nativeID = strings.TrimSpace(nativeID)
	if !validSessionID(nativeID) {
		return false, nil
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
// "ID:" line on success and "Not found: Session <id> not found" otherwise.
func sessionShowResult(nativeID, out string, err error) (bool, error) {
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "ID:" && fields[1] == nativeID {
				return true, nil
			}
		}
		return false, fmt.Errorf("stakpak: sessions show %s printed no matching ID", nativeID)
	}
	if strings.Contains(out, "Session "+nativeID+" not found") {
		return false, nil
	}
	return false, fmt.Errorf("stakpak: sessions show %s: %w", nativeID, err)
}
