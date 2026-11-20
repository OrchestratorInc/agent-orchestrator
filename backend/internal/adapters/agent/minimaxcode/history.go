package minimaxcode

import (
	"bufio"
	"bytes"
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
	// Judge the cleaned path: "exact/../.." escapes as surely as "..". On
	// Windows a drive-relative "C:..\.." is not absolute but still escapes.
	clean := filepath.Clean(relative)
	if relative == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
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
// Roles follow its canonical history validation. Custom records include the
// plugin_hook_context that carries AO's standing instructions.
func validHistoryMessage(data []byte) bool {
	var row struct {
		ID      string `json:"message_id"`
		TurnID  string `json:"turn_id"`
		Message struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			Summary    *string         `json:"summary"`
			CustomType string          `json:"customType"`
			Command    *string         `json:"command"`
			Output     *string         `json:"output"`
			FromID     string          `json:"fromId"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &row) != nil || row.ID == "" || row.TurnID == "" {
		return false
	}
	message := row.Message
	switch message.Role {
	case "compactionSummary":
		return message.Summary != nil && *message.Summary != "" && len(message.Content) == 0
	case "branchSummary":
		return message.Summary != nil && message.FromID != ""
	case "bashExecution":
		return message.Command != nil && message.Output != nil
	case "custom":
		return message.CustomType != "" && (jsonString(message.Content) || contentParts(message.Content, true))
	case "user":
		return jsonString(message.Content) || contentParts(message.Content, false)
	case "assistant", "toolResult":
		return contentParts(message.Content, false)
	default:
		return false
	}
}

func jsonString(raw json.RawMessage) bool {
	var value string
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"' && json.Unmarshal(trimmed, &value) == nil
}

// contentParts reports whether raw is an array of typed content blocks.
func contentParts(raw json.RawMessage, allowEmpty bool) bool {
	trimmed := bytes.TrimSpace(raw)
	var content []json.RawMessage
	if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(trimmed, &content) != nil || (len(content) == 0 && !allowEmpty) {
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
