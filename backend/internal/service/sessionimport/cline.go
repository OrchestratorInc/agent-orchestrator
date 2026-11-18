package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Cline 3 stores a manifest and a messages document in one globally indexed
// session directory. --id reuses that id and loads its messages with the
// current cwd/workspace config; no cwd-keyed copy is necessary.
var clineImportID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)

func clineRoot(home string) string {
	if root := os.Getenv("CLINE_DATA_DIR"); root != "" {
		return root
	}
	if root := os.Getenv("CLINE_DIR"); root != "" {
		return filepath.Join(root, "data")
	}
	return filepath.Join(home, ".cline", "data")
}

func discoverCline(ctx context.Context, root string) ([]sourceFiles, error) {
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !clineImportID.MatchString(entry.Name()) {
			continue
		}
		group := sourceFiles{harness: domain.HarnessCline, root: root, key: entry.Name()}
		for _, suffix := range []string{".json", ".messages.json"} {
			path := filepath.Join(root, "sessions", entry.Name(), entry.Name()+suffix)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				break
			}
			group.paths = append(group.paths, path)
			group.size += info.Size()
			if info.ModTime().After(group.modTime) {
				group.modTime = info.ModTime()
			}
		}
		if len(group.paths) == 2 {
			out = append(out, group)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

// clineDocument streams JSON objects from a document's top-level array. The
// shared recordSkim bounds each message separately, including enormous tool
// payloads. The top-level scalar skim validates document identity without
// holding the messages array a second time. Complete messages survive a torn
// final append, just as the shared JSONL reader does.
func clineDocument(ctx context.Context, root *os.Root, home, path string, message func([]byte)) ([]byte, error) {
	rel, err := filepath.Rel(home, canonical(path))
	if err != nil || !filepath.IsLocal(rel) {
		return nil, errors.New("cline document outside provider home")
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxTranscriptBytes {
		return nil, errors.New("cline document exceeds read budget or is not a file")
	}
	var header, item recordSkim
	header.reset()
	header.overflow = true
	depth, capturing := 0, false
	inString, escaped := false, false
	buffer := make([]byte, 32<<10)
	reader := io.LimitReader(f, info.Size())
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(buffer)
		header.write(buffer[:n])
		for i, c := range buffer[:n] {
			if !inString && c == '{' && depth == 2 && message != nil {
				item.reset()
				capturing = true
			}
			if capturing {
				item.write(buffer[i : i+1])
			}
			if inString {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					inString = false
				}
				continue
			}
			switch c {
			case '"':
				inString = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if capturing && depth == 2 {
					message(item.record())
					capturing = false
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	current, err := root.Stat(rel)
	if err != nil || !os.SameFile(info, current) || current.Size() < info.Size() {
		return nil, errors.New("cline document changed while reading; scan again")
	}
	return header.record(), nil
}

func readClineTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessCline, root: canonical(root)}
	if len(paths) == 1 {
		paths = []string{strings.TrimSuffix(paths[0], ".messages.json") + ".json", paths[0]}
	}
	if len(paths) != 2 {
		return t, errors.New("cline manifest and messages required")
	}
	t.nativeID = filepath.Base(filepath.Dir(paths[1]))
	if !clineImportID.MatchString(t.nativeID) || filepath.Base(paths[1]) != t.nativeID+".messages.json" || filepath.Base(paths[0]) != t.nativeID+".json" || filepath.Dir(paths[0]) != filepath.Dir(paths[1]) {
		return t, errors.New("invalid cline conversation paths")
	}
	provider, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	body, err := clineDocument(ctx, provider, t.root, paths[0], nil)
	if err != nil {
		return t, err
	}
	var meta struct {
		ID      string    `json:"session_id"`
		CWD     string    `json:"cwd"`
		Started time.Time `json:"started_at"`
		Ended   time.Time `json:"ended_at"`
	}
	if json.Unmarshal(body, &meta) != nil || meta.ID != t.nativeID || !filepath.IsAbs(meta.CWD) {
		return t, errors.New("cline identity or working directory invalid")
	}
	t.cwd, t.last = meta.CWD, meta.Started
	now := time.Now().Add(time.Hour)
	if meta.Ended.After(t.last) && meta.Ended.Before(now) {
		t.last = meta.Ended
	}
	var items []tagged
	body, err = clineDocument(ctx, provider, t.root, paths[1], func(raw []byte) {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			TS      int64           `json:"ts"`
			Omitted bool            `json:"aoOmitted"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Role != "user" && m.Role != "assistant" {
			return
		}
		var text string
		if m.Role == "user" {
			text = codexPrompt(clineUserText(m.Content))
		} else {
			text = visibleText(m.Content)
		}
		t.shortened = t.shortened || m.Omitted || strings.Contains(text, omittedText)
		text = strings.ReplaceAll(text, omittedText, "")
		if strings.TrimSpace(text) == "" {
			return
		}
		at := time.UnixMilli(m.TS)
		if m.TS <= 0 || at.After(now) {
			at = t.last
		}
		if at.After(t.last) {
			t.last = at
		}
		if t.title == "" && m.Role == "user" {
			t.title = importTitle(text)
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(m.Role), Text: text, At: at}})
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
	})
	if err != nil {
		return t, err
	}
	var header struct {
		ID      string    `json:"sessionId"`
		Updated time.Time `json:"updated_at"`
	}
	if len(body) > 0 && (json.Unmarshal(body, &header) != nil || header.ID != t.nativeID) {
		return t, errors.New("cline messages identity mismatch")
	}
	if header.Updated.After(t.last) && header.Updated.Before(now) {
		t.last = header.Updated
	}
	if t.title == "" {
		return t, errors.New("no readable cline prompt")
	}
	t.path, t.paths = paths[1], append([]string(nil), paths...)
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}

// clineUserText is a user turn's typed text. Cline appends the context it
// injects as separate blocks of the same turn, so each block is filtered on
// its own; filtering the joined text would let a trailing block through.
func clineUserText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		if clineInjected(text) {
			return ""
		}
		return text
	}
	var blocks []textBlock
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	kept := blocks[:0]
	for _, b := range blocks {
		if !clineInjected(b.Text) {
			kept = append(kept, b)
		}
	}
	return blockText(kept)
}

func clineInjected(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<system-reminder>") || strings.HasPrefix(trimmed, "<environment_details>")
}
