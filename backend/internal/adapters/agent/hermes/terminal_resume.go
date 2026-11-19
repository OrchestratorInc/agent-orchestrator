package hermes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PrepareNativeTerminalResume materializes the terminal's resume identity for a
// native conversation that a Chat (ACP) session created. The terminal restores
// through `hermes chat --continue <title>`, keyed by the AO-recorded native
// title that only terminal launches write — so a conversation that has lived
// only in Chat has no resume identity yet. Reading the conversation's own row
// from Hermes' state.db by its exact id, verifying it belongs to this session's
// workspace and resolves back unambiguously by title, then persisting that
// title, lets the ordinary GetRestoreCommand path resume the very conversation
// Chat was using. found=false reports no safely resumable conversation
// (unknown id, foreign workspace, missing or ambiguous title) so the caller
// starts fresh instead of resuming a stranger.
func (p *Plugin) PrepareNativeTerminalResume(ctx context.Context, dataDir, aoSessionID, nativeConversationID, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id := strings.TrimSpace(nativeConversationID)
	if id == "" || id == "latest" || strings.ContainsAny(id, " /\\\r\n\t") {
		return false, nil
	}
	home, err := nativeHome(dataDir, aoSessionID)
	if err != nil {
		// No recorded native home means no native conversation of this
		// session's own to resume — a fresh start, not a failure.
		return false, nil
	}
	db, err := sql.Open("sqlite", nativeStateDSN(home))
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	var title, cwd string
	err = db.QueryRowContext(ctx,
		"SELECT COALESCE(title,''), COALESCE(cwd,'') FROM sessions WHERE id=?", id,
	).Scan(&title, &cwd)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("hermes: native state database unavailable: %w", err)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return false, nil
	}
	if workspacePath != "" && filepath.Clean(cwd) != filepath.Clean(workspacePath) {
		return false, nil
	}
	// The restore resolves the conversation by title, taking the oldest row.
	// If that is not this exact conversation, the title is ambiguous and
	// resuming it could land in a stranger's transcript: start fresh instead.
	var byTitle string
	if err := db.QueryRowContext(ctx,
		"SELECT id FROM sessions WHERE title=? ORDER BY started_at LIMIT 1", title,
	).Scan(&byTitle); err != nil {
		return false, fmt.Errorf("hermes: native state database unavailable: %w", err)
	}
	if byTitle != id {
		return false, nil
	}
	dir := sessionDir(dataDir, aoSessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(dir, "native-title"), []byte(title), 0o600); err != nil {
		return false, err
	}
	return true, nil
}
