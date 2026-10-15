// Package sessionimport reads local provider history without starting or modifying a provider.
package sessionimport

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const maxTranscriptBytes = 64 << 20
const maxVisibleBytes = 16 << 20

var nativeIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Message is the readable archive; tool payloads and provider internal state are not display messages.
type Message struct {
	Role domain.MessageRole
	Text string
	At   time.Time
}

type transcript struct {
	harness                                domain.AgentHarness
	root, path, nativeID, cwd, fingerprint string
	messages                               []Message
	last                                   time.Time
}

type record struct {
	Type          string    `json:"type"`
	UUID          string    `json:"uuid"`
	ParentUUID    string    `json:"parentUuid"`
	SessionID     string    `json:"sessionId"`
	CWD           string    `json:"cwd"`
	Timestamp     time.Time `json:"timestamp"`
	Sidechain     bool      `json:"isSidechain"`
	Meta          bool      `json:"isMeta"`
	TurnCompanion bool      `json:"turnCompanion"`
	Message       struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Payload struct {
		ID      string          `json:"id"`
		CWD     string          `json:"cwd"`
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Message string          `json:"message"`
		Content json.RawMessage `json:"content"`
	} `json:"payload"`
}

func visibleText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text", "input_text", "output_text":
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func readTranscript(ctx context.Context, harness domain.AgentHarness, root, path string) (transcript, error) {
	t := transcript{harness: harness, root: root, path: path}
	// Never follow a provider-file symlink out of the explicitly scanned root.
	leaf, err := os.Lstat(path)
	if err != nil || !leaf.Mode().IsRegular() {
		return t, errors.New("transcript is not a regular file")
	}
	root = canonical(root)
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return t, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return t, errors.New("transcript is outside provider home")
	}
	providerRoot, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	t.root, t.path = root, path
	f, err := providerRoot.Open(rel)
	if err != nil {
		return t, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxTranscriptBytes {
		return t, errors.New("transcript exceeds read budget or is not a file")
	}
	hash := sha256.New()
	scan := bufio.NewScanner(io.TeeReader(io.LimitReader(f, maxTranscriptBytes+1), hash))
	scan.Buffer(make([]byte, 64*1024), 8<<20)
	bytes := 0
	seen := make(map[string]int)
	var fallback []Message
	var claudeRecords []record
	claudeIndices := map[string]int{}
	claudeLeaf := ""
	if harness == domain.HarnessClaudeCode {
		t.nativeID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	add := func(id, role, text string, at time.Time, dst *[]Message) error {
		if role != "user" && role != "assistant" || strings.TrimSpace(text) == "" {
			return nil
		}
		bytes += len(text)
		if bytes > maxVisibleBytes {
			return errors.New("visible history exceeds archive budget")
		}
		msg := Message{Role: domain.MessageRole(role), Text: text, At: at}
		if id != "" {
			if index, ok := seen[id]; ok {
				(*dst)[index] = msg
				return nil
			}
			seen[id] = len(*dst)
		}
		*dst = append(*dst, msg)
		return nil
	}
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		if strings.TrimSpace(string(scan.Bytes())) == "" {
			continue
		}
		var r record
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			return t, fmt.Errorf("malformed transcript: %w", err)
		}
		if r.Timestamp.After(t.last) {
			t.last = r.Timestamp
		}
		if harness == domain.HarnessClaudeCode {
			if r.Sidechain {
				continue
			}
			if r.SessionID != "" && r.SessionID != t.nativeID {
				return t, errors.New("native identity mismatch")
			}
			if r.UUID != "" {
				if _, duplicate := claudeIndices[r.UUID]; duplicate {
					return t, errors.New("ambiguous native UUID")
				}
				claudeIndices[r.UUID] = len(claudeRecords)
				claudeLeaf = r.UUID
			}
			claudeRecords = append(claudeRecords, r)
		} else {
			switch r.Type {
			case "session_meta":
				if t.nativeID != "" && t.nativeID != r.Payload.ID {
					return t, errors.New("native identity mismatch")
				}
				t.nativeID, t.cwd = r.Payload.ID, r.Payload.CWD
			case "response_item":
				if r.Payload.Type == "message" {
					if err := add(r.Payload.ID, r.Payload.Role, visibleText(r.Payload.Content), r.Timestamp, &t.messages); err != nil {
						return t, err
					}
				}
			case "event_msg":
				role := ""
				if r.Payload.Type == "user_message" {
					role = "user"
				}
				if r.Payload.Type == "agent_message" {
					role = "assistant"
				}
				// Older rollouts use event_msg only. Prefer canonical response_item records when present.
				if err := add("", role, r.Payload.Message, r.Timestamp, &fallback); err != nil {
					return t, err
				}
			}
		}
	}
	if err := scan.Err(); err != nil {
		return t, err
	}
	if claudeLeaf != "" {
		var chain []record
		visited := map[string]bool{}
		for claudeLeaf != "" {
			index, found := claudeIndices[claudeLeaf]
			if !found || visited[claudeLeaf] {
				return t, errors.New("incomplete native ancestry")
			}
			visited[claudeLeaf] = true
			r := claudeRecords[index]
			chain = append(chain, r)
			claudeLeaf = r.ParentUUID
		}
		slices.Reverse(chain)
		claudeRecords = chain
	}
	for _, r := range claudeRecords {
		if r.CWD != "" {
			t.cwd = r.CWD
		}
		if (r.Type != "user" && r.Type != "assistant") || (r.Meta && r.TurnCompanion) {
			continue
		}
		role := r.Message.Role
		if role == "" {
			role = r.Type
		}
		id := r.UUID
		if r.Message.ID != "" {
			id = r.Message.ID
		}
		if err := add(id, role, visibleText(r.Message.Content), r.Timestamp, &t.messages); err != nil {
			return t, err
		}
	}
	if len(t.messages) == 0 {
		t.messages = fallback
	}
	if !nativeIDPattern.MatchString(t.nativeID) || len(t.messages) == 0 {
		return t, errors.New("no readable native conversation")
	}
	if t.cwd != "" && !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
	}
	after, err := f.Stat()
	current, statErr := os.Stat(path)
	if err != nil || statErr != nil || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || after.Size() != current.Size() || !after.ModTime().Equal(current.ModTime()) {
		return t, errors.New("transcript changed while reading; scan again")
	}
	if t.last.IsZero() {
		t.last = before.ModTime()
	}
	for i := range t.messages {
		if t.messages[i].At.IsZero() {
			t.messages[i].At = t.last
		}
	}
	t.fingerprint = hex.EncodeToString(hash.Sum(nil))
	return t, nil
}
