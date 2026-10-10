package minimaxcode

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // Register the driver for read-only native session validation.
)

func validateNativeHistory(ctx context.Context, profile, id, workspace string) error {
	dbPath := filepath.Join(profile, "v2", "sqlite", "runtime-state.sqlite")
	info, err := os.Lstat(dbPath)
	if err != nil {
		return errors.New("native session database is unavailable")
	}
	if !info.Mode().IsRegular() {
		return errors.New("native database is not a regular file")
	}
	uri := url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return errors.New("native session database could not be opened")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var cwd, relative string
	var archived int
	err = db.QueryRowContext(ctx, `SELECT workspace_dir, history_relative_dir, archived FROM local_runtime_sessions WHERE session_id=?`, id).Scan(&cwd, &relative, &archived)
	if err != nil {
		return errors.New("exact native conversation is missing or unreadable")
	}
	actual, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return errors.New("native workspace is unavailable")
	}
	wanted, err := filepath.EvalSymlinks(workspace)
	if err != nil || actual != wanted {
		return errors.New("native conversation belongs to a different workspace")
	}
	if archived != 0 {
		return errors.New("native conversation is archived")
	}
	if relative == "" || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(filepath.Clean(relative), ".."+string(os.PathSeparator)) {
		return errors.New("native history location is invalid")
	}
	canonicalProfile, err := filepath.EvalSymlinks(profile)
	if err != nil {
		return errors.New("native session root is unavailable")
	}
	history := filepath.Join(canonicalProfile, "v2", "sessions", relative)
	canonicalHistory, err := filepath.EvalSymlinks(history)
	if err != nil {
		return errors.New("native history is unavailable")
	}
	if canonicalHistory != history {
		return errors.New("native history must not traverse symlinks")
	}
	data, err := os.ReadFile(filepath.Join(history, "manifest.json"))
	if err != nil {
		return errors.New("native history manifest is unavailable")
	}
	var manifest struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.SessionID != id {
		return errors.New("native history identity does not match")
	}
	messages := filepath.Join(history, "messages.jsonl")
	st, err := os.Lstat(messages)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 128<<20 {
		return errors.New("native conversation history is missing or outside supported bounds")
	}
	f, err := os.Open(messages)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validHistoryMessage(scanner.Bytes()) {
			return errors.New("native conversation history is malformed")
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("native history cannot be validated: %w", err)
	}
	return nil
}

// MiniMax 0.6.5 stores native message envelopes, not arbitrary JSON values.
// Refuse unknown shapes rather than starting a conversation with lost context.
func validHistoryMessage(data []byte) bool {
	var row struct {
		ID      string `json:"message_id"`
		TurnID  string `json:"turn_id"`
		Message struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &row) != nil || row.ID == "" || row.TurnID == "" || len(row.Message.Content) == 0 {
		return false
	}
	switch row.Message.Role {
	case "user", "assistant", "toolResult":
	default:
		return false
	}
	for _, part := range row.Message.Content {
		var item struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(part, &item) != nil || item.Type == "" {
			return false
		}
	}
	return true
}
