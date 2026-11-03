// Package codewhalestore handles self-contained Codewhale native snapshots.
package codewhalestore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxBytes = 16 << 20

// Message is visible text on the native journal's active branch.
type Message struct {
	Role string
	Text string
	At   time.Time
}

// Archive holds the native identity and supported visible history.
type Archive struct {
	ID       string
	CWD      string
	Title    string
	Model    string
	Updated  time.Time
	Messages []Message
	// Runtime authority is distinct from the native conversation identity.
	RuntimeBound bool
	RuntimeDir   string
	RuntimeScope string
}

type nativeMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Cache json.RawMessage `json:"cache_control"`
	} `json:"content"`
}

type entry struct {
	ID      string        `json:"id"`
	Parent  string        `json:"parent_id"`
	Kind    string        `json:"kind"`
	Text    string        `json:"text"`
	Message nativeMessage `json:"message"`
	Created time.Time     `json:"created_at"`
}

type document struct {
	Version  int `json:"schema_version"`
	Metadata struct {
		ID           string    `json:"id"`
		Workspace    string    `json:"workspace"`
		Title        string    `json:"title"`
		Model        string    `json:"model"`
		Updated      time.Time `json:"updated_at"`
		SpawnDepth   int       `json:"spawn_depth"`
		RuntimeStore *struct {
			DataDir string `json:"data_dir"`
			Scope   string `json:"execution_scope"`
		} `json:"runtime_store"`
	} `json:"metadata"`
	Messages []nativeMessage `json:"messages"`
	Leaf     string          `json:"leaf_id"`
	Journal  *struct {
		Version    int     `json:"schema_version"`
		SpawnDepth int     `json:"spawn_depth"`
		Leaf       string  `json:"leaf_id"`
		Entries    []entry `json:"entries"`
	} `json:"journal"`
}

func read(ctx context.Context, root, path string, importing bool) (Archive, map[string]json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return Archive{}, nil, err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return Archive{}, nil, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || filepath.Dir(rel) != "sessions" || filepath.Ext(rel) != ".json" {
		return Archive{}, nil, errors.New("codewhale source is outside the native session store")
	}
	info, err := provider.Lstat(rel)
	if err != nil {
		return Archive{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return Archive{}, nil, errors.New("codewhale snapshot is not a bounded regular file")
	}
	f, err := provider.Open(rel)
	if err != nil {
		return Archive{}, nil, err
	}
	body, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	_ = f.Close()
	if err != nil {
		return Archive{}, nil, err
	}
	if len(body) > maxBytes {
		return Archive{}, nil, errors.New("codewhale snapshot exceeds import limit")
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		return Archive{}, nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Archive{}, nil, err
	}
	if _, err := uuid.Parse(doc.Metadata.ID); err != nil || filepath.Base(path) != doc.Metadata.ID+".json" {
		return Archive{}, nil, errors.New("codewhale native identity mismatch")
	}
	if doc.Version > 1 || !filepath.IsAbs(doc.Metadata.Workspace) || doc.Metadata.SpawnDepth != 0 || doc.Messages == nil {
		return Archive{}, nil, errors.New("unsupported codewhale native snapshot")
	}
	// These fields bind external paths, grants or executable work to the old
	// owner. This first importer supports self-contained conversation history.
	if importing {
		if err := selfContained(provider, raw, doc.Metadata.ID); err != nil {
			return Archive{}, nil, err
		}
	}
	out := Archive{ID: doc.Metadata.ID, CWD: doc.Metadata.Workspace, Title: doc.Metadata.Title, Model: doc.Metadata.Model, Updated: doc.Metadata.Updated}
	if binding := doc.Metadata.RuntimeStore; binding != nil {
		out.RuntimeBound, out.RuntimeDir, out.RuntimeScope = true, binding.DataDir, binding.Scope
	}
	add := func(message nativeMessage, at time.Time) {
		if message.Role != "user" && message.Role != "assistant" {
			return
		}
		if joined := visibleText(message); strings.TrimSpace(joined) != "" {
			out.Messages = append(out.Messages, Message{Role: message.Role, Text: joined, At: at})
		}
	}
	if doc.Journal == nil {
		for _, message := range doc.Messages {
			add(message, out.Updated)
		}
	} else {
		journal := doc.Journal
		if journal.Version > 1 || journal.SpawnDepth != 0 || len(journal.Entries) > 100000 || (doc.Leaf != "" && doc.Leaf != journal.Leaf) {
			return Archive{}, nil, errors.New("unsupported codewhale journal")
		}
		byID := make(map[string]entry, len(journal.Entries))
		for _, item := range journal.Entries {
			if item.ID == "" {
				return Archive{}, nil, errors.New("missing codewhale journal entry id")
			}
			if _, exists := byID[item.ID]; exists {
				return Archive{}, nil, errors.New("duplicate codewhale journal entry id")
			}
			byID[item.ID] = item
		}
		colors := make(map[string]int, len(byID))
		for id := range byID {
			var chain []string
			for id != "" && colors[id] != 2 {
				item, ok := byID[id]
				if !ok || colors[id] == 1 {
					return Archive{}, nil, errors.New("broken codewhale native journal graph")
				}
				switch item.Kind {
				case "message", "user", "assistant", "system", "compaction", "branch_summary":
				default:
					return Archive{}, nil, errors.New("unsupported codewhale journal entry")
				}
				colors[id] = 1
				chain = append(chain, id)
				id = item.Parent
			}
			for _, id := range chain {
				colors[id] = 2
			}
		}
		var branch []entry
		seen := make(map[string]bool)
		for id := journal.Leaf; id != ""; {
			item, ok := byID[id]
			if !ok || seen[id] {
				return Archive{}, nil, errors.New("broken codewhale native journal branch")
			}
			seen[id] = true
			branch = append(branch, item)
			id = item.Parent
		}
		for i := len(branch) - 1; i >= 0; i-- {
			item := branch[i]
			switch item.Kind {
			case "message":
				add(item.Message, item.Created)
			case "user", "assistant":
				if strings.TrimSpace(item.Text) != "" {
					out.Messages = append(out.Messages, Message{Role: item.Kind, Text: item.Text, At: item.Created})
				}
			case "system", "compaction", "branch_summary":
				// Preserve native context in the copied graph, not display text.
			default:
				return Archive{}, nil, errors.New("unsupported codewhale journal entry")
			}
		}
	}
	if len(out.Messages) == 0 {
		return Archive{}, nil, errors.New("codewhale native conversation has no visible messages")
	}
	return out, raw, ctx.Err()
}

func selfContained(provider *os.Root, raw map[string]json.RawMessage, id string) error {
	for _, key := range []string{"context_references", "artifacts", "approval_receipts"} {
		var items []json.RawMessage
		if value := raw[key]; len(value) != 0 && json.Unmarshal(value, &items) != nil {
			return errors.New("invalid codewhale auxiliary state")
		}
		if len(items) != 0 {
			return errors.New("codewhale conversation depends on external state")
		}
	}
	if value := raw["work_state"]; len(value) != 0 && string(value) != "null" {
		return errors.New("codewhale saved work authority cannot be relocated")
	}
	for _, sidecar := range []string{
		filepath.Join("sessions", ".goals", id+".json"),
		filepath.Join("sessions", "checkpoints", id+".json"),
		filepath.Join("sessions", ".journal-archive", id+".jsonl"),
		filepath.Join("sessions", id, "approval_receipts.jsonl"),
	} {
		_, err := provider.Lstat(sidecar)
		if err == nil {
			return errors.New("codewhale conversation depends on a native sidecar")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Read loads a supported native conversation without changing it.
func Read(ctx context.Context, root, path string) (Archive, error) {
	archive, _, err := read(ctx, root, path, false)
	return archive, err
}

// ReadImport additionally refuses snapshots depending on old execution state.
func ReadImport(ctx context.Context, root, path string) (Archive, error) {
	archive, _, err := read(ctx, root, path, true)
	return archive, err
}

// Copy creates a private native conversation with a stable worktree-specific
// identity. Native --resume keeps that id; the shared S2 seam adopts it in AO.
func Copy(ctx context.Context, root, path, launchCWD string) (Archive, string, error) {
	archive, raw, err := read(ctx, root, path, true)
	if err != nil {
		return Archive{}, "", err
	}
	cwd, err := filepath.EvalSymlinks(launchCWD)
	if err != nil {
		return Archive{}, "", err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return Archive{}, "", err
	}
	// Namespaced identity makes preparation idempotent before AO checkpoints it.
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("ao-codewhale-import\x00"+filepath.Clean(root)+"\x00"+archive.ID+"\x00"+cwd)).String()
	target := filepath.Join(root, "sessions", id+".json")
	if existing, err := Read(ctx, root, target); err == nil {
		if existing.ID != id || existing.CWD != cwd {
			return Archive{}, "", errors.New("codewhale target identity differs")
		}
		return existing, target, nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(raw["metadata"], &metadata); err != nil {
		return Archive{}, "", err
	}
	metadata["id"], _ = json.Marshal(id)
	metadata["workspace"], _ = json.Marshal(cwd)
	delete(metadata, "runtime_store")
	delete(metadata, "publication_id")
	raw["metadata"], err = json.Marshal(metadata)
	if err != nil {
		return Archive{}, "", err
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return Archive{}, "", err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return Archive{}, "", err
	}
	defer func() { _ = provider.Close() }()
	temp := filepath.Join("sessions", ".ao-import-"+uuid.NewString()+".tmp")
	f, err := provider.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Archive{}, "", err
	}
	defer func() { _ = provider.Remove(temp) }()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return Archive{}, "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return Archive{}, "", err
	}
	if err := f.Close(); err != nil {
		return Archive{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return Archive{}, "", err
	}
	if err := provider.Link(temp, filepath.Join("sessions", id+".json")); err != nil {
		return Archive{}, "", err
	}
	prepared, err := Read(ctx, root, target)
	return prepared, target, err
}
