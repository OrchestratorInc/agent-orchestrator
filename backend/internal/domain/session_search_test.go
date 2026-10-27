package domain_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestParseSessionSearchQuery covers the one parser the palette, the Search
// page, and `ao search` all use. Each case names the syntax a person types.
// The match options exist so the benchmark can measure the alternatives; each
// must still produce the expression it names.
func TestParseSessionSearchQueryWithOptions(t *testing.T) {
	cases := []struct {
		name    string
		options domain.SessionSearchMatchOptions
		want    string
	}{
		{"no stemming", domain.SessionSearchMatchOptions{NoStemming: true}, `("flaky" OR "logins"*)`},
		// v1 required every word as typed, stopwords included.
		{"every word required", domain.SessionSearchMatchOptions{RequireAllWords: true, NoStemming: true}, `"the"* AND "flaky"* AND "logins"*`},
		{"prefix every word", domain.SessionSearchMatchOptions{NoStemming: true, Prefix: domain.SessionSearchPrefixAll}, `("flaky"* OR "logins"*)`},
		{"prefix none", domain.SessionSearchMatchOptions{NoStemming: true, Prefix: domain.SessionSearchPrefixNone}, `("flaky" OR "logins")`},
		{"keep stopwords", domain.SessionSearchMatchOptions{NoStemming: true, KeepStopwords: true}, `("the" OR "flaky" OR "logins"*)`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := domain.ParseSessionSearchQueryWith("the flaky logins", testCase.options).MatchQuery; got != testCase.want {
				t.Fatalf("match = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestParseSessionSearchQuery(t *testing.T) {
	cases := []struct {
		name  string
		input string
		check func(t *testing.T, query domain.SessionSearchParsedQuery)
	}{
		{
			// Plain words are ranked, not required: bm25 orders the documents
			// that contain any of them. Each matches as a prefix of its stem, so
			// "flaky" also finds "flake" and "flakiness".
			name:  "plain words are OR-ed stems ranked by bm25",
			input: "flaky login",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `("flak"* OR "login"*)` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
				if query.AllTermsQuery != `"flak"* AND "login"*` {
					t.Fatalf("all terms = %q", query.AllTermsQuery)
				}
				if len(query.Terms) != 2 {
					t.Fatalf("terms = %v", query.Terms)
				}
			},
		},
		{
			// A natural-language question: its stopwords would match nearly every
			// message, so only its content words are ranked.
			name:  "a question ranks its content words",
			input: "how did we fix the failing login tests?",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `("fix" OR "fail"* OR "login" OR "test"*)` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			// Only the last word is a prefix (it is the one still being typed),
			// unless stemming makes a word a prefix of its root.
			name:  "only the last plain word is prefix-matched",
			input: "login bu",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `("login" OR "bu"*)` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			// A query of nothing but stopwords still searches for them.
			name:  "a query of only stopwords keeps them",
			input: "the",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"the"*` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "a quoted phrase matches whole and is not prefix-expanded",
			input: `"resets its backoff"`,
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"resets its backoff"` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "an exact token is not prefix-expanded",
			input: `'handleSubmit`,
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"handleSubmit"` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "a prefix anchor expands",
			input: `^src/auth`,
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"src/auth"*` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "exclusion is a session-level expression, not part of the match",
			input: "backoff !upload",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"backoff"*` {
					t.Fatalf("match = %q, want only the positive term", query.MatchQuery)
				}
				if query.ExcludeQuery != `"upload"` {
					t.Fatalf("exclude = %q", query.ExcludeQuery)
				}
			},
		},
		{
			name:  "several exclusions OR together",
			input: "backoff !upload !legacy",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.ExcludeQuery != `"upload" OR "legacy"` {
					t.Fatalf("exclude = %q", query.ExcludeQuery)
				}
			},
		},
		{
			name:  "harness and project operators filter",
			input: "retry harness:codex project:api",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Harness != "codex" || query.Project != "api" {
					t.Fatalf("query = %+v", query)
				}
				if query.MatchQuery != `"retr"*` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "machine and path operators filter",
			input: "retry machine:mac path:src/auth",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Machine != "mac" || query.Path != "src/auth" {
					t.Fatalf("query = %+v", query)
				}
			},
		},
		{
			name:  "status aliases normalize to the durable states",
			input: "retry status:terminated",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Status != domain.SessionSearchStatusKilled {
					t.Fatalf("status = %q", query.Status)
				}
			},
		},
		{
			name:  "status live and hibernated are distinct",
			input: "retry status:hibernated",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Status != domain.SessionSearchStatusHibernated {
					t.Fatalf("status = %q", query.Status)
				}
			},
		},
		{
			name:  "before and after set the bounds",
			input: "retry after:2026-07-01 before:2026-08-01",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.From == nil || query.From.Format(time.DateOnly) != "2026-07-01" {
					t.Fatalf("from = %v", query.From)
				}
				if query.To == nil || query.To.Format(time.DateOnly) != "2026-08-01" {
					t.Fatalf("to = %v", query.To)
				}
			},
		},
		{
			name:  "before accepts a full timestamp",
			input: "retry before:2026-08-01T09:30:00Z",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.To == nil || !query.To.Equal(time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)) {
					t.Fatalf("to = %v", query.To)
				}
			},
		},
		{
			name:  "sort recent switches to browsing order",
			input: "retry sort:recent",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Sort != domain.SessionSearchSortRecent {
					t.Fatalf("sort = %q", query.Sort)
				}
			},
		},
		{
			name:  "a PR URL resolves to its session",
			input: "https://github.com/OrchestratorInc/agent-orchestrator/pull/6553",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.PRRef == nil {
					t.Fatal("PR URL not recognized")
				}
				if query.PRRef.URL != "https://github.com/OrchestratorInc/agent-orchestrator/pull/6553" {
					t.Fatalf("url = %q", query.PRRef.URL)
				}
			},
		},
		{
			name:  "a non-PR URL stays a search term",
			input: "https://example.com/docs",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.PRRef != nil {
					t.Fatalf("recognized a non-PR URL: %+v", query.PRRef)
				}
				if query.MatchQuery == "" {
					t.Fatal("a non-PR URL must still be searchable")
				}
			},
		},
		{
			name:  "an operator with no value stays a search term",
			input: "project:",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Project != "" {
					t.Fatalf("empty operator filtered everything out: %+v", query)
				}
				if query.MatchQuery == "" {
					t.Fatal("a bare operator must stay searchable")
				}
			},
		},
		{
			// Nothing in it is a letter or digit, so nothing can match: no
			// expression at all, rather than one FTS5 could reject.
			name:  "a lone quote matches nothing and is not FTS syntax",
			input: `"`,
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != "" {
					t.Fatalf("match = %q, want none", query.MatchQuery)
				}
			},
		},
		{
			name:  "a lone dash matches nothing and is not FTS syntax",
			input: "-",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != "" {
					t.Fatalf("match = %q, want none", query.MatchQuery)
				}
			},
		},
		{
			// "C++" tokenizes to the single letter c; as a prefix it expanded
			// to every term starting with c and took seconds on a real archive.
			name:  "a one-character word never becomes a prefix",
			input: "C++",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if strings.Contains(query.MatchQuery, "*") {
					t.Fatalf("match = %q, want no prefix term", query.MatchQuery)
				}
			},
		},
		{
			name:  "a one-character word beside real words is dropped",
			input: `100% \n`,
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"100%"*` {
					t.Fatalf("match = %q, want only the number", query.MatchQuery)
				}
			},
		},
		{
			name: "a pasted log ranks by its first distinct words only",
			input: strings.Repeat("ERROR reaper deadline ", 40) + strings.Join(func() []string {
				ids := make([]string, 60)
				for i := range ids {
					ids[i] = fmt.Sprintf("session%02d", i)
				}
				return ids
			}(), " "),
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if terms := strings.Count(query.MatchQuery, " OR ") + 1; terms != 24 {
					t.Fatalf("ranked terms = %d, want 24 (repeats once, then capped): %q", terms, query.MatchQuery)
				}
				if !strings.Contains(query.MatchQuery, `"error"`) && !strings.Contains(query.MatchQuery, `"ERROR"`) {
					t.Fatalf("the first words must be kept: %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "an exclusion alone leaves nothing to match",
			input: "!upload",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != "" {
					t.Fatalf("match = %q, want empty so the caller browses", query.MatchQuery)
				}
				if query.ExcludeQuery != `"upload"` {
					t.Fatalf("exclude = %q", query.ExcludeQuery)
				}
			},
		},
		{
			name:  "a created range sets both bounds at once",
			input: "retry created:2026-01-01..2026-06-01",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.From == nil || query.From.Format(time.DateOnly) != "2026-01-01" {
					t.Fatalf("from = %v", query.From)
				}
				if query.To == nil || query.To.Format(time.DateOnly) != "2026-06-01" {
					t.Fatalf("to = %v", query.To)
				}
				if query.MatchQuery != `"retr"*` {
					t.Fatalf("match = %q, want the operator removed from the text", query.MatchQuery)
				}
			},
		},
		{
			name:  "a created range with an open start",
			input: "retry created:*..2026-06-01",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.From != nil {
					t.Fatalf("from = %v, want no lower bound", query.From)
				}
				if query.To == nil || query.To.Format(time.DateOnly) != "2026-06-01" {
					t.Fatalf("to = %v", query.To)
				}
			},
		},
		{
			name:  "a malformed range is reported, not ignored",
			input: "retry created:bogus..2026-06-01",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if len(query.InvalidOperators) != 1 {
					t.Fatalf("invalid = %v, want the malformed operator named", query.InvalidOperators)
				}
				if err := query.Validate(); err == nil {
					t.Fatal("a malformed range must fail validation")
				}
			},
		},
		{
			name:  "a suffix anchor is recorded",
			input: "login.ts$",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if len(query.Suffixes) != 1 || query.Suffixes[0] != "login.ts" {
					t.Fatalf("suffixes = %v", query.Suffixes)
				}
				// The term is still matched so the candidate set is index-driven.
				if query.MatchQuery != `"login.ts"*` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "a pipe joins terms with OR",
			input: "foo | bar",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if !query.HasOr {
					t.Fatal("OR not recorded")
				}
				if query.MatchQuery != `"foo" OR "bar"*` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "the OR keyword joins terms with OR",
			input: "foo OR bar",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.MatchQuery != `"foo" OR "bar"*` {
					t.Fatalf("match = %q", query.MatchQuery)
				}
			},
		},
		{
			name:  "text echoes what the person typed",
			input: "  retry   harness:codex  ",
			check: func(t *testing.T, query domain.SessionSearchParsedQuery) {
				if query.Text != "retry harness:codex" {
					t.Fatalf("text = %q", query.Text)
				}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.check(t, domain.ParseSessionSearchQuery(testCase.input))
		})
	}
}

func TestParseSessionSearchQueryRejectsInvertedRange(t *testing.T) {
	query := domain.ParseSessionSearchQuery("retry after:2026-08-01 before:2026-07-01")
	if err := query.Validate(); err == nil {
		t.Fatal("inverted range accepted")
	}
}

// An identifier is findable by its parts, and the original is always kept.
func TestSessionSearchSplitIdentifiers(t *testing.T) {
	body := "handleSubmit src/auth/login_test.ts"
	expanded := domain.SessionSearchSplitIdentifiers(body, 10)
	if expanded[:len(body)] != body {
		t.Fatalf("expanded body does not keep the original prefix: %q", expanded)
	}
	for _, want := range []string{"submit", "handle", "login", "test", "auth", "src"} {
		if !containsWord(expanded, want) {
			t.Fatalf("expanded body %q is missing %q", expanded, want)
		}
	}
}

// TestSessionSearchSplitIdentifiersCamelAndAcronymBoundaries is the tokenization
// contract for code identifiers: a person searches for a word they can see in
// the source, which means camelCase, PascalCase, acronym runs, snake_case, and
// dotted/slashed paths all have to expose their parts.
func TestSessionSearchSplitIdentifiersCamelAndAcronymBoundaries(t *testing.T) {
	cases := []struct {
		identifier string
		want       []string
	}{
		{"handleSubmit", []string{"handle", "submit"}},
		{"snake_case_name", []string{"snake", "case", "name"}},
		{"src/auth/login_test.ts", []string{"src", "auth", "login", "test", "ts"}},
		{"MAX_RETRY_COUNT", []string{"max", "retry", "count"}},
		// The acronym run is the case a naive camel-case split loses: without it
		// "http" and "response" are invisible inside this identifier.
		{"parseHTTPResponse", []string{"parse", "http", "response"}},
		{"HTTPServer", []string{"http", "server"}},
		{"getHTTPStatusCode", []string{"get", "http", "status", "code"}},
		{"foo.bar.baz", []string{"foo", "bar", "baz"}},
		{"path/to/SomeFile.go", []string{"path", "to", "some", "file", "go"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.identifier, func(t *testing.T) {
			expanded := domain.SessionSearchSplitIdentifiers("x "+testCase.identifier, 20)
			for _, want := range testCase.want {
				if !containsWord(expanded, want) {
					t.Fatalf("%q expanded to %q, missing %q", testCase.identifier, expanded, want)
				}
			}
			// The original identifier must survive, so searching it whole still
			// matches without relying on the split.
			if !strings.Contains(expanded, testCase.identifier) {
				t.Fatalf("%q lost its original form: %q", testCase.identifier, expanded)
			}
		})
	}
}

// A token with no internal boundary must not gain spurious parts, or every
// lowercase word would be indexed twice and bm25 would be skewed by pure
// duplication. snake_case is a real boundary, so it is not in this list.
func TestSessionSearchSplitIdentifiersLeavesSimpleTokensAlone(t *testing.T) {
	for _, body := range []string{"id", "login", "response"} {
		expanded := domain.SessionSearchSplitIdentifiers(body, 10)
		if expanded != body {
			t.Fatalf("%q expanded to %q, want it unchanged", body, expanded)
		}
	}
}

func TestSessionSearchSplitIdentifiersIsBounded(t *testing.T) {
	body := "aaaBbb cccDdd eeeFff gggHhh"
	expanded := domain.SessionSearchSplitIdentifiers(body, 2)
	if containsWord(expanded, "hhh") {
		t.Fatalf("expansion exceeded its identifier budget: %q", expanded)
	}
	if !containsWord(expanded, "bbb") {
		t.Fatalf("expansion stopped too early: %q", expanded)
	}
}

func TestSessionSearchKindTierOrder(t *testing.T) {
	order := []domain.SessionSearchKind{
		domain.SessionSearchKindTitle,
		domain.SessionSearchKindUser,
		domain.SessionSearchKindPath,
		domain.SessionSearchKindAssistant,
		domain.SessionSearchKindTool,
		domain.SessionSearchKindCommand,
	}
	for index := 1; index < len(order); index++ {
		if domain.SessionSearchKindTier(order[index]) < domain.SessionSearchKindTier(order[index-1]) {
			t.Fatalf("tier order regressed at %q: %v", order[index], order)
		}
	}
	if domain.SessionSearchKindTier(domain.SessionSearchKindTitle) >= domain.SessionSearchKindTier(domain.SessionSearchKindCommand) {
		t.Fatal("a title must outrank command output")
	}
}

func TestSessionSearchResumeCommand(t *testing.T) {
	if got := domain.SessionSearchResumeCommand("proj-1"); got != "ao session resume-agent proj-1" {
		t.Fatalf("resume command = %q", got)
	}
	if got := domain.SessionSearchResumeCommand(""); got != "" {
		t.Fatalf("empty session produced %q", got)
	}
}

func TestSessionSearchTitlePrefersTheDisplayName(t *testing.T) {
	if got := domain.SessionSearchTitle("Fix the login test", "provider thread"); got != "Fix the login test" {
		t.Fatalf("title = %q", got)
	}
	if got := domain.SessionSearchTitle("", "provider thread"); got != "provider thread" {
		t.Fatalf("title = %q, want the provider fallback", got)
	}
	if got := domain.SessionSearchTitle("  ", "  "); got != "" {
		t.Fatalf("title = %q, want empty", got)
	}
}

func containsWord(haystack, needle string) bool {
	for _, field := range splitFields(haystack) {
		if field == needle {
			return true
		}
	}
	return false
}

func splitFields(value string) []string {
	var fields []string
	current := ""
	for _, character := range value {
		if character == ' ' || character == '\n' || character == '\t' {
			if current != "" {
				fields = append(fields, current)
				current = ""
			}
			continue
		}
		current += string(character)
	}
	if current != "" {
		fields = append(fields, current)
	}
	return fields
}
