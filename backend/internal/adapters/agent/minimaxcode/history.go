package minimaxcode

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // Register the driver for read-only native session validation.
)

const maxHistoryManifestBytes = 1 << 20

func validateNativeHistory(ctx context.Context, profile, id, workspace string) error {
	dbPath := filepath.Join(profile, "v2", "sqlite", "runtime-state.sqlite")
	info, err := os.Lstat(dbPath)
	if err != nil {
		return errors.New("native session database is unavailable")
	}
	if !info.Mode().IsRegular() {
		return errors.New("native database is not a regular file")
	}
	// Escape the literal filename without turning a Windows drive into a URI authority.
	uri := url.URL{Path: dbPath}
	db, err := sql.Open("sqlite", "file:"+uri.EscapedPath()+"?mode=ro")
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
	manifestPath := filepath.Join(history, "manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > maxHistoryManifestBytes {
		return errors.New("native history manifest is unavailable or outside supported bounds")
	}
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return errors.New("native history manifest is unavailable")
	}
	defer func() { _ = manifestFile.Close() }()
	openedManifest, err := manifestFile.Stat()
	if err != nil || !os.SameFile(manifestInfo, openedManifest) || !openedManifest.Mode().IsRegular() {
		return errors.New("native history manifest changed during validation")
	}
	data, err := io.ReadAll(io.LimitReader(manifestFile, maxHistoryManifestBytes+1))
	if err != nil || len(data) > maxHistoryManifestBytes {
		return errors.New("native history manifest is unavailable or outside supported bounds")
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
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Summary *string         `json:"summary"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &row) != nil || row.ID == "" || row.TurnID == "" {
		return false
	}
	if row.Message.Role == "compactionSummary" {
		return row.Message.Summary != nil && *row.Message.Summary != "" && len(row.Message.Content) == 0
	}
	var content []json.RawMessage
	if json.Unmarshal(row.Message.Content, &content) != nil || len(content) == 0 {
		return false
	}
	switch row.Message.Role {
	case "user", "assistant", "toolResult":
	default:
		return false
	}
	for _, part := range content {
		var item struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(part, &item) != nil || item.Type == "" {
			return false
		}
	}
	return true
}
