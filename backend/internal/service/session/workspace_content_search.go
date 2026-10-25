package session

import (
	"bytes"
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	workspaceContentResults = 8
	// Bound output as well as matches per file so common queries cannot
	// consume unbounded memory before ranking.
	workspaceContentOutputLimit = 512 << 10
	workspaceContentTimeout     = 5 * time.Second
	workspaceContentWindow      = 160
)

// WorkspaceContentHit is the best matching line of one file. Text wraps each
// match in \x02 and \x03.
type WorkspaceContentHit struct {
	Path string
	Line int
	Text string
}

type workspaceGrepRoot struct {
	dir, prefix string
	exclude     []string
	noIndex     bool
}

// SearchWorkspaceContent returns the best line of each file whose lines hold
// every word of query (case-insensitive, any order), best first. When no line
// holds every word, lines holding the most words win, so one misspelled word
// does not empty the result.
func (s *Service) SearchWorkspaceContent(ctx context.Context, id domain.SessionID, query string) ([]WorkspaceContentHit, error) {
	terms := domain.SearchTerms(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, apierr.Invalid("WORKSPACE_SEARCH_QUERY_REQUIRED", "query is required", nil)
	}
	roots, err := s.workspaceGrepRoots(ctx, id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workspaceContentTimeout)
	defer cancel()
	hits, err := grepWorkspaceRoots(ctx, roots, terms, true)
	if err == nil && len(hits) == 0 && len(terms) > 1 {
		hits, err = grepWorkspaceRoots(ctx, roots, terms, false)
	}
	if err != nil {
		return nil, err
	}
	return rankWorkspaceContent(hits, terms, strings.ToLower(strings.Join(strings.Fields(query), " "))), nil
}

// workspaceGrepRoots lists the directories that hold a session's files, the
// set the Files browser lists: one worktree, each repository of a workspace
// project, or a scratch folder (no Git index).
func (s *Service) workspaceGrepRoots(ctx context.Context, id domain.SessionID) ([]workspaceGrepRoot, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return nil, err
	}
	kind := domain.ProjectKindSingleRepo
	if isStandaloneScratchWorkspace(rec) {
		kind = domain.ProjectKindScratch
	}
	if projectOK {
		kind = project.Kind.WithDefault()
	}
	root := rec.Metadata.WorkspacePath
	if kind == domain.ProjectKindScratch {
		return []workspaceGrepRoot{{dir: root, noIndex: true}}, nil
	}
	if kind != domain.ProjectKindWorkspace {
		return []workspaceGrepRoot{{dir: root}}, nil
	}
	rows, err := s.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil || len(rows) == 0 {
		return []workspaceGrepRoot{{dir: root}}, err
	}
	prefixes := workspaceProjectPrefixes(root, rows)
	var roots []workspaceGrepRoot
	for _, row := range rows {
		prefix, ok := prefixes[row.RepoName]
		if !ok || strings.TrimSpace(row.WorktreePath) == "" {
			continue
		}
		grepRoot := workspaceGrepRoot{dir: row.WorktreePath, prefix: prefix}
		if prefix == "" {
			grepRoot.exclude = nonEmptyWorkspacePrefixes(prefixes)
		}
		roots = append(roots, grepRoot)
	}
	return roots, nil
}

func grepWorkspaceRoots(ctx context.Context, roots []workspaceGrepRoot, terms []string, all bool) ([]WorkspaceContentHit, error) {
	var hits []WorkspaceContentHit
	for _, root := range roots {
		found, err := grepWorkspace(ctx, root, terms, all)
		if err != nil {
			return nil, err
		}
		hits = append(hits, found...)
	}
	return hits, nil
}

// grepWorkspace runs git grep over tracked and untracked, non-ignored text
// files, ranking among the first three matching lines in each file.
// all=true requires every term on the line.
func grepWorkspace(ctx context.Context, root workspaceGrepRoot, terms []string, all bool) ([]WorkspaceContentHit, error) {
	args := []string{"grep", "--null", "--line-number", "--ignore-case", "--fixed-strings", "-I", "--no-color", "--max-count=3"}
	if root.noIndex {
		args = append(args, "--no-index", "--exclude-standard")
	} else {
		args = append(args, "--untracked")
	}
	for i, term := range terms {
		if i > 0 && all {
			args = append(args, "--and")
		}
		args = append(args, "-e", term)
	}
	args = append(args, "--", ".")
	for _, prefix := range root.exclude {
		args = append(args, ":(exclude)"+prefix)
	}
	grepCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := workspaceGitCommand(grepCtx, root.dir, args...)
	stdout := &grepOutput{limit: workspaceContentOutputLimit, full: stop}
	stderr := &cappedWorkspaceOutput{limit: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Exit 1 with nothing on stderr is git grep's "no match".
	noMatch := func() bool { return cmd.ProcessState.ExitCode() == 1 && stderr.buffer.Len() == 0 }
	if err := cmd.Run(); err != nil && !stdout.filled && (cmd.ProcessState == nil || !noMatch()) {
		return nil, classifyWorkspaceGitError(ctx, err, strings.TrimSpace(stderr.buffer.String()))
	}
	var hits []WorkspaceContentHit
	for _, record := range bytes.Split(stdout.buffer.Bytes(), []byte{'\n'}) {
		fields := bytes.SplitN(record, []byte{0}, 3)
		if len(fields) != 3 {
			continue
		}
		line, err := strconv.Atoi(string(fields[1]))
		if err != nil {
			continue
		}
		rel := string(fields[0])
		if root.prefix != "" {
			rel = root.prefix + "/" + rel
		}
		hits = append(hits, WorkspaceContentHit{Path: rel, Line: line, Text: string(fields[2])})
	}
	return hits, nil
}

// grepOutput keeps the first limit bytes, then stops git.
type grepOutput struct {
	buffer bytes.Buffer
	limit  int
	full   context.CancelFunc
	filled bool
}

func (w *grepOutput) Write(p []byte) (int, error) {
	if room := w.limit - w.buffer.Len(); room < len(p) {
		w.buffer.Write(p[:max(room, 0)])
		w.filled = true
		w.full()
		return len(p), nil
	}
	return w.buffer.Write(p)
}

// workspaceDefinitionPattern captures the name a line declares (Go methods
// included): "func (s *Service) Search(" declares Search.
var workspaceDefinitionPattern = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:pub\s+)?(?:func|type|const|let|var|class|interface|enum|struct|def|function|fn)\s+(?:\([^)]*\)\s*)?\*?([A-Za-z_$][\w$]*)`)

// Tests, generated code, translations, vendored code and lockfiles.
var workspaceLowValuePattern = regexp.MustCompile(`(^|/)(tests?|__tests__|testdata|gen|generated|i18n|locales|vendor|node_modules)/|[._](test|spec|gen|generated|min)\.|(\.lock|-lock\.json|-lock\.yaml|(^|/)go\.sum)$`)

// rankWorkspaceContent keeps each file's best line and orders files by it.
// A line scores for each term it holds (more at a word start), the query as
// typed, declaring a name that holds a term, and a file name that holds a
// term; low-value paths and minified lines rank lower.
func rankWorkspaceContent(hits []WorkspaceContentHit, terms []string, phrase string) []WorkspaceContentHit {
	type scored struct {
		hit   WorkspaceContentHit
		score int
	}
	best := map[string]scored{}
	for _, hit := range hits {
		lower := asciiLower(hit.Text)
		score := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				score += 10
				if termAtWordStart(hit.Text, lower, term) {
					score += 2
				}
			}
		}
		if len(terms) > 1 && strings.Contains(lower, phrase) {
			score += 8
		}
		if declared := workspaceDefinitionPattern.FindStringSubmatch(hit.Text); len(declared) == 2 && containsAny(declared[1], terms...) {
			score += 4
		}
		if containsAny(path.Base(hit.Path), terms...) {
			score += 3
		}
		if workspaceLowValuePattern.MatchString(strings.ToLower(hit.Path)) {
			score -= 6
		}
		if len(hit.Text) > 1000 {
			score -= 8
		}
		if current, ok := best[hit.Path]; !ok || score > current.score {
			best[hit.Path] = scored{hit, score}
		}
	}
	ranked := make([]scored, 0, len(best))
	for _, entry := range best {
		ranked = append(ranked, entry)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].hit.Path < ranked[j].hit.Path
	})
	out := make([]WorkspaceContentHit, 0, workspaceContentResults)
	for _, entry := range ranked[:min(len(ranked), workspaceContentResults)] {
		entry.hit.Text = markedWindow(entry.hit.Text, terms)
		out = append(out, entry.hit)
	}
	return out
}

// termAtWordStart reports whether term starts a word or a camelCase part in
// text, so "form" in "FormField" beats "form" in "uniforms".
func termAtWordStart(text, lower, term string) bool {
	for at, next := 0, 0; next >= 0; at += next + 1 {
		next = strings.Index(lower[at:], term)
		if next < 0 {
			break
		}
		i := at + next
		if i == 0 || !isWordByte(text[i-1]) || isUpperByte(text[i]) && !isUpperByte(text[i-1]) {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || isUpperByte(b) || b >= 0x80
}

func isUpperByte(b byte) bool { return b >= 'A' && b <= 'Z' }

// markedWindow trims a line to a window starting just before its first match
// and wraps each match in \x02 and \x03.
func markedWindow(text string, terms []string) string {
	text = strings.TrimSpace(text)
	lower := asciiLower(text)
	first := len(text)
	for _, term := range terms {
		if i := strings.Index(lower, term); i >= 0 && i < first {
			first = i
		}
	}
	start := 0
	if first < len(text) && first > workspaceContentWindow/4 {
		start = runeStart(text, first-workspaceContentWindow/8)
	}
	end := runeStart(text, min(len(text), start+workspaceContentWindow))
	text, lower = text[start:end], lower[start:end]
	marked := make([]bool, len(text)+1)
	for _, term := range terms {
		for at := 0; ; {
			i := strings.Index(lower[at:], term)
			if i < 0 {
				break
			}
			for j := at + i; j < at+i+len(term); j++ {
				marked[j] = true
			}
			at += i + len(term)
		}
	}
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if marked[i] && (i == 0 || !marked[i-1]) {
			out.WriteByte('\x02')
		}
		out.WriteByte(text[i])
		if marked[i] && !marked[i+1] {
			out.WriteByte('\x03')
		}
	}
	return out.String()
}

// asciiLower lowercases ASCII only, so byte offsets match the original text.
func asciiLower(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, text)
}

func runeStart(text string, i int) int {
	for i > 0 && i < len(text) && text[i]&0xC0 == 0x80 {
		i--
	}
	return i
}
