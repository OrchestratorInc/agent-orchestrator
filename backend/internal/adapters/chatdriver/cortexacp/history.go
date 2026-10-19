package cortexacp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// These are admission limits, not native format limits. Oversized/unrecognized
// state stays closed until qualified; the guard never repairs or replays it.
const maxSnapshotBytes = 16 << 20
const maxJournalBytes = 64 << 20
const maxJournalLineBytes = 4 << 20

// validateNativeHistory checks native-owned state before a cold session/load.
// Paths are documented in the official ACP guide. Field aliases and split-file
// precedence follow the cached official decoder (SHA256
// f82b8f5125099ebc3259803a8e26c305ebc6be05437f9355be41a6adea327596).
// This candidate guard still requires current-release authenticated qualification.
// In that decoder caller cwd overrides saved cwd and malformed journal records
// are skipped; neither behavior may silently lose or relocate an AO conversation.
func validateNativeHistory(ctx context.Context, cfg ports.ChatResumeConfig) error {
	fail := func(reason string) error {
		return fmt.Errorf("%w: Cortex native history %s", ports.ErrChatResumeFailed, reason)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	id := cfg.ProviderConversationID
	if id == "" || len(id) > 128 || strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) >= 0 {
		return fail("has an unsafe session identifier")
	}
	root, err := os.OpenRoot(cfg.Env["HOME"])
	if err != nil {
		return fail("profile is unavailable")
	}
	defer root.Close()
	relative := filepath.Join(".snowflake", "cortex", "conversations", id)
	snapshot, err := readNativeFile(root, relative+".json", maxSnapshotBytes)
	if err != nil {
		return fail("snapshot is missing, unreadable, outside the profile, or oversized")
	}
	var saved struct {
		SessionID       string            `json:"sessionId"`
		LegacyID        string            `json:"session_id"`
		Workspace       string            `json:"workingDirectory"`
		LegacyWorkspace string            `json:"working_directory"`
		History         []json.RawMessage `json:"history"`
		Messages        []json.RawMessage `json:"messages"`
		HistoryLength   *int              `json:"history_length"`
	}
	if json.Unmarshal(snapshot, &saved) != nil {
		return fail("snapshot is malformed")
	}
	if saved.SessionID != "" && saved.LegacyID != "" && saved.SessionID != saved.LegacyID {
		return fail("snapshot identity aliases disagree")
	}
	if saved.SessionID == "" {
		saved.SessionID = saved.LegacyID
	}
	if saved.SessionID != id {
		return fail("snapshot identity does not match the requested session")
	}
	if saved.Workspace != "" && saved.LegacyWorkspace != "" && saved.Workspace != saved.LegacyWorkspace {
		return fail("snapshot workspace aliases disagree")
	}
	if saved.Workspace == "" {
		saved.Workspace = saved.LegacyWorkspace
	}
	stored, storedErr := filepath.EvalSymlinks(saved.Workspace)
	requested, requestedErr := filepath.EvalSymlinks(cfg.WorkspacePath)
	if !filepath.IsAbs(saved.Workspace) || storedErr != nil || requestedErr != nil || stored != requested {
		return fail("workspace does not match the requested workspace")
	}
	if saved.HistoryLength != nil && *saved.HistoryLength < 0 {
		return fail("snapshot history length is invalid")
	}
	// The native reader gives an existing journal precedence over inline history.
	// Validate every record instead of accepting native skip-on-corruption behavior.
	journalName := relative + ".history.jsonl"
	journalInfo, err := root.Lstat(journalName)
	if err == nil && !journalInfo.Mode().IsRegular() {
		return fail("journal is not a regular native file")
	}
	var journal *os.File
	if err == nil {
		journal, err = root.Open(journalName)
	}
	count := 0
	if err == nil {
		defer journal.Close()
		info, statErr := journal.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxJournalBytes {
			return fail("journal is not a bounded regular file")
		}
		messageIDs := make(map[string]struct{})
		var flatJournal *bool
		scanner := bufio.NewScanner(io.LimitReader(journal, maxJournalBytes+1))
		scanner.Buffer(make([]byte, 4096), maxJournalLineBytes)
		consumed := 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			consumed += len(scanner.Bytes()) + 1
			if consumed > maxJournalBytes {
				return fail("journal exceeds the admission limit")
			}
			if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
				continue
			}
			var framing struct {
				MessageID *string `json:"message_id"`
				Format    string  `json:"transcript_format"`
			}
			if json.Unmarshal(scanner.Bytes(), &framing) != nil {
				return fail("journal framing is malformed")
			}
			flat := framing.MessageID != nil || framing.Format == "flat"
			if flat && (framing.MessageID == nil || *framing.MessageID == "") {
				return fail("flat journal record has no message identity")
			}
			if flatJournal != nil && *flatJournal != flat {
				return fail("journal mixes incompatible native formats")
			}
			flatJournal = &flat
			id, valid := nativeMessageIdentity(scanner.Bytes(), false)
			if !valid {
				return fail("journal contains a malformed record")
			}
			messageIDs[id] = struct{}{}
		}
		count = len(messageIDs)
		if scanner.Err() != nil {
			return fail("journal is unreadable or contains an oversized record")
		}
	} else if os.IsNotExist(err) {
		history := saved.History
		legacy := len(history) == 0 && len(saved.Messages) > 0
		if legacy {
			history = saved.Messages
		}
		for _, message := range history {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, valid := nativeMessageIdentity(message, legacy); !valid {
				return fail("snapshot contains a malformed history record")
			}
			count++
		}
		if saved.HistoryLength != nil && *saved.HistoryLength > count {
			return fail("split journal is missing")
		}
	} else {
		return fail("journal is unreadable or outside the profile")
	}
	if saved.HistoryLength != nil && *saved.HistoryLength > count {
		return fail("history is shorter than the saved snapshot")
	}
	if cfg.HasPriorTurns && count == 0 {
		return fail("has no records for a conversation with prior turns")
	}
	return ctx.Err()
}

func readNativeFile(root *os.Root, name string, limit int64) ([]byte, error) {
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular native file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds admission limit")
	}
	return data, nil
}

func nativeMessageIdentity(data []byte, legacy bool) (string, bool) {
	var message struct {
		ID        string          `json:"id"`
		MessageID string          `json:"message_id"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(data, &message) != nil || message.Role == "" || (!legacy && message.ID == "") {
		return "", false
	}
	if legacy {
		var text string
		return message.ID, json.Unmarshal(message.Content, &text) == nil && string(message.Content) != "null"
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(message.Content, &content) == nil && content != nil {
		for _, block := range content {
			var kind string
			if json.Unmarshal(block["type"], &kind) != nil || kind == "" {
				return "", false
			}
		}
		if message.MessageID != "" {
			return message.MessageID, true
		}
		return message.ID, true
	}
	return "", false
}
