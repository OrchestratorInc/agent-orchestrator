package sessionsearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestReadersOnRealTranscripts runs the three native readers over the
// transcripts on this machine and reports what each one extracts. It is a
// manual check, skipped unless AO_SEARCH_REAL_TRANSCRIPTS=1, because the
// formats it guards against belong to other tools and change without notice.
//
// Every file is opened read-only and nothing is indexed: the readers' output is
// counted and dropped one file at a time. Roots default to where Claude Code,
// Codex and OpenCode v2 write under $HOME; AO_SEARCH_REAL_CLAUDE,
// AO_SEARCH_REAL_CODEX (both colon-separated) and AO_SEARCH_REAL_OPENCODE
// override them.
//
// It fails when a file cannot be read, or when a reader emits the same user or
// assistant message twice within two seconds: the signature of reading one
// turn from two streams.
func TestReadersOnRealTranscripts(t *testing.T) {
	if os.Getenv("AO_SEARCH_REAL_TRANSCRIPTS") != "1" {
		t.Skip("set AO_SEARCH_REAL_TRANSCRIPTS=1 to check the readers against this machine's transcripts")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	claudeRoots := realRoots("AO_SEARCH_REAL_CLAUDE", filepath.Join(home, ".claude", "projects"))
	if os.Getenv("AO_SEARCH_REAL_CLAUDE") == "" {
		accounts, _ := filepath.Glob(filepath.Join(home, ".ao", "data", "agent-accounts", "claude", "*", "projects"))
		claudeRoots = append(claudeRoots, accounts...)
	}
	codexRoots := realRoots("AO_SEARCH_REAL_CODEX",
		filepath.Join(home, ".codex", "sessions"), filepath.Join(home, ".codex", "archived_sessions"))
	openCode := os.Getenv("AO_SEARCH_REAL_OPENCODE")
	if openCode == "" {
		openCode = filepath.Join(home, ".local", "share", "opencode-v2-home", "opencode", "opencode.db")
	}

	ctx := context.Background()
	claude := newReaderTally("claude-code")
	for _, path := range jsonlFiles(t, claudeRoots) {
		lines, _, err := readNativeTranscript(ctx, path, 0, parseClaudeTranscriptLine)
		claude.add(t, path, lines, err)
	}
	codex := newReaderTally("codex")
	for _, path := range jsonlFiles(t, codexRoots) {
		lines, _, err := readNativeTranscript(ctx, path, 0, codexParser(codexHasItemStream(path)))
		codex.add(t, path, lines, err)
	}
	openCodeTally := newReaderTally("opencode")
	if _, err := os.Stat(openCode); err == nil {
		store, err := openOpenCodeStore(ctx, openCode)
		if err != nil {
			t.Fatalf("open %s: %v", openCode, err)
		}
		defer store.close()
		ids := openCodeSessionIDs(t, store)
		for _, id := range ids {
			lines, _, err := store.messages(ctx, id, 0)
			openCodeTally.add(t, openCode+"#"+id, lines, err)
		}
	}
	for _, tally := range []*readerTally{claude, codex, openCodeTally} {
		t.Log(tally.String())
	}
}

func realRoots(env string, defaults ...string) []string {
	if value := os.Getenv(env); value != "" {
		return filepath.SplitList(value)
	}
	return defaults
}

func jsonlFiles(t *testing.T, roots []string) []string {
	t.Helper()
	var files []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // A missing or unreadable root is reported as zero files.
			}
			if entry.Type().IsRegular() && strings.HasSuffix(path, ".jsonl") {
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

func openCodeSessionIDs(t *testing.T, store *openCodeStore) []string {
	t.Helper()
	table := "session_message"
	if ok, err := store.hasTable(context.Background(), table); err != nil || !ok {
		table = "message"
	}
	rows, err := store.db.QueryContext(context.Background(), `SELECT DISTINCT session_id FROM `+table) //nolint:gosec // table is one of two constants.
	if err != nil {
		t.Fatalf("list opencode sessions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan opencode session: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list opencode sessions: %v", err)
	}
	return ids
}

type readerTally struct {
	harness    string
	sources    int
	empty      int
	units      map[domain.SessionSearchKind]int
	bytes      map[domain.SessionSearchKind]int
	withPaths  int
	duplicates int
}

func newReaderTally(harness string) *readerTally {
	return &readerTally{
		harness: harness,
		units:   map[domain.SessionSearchKind]int{},
		bytes:   map[domain.SessionSearchKind]int{},
	}
}

func (r *readerTally) add(t *testing.T, source string, lines []nativeLine, err error) {
	t.Helper()
	if err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("%s: %s: %v", r.harness, source, err)
		return
	}
	r.sources++
	if len(lines) == 0 {
		r.empty++
	}
	lastSeen := map[string]time.Time{}
	for _, line := range lines {
		r.units[line.kind]++
		r.bytes[line.kind] += len(line.text)
		if len(line.paths) > 0 {
			r.withPaths++
		}
		if line.kind != domain.SessionSearchKindUser && line.kind != domain.SessionSearchKindAssistant {
			continue
		}
		key := string(line.kind) + "\x00" + line.text
		if seen, ok := lastSeen[key]; ok && !line.at.IsZero() && line.at.Sub(seen).Abs() < 2*time.Second {
			r.duplicates++
			if r.duplicates <= 5 {
				t.Errorf("%s: %s: %s message emitted twice: %.80q", r.harness, source, line.kind, line.text)
			}
		}
		lastSeen[key] = line.at
	}
}

func (r *readerTally) String() string {
	kinds := make([]string, 0, len(r.units))
	for kind := range r.units {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s: %d sources (%d with no text), %d duplicates, %d units with paths",
		r.harness, r.sources, r.empty, r.duplicates, r.withPaths)
	for _, kind := range kinds {
		k := domain.SessionSearchKind(kind)
		fmt.Fprintf(&builder, "\n    %-9s %6d units %8d KB", kind, r.units[k], r.bytes[k]/1024)
	}
	return builder.String()
}
