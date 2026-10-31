package usage

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
	"strings"
)

// jcodeSessionIDPattern is jcode's session id: session_<word>_<millis>_<16 hex>.
var jcodeSessionIDPattern = regexp.MustCompile(`^session_[a-z0-9]+_\d+_[0-9a-f]{16}$`)

// maxJCodeSessionBytes bounds one read of a native session file, which holds
// the whole transcript including tool output.
const maxJCodeSessionBytes = 64 << 20

type jcodeNativeMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Timestamp  string          `json:"timestamp"`
	TokenUsage json.RawMessage `json:"token_usage"`
}

type jcodeNativeSession struct {
	ID          string               `json:"id"`
	Model       string               `json:"model"`
	ProviderKey string               `json:"provider_key"`
	Messages    []jcodeNativeMessage `json:"messages"`
}

// jcodeJournalEntry is one line of sessions/<id>.journal.jsonl. Between
// checkpoints jcode appends each save's new messages there instead of
// rewriting the snapshot, so a finished turn can exist only in the journal.
type jcodeJournalEntry struct {
	Meta struct {
		Model       *string `json:"model"`
		ProviderKey *string `json:"provider_key"`
	} `json:"meta"`
	AppendMessages []jcodeNativeMessage `json:"append_messages"`
}

type jcodeLedgerLine struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	Timestamp  string          `json:"timestamp"`
	Model      string          `json:"model"`
	Provider   string          `json:"provider"`
	TokenUsage json.RawMessage `json:"token_usage"`
}

// syncJCodeLedger appends to AO's ledger every assistant message of the native
// session whose token_usage is not already there, and returns the ledger path.
// jcode keeps a session as a snapshot (sessions/<id>.json) plus a journal of
// later saves (sessions/<id>.journal.jsonl) that a checkpoint folds back into
// the snapshot; both are read. A resumed session lists all earlier messages
// again and a journal may repeat snapshot messages, so messages are matched by
// their native id, which jcode keeps unique per session: a
// message is exported once, whatever happens to the native file. A message is
// attributed to the session's model at export time; exports run on every hook,
// so that is the model of the turn that just ran.
func (c *Collector) syncJCodeLedger(ctx context.Context, nativeID string) (string, error) {
	if !jcodeSessionIDPattern.MatchString(nativeID) || c.roots.JCodeHome == "" || c.roots.JCodeLedger == "" {
		return "", nil
	}
	native, err := readJCodeSession(filepath.Join(c.roots.JCodeHome, "sessions", nativeID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read jcode session: %w", err)
	}
	if native.ID != nativeID {
		return "", nil
	}
	if err := applyJCodeJournal(filepath.Join(c.roots.JCodeHome, "sessions", nativeID+".journal.jsonl"), &native); err != nil {
		return "", fmt.Errorf("read jcode session journal: %w", err)
	}
	if err := os.MkdirAll(c.roots.JCodeLedger, 0o700); err != nil {
		return "", fmt.Errorf("create jcode usage ledger dir: %w", err)
	}
	path := filepath.Join(c.roots.JCodeLedger, nativeID+".jsonl")
	exported, err := ledgerMessageIDs(path)
	if err != nil {
		return "", err
	}
	var lines bytes.Buffer
	for _, message := range native.Messages {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		id := strings.TrimSpace(message.ID)
		if message.Role != "assistant" || id == "" || exported[id] || !jsonValuePresent(message.TokenUsage) {
			continue
		}
		line, err := json.Marshal(jcodeLedgerLine{
			Type:       "jcode.usage",
			ID:         id,
			SessionID:  nativeID,
			Timestamp:  message.Timestamp,
			Model:      native.Model,
			Provider:   native.ProviderKey,
			TokenUsage: message.TokenUsage,
		})
		if err != nil {
			return "", err
		}
		lines.Write(line)
		lines.WriteByte('\n')
		exported[id] = true
	}
	if lines.Len() == 0 {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			// Register the source only once there is usage to read.
			return "", nil
		}
		return path, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // AO-owned ledger path from a validated id
	if err != nil {
		return "", fmt.Errorf("open jcode usage ledger: %w", err)
	}
	if _, err := file.Write(lines.Bytes()); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("append jcode usage ledger: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func readJCodeSession(path string) (jcodeNativeSession, error) {
	file, err := os.Open(path) //nolint:gosec // jcode's own session file for a validated id
	if err != nil {
		return jcodeNativeSession{}, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxJCodeSessionBytes+1))
	if err != nil {
		return jcodeNativeSession{}, err
	}
	if len(data) > maxJCodeSessionBytes {
		return jcodeNativeSession{}, errors.New("jcode session file exceeds the usage read limit")
	}
	var session jcodeNativeSession
	if err := json.Unmarshal(data, &session); err != nil {
		// jcode may be mid-write; the next hook retries.
		return jcodeNativeSession{}, nil
	}
	return session, nil
}

// applyJCodeJournal adds the journal's messages to the snapshot's and takes
// the latest model and provider it records. An unreadable line, such as one
// jcode is still writing, is skipped; the next hook reads it again.
func applyJCodeJournal(path string, session *jcodeNativeSession) error {
	file, err := os.Open(path) //nolint:gosec // jcode's own journal for a validated id
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(io.LimitReader(file, maxJCodeSessionBytes))
	for {
		line, readErr := reader.ReadBytes('\n')
		var entry jcodeJournalEntry
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &entry) == nil {
			session.Messages = append(session.Messages, entry.AppendMessages...)
			if entry.Meta.Model != nil && strings.TrimSpace(*entry.Meta.Model) != "" {
				session.Model = *entry.Meta.Model
			}
			if entry.Meta.ProviderKey != nil && strings.TrimSpace(*entry.Meta.ProviderKey) != "" {
				session.ProviderKey = *entry.Meta.ProviderKey
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func ledgerMessageIDs(path string) (map[string]bool, error) {
	ids := map[string]bool{}
	file, err := os.Open(path) //nolint:gosec // AO-owned ledger path
	if errors.Is(err, os.ErrNotExist) {
		return ids, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var line struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line.ID != "" {
			ids[line.ID] = true
		}
	}
	return ids, scanner.Err()
}

func jsonValuePresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
