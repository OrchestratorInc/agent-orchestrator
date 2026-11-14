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
)

const openCodeFingerprintVersion = "opencode-v2:"

var (
	openCodeCopiedMessageKeys = []string{"id", "sessionID", "parentID", "path"}
	openCodeCopiedPartKeys    = []string{"id", "sessionID", "messageID"}
)

func NativeSessionFingerprint(ctx context.Context, db *sql.DB, sessionID string) (string, error) {
	parts := map[string][]string{}
	partRows, err := db.QueryContext(ctx, `SELECT message_id, data FROM part WHERE session_id = ?`, sessionID)
	if err != nil {
		return "", fmt.Errorf("opencode: read session parts: %w", err)
	}
	defer func() { _ = partRows.Close() }()
	for partRows.Next() {
		var message, data string
		if err := partRows.Scan(&message, &data); err != nil {
			return "", fmt.Errorf("opencode: read session parts: %w", err)
		}
		canonical, err := canonicalOpenCodeData(data, openCodeCopiedPartKeys)
		if err != nil {
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

func openCodeSessionFingerprint(ctx context.Context, configDir, id string) (string, error) {
	db, err := openOpenCodeStore(configDir)
	if err != nil || db == nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	return NativeSessionFingerprint(ctx, db, id)
}
