package sessionimport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Cursor's editor keeps the full copy of each agent chat in its own database,
// User/globalStorage/state.vscdb: the chat under cursorDiskKV
// composerData:<id>, each message under bubbleId:<id>:<message id>, and the
// folder of every chat in the ItemTable entry composer.composerHeaders. Its
// ids are the transcripts' ids. The database is opened read-only and is only
// queried by those exact keys: ItemTable also holds Cursor's sign-in tokens.
const (
	maxCursorBubbleBytes   = 16 << 20
	maxCursorComposerBytes = 64 << 20
)

// cursorEditorState is the editor database's path; tests replace it.
var cursorEditorState = func() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home == "" {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb")
		}
		return ""
	default:
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			if home == "" {
				return ""
			}
			config = filepath.Join(home, ".config")
		}
		return filepath.Join(config, "Cursor", "User", "globalStorage", "state.vscdb")
	}
}

// cursorEditorChat is what the editor database holds for one chat.
type cursorEditorChat struct {
	title     string
	messages  []Message
	shortened bool
}

type cursorBubble struct {
	BubbleID  string          `json:"bubbleId"`
	Type      int             `json:"type"`
	Text      string          `json:"text"`
	CreatedAt json.RawMessage `json:"createdAt"`
}

// readCursorEditorChat reads one chat from the editor database. found is
// false when the database or the chat is missing or unreadable; the
// transcript is then the only copy.
func readCursorEditorChat(ctx context.Context, id string) (chat cursorEditorChat, found bool) {
	path := cursorEditorState()
	if path == "" {
		return chat, false
	}
	db, err := openReadOnlySQLite(path)
	if err != nil || db == nil {
		return chat, false
	}
	defer func() { _ = db.Close() }()
	var body []byte
	if err := db.QueryRowContext(ctx, `SELECT CASE WHEN length(value) <= ? THEN value END FROM cursorDiskKV WHERE key = ?`, maxCursorComposerBytes, "composerData:"+id).Scan(&body); err != nil || body == nil {
		return chat, false
	}
	var data struct {
		Name    string `json:"name"`
		Headers []struct {
			BubbleID string `json:"bubbleId"`
		} `json:"fullConversationHeadersOnly"`
		// Older Cursor versions keep the messages in the chat itself.
		Conversation    []cursorBubble          `json:"conversation"`
		ConversationMap map[string]cursorBubble `json:"conversationMap"`
	}
	if json.Unmarshal(body, &data) != nil {
		return chat, false
	}
	if name := importTitle(data.Name); name != cursorPlaceholderTitle {
		chat.title = name
	}
	bubbles := map[string]cursorBubble{}
	for bubbleID, bubble := range data.ConversationMap {
		bubbles[bubbleID] = bubble
	}
	listed, shortened, err := cursorEditorBubbles(ctx, db, id)
	if err != nil {
		return chat, false
	}
	chat.shortened = shortened
	for _, bubble := range listed {
		bubbles[bubble.BubbleID] = bubble
	}
	// The headers order the messages. Newer Cursor versions can leave them
	// empty, so the messages' own times order them then.
	var ordered []cursorBubble
	switch {
	case len(data.Headers) > 0:
		for _, header := range data.Headers {
			if bubble, ok := bubbles[header.BubbleID]; ok {
				ordered = append(ordered, bubble)
			}
		}
	case len(data.Conversation) > 0:
		ordered = data.Conversation
	default:
		ordered = listed
		slices.SortStableFunc(ordered, func(a, b cursorBubble) int {
			return cmp.Or(cursorBubbleTime(a.CreatedAt).Compare(cursorBubbleTime(b.CreatedAt)), strings.Compare(a.BubbleID, b.BubbleID))
		})
	}
	var items []tagged
	for _, bubble := range ordered {
		at := cursorBubbleTime(bubble.CreatedAt)
		switch bubble.Type {
		case 1:
			if prompt, _, _ := cursorPrompt(bubble.Text); prompt != "" {
				items = append(items, tagged{Message: Message{Role: domain.MessageRoleUser, Text: prompt, At: at}})
			}
		case 2:
			text := cursorReply(bubble.Text)
			if text == "" {
				continue
			}
			if n := len(items); n > 0 && items[n-1].Role == domain.MessageRoleAssistant {
				items[n-1].Text += "\n\n" + text
			} else {
				items = append(items, tagged{Message: Message{Role: domain.MessageRoleAssistant, Text: text, At: at}})
			}
		}
	}
	n := len(items)
	items = trimVisible(items)
	chat.shortened = chat.shortened || len(items) < n
	for _, item := range items {
		chat.messages = append(chat.messages, item.Message)
	}
	return chat, true
}

// cursorEditorBubbles lists one chat's messages, in key order. Each message
// has its own key; ':' + 1 is ';', so the range is exactly this chat's.
// shortened reports a message too large to read.
func cursorEditorBubbles(ctx context.Context, db *sql.DB, id string) (listed []cursorBubble, shortened bool, err error) {
	prefix := "bubbleId:" + id + ":"
	rows, err := db.QueryContext(ctx, `SELECT key, CASE WHEN length(value) <= ? THEN value END FROM cursorDiskKV WHERE key >= ? AND key < ?`, maxCursorBubbleBytes, prefix, "bubbleId:"+id+";")
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, false, err
		}
		if value == nil {
			shortened = true
			continue
		}
		var bubble cursorBubble
		if json.Unmarshal(value, &bubble) != nil {
			continue
		}
		bubble.BubbleID = strings.TrimPrefix(key, prefix)
		listed = append(listed, bubble)
	}
	return listed, shortened, rows.Err()
}

// cursorEditorFolder is the folder Cursor's chat list files the chat under.
// The list holds every chat, so it is read only when a folder is in doubt.
func cursorEditorFolder(ctx context.Context, id string) string {
	path := cursorEditorState()
	if path == "" {
		return ""
	}
	db, err := openReadOnlySQLite(path)
	if err != nil || db == nil {
		return ""
	}
	defer func() { _ = db.Close() }()
	var body []byte
	if err := db.QueryRowContext(ctx, `SELECT CASE WHEN length(value) <= ? THEN value END FROM ItemTable WHERE key = 'composer.composerHeaders'`, maxCursorComposerBytes).Scan(&body); err != nil || body == nil {
		return ""
	}
	type header struct {
		ComposerID          string `json:"composerId"`
		WorkspaceIdentifier struct {
			URI struct {
				FSPath string `json:"fsPath"`
				Scheme string `json:"scheme"`
			} `json:"uri"`
		} `json:"workspaceIdentifier"`
	}
	var headers struct {
		AllComposers []header `json:"allComposers"`
	}
	if json.Unmarshal(body, &headers) != nil {
		_ = json.Unmarshal(body, &headers.AllComposers)
	}
	for _, h := range headers.AllComposers {
		uri := h.WorkspaceIdentifier.URI
		if h.ComposerID == id && (uri.Scheme == "" || uri.Scheme == "file") && filepath.IsAbs(uri.FSPath) {
			return uri.FSPath
		}
	}
	return ""
}

// cursorBubbleTime reads a message time, written as Unix milliseconds or RFC 3339.
func cursorBubbleTime(raw json.RawMessage) time.Time {
	var ms float64
	if json.Unmarshal(raw, &ms) == nil && ms > 0 {
		return time.UnixMilli(int64(ms))
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return at
		}
	}
	return time.Time{}
}
