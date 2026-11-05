package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
)

// openCodeFingerprintVersion names the fingerprint's inputs, so a stored
// fingerprint from another definition never matches by accident.
const openCodeFingerprintVersion = "opencode-v2:"

// Keys an `opencode export`/`import` copy changes, so they are left out: ids
// and the folder the copy is bound to. Part timestamps live only in table
// columns, which are not hashed; message times are part of the hashed data.
var (
	openCodeCopiedMessageKeys = []string{"id", "sessionID", "parentID", "path"}
	openCodeCopiedPartKeys    = []string{"id", "sessionID", "messageID"}
)

// NativeSessionFingerprints pins the history discovery read for each session:
// every message's and part's stored content in canonical JSON, minus only what
// a copy relocates. Messages and their parts are hashed as sorted sets because
// a copy's new ids may reorder equal timestamps. An empty sessionID covers
// every session, reusing a cached result while a session's stamps are unchanged.
func NativeSessionFingerprints(ctx context.Context, db *sql.DB, sessionID string) (map[string]string, error) {
	if sessionID != "" {
		fp, err := openCodeContentFingerprint(ctx, db, sessionID)
		if err != nil || fp == "" {
			return map[string]string{}, err
		}
		return map[string]string{sessionID: fp}, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT s.id,
		       (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id),
		       (SELECT COALESCE(MAX(m.time_updated), 0) FROM message m WHERE m.session_id = s.id),
		       (SELECT COUNT(*) FROM part p WHERE p.session_id = s.id),
		       (SELECT COALESCE(MAX(p.time_updated), 0) FROM part p WHERE p.session_id = s.id)
		FROM session s`)
	if err != nil {
		return nil, fmt.Errorf("opencode: read session stamps: %w", err)
	}
	type stamped struct{ id, stamp string }
	var sessions []stamped
	for rows.Next() {
		var id string
		var messages, messageUpdated, parts, partUpdated int64
		if err := rows.Scan(&id, &messages, &messageUpdated, &parts, &partUpdated); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("opencode: read session stamps: %w", err)
		}
		if messages == 0 {
			continue
		}
		sessions = append(sessions, stamped{id: id, stamp: fmt.Sprintf("%d:%d:%d:%d", messages, messageUpdated, parts, partUpdated)})
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, fmt.Errorf("opencode: read session stamps: %w", err)
	}
	out := make(map[string]string, len(sessions))
	for _, s := range sessions {
		if fp, ok := openCodeFingerprintCache.get(s.id, s.stamp); ok {
			out[s.id] = fp
			continue
		}
		fp, err := openCodeContentFingerprint(ctx, db, s.id)
		if err != nil {
			return nil, err
		}
		if fp != "" {
			out[s.id] = fp
			openCodeFingerprintCache.put(s.id, s.stamp, fp)
		}
	}
	return out, nil
}

// openCodeContentFingerprint hashes one session's history. A session without
// messages has no fingerprint.
func openCodeContentFingerprint(ctx context.Context, db *sql.DB, sessionID string) (string, error) {
	parts := map[string][]string{}
	partRows, err := db.QueryContext(ctx, `SELECT message_id, data FROM part WHERE session_id = ?`, sessionID)
	if err != nil {
		return "", fmt.Errorf("opencode: read session parts: %w", err)
	}
	for partRows.Next() {
		var message, data string
		if err := partRows.Scan(&message, &data); err != nil {
			_ = partRows.Close()
			return "", fmt.Errorf("opencode: read session parts: %w", err)
		}
		canonical, err := canonicalOpenCodeData(data, openCodeCopiedPartKeys)
		if err != nil {
			_ = partRows.Close()
			return "", err
		}
		parts[message] = append(parts[message], canonical)
	}
	err = partRows.Err()
	_ = partRows.Close()
	if err != nil {
		return "", fmt.Errorf("opencode: read session parts: %w", err)
	}
	messageRows, err := db.QueryContext(ctx, `SELECT id, time_created, data FROM message WHERE session_id = ?`, sessionID)
	if err != nil {
		return "", fmt.Errorf("opencode: read session messages: %w", err)
	}
	defer func() { _ = messageRows.Close() }()
	var messages []string
	for messageRows.Next() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var id, data string
		var created int64
		if err := messageRows.Scan(&id, &created, &data); err != nil {
			return "", fmt.Errorf("opencode: read session messages: %w", err)
		}
		canonical, err := canonicalOpenCodeData(data, openCodeCopiedMessageKeys)
		if err != nil {
			return "", err
		}
		own := parts[id]
		slices.Sort(own)
		var b bytes.Buffer
		b.WriteString(strconv.FormatInt(created, 10))
		b.WriteByte(0)
		b.WriteString(canonical)
		for _, part := range own {
			b.WriteByte(0)
			b.WriteString(part)
		}
		sum := sha256.Sum256(b.Bytes())
		messages = append(messages, hex.EncodeToString(sum[:]))
	}
	if err := messageRows.Err(); err != nil {
		return "", fmt.Errorf("opencode: read session messages: %w", err)
	}
	if len(messages) == 0 {
		return "", nil
	}
	slices.Sort(messages)
	sum := sha256.New()
	for _, message := range messages {
		sum.Write([]byte(message))
		sum.Write([]byte{'\n'})
	}
	return openCodeFingerprintVersion + strconv.Itoa(len(messages)) + ":" + hex.EncodeToString(sum.Sum(nil)), nil
}

// canonicalOpenCodeData re-encodes stored JSON with sorted keys and exact
// numbers, dropping the top-level keys a copy relocates.
func canonicalOpenCodeData(data string, drop []string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(data)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", fmt.Errorf("opencode: read stored session data: %w", err)
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range drop {
			delete(object, key)
		}
	}
	out, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// openCodeFingerprintCache keeps discovery from rehashing unchanged sessions
// on every scan. Entries are replaced when a session's stamps change.
var openCodeFingerprintCache = &fingerprintCache{entries: map[string]fingerprintEntry{}}

const maxOpenCodeFingerprintCache = 4096

type fingerprintEntry struct{ stamp, fp string }

type fingerprintCache struct {
	mu      sync.Mutex
	entries map[string]fingerprintEntry
}

func (c *fingerprintCache) get(id, stamp string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	return entry.fp, ok && entry.stamp == stamp
}

func (c *fingerprintCache) put(id, stamp, fp string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxOpenCodeFingerprintCache {
		c.entries = map[string]fingerprintEntry{}
	}
	c.entries[id] = fingerprintEntry{stamp: stamp, fp: fp}
}

// openCodeSessionFingerprint fingerprints one session in the store at
// configDir, read-only. A session without messages has no fingerprint.
func openCodeSessionFingerprint(ctx context.Context, configDir, id string) (string, error) {
	db, err := openOpenCodeStore(configDir)
	if err != nil || db == nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	return openCodeContentFingerprint(ctx, db, id)
}
