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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// jcodeSessionIDPattern is jcode's session id: session_<word>_<millis>_<16 hex>.
var jcodeSessionIDPattern = regexp.MustCompile(`^session_[a-z0-9]+_\d+_[0-9a-f]{16}$`)

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
func (c *Collector) syncJCodeLedger(ctx context.Context, nativeID string, sessionID domain.SessionID) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.jcodeMu.Lock()
	defer c.jcodeMu.Unlock()
	if !jcodeSessionIDPattern.MatchString(nativeID) || c.roots.JCodeLedger == "" {
		return "", nil
	}
	home, err := c.jcodeHomeForSession(ctx, sessionID)
	if err != nil || home == "" {
		return "", err
	}
	snapshotPath := filepath.Join(home, "sessions", nativeID+".json")
	journalPath := filepath.Join(home, "sessions", nativeID+".journal.jsonl")
	path := filepath.Join(c.roots.JCodeLedger, nativeID+".jsonl")
	snapshotInfo, err := os.Stat(snapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read jcode session: %w", err)
	}
	journalInfo, err := jcodeFileInfo(ctx, journalPath)
	if err != nil {
		return "", err
	}
	ledgerInfo, err := jcodeFileInfo(ctx, path)
	if err != nil {
		return "", err
	}
	state := c.jcodeReads[snapshotPath]
	if state != nil && state.synced && sameJCodeFile(state.snapshotInfo, snapshotInfo) &&
		sameJCodeFile(state.journalInfo, journalInfo) && sameJCodeFile(state.ledgerInfo, ledgerInfo) {
		if ledgerInfo == nil {
			return "", nil
		}
		return path, nil
	}
	if state == nil || !sameJCodeFile(state.snapshotInfo, snapshotInfo) {
		native, err := readJCodeSession(ctx, snapshotPath)
		if err != nil {
			return "", fmt.Errorf("read jcode session: %w", err)
		}
		if native.ID != nativeID {
			return "", nil
		}
		state = &jcodeReadState{snapshotInfo: snapshotInfo, snapshot: native, native: cloneJCodeSession(native)}
	}
	state.synced = false
	if state.journalInfo != nil && (journalInfo == nil || !os.SameFile(state.journalInfo, journalInfo) || journalInfo.Size() < state.journalOffset) {
		state.native = cloneJCodeSession(state.snapshot)
		state.journalOffset = 0
	}
	state.journalOffset, err = applyJCodeJournal(ctx, journalPath, &state.native, state.journalOffset)
	if err != nil {
		return "", fmt.Errorf("read jcode session journal: %w", err)
	}
	state.journalInfo = journalInfo
	if err := os.MkdirAll(c.roots.JCodeLedger, 0o700); err != nil {
		return "", fmt.Errorf("create jcode usage ledger dir: %w", err)
	}
	if state.exported == nil || !sameJCodeFile(state.ledgerInfo, ledgerInfo) {
		state.exported, err = ledgerMessageIDs(path)
		if err != nil {
			return "", err
		}
	}
	exported := state.exported
	native := state.native
	var lines bytes.Buffer
	pending := map[string]bool{}
	for _, message := range native.Messages {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		id := strings.TrimSpace(message.ID)
		if message.Role != "assistant" || id == "" || exported[id] || pending[id] || !jsonValuePresent(message.TokenUsage) {
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
		pending[id] = true
	}
	remember := func() error {
		info, err := jcodeFileInfo(ctx, path)
		if err != nil {
			return err
		}
		state.ledgerInfo, state.synced = info, true
		if c.jcodeReads == nil {
			c.jcodeReads = map[string]*jcodeReadState{}
		}
		c.jcodeReads[snapshotPath] = state
		return nil
	}
	if lines.Len() == 0 {
		if err := remember(); err != nil {
			return "", err
		}
		if state.ledgerInfo == nil {
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
		state.exported = nil // a partial write must be reconciled from disk
		return "", fmt.Errorf("append jcode usage ledger: %w", err)
	}
	if err := file.Close(); err != nil {
		state.exported = nil
		return "", err
	}
	for id := range pending {
		exported[id] = true
	}
	return path, remember()
}

// jcodeHomeForSession mirrors the project override used by the native launch.
// JCode's runtime augmentation changes sockets and hooks, not JCODE_HOME. Only
// AO's exported ledger is registered as an ingestor source; native roots do not
// widen the ledger allow-list.
func (c *Collector) jcodeHomeForSession(ctx context.Context, id domain.SessionID) (string, error) {
	if id == "" {
		return c.roots.JCodeHome, nil
	}
	session, ok, err := c.store.GetSession(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrUsageSessionNotFound
	}
	if session.ProjectID == "" {
		return c.roots.JCodeHome, nil
	}
	project, ok, err := c.store.GetProject(ctx, string(session.ProjectID))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("jcode usage project %s is missing", session.ProjectID)
	}
	if home, configured := project.Config.Env["JCODE_HOME"]; configured {
		if home = strings.TrimSpace(home); home != "" {
			return home, nil
		}
		home, err := os.UserHomeDir()
		return filepath.Join(home, ".jcode"), err
	}
	return c.roots.JCodeHome, nil
}

func readJCodeSession(ctx context.Context, path string) (jcodeNativeSession, error) {
	file, err := os.Open(path) //nolint:gosec // jcode's own session file for a validated id
	if err != nil {
		return jcodeNativeSession{}, err
	}
	defer func() { _ = file.Close() }()
	session, err := decodeJCodeSession(ctx, json.NewDecoder(file))
	var syntax *json.SyntaxError
	var shape *json.UnmarshalTypeError
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errJCodeSnapshotShape) || errors.As(err, &syntax) || errors.As(err, &shape) {
		// jcode may be mid-write; the next hook retries.
		return jcodeNativeSession{}, nil
	}
	return session, err
}

// applyJCodeJournal adds the journal's messages to the snapshot's and takes
// the latest model and provider it records. An incomplete final line keeps its
// cursor position so the next hook can retry after jcode finishes writing it.
func applyJCodeJournal(ctx context.Context, path string, session *jcodeNativeSession, offset int64) (int64, error) {
	file, err := os.Open(path) //nolint:gosec // jcode's own journal for a validated id
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return offset, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	reader := bufio.NewReader(file)
	for {
		if err := ctx.Err(); err != nil {
			return offset, err
		}
		line, readErr := reader.ReadBytes('\n')
		var entry jcodeJournalEntry
		valid := len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &entry) == nil
		if valid {
			for _, message := range entry.AppendMessages {
				if message.Role == "assistant" && jsonValuePresent(message.TokenUsage) {
					session.Messages = append(session.Messages, message)
				}
			}
			if entry.Meta.Model != nil && strings.TrimSpace(*entry.Meta.Model) != "" {
				session.Model = *entry.Meta.Model
			}
			if entry.Meta.ProviderKey != nil && strings.TrimSpace(*entry.Meta.ProviderKey) != "" {
				session.ProviderKey = *entry.Meta.ProviderKey
			}
		}
		if valid || readErr == nil {
			offset += int64(len(line))
		}
		if errors.Is(readErr, io.EOF) {
			return offset, nil
		}
		if readErr != nil {
			return offset, readErr
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
