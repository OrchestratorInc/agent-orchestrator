package session

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Task suggestions are follow-up tasks the desktop offers to start next to a
// running worker. Each source is best-effort and time-boxed: a project without
// a tracker, auth, network or git still answers, just with fewer suggestions.
const (
	maxTaskSuggestionsPerSource = 3
	taskSuggestionIssueTimeout  = 5 * time.Second
	taskSuggestionGrepTimeout   = 3 * time.Second
	taskSuggestionIssueScan     = 20
	taskSuggestionTodoScan      = 200
	taskSuggestionTextLimit     = 160
)

// TaskSuggestionSource names where a suggestion came from.
type TaskSuggestionSource string

// Task suggestion sources: an open tracker issue, or a TODO/FIXME note in a
// tracked file.
const (
	TaskSuggestionSourceIssue TaskSuggestionSource = "issue"
	TaskSuggestionSourceTodo  TaskSuggestionSource = "todo"
)

// TaskSuggestion is one candidate follow-up task. The desktop writes the
// prompt from these facts in the user's language, so nothing here is prose.
type TaskSuggestion struct {
	Source TaskSuggestionSource
	// Issue fields.
	IssueNumber int
	Title       string
	URL         string
	// TODO fields.
	Path string
	Line int
	Text string
}

// TaskSuggestions proposes up to three open issues and three TODO/FIXME
// comments for a project. Issues already worked on by a session are skipped.
func (s *Service) TaskSuggestions(ctx context.Context, projectID domain.ProjectID) ([]TaskSuggestion, error) {
	project, err := s.requireProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := s.issueSuggestions(ctx, project)
	return append(out, todoSuggestions(ctx, project.Path)...), nil
}

func (s *Service) issueSuggestions(ctx context.Context, project domain.ProjectRecord) []TaskSuggestion {
	if s.tracker == nil || strings.TrimSpace(project.RepoOriginURL) == "" {
		return nil
	}
	provider, host, repo, ok := s.repoForTracker(project, "")
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskSuggestionIssueTimeout)
	defer cancel()
	issues, err := s.tracker.List(ctx, domain.TrackerRepo{Provider: provider, Native: repo, Host: host}, domain.ListFilter{State: domain.ListOpen, Limit: taskSuggestionIssueScan})
	if err != nil {
		return nil
	}
	taken := s.issueNumbersInProject(ctx, domain.ProjectID(project.ID))
	var out []TaskSuggestion
	for _, issue := range issues {
		if len(out) == maxTaskSuggestionsPerSource {
			break
		}
		n := issueNumber(issue.ID.Native)
		title := strings.TrimSpace(issue.Title)
		if n == 0 || title == "" || taken[n] || issue.State == domain.IssueDone || issue.State == domain.IssueCancelled {
			continue
		}
		out = append(out, TaskSuggestion{Source: TaskSuggestionSourceIssue, IssueNumber: n, Title: truncateRunes(title, taskSuggestionTextLimit), URL: issue.URL})
	}
	return out
}

// issueNumbersInProject collects the issue numbers sessions in the project
// already carry, whatever form the issue id was given in.
func (s *Service) issueNumbersInProject(ctx context.Context, projectID domain.ProjectID) map[int]bool {
	taken := map[int]bool{}
	if s.store == nil {
		return taken
	}
	rows, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return taken
	}
	for _, row := range rows {
		if row.ProjectID != projectID || row.IssueID == "" {
			continue
		}
		if n := issueNumber(string(row.IssueID)); n > 0 {
			taken[n] = true
		}
	}
	return taken
}

var trailingIssueNumber = regexp.MustCompile(`(\d+)\D*$`)

// issueNumber reads the issue number from "owner/repo#12", "github:o/r#12",
// "#12", "12" or an issue URL. Zero means none.
func issueNumber(id string) int {
	m := trailingIssueNumber.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// todoComment matches a TODO or FIXME marker used as a note ("TODO:",
// "FIXME(x):", "TODO -"), not the word inside an identifier or a sentence.
var todoComment = regexp.MustCompile(`\b(TODO|FIXME)\b\s*(\([^)]*\))?\s*[:\-]\s*(.+)$`)

// Paths whose TODOs are someone else's: vendored code, generated output and
// fixtures.
var skippedTodoPath = regexp.MustCompile(`(^|/)(vendor|node_modules|third_party|dist|build|gen|testdata|fixtures)/|\.min\.|\.lock$|(^|/)CHANGELOG`)

// todoSuggestions finds FIXME notes first, then TODOs, in tracked files.
func todoSuggestions(ctx context.Context, dir string) []TaskSuggestion {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskSuggestionGrepTimeout)
	defer cancel()
	// -I skips binaries; -w matches the marker as a word, not inside one.
	cmd := process.CommandContext(ctx, "git", "-C", dir, "grep", "-n", "-I", "--no-color", "-w", "-e", "TODO", "-e", "FIXME", "--")
	output, err := cmd.Output()
	if err != nil && len(output) == 0 {
		return nil
	}
	var fixmes, todos []TaskSuggestion
	// At most two per file, so one noisy file cannot fill the list.
	perFile := map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanned := 0; scanner.Scan() && scanned < taskSuggestionTodoScan; scanned++ {
		path, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok || skippedTodoPath.MatchString(path) || perFile[path] == 2 {
			continue
		}
		lineText, content, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		line, err := strconv.Atoi(lineText)
		if err != nil || line <= 0 {
			continue
		}
		m := todoComment.FindStringSubmatch(content)
		if m == nil {
			continue
		}
		text := cleanTodoText(m[3])
		if utf8.RuneCountInString(text) < 8 {
			continue
		}
		perFile[path]++
		suggestion := TaskSuggestion{Source: TaskSuggestionSourceTodo, Path: path, Line: line, Text: truncateRunes(text, taskSuggestionTextLimit)}
		if m[1] == "FIXME" {
			fixmes = append(fixmes, suggestion)
		} else {
			todos = append(todos, suggestion)
		}
	}
	fixmes = append(fixmes, todos...)
	if len(fixmes) > maxTaskSuggestionsPerSource {
		fixmes = fixmes[:maxTaskSuggestionsPerSource]
	}
	return fixmes
}

// cleanTodoText drops comment closers left at the end of a TODO line.
func cleanTodoText(text string) string {
	text = strings.TrimSpace(text)
	for _, closer := range []string{"*/", "-->", "#}", "%>"} {
		text = strings.TrimSpace(strings.TrimSuffix(text, closer))
	}
	return text
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
