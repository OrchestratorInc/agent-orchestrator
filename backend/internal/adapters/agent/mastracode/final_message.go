package mastracode

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// finalMessageWait bounds how long a Stop hook waits for the native history to
// persist the turn's reply.
const finalMessageWait = 2 * time.Second

// TerminalTurnReply returns the reply that closed the turn a Stop hook
// reports (ports.AgentTerminalReplyReader). The released TUI calls its Stop
// hook without assistant_message, so the daemon reads the answer from native
// history: the database AO recorded in this session's own native state, never
// a hook-supplied path. It takes the newest message, once it is the
// assistant's. A resumed TUI reports the placeholder "session-init" instead of
// its thread, and AO's hook script drops it; the database is AO's per-session
// store, so without a thread its newest message is read. A turn whose reply is
// not yet persisted yields "".
func (*Plugin) TerminalTurnReply(ctx context.Context, cfg ports.TerminalReplyConfig) string {
	session := string(cfg.SessionID)
	if !filepath.IsAbs(cfg.DataDir) || session == "" || filepath.Base(session) != session || session == "." || session == ".." {
		return ""
	}
	thread := strings.TrimSpace(cfg.NativeSessionID)
	if thread != "" && !nativeIDPattern.MatchString(thread) {
		return ""
	}
	state, err := readNativeState(nativeStatePath(cfg.DataDir, session))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(state.Database); err != nil {
		return ""
	}
	u := url.URL{Path: state.Database}
	db, err := sql.Open("sqlite", "file:"+u.EscapedPath()+"?mode=ro&_pragma=busy_timeout(500)")
	if err != nil {
		return ""
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, finalMessageWait)
	defer cancel()
	for {
		if text, done := newestAssistantText(ctx, db, thread); done {
			return text
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// newestAssistantText reads the newest message, of thread when it is known.
// done is false while the newest message is not yet the assistant's reply.
func newestAssistantText(ctx context.Context, db *sql.DB, thread string) (string, bool) {
	var role, content string
	query, args := `SELECT role, content FROM mastra_messages ORDER BY "createdAt" DESC LIMIT 1`, []any{}
	if thread != "" {
		query, args = `SELECT role, content FROM mastra_messages WHERE thread_id = ? ORDER BY "createdAt" DESC LIMIT 1`, []any{thread}
	}
	err := db.QueryRowContext(ctx, query, args...).Scan(&role, &content)
	if err != nil || role != "assistant" {
		return "", false
	}
	return assistantReplyText(content), true
}

// assistantReplyText joins the text parts after the message's last tool step,
// so narration before a tool call is not reported as the turn's answer.
func assistantReplyText(content string) string {
	var message struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if json.Unmarshal([]byte(content), &message) != nil {
		return ""
	}
	var texts []string
	for _, part := range message.Parts {
		switch part.Type {
		case "text":
			if text := strings.TrimSpace(part.Text); text != "" {
				texts = append(texts, text)
			}
		case "reasoning", "step-start":
		default:
			texts = texts[:0]
		}
	}
	return strings.Join(texts, "\n\n")
}
