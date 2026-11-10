package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const copilotMetadataBudget = 1 << 20

func copilotRoot(home string) string {
	if root := strings.TrimSpace(os.Getenv("COPILOT_HOME")); root != "" {
		return root
	}
	return filepath.Join(home, ".copilot")
}

// Copilot's CLI and ACP server use the same UUID-addressed event store.
// Only the immediate session-state/<uuid>/events.jsonl is a conversation;
// session.db, model snapshots and nested subagent files are not transcripts.
func discoverCopilot(ctx context.Context, root string) ([]sourceFiles, error) {
	dir := filepath.Join(root, "session-state")
	var out []sourceFiles
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // Unreadable provider histories are hidden.
		}
		if entry.IsDir() {
			if path != dir && (filepath.Dir(path) != dir || !nativeIDPattern.MatchString(entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || entry.Name() != "events.jsonl" || filepath.Dir(filepath.Dir(path)) != dir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // Unreadable sources are ineligible.
		}
		id := strings.ToLower(filepath.Base(filepath.Dir(path)))
		if !nativeIDPattern.MatchString(id) {
			return nil
		}
		file := sourceFiles{harness: domain.HarnessCopilot, root: root, key: id, paths: []string{path}, size: info.Size(), modTime: info.ModTime()}
		if metadata, err := os.Lstat(filepath.Join(filepath.Dir(path), "workspace.yaml")); err == nil && metadata.Mode().IsRegular() {
			file.size += metadata.Size()
			if metadata.ModTime().After(file.modTime) {
				file.modTime = metadata.ModTime()
			}
		}
		out = append(out, file)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

type copilotRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agentId"`
	Data      struct {
		SessionID               string      `json:"sessionId"`
		Content                 contentText `json:"content"`
		Title                   string      `json:"title"`
		CWD                     string      `json:"cwd"`
		Source                  string      `json:"source"`
		IsAutopilotContinuation bool        `json:"isAutopilotContinuation"`
		ParentToolCallID        string      `json:"parentToolCallId"`
		UpToEventID             string      `json:"upToEventId"`
		EventIDs                []string    `json:"eventIds"`
		Context                 struct {
			CWD string `json:"cwd"`
		} `json:"context"`
	} `json:"data"`
}

type copilotWorkspace struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	CWD  string `yaml:"cwd"`
}

type copilotNode struct {
	seq       int
	cwd       string
	title     string
	msg       Message
	shortened bool
}

// readCopilotTranscript reads timeline text, not transformed model inputs,
// streamed deltas, model snapshots, tool output, reasoning or skill injection.
// Context truncation/compaction changes the model window, not the readable
// timeline. Explicit context clears and snapshot rewinds retract display turns.
func readCopilotTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessCopilot, root: canonical(root)}
	if err := ctx.Err(); err != nil {
		return t, err
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "events.jsonl" {
		return t, errors.New("no Copilot event transcript")
	}
	t.path, t.paths = paths[0], paths
	t.nativeID = strings.ToLower(filepath.Base(filepath.Dir(t.path)))
	rel, err := filepath.Rel(t.root, canonical(filepath.Dir(t.path)))
	if err != nil || rel != filepath.Join("session-state", filepath.Base(filepath.Dir(t.path))) || !nativeIDPattern.MatchString(t.nativeID) {
		return t, errors.New("not a native Copilot session directory")
	}
	providerRoot, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	metadata := readCopilotWorkspace(providerRoot, rel)
	if metadata.ID != "" && !strings.EqualFold(metadata.ID, t.nativeID) {
		return t, errors.New("copilot workspace identity mismatch")
	}
	var nodes []copilotNode
	seen := map[string]int{}
	seq, held, drop, opening := 0, 0, 0, -1
	started := false
	now := time.Now()
	recount := func() {
		held, drop, opening = 0, 0, -1
		for i, node := range nodes {
			held += len(node.msg.Text)
			if opening < 0 && node.msg.Role == domain.MessageRoleUser && node.msg.Text != "" {
				opening = i
			}
		}
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r copilotRecord
		if json.Unmarshal(line, &r) != nil {
			return nil //nolint:nilerr // Torn and unrelated events do not hide valid neighbors.
		}
		if _, duplicate := seen[r.ID]; r.ID != "" && duplicate {
			return nil
		}
		seq++
		if r.ID != "" {
			seen[r.ID] = seq
		}
		if r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
			t.last = r.Timestamp
		}
		node := copilotNode{seq: seq}
		switch r.Type {
		case "session.start":
			if !strings.EqualFold(r.Data.SessionID, t.nativeID) {
				return errors.New("copilot native identity mismatch")
			}
			started, node.cwd = true, r.Data.Context.CWD
		case "session.resume":
			node.cwd = r.Data.Context.CWD
		case "session.context_changed":
			node.cwd = r.Data.CWD
		case "session.title_changed":
			node.title = r.Data.Title
		case "session.context_cleared":
			nodes = slices.DeleteFunc(nodes, func(n copilotNode) bool { return n.msg.Role != "" })
			recount()
			return nil
		case "session.snapshot_rewind":
			remove := map[int]bool{}
			cut := 0
			if len(r.Data.EventIDs) > 0 {
				for _, id := range r.Data.EventIDs {
					remove[seen[id]] = true
					delete(seen, id)
				}
			} else {
				cut = seen[r.Data.UpToEventID]
				if cut == 0 {
					return errors.New("copilot rewind target is missing")
				}
				for id, at := range seen {
					if at >= cut {
						delete(seen, id)
					}
				}
			}
			nodes = slices.DeleteFunc(nodes, func(n copilotNode) bool { return remove[n.seq] || cut > 0 && n.seq >= cut })
			recount()
			return nil
		case "user.message", "assistant.message":
			// A subagent's events carry a top-level agentId; parentToolCallId is optional and deprecated.
			if !started || r.Data.Source != "" && r.Data.Source != "user" || r.Data.IsAutopilotContinuation || r.Data.ParentToolCallID != "" || r.AgentID != "" {
				return nil
			}
			text := string(r.Data.Content)
			if strings.Contains(text, omittedText) {
				node.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
			}
			if len(text) > maxVisibleBytes {
				node.shortened, text = true, text[:maxVisibleBytes]
			}
			if strings.TrimSpace(text) == "" {
				return nil
			}
			role := domain.MessageRoleUser
			if r.Type == "assistant.message" {
				role = domain.MessageRoleAssistant
			}
			node.msg = Message{Role: role, Text: text, At: r.Timestamp}
		default:
			return nil
		}
		nodes = append(nodes, node)
		if opening < 0 && node.msg.Role == domain.MessageRoleUser {
			opening = len(nodes) - 1
		}
		held += len(node.msg.Text)
		dropOldestHeld(nodes, &held, &drop, opening, func(n *copilotNode) (*string, *bool) { return &n.msg.Text, &n.shortened })
		return nil
	}
	if err := readOne(providerRoot, t.root, t.path, handle); err != nil {
		return t, err
	}
	var items []tagged
	for _, node := range nodes {
		if node.cwd != "" {
			t.cwd = node.cwd
		}
		if node.title != "" {
			t.title = node.title
		}
		t.shortened = t.shortened || node.shortened
		if node.msg.Text != "" {
			items = append(items, tagged{Message: node.msg})
		}
	}
	kept := trimVisible(items)
	t.shortened = t.shortened || len(kept) < len(items)
	for _, item := range kept {
		t.messages = append(t.messages, item.Message)
	}
	if !started || !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
		return t, errors.New("no readable Copilot conversation")
	}
	if t.cwd == "" {
		t.cwd = metadata.CWD
	}
	if metadata.Name != "" {
		t.title = metadata.Name
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}

func readCopilotWorkspace(root *os.Root, sessionDir string) copilotWorkspace {
	var metadata copilotWorkspace
	path := filepath.Join(sessionDir, "workspace.yaml")
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > copilotMetadataBudget {
		return metadata
	}
	file, err := root.Open(path)
	if err != nil {
		return metadata
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, copilotMetadataBudget+1))
	if err != nil || len(data) > copilotMetadataBudget {
		return metadata
	}
	if yaml.Unmarshal(data, &metadata) != nil {
		return copilotWorkspace{}
	}
	return metadata
}
