package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// These DTOs mirror the daemon's session search API without importing
// controller types, matching how the other commands keep the wire boundary.
type sessionSearchResultDTO struct {
	SessionID     string  `json:"sessionId"`
	ProjectID     string  `json:"projectId"`
	Harness       string  `json:"harness"`
	Kind          string  `json:"kind"`
	MessageID     string  `json:"messageId"`
	Seq           int64   `json:"seq"`
	Title         string  `json:"title"`
	Snippet       string  `json:"snippet"`
	Rank          float64 `json:"rank"`
	CreatedAt     string  `json:"createdAt"`
	SessionName   string  `json:"sessionName"`
	ProjectName   string  `json:"projectName"`
	Machine       string  `json:"machine"`
	SessionState  string  `json:"sessionState"`
	ResumeCommand string  `json:"resumeCommand"`
	// SessionMatches counts the session's matched messages in a grouped list.
	SessionMatches int `json:"sessionMatches"`
	// ReviewID is set when the match is in one of the session's reviewer chats.
	ReviewID string `json:"reviewId,omitempty"`
}

type sessionSearchCoverageDTO struct {
	State              string   `json:"state"`
	IndexedSources     int64    `json:"indexedSources"`
	PendingSources     int64    `json:"pendingSources"`
	UnsupportedSources int64    `json:"unsupportedSources"`
	UnavailableSources int64    `json:"unavailableSources"`
	IndexedDocs        int64    `json:"indexedDocs"`
	UnindexedHarnesses []string `json:"unindexedHarnesses"`
	BackfilledAt       string   `json:"backfilledAt"`
}

type sessionSearchResponseDTO struct {
	Query    string                   `json:"query"`
	Results  []sessionSearchResultDTO `json:"results"`
	Total    int                      `json:"total"`
	Limit    int                      `json:"limit"`
	Offset   int                      `json:"offset"`
	Sort     string                   `json:"sort"`
	Coverage sessionSearchCoverageDTO `json:"coverage"`
}

type sessionSearchIndexStatusDTO struct {
	Coverage sessionSearchCoverageDTO `json:"coverage"`
}

// newSearchCommand searches past AO sessions: titles, user messages, agent
// replies, tool calls, command output, and the file paths a session touched.
//
// It is the agent-facing half of the desktop search surface: a worker can look
// up what it (or another session) already worked out instead of re-deriving it.
// The query language is the same one the palette and the Search page accept.
func newSearchCommand(ctx *commandContext) *cobra.Command {
	var project, harness, machine, status, path, since, until, sortBy string
	var limit, offset int
	var jsonOutput, indexStatus, perMessage bool
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search past sessions by title, message, tool output, and touched files",
		Long: "Search every past AO session — live, hibernated, and terminated — for a word, a phrase, or a PR URL.\n\n" +
			"Each session is listed once, at its best-matching message, with how many of its messages matched (--per-message lists\n" +
			"every matching message). Ranking is BM25: words may be asked as a question (\"how did we fix the flaky\n" +
			"login test\"); word forms match (\"failing\" finds \"failed\"). One score per session blends its name and\n" +
			"branch with its content: the name weighs most, then your own words and the agent's reply, then tool output.\n" +
			"Operators work the same way they do in the desktop palette:\n\n" +
			"  harness:codex  project:api  machine:mac  path:src/auth  status:killed\n" +
			"  before:2026-08-01  after:2026-07-01  sort:recent\n" +
			"  \"exact phrase\"   'exact token   ^prefix   !exclude\n\n" +
			"Use --json for scripting; the result carries the session id and, for a message match, the message id\n" +
			"a caller can open.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if indexStatus {
				var response sessionSearchIndexStatusDTO
				if err := ctx.getJSON(cmd.Context(), "search/index-status", &response); err != nil {
					return err
				}
				if jsonOutput {
					return writeJSON(cmd.OutOrStdout(), response)
				}
				return writeIndexStatus(cmd, response)
			}
			query := strings.TrimSpace(strings.Join(args, " "))
			filtersUsed := project != "" || harness != "" || machine != "" || status != "" || path != ""
			if query == "" && !filtersUsed {
				return usageError{errors.New("provide a word to search for, a filter, or --index-status")}
			}
			params := url.Values{}
			if query != "" {
				params.Set("q", query)
			}
			if project != "" {
				params.Set("projectId", strings.TrimSpace(project))
			}
			if harness != "" {
				params.Set("harness", strings.TrimSpace(harness))
			}
			if machine != "" {
				params.Set("machine", strings.TrimSpace(machine))
			}
			if status != "" {
				normalized := strings.ToLower(strings.TrimSpace(status))
				switch normalized {
				case "killed", "terminated", "dead", "archived":
					normalized = "killed"
				case "live", "running", "active":
					normalized = "live"
				case "hibernated", "asleep":
					normalized = "hibernated"
				default:
					return usageError{fmt.Errorf("--status: want killed, live, or hibernated")}
				}
				params.Set("status", normalized)
			}
			if path != "" {
				params.Set("path", strings.TrimSpace(path))
			}
			if since != "" {
				normalized, err := normalizeSearchDate(since)
				if err != nil {
					return usageError{fmt.Errorf("--since: %w", err)}
				}
				params.Set("from", normalized)
			}
			if until != "" {
				normalized, err := normalizeSearchDate(until)
				if err != nil {
					return usageError{fmt.Errorf("--until: %w", err)}
				}
				params.Set("to", normalized)
			}
			if sortBy != "" {
				normalized := strings.ToLower(strings.TrimSpace(sortBy))
				if normalized != "relevance" && normalized != "recent" {
					return usageError{fmt.Errorf("--sort: want relevance or recent")}
				}
				params.Set("sort", normalized)
			}
			if limit > 0 {
				params.Set("limit", fmt.Sprint(limit))
			}
			if offset > 0 {
				params.Set("offset", fmt.Sprint(offset))
			}
			if perMessage {
				params.Set("group", "message")
			}

			var response sessionSearchResponseDTO
			if err := ctx.getJSON(cmd.Context(), "search/sessions?"+params.Encode(), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeSessionSearchTable(cmd, response)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Only results from this project id")
	cmd.Flags().StringVar(&harness, "harness", "", "Only results from this harness, such as claude-code or codex")
	cmd.Flags().StringVar(&machine, "machine", "", "Only results from this host")
	cmd.Flags().StringVar(&status, "status", "", "Only sessions in this state: killed, live, or hibernated")
	cmd.Flags().StringVar(&path, "path", "", "Only sessions that touched a path containing this text")
	cmd.Flags().StringVar(&since, "since", "", "Only results at or after this date (YYYY-MM-DD) or RFC 3339 time")
	cmd.Flags().StringVar(&until, "until", "", "Only results at or before this date (YYYY-MM-DD) or RFC 3339 time")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Order by relevance (default) or recent")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum results (1-100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Skip this many results")
	cmd.Flags().BoolVar(&perMessage, "per-message", false, "List every matching message instead of each session once")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	cmd.Flags().BoolVar(&indexStatus, "index-status", false, "Report what the index covers instead of searching")
	return cmd
}

// normalizeSearchDate accepts a date or an RFC 3339 timestamp, so both
// `--since 2026-08-01` and `--since 2026-08-01T09:00:00Z` work.
func normalizeSearchDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if _, err := time.Parse(time.DateOnly, raw); err == nil {
		return raw, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC().Format(time.RFC3339), nil
	}
	return "", errors.New("want YYYY-MM-DD or an RFC 3339 time")
}

// writeSessionSearchTable renders one result per line. A match inside a long
// message would be unreadable in a table, so the snippet is normalized to one
// line and the session id is kept as the handle a caller can open.
func writeSessionSearchTable(cmd *cobra.Command, response sessionSearchResponseDTO) error {
	out := cmd.OutOrStdout()
	if len(response.Results) == 0 {
		// An empty page is still an answer: say so, and say what the index could
		// not see, so "no results" is not read as "this never happened".
		if _, err := fmt.Fprintln(out, "No sessions matched."); err != nil {
			return err
		}
		if note := coverageNote(response.Coverage); note != "" {
			_, err := fmt.Fprintf(out, "%s\n", note)
			return err
		}
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tSESSION\tWHEN\tHARNESS\tKIND\tMATCH")
	for _, result := range response.Results {
		name := result.SessionName
		if name == "" {
			name = result.SessionID
		}
		when := result.CreatedAt
		if parsed, err := time.Parse(time.RFC3339Nano, result.CreatedAt); err == nil {
			when = parsed.Local().Format("2006-01-02 15:04")
		}
		// The row is the session's best-matching message; the count is how many
		// of its messages matched, as the desktop row shows it.
		match := oneLine(result.Snippet)
		switch {
		case result.SessionMatches == 1:
			match += " (1 match)"
		case result.SessionMatches > 1:
			match += fmt.Sprintf(" (%d matches)", result.SessionMatches)
		}
		// The id first: it is what `ao session resume-agent` and an in-app
		// session link take, and a name is not unique.
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			result.SessionID, name, when, result.Harness, result.Kind, match)
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if note := coverageNote(response.Coverage); note != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", note)
	}
	return nil
}

// writeIndexStatus reports what the index covers, which is what a caller checks
// before trusting an empty search.
func writeIndexStatus(cmd *cobra.Command, response sessionSearchIndexStatusDTO) error {
	out := cmd.OutOrStdout()
	coverage := response.Coverage
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(writer, "STATE\t%s\n", coverage.State)
	_, _ = fmt.Fprintf(writer, "DOCUMENTS\t%d\n", coverage.IndexedDocs)
	_, _ = fmt.Fprintf(writer, "SOURCES\t%d indexed, %d pending, %d unavailable, %d unsupported\n",
		coverage.IndexedSources, coverage.PendingSources, coverage.UnavailableSources, coverage.UnsupportedSources)
	if coverage.BackfilledAt != "" {
		_, _ = fmt.Fprintf(writer, "BACKFILLED\t%s\n", coverage.BackfilledAt)
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if len(coverage.UnindexedHarnesses) > 0 {
		_, _ = fmt.Fprintf(out, "NOT INDEXED\t%s\n", strings.Join(coverage.UnindexedHarnesses, ", "))
	}
	return nil
}

// coverageNote says what the index cannot see, so an empty result is not read as
// "this never happened".
func coverageNote(coverage sessionSearchCoverageDTO) string {
	parts := make([]string, 0, 2)
	switch coverage.State {
	case "backfilling":
		parts = append(parts, fmt.Sprintf("Index is still backfilling (%d sources pending).", coverage.PendingSources))
	case "unavailable":
		parts = append(parts, "The index could not be built; the daemon retries it every 30 seconds (see its log).")
	}
	if len(coverage.UnindexedHarnesses) > 0 {
		parts = append(parts, fmt.Sprintf("Transcripts are not indexed for: %s.", strings.Join(coverage.UnindexedHarnesses, ", ")))
	}
	return strings.Join(parts, " ")
}
