package kilocode

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Reserve the private export before the native CLI can publish its IDs. This
// survives a lost S2 identity checkpoint without copying the provider database
// or persisting launch environment, settings, or credentials.
type kiloImportReservation struct {
	SourceID          string          `json:"sourceId"`
	ConfigDir         string          `json:"configDir"`
	CWD               string          `json:"cwd"`
	SourceFingerprint string          `json:"sourceFingerprint"`
	SourceDigest      [32]byte        `json:"sourceDigest"`
	NativeID          string          `json:"nativeId"`
	Export            json.RawMessage `json:"export"`
}

const kiloReservationLimit = (64 << 20) + (16 << 10)

type kiloImportPart struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	State     struct {
		Status      string `json:"status"`
		Attachments []struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
		} `json:"attachments"`
	} `json:"state"`
}

type kiloImportHistory struct {
	Info struct {
		ID          string `json:"id"`
		Directory   string `json:"directory"`
		WorkspaceID string `json:"workspaceID"`
	} `json:"info"`
	Messages []struct {
		Info struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		} `json:"info"`
		Parts []kiloImportPart `json:"parts"`
	} `json:"messages"`
}

func kiloReservationName(config, source, cwd, fingerprint string) string {
	binding, _ := json.Marshal([]string{config, source, cwd, fingerprint})
	return fmt.Sprintf("%x.json", sha256.Sum256(binding))
}

// kiloReservationGenerations bounds stale-reservation rebuilds in one attempt.
const kiloReservationGenerations = 16

// kiloSuccessorReservationName names the write-once reservation that replaces
// an unpublished one whose source advanced. It is keyed by the stale native ID
// under the original binding, so every retry follows the same chain.
func kiloSuccessorReservationName(base, staleNativeID string) string {
	return strings.TrimSuffix(base, ".json") + "." + staleNativeID + ".json"
}

var kiloReservationLocks sync.Map

// lockKiloReservation serializes preparation of one binding in this daemon so
// a rebuild never races another attempt's native import of the stale graph.
func lockKiloReservation(path string) func() {
	value, _ := kiloReservationLocks.LoadOrStore(path, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func readKiloReservation(ctx context.Context, root *os.Root, name string) (kiloImportReservation, error) {
	var r kiloImportReservation
	if err := ctx.Err(); err != nil {
		return r, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > kiloReservationLimit {
		return r, errors.New("kilo import reservation is not a supported private file")
	}
	f, err := root.Open(name)
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, kiloReservationLimit+1))
	if err != nil {
		return r, err
	}
	if len(body) > kiloReservationLimit || json.Unmarshal(body, &r) != nil || len(r.Export) > 64<<20 {
		return r, errors.New("invalid kilo import reservation")
	}
	return r, ctx.Err()
}

func (r kiloImportReservation) history() (kiloImportHistory, []string, error) {
	var history kiloImportHistory
	if json.Unmarshal(r.Export, &history) != nil || history.Info.ID != r.NativeID || r.NativeID == r.SourceID || history.Info.Directory != r.CWD || history.Info.WorkspaceID != "" || len(history.Messages) == 0 {
		return history, nil, errors.New("kilo reservation has mismatched native history")
	}
	ids := make([]string, 0, 1+len(history.Messages))
	seen := map[string]bool{r.SourceID: true}
	add := func(id, prefix string) bool {
		if len(id) != 30 || id[:4] != prefix+"_" || seen[id] {
			return false
		}
		seen[id] = true
		ids = append(ids, id)
		return true
	}
	if !add(r.NativeID, "ses") {
		return history, nil, errors.New("invalid kilo reservation session identity")
	}
	for _, message := range history.Messages {
		if message.Info.SessionID != r.NativeID || !add(message.Info.ID, "msg") {
			return history, nil, errors.New("invalid kilo reservation message identity")
		}
		for _, part := range message.Parts {
			if part.SessionID != r.NativeID || part.MessageID != message.Info.ID || !add(part.ID, "prt") {
				return history, nil, errors.New("invalid kilo reservation part identity")
			}
			if part.State.Status == "completed" {
				for _, attachment := range part.State.Attachments {
					if attachment.SessionID != r.NativeID || attachment.MessageID != message.Info.ID || !add(attachment.ID, "prt") {
						return history, nil, errors.New("invalid kilo reservation attachment identity")
					}
				}
			}
		}
	}
	return history, ids, nil
}

func kiloImportIDExists(ctx context.Context, db *sql.DB, id string) (bool, error) {
	var present bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT id FROM session WHERE id = ? UNION ALL SELECT id FROM message WHERE id = ? UNION ALL SELECT id FROM part WHERE id = ? UNION ALL SELECT id FROM session_message WHERE id = ?)`, id, id, id, id).Scan(&present)
	return present, err
}

func verifyKiloPublishedImport(ctx context.Context, db *sql.DB, nativeID, cwd string, expected kiloImportHistory) error {
	var directory string
	var workspace sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT directory, workspace_id FROM session WHERE id = ?`, nativeID).Scan(&directory, &workspace); err != nil {
		return fmt.Errorf("confirm imported kilo identity: %w", err)
	}
	if filepath.Clean(directory) != cwd || (workspace.Valid && workspace.String != "") {
		return errors.New("imported kilo execution location does not match the worktree")
	}
	for _, message := range expected.Messages {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE id = ? AND session_id = ?`, message.Info.ID, nativeID).Scan(&count); err != nil || count != 1 {
			return errors.New("imported kilo message history was not confirmed")
		}
		for _, part := range message.Parts {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM part WHERE id = ? AND session_id = ? AND message_id = ?`, part.ID, nativeID, message.Info.ID).Scan(&count); err != nil || count != 1 {
				return errors.New("imported kilo part history was not confirmed")
			}
		}
	}
	return nil
}
