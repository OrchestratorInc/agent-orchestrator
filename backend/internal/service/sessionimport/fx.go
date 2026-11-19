package sessionimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var fxIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var fxBlobPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func fxRoot(home string) string {
	if root := strings.TrimSpace(os.Getenv("FX_HOME")); root != "" {
		return root
	}
	return filepath.Join(home, ".fx")
}

// FX v1 has session.json + events.jsonl per id; v2 has an append-only log.
// A converted conversation may remain in both stores. Prefer v2's authoritative
// copy without reading tool-result blobs, the index, or recovery backups.
func discoverFX(ctx context.Context, root string) ([]sourceFiles, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = r.Close() }()
	byID := map[string]sourceFiles{}
	for _, version := range []string{"", "v2"} {
		dir := filepath.Join("sessions", version)
		entries, err := fs.ReadDir(r.FS(), filepath.ToSlash(dir))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := entry.Name()
			if !entry.IsDir() || id == "v2" || !fxIDPattern.MatchString(id) {
				continue
			}
			names := []string{"session.json", "events.jsonl"}
			if version == "v2" {
				names = []string{"log.jsonl"}
			}
			f := sourceFiles{harness: domain.HarnessFX, root: root, key: id}
			for _, name := range names {
				rel := filepath.Join(dir, id, name)
				info, err := r.Lstat(rel)
				if err != nil || !info.Mode().IsRegular() {
					f.paths = nil
					break
				}
				f.paths = append(f.paths, filepath.Join(root, rel))
				f.size += info.Size()
				if info.ModTime().After(f.modTime) {
					f.modTime = info.ModTime()
				}
			}
			if len(f.paths) == len(names) {
				byID[id] = f
			}
		}
	}
	out := make([]sourceFiles, 0, len(byID))
	for _, f := range byID {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b sourceFiles) int { return b.modTime.Compare(a.modTime) })
	return out, nil
}

type fxImportRecord struct {
	ID            string          `json:"id"`
	WorkspaceRoot string          `json:"workspace_root"`
	SubagentChild bool            `json:"subagent_child"`
	UpdatedAt     int64           `json:"updated_at_ms"`
	Timestamp     int64           `json:"timestamp_ms"`
	TS            int64           `json:"ts"`
	Kind          string          `json:"kind"`
	Type          string          `json:"type"`
	Workspace     string          `json:"workspace"`
	Role          string          `json:"role"`
	Key           string          `json:"key"`
	Value         json.RawMessage `json:"value"`
	Data          json.RawMessage `json:"data"`
	Event         struct {
		User *struct {
			Text string `json:"text"`
		} `json:"user"`
		Assistant *struct {
			Text string `json:"text"`
		} `json:"assistant"`
	} `json:"event"`
}

func readFXTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	// First-open refresh can retain a v1 path after native FX converts the
	// conversation. Rediscover that exact ID, preferring v2 over the old copy.
	if len(paths) == 1 && filepath.Base(paths[0]) == "events.jsonl" {
		id := filepath.Base(filepath.Dir(paths[0]))
		files, err := discoverFX(ctx, root)
		if err != nil {
			return transcript{}, err
		}
		paths = nil
		for _, file := range files {
			if file.key == id {
				paths = file.paths
				break
			}
		}
	}
	t := transcript{harness: domain.HarnessFX, root: canonical(root), paths: paths}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	t.path = paths[len(paths)-1]
	t.nativeID = filepath.Base(filepath.Dir(t.path))
	if !fxIDPattern.MatchString(t.nativeID) {
		return t, errors.New("invalid fx session id")
	}
	r, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = r.Close() }()
	var items []tagged
	sawHeader := false
	badRecord := false
	add := func(role, text string, at time.Time) {
		if role == "user" {
			text = codexPrompt(text)
			if i := strings.Index(text, "## AO Task\n"); strings.HasPrefix(text, "## AO Standing Instructions") && i >= 0 {
				text = strings.TrimSpace(text[i+len("## AO Task\n"):])
			}
		}
		if strings.Contains(text, omittedText) {
			t.shortened = true
			text = strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if badRecord {
			return errors.New("fx transcript contains interior corruption")
		}
		var record fxImportRecord
		if json.Unmarshal(line, &record) != nil {
			badRecord = true
			return nil //nolint:nilerr // Only a bad final record is tolerated; any following record rejects the source.
		}
		if record.ID != "" && (record.Kind == "session_created" || record.WorkspaceRoot != "") {
			if record.ID != t.nativeID || record.Role == "child" || record.SubagentChild {
				return errors.New("fx native identity mismatch or child session")
			}
			sawHeader = true
			t.cwd = record.WorkspaceRoot
			if record.Workspace != "" {
				t.cwd = record.Workspace
			}
		}
		if record.Kind == "set" && record.Key == "workspace" {
			var cwd string
			if json.Unmarshal(record.Value, &cwd) == nil {
				t.cwd = cwd
			}
		}
		millis := max(record.TS, record.Timestamp, record.UpdatedAt)
		at := time.UnixMilli(millis)
		if millis > 0 && at.Before(time.Now().Add(time.Hour)) && at.After(t.last) {
			t.last = at
		}
		switch {
		case record.Kind == "item" && (record.Type == "user" || record.Type == "assistant"):
			var data struct {
				Text string `json:"text"`
				Blob string `json:"$blob"`
			}
			if err := json.Unmarshal(record.Data, &data); err != nil {
				badRecord = true
				return nil //nolint:nilerr // Malformed inline data has the same final-record tolerance as the outer record.
			}
			if data.Blob != "" {
				text, err := readFXBlob(r, t.root, t.path, data.Blob)
				if err != nil {
					return err
				}
				data.Text = text
			}
			add(record.Type, data.Text, at)
		case record.Event.User != nil:
			add("user", record.Event.User.Text, at)
		case record.Event.Assistant != nil:
			add("assistant", record.Event.Assistant.Text, at)
		}
		return nil
	}
	for _, path := range paths {
		if err := readOne(r, t.root, path, handle); err != nil {
			return t, err
		}
	}
	if !sawHeader || !filepath.IsAbs(t.cwd) || !slices.ContainsFunc(items, func(m tagged) bool { return m.Role == domain.MessageRoleUser }) {
		return t, errors.New("no readable native conversation")
	}
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
		if t.title == "" && item.Role == domain.MessageRoleUser {
			t.title = strings.Join(strings.Fields(item.Text), " ")
		}
	}
	if title := []rune(t.title); len(title) > 100 {
		t.title = string(title[:100]) + "…"
	}
	return t, nil
}

// Native FX stores large item data as JSON in a session-local SHA-256 blob.
// Read only visible message blobs, with the same root confinement as the log.
func readFXBlob(root *os.Root, home, transcriptPath, hash string) (string, error) {
	if !fxBlobPattern.MatchString(hash) {
		return "", errors.New("invalid fx blob hash")
	}
	rel, err := filepath.Rel(home, filepath.Join(filepath.Dir(canonical(transcriptPath)), "blobs", hash))
	if err != nil {
		return "", err
	}
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxVisibleBytes {
		return "", errors.New("fx blob is missing, oversized or not a regular file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxVisibleBytes+1))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	if len(body) > maxVisibleBytes || hex.EncodeToString(digest[:]) != hash {
		return "", errors.New("fx blob exceeds read budget or has a mismatched digest")
	}
	var data struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	return data.Text, nil
}
