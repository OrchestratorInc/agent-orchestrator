package opencode

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
)

// openCodeFingerprintVersion names the fingerprint's inputs, so a stored
// fingerprint from another definition never matches by accident.
const openCodeFingerprintVersion = "opencode-v1:"

// NativeSessionFingerprints pins the history discovery read for each session.
// It covers only what an `opencode export`/`import` copy preserves: each
// message's creation time and role, and its parts' count and text length.
// Copies get new ids and part timestamps, so those are left out, and messages
// are hashed as a sorted set because copied ids may reorder equal timestamps.
// An empty sessionID fingerprints every session in one pass.
func NativeSessionFingerprints(ctx context.Context, db *sql.DB, sessionID string) (map[string]string, error) {
	query := `
		SELECT m.session_id, m.time_created, COALESCE(json_extract(m.data, '$.role'), ''),
		       (SELECT COUNT(*) FROM part p WHERE p.message_id = m.id),
		       (SELECT COALESCE(SUM(length(COALESCE(json_extract(p.data, '$.text'), ''))), 0)
		          FROM part p WHERE p.message_id = m.id)
		FROM message m`
	var args []any
	if sessionID != "" {
		query += ` WHERE m.session_id = ?`
		args = append(args, sessionID)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("opencode: read session history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	signatures := map[string][]string{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var session, role string
		var created, parts, textBytes int64
		if err := rows.Scan(&session, &created, &role, &parts, &textBytes); err != nil {
			return nil, fmt.Errorf("opencode: read session history: %w", err)
		}
		signatures[session] = append(signatures[session], strconv.FormatInt(created, 10)+"\x00"+role+"\x00"+
			strconv.FormatInt(parts, 10)+"\x00"+strconv.FormatInt(textBytes, 10))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("opencode: read session history: %w", err)
	}
	out := make(map[string]string, len(signatures))
	for session, messages := range signatures {
		slices.Sort(messages)
		sum := sha256.New()
		for _, message := range messages {
			sum.Write([]byte(message))
			sum.Write([]byte{'\n'})
		}
		out[session] = openCodeFingerprintVersion + strconv.Itoa(len(messages)) + ":" + hex.EncodeToString(sum.Sum(nil))
	}
	return out, nil
}

// openCodeSessionFingerprint fingerprints one session in the store at
// configDir, read-only. A session without messages has no fingerprint.
func openCodeSessionFingerprint(ctx context.Context, configDir, id string) (string, error) {
	db, err := openOpenCodeStore(configDir)
	if err != nil || db == nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	prints, err := NativeSessionFingerprints(ctx, db, id)
	if err != nil {
		return "", err
	}
	return prints[id], nil
}
