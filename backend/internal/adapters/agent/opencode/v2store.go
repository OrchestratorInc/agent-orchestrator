package opencode

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

// OpenCode 2 keeps the same opencode.db as OpenCode 1 and upgrades it in place:
// it adds session_v2 and session_message and from then on writes only those.
// The OpenCode 1 session and message tables stay behind, frozen at the
// upgrade, so once session_v2 exists they no longer hold the current history.

// v2FingerprintVersion names the scheme, so a changed scheme never matches an
// old fingerprint.
const v2FingerprintVersion = "opencode-v2-store:"

// HasV2Store reports whether db has been upgraded by OpenCode 2.
func HasV2Store(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_v2'`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("opencode: inspect store schema: %w", err)
	}
	return n > 0, nil
}

// V2SessionFingerprint hashes a session's messages in order. It covers each
// message's type and stored data but not its id or sequence number, which an
// `opencode session import` copy renumbers, so a faithful copy has the same
// fingerprint as its source. A session with no messages has none.
func V2SessionFingerprint(ctx context.Context, db *sql.DB, sessionID string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, data FROM session_message WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return "", fmt.Errorf("opencode: read session messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	h := sha256.New()
	n := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var kind, data string
		if err := rows.Scan(&kind, &data); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s\x00%d:%s\x00", len(kind), kind, len(data), data)
		n++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if n == 0 {
		return "", errors.New("opencode: the session has no messages")
	}
	return v2FingerprintVersion + hex.EncodeToString(h.Sum(nil)), nil
}

// openCodeID matches an OpenCode row id such as ses_… or msg_….
var openCodeID = regexp.MustCompile(`^[a-z]+_[A-Za-z0-9]+$`)

// DerivedID returns a new OpenCode id with old's prefix and time component and
// a tail derived from salt, so the same inputs always give the same id.
func DerivedID(old, salt string) (string, error) {
	if !openCodeID.MatchString(old) || len(old) <= openCodeIDTimeLen {
		return "", fmt.Errorf("opencode: unexpected id %q", old)
	}
	return derivedOpenCodeID(old, salt), nil
}

// RunCommand runs the opencode binary in dir with env and returns its stdout,
// bounded like an export.
func RunCommand(ctx context.Context, binary, dir string, env []string, args ...string) ([]byte, error) {
	return runOpenCode(ctx, binary, dir, env, args...)
}
