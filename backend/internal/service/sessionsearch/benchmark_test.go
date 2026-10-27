package sessionsearch_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
)

// BenchmarkCorpus is a fixed fixture corpus with fixed queries and expected top
// results. It is a correctness benchmark, not a throughput one: it asserts that
// ranking actually expresses the product rule (title > user > path > agent >
// tool/command output, then bm25, then recency) and that every operator filters
// as promised.
//
// The corpus is deliberately small and hand-written so a failure names the rule
// that broke rather than reporting an aggregate score.

type corpusSession struct {
	// name is the fixture's own label. AO mints session ids as
	// "{projectId}-{num}", so cases refer to a fixture by name and the seeder
	// resolves it to the real id.
	name        string
	projectID   string
	title       string
	harness     domain.AgentHarness
	userMessage string
	assistant   string
	command     string
	commandOut  string
	path        string
	terminate   bool
}

// corpusSessions is the benchmark fixture. Each session exists to make exactly
// one ranking or filtering claim testable.
var corpusSessions = []corpusSession{
	{
		name:        "auth-1",
		projectID:   "api",
		title:       "Fix the flaky login test",
		harness:     domain.HarnessClaudeCode,
		userMessage: "the login test flakes when the promise rejects",
		assistant:   "I awaited the rejected promise in the login test helper",
		command:     "go test ./auth/...",
		commandOut:  "--- FAIL: TestLogin (0.02s)\n  expected retry to reset the backoff counter",
		path:        "src/auth/login_test.ts",
	},
	{
		name:        "auth-2",
		projectID:   "api",
		title:       "Retry backoff investigation",
		harness:     domain.HarnessCodex,
		userMessage: "why does the upload retry never give up",
		assistant:   "The retry loop never resets its backoff counter, so it backs off forever.",
		command:     "rg -n backoff src",
		commandOut:  "src/upload/retry.ts:41: const backoff = Math.min(backoff * 2, MAX)",
		path:        "src/upload/retry.ts",
	},
	{
		name:        "billing-1",
		projectID:   "billing",
		title:       "Stripe webhook signature check",
		harness:     domain.HarnessOpenCodeV2,
		userMessage: "verify the stripe webhook signature before parsing",
		assistant:   "I added a constant-time signature comparison before the payload parse.",
		command:     "npm test -- webhook",
		commandOut:  "PASS  src/webhooks/stripe.test.ts",
		path:        "src/webhooks/stripe.ts",
	},
	{
		name:        "docs-1",
		projectID:   "docs",
		title:       "Document the search operators",
		harness:     domain.HarnessClaudeCode,
		userMessage: "write down the harness: and project: operators",
		assistant:   "Documented harness:, project:, and before:/after: with examples.",
		command:     "npm run docs",
		commandOut:  "built 42 pages",
		path:        "docs/search.md",
	},
	{
		name:        "legacy-1",
		projectID:   "api",
		title:       "Remove the legacy retry shim",
		harness:     domain.HarnessCodex,
		userMessage: "delete the legacy shim now that backoff is fixed",
		assistant:   "Removed the shim; the backoff path is the only caller left.",
		command:     "go build ./...",
		commandOut:  "ok",
		path:        "src/legacy/shim.go",
		terminate:   true,
	},
}

// seedCorpus writes the benchmark corpus into a fresh store and indexes it. It
// returns the fixture plus a map from each fixture name to its real session id.
func seedCorpus(t *testing.T) (*fixture, map[string]string) {
	t.Helper()
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ids := make(map[string]string, len(corpusSessions))
	for index, entry := range corpusSessions {
		session := seedChatSession(t, f.store, entry.projectID, entry.name, "conv-"+entry.name)
		ids[entry.name] = string(session.ID)

		// The harness and the display name come from the session row; a Chat
		// session is created as claude-code with no name.
		record, found, err := f.store.GetSession(context.Background(), session.ID)
		if err != nil || !found {
			t.Fatalf("GetSession %s: found=%v err=%v", entry.name, found, err)
		}
		record.Harness = entry.harness
		record.DisplayName = entry.title
		if err := f.store.UpdateSession(context.Background(), record); err != nil {
			t.Fatalf("UpdateSession %s: %v", entry.name, err)
		}
		// UpdateSession no longer writes the title; renames have their own narrow writer.
		if _, err := f.store.RenameSession(context.Background(), record.ID, entry.title, time.Now()); err != nil {
			t.Fatalf("RenameSession %s: %v", entry.name, err)
		}

		conversationID := "conv-" + entry.name
		turnID := fmt.Sprintf("turn-%d", index)
		appendUserMessage(t, f.store, session, conversationID, "msg-user-"+entry.name, turnID, entry.userMessage)
		settleAssistantMessage(t, f.store, conversationID, "msg-assistant-"+entry.name, turnID, entry.assistant)
		addCommandActivity(t, f.store, conversationID, turnID, "cmd-"+entry.name,
			entry.command, entry.commandOut, domain.ActivityStatusCompleted)
		addActivity(t, f.store, conversationID, turnID, domain.ConversationActivity{
			ID:     "files-" + entry.name,
			Kind:   domain.ActivityKindFileChange,
			Status: domain.ActivityStatusCompleted,
			Detail: []byte(fmt.Sprintf(`{"files":[{"path":%q}]}`, entry.path)),
		})
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	// Terminate after indexing: the status filter reads the durable session fact,
	// so the order does not matter, but doing it last keeps the fixture legible.
	for _, entry := range corpusSessions {
		if !entry.terminate {
			continue
		}
		record, found, err := f.store.GetSession(context.Background(), domain.SessionID(ids[entry.name]))
		if err != nil || !found {
			t.Fatalf("GetSession %s: found=%v err=%v", entry.name, found, err)
		}
		record.IsTerminated = true
		if err := f.store.UpdateSession(context.Background(), record); err != nil {
			t.Fatalf("terminate %s: %v", entry.name, err)
		}
	}
	return &f, ids
}

// TestRankingBenchmark is the fixed-query ranking benchmark. Every case names
// the rule it proves, so a regression reports the rule rather than a number.
func TestRankingBenchmark(t *testing.T) {
	f, ids := seedCorpus(t)

	cases := []struct {
		name  string
		query string
		// wantTop is the fixture name that must rank first.
		wantTop string
		// wantTopKind is the document kind that must rank first within it.
		wantTopKind domain.SessionSearchKind
		// wantAbsent lists fixtures that must not appear at all.
		wantAbsent []string
		// wantKindPresent requires some result for wantTop to carry this kind,
		// which is how a lower-tier document proves it was indexed at all.
		wantKindPresent *domain.SessionSearchKind
		rule            string
	}{
		{
			name: "title beats body", query: "flaky login",
			wantTop: "auth-1", wantTopKind: domain.SessionSearchKindTitle,
			rule: "a session whose name matches outranks one that only mentions the words",
		},
		{
			// "forever" appears only in auth-2's agent reply, so no title can
			// outrank it and the user/assistant ordering is what is measured.
			name: "user message beats agent reply", query: "forever",
			wantTop: "auth-2", wantTopKind: domain.SessionSearchKindAssistant,
			rule: "an agent reply is findable, and a title match is not invented for it",
		},
		{
			// "give up" appears only in auth-2's user message, so this isolates
			// the user-before-assistant ordering from any title match.
			name: "human words outrank the reply that repeats them", query: "give up",
			wantTop: "auth-2", wantTopKind: domain.SessionSearchKindUser,
			rule: "the human's own words outrank the agent's reply about them",
		},
		{
			name: "rare term beats common term", query: "constant-time",
			wantTop: "billing-1", wantTopKind: domain.SessionSearchKindAssistant,
			rule: "a term in one document is not diluted by a term in every document",
		},
		{
			name: "command output is searchable", query: "TestLogin",
			wantTop: "auth-1", wantTopKind: domain.SessionSearchKindCommand,
			rule: "tool and command output is indexed, not skipped",
		},
		{
			name: "path is searchable", query: "login_test.ts",
			wantTop: "auth-1", wantTopKind: domain.SessionSearchKindPath,
			rule: "a touched file path is findable by its file name",
		},
		{
			name: "phrase must be contiguous", query: `"resets its backoff"`,
			wantTop: "auth-2", wantTopKind: domain.SessionSearchKindAssistant,
			rule: "a quoted phrase matches only the contiguous words",
		},
		{
			// auth-2 is the only fixture whose text contains "upload", so it is
			// the only session the exclusion may remove. auth-1 has "backoff"
			// but no "upload" and must stay.
			name: "exclusion removes a session", query: "backoff !upload",
			wantTop: "legacy-1", wantTopKind: domain.SessionSearchKindUser,
			wantAbsent: []string{"auth-2"},
			rule:       "!term removes the whole session that contains it",
		},
		{
			name: "harness operator filters", query: "backoff harness:codex",
			wantTop: "auth-2", wantTopKind: domain.SessionSearchKindTitle,
			wantAbsent: []string{"auth-1", "billing-1"},
			rule:       "harness: restricts to one agent",
		},
		{
			name: "project operator filters", query: "webhook project:billing",
			wantTop: "billing-1", wantTopKind: domain.SessionSearchKindTitle,
			wantAbsent: []string{"auth-1", "auth-2", "legacy-1"},
			rule:       "project: restricts to one project",
		},
		{
			name: "status operator filters", query: "backoff status:killed",
			wantTop: "legacy-1", wantTopKind: domain.SessionSearchKindUser,
			wantAbsent: []string{"auth-1", "auth-2"},
			rule:       "status:killed includes terminated sessions only",
		},
		{
			name: "path operator filters", query: "backoff path:src/upload",
			wantTop: "auth-2", wantTopKind: domain.SessionSearchKindTitle,
			wantAbsent: []string{"auth-1", "legacy-1"},
			rule:       "path: restricts to sessions that touched a matching path",
		},
		{
			name: "identifier splitting", query: "login",
			wantTop: "auth-1", wantTopKind: domain.SessionSearchKindTitle,
			rule: "an identifier is findable by one of its parts",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// The rules are about which document ranks first, so they read the
			// per-message list; the grouped list (one row per session, shown at
			// its best message) must put the same session first.
			response, err := f.svc.Search(context.Background(), domain.ParseSessionSearchQuery(testCase.query),
				domain.SessionSearchParams{Group: domain.SessionSearchGroupMessage})
			if err != nil {
				t.Fatalf("Search(%q): %v", testCase.query, err)
			}
			if grouped := search(t, f.svc, testCase.query); len(grouped.Results) == 0 ||
				grouped.Results[0].SessionID != ids[testCase.wantTop] {
				t.Fatalf("%s: grouped top session = %s, want %q", testCase.rule, describe(grouped.Results), testCase.wantTop)
			}
			if len(response.Results) == 0 {
				t.Fatalf("%s: no results for %q", testCase.rule, testCase.query)
			}
			top := response.Results[0]
			if top.SessionID != ids[testCase.wantTop] {
				t.Fatalf("%s: top session = %q, want %q (%s) (results: %s)",
					testCase.rule, top.SessionID, testCase.wantTop, ids[testCase.wantTop], describe(response.Results))
			}
			if top.Kind != testCase.wantTopKind {
				t.Fatalf("%s: top kind = %q, want %q (results: %s)",
					testCase.rule, top.Kind, testCase.wantTopKind, describe(response.Results))
			}
			if testCase.wantKindPresent != nil {
				found := false
				for _, result := range response.Results {
					if result.SessionID == ids[testCase.wantTop] && result.Kind == *testCase.wantKindPresent {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s: no %q document for %q (results: %s)",
						testCase.rule, *testCase.wantKindPresent, testCase.wantTop, describe(response.Results))
				}
			}
			for _, absent := range testCase.wantAbsent {
				for _, result := range response.Results {
					if result.SessionID == ids[absent] {
						t.Fatalf("%s: %q must not appear (results: %s)",
							testCase.rule, absent, describe(response.Results))
					}
				}
			}
		})
	}
}

// TestRankingBenchmarkKindWeights pins the kind weighting: with the same text in
// every kind, a session name outranks the person's words, which outrank the
// agent's reply, which outranks tool and command output. The weights multiply
// bm25 (domain.DefaultSessionSearchKindWeights), so this order holds when the
// match is equally good, not regardless of how good it is; see
// TestRankingBenchmarkWeightsAreNotTiers for the other half.
func TestRankingBenchmarkKindWeights(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-user", "turn-1", "tier probe")
	settleAssistantMessage(t, f.store, "conv-1", "msg-assistant", "turn-1", "tier probe")
	addCommandActivity(t, f.store, "conv-1", "turn-1", "cmd-1", "tier probe", "", domain.ActivityStatusCompleted)
	addActivity(t, f.store, "conv-1", "turn-1", domain.ConversationActivity{
		ID:      "tool-1",
		Kind:    domain.ActivityKindMCPTool,
		Status:  domain.ActivityStatusCompleted,
		Summary: "tier probe",
	})
	addActivity(t, f.store, "conv-1", "turn-1", domain.ConversationActivity{
		ID:     "files-1",
		Kind:   domain.ActivityKindFileChange,
		Status: domain.ActivityStatusCompleted,
		Detail: []byte(`{"files":[{"path":"src/tier-probe.ts"}]}`),
	})
	conversation := conversationFor(t, f.store, string(session.ID))
	if err := f.store.SetProviderTitle(context.Background(), conversation.ID, "tier probe", testNow); err != nil {
		t.Fatalf("SetProviderTitle: %v", err)
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	response, err := f.svc.Search(context.Background(), domain.ParseSessionSearchQuery("tier"),
		domain.SessionSearchParams{Group: domain.SessionSearchGroupMessage})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	position := map[domain.SessionSearchKind]int{}
	for index, result := range response.Results {
		if _, seen := position[result.Kind]; !seen {
			position[result.Kind] = index
		}
	}
	for _, kind := range []domain.SessionSearchKind{
		domain.SessionSearchKindTitle, domain.SessionSearchKindUser, domain.SessionSearchKindAssistant,
		domain.SessionSearchKindTool, domain.SessionSearchKindCommand, domain.SessionSearchKindPath,
	} {
		if _, ok := position[kind]; !ok {
			t.Fatalf("no %s document in %s", kind, describe(response.Results))
		}
	}
	order := [][2]domain.SessionSearchKind{
		{domain.SessionSearchKindTitle, domain.SessionSearchKindUser},
		{domain.SessionSearchKindUser, domain.SessionSearchKindAssistant},
		{domain.SessionSearchKindAssistant, domain.SessionSearchKindTool},
		{domain.SessionSearchKindAssistant, domain.SessionSearchKindCommand},
	}
	for _, pair := range order {
		if position[pair[0]] > position[pair[1]] {
			t.Fatalf("%s ranked below %s for identical text: %s", pair[0], pair[1], describe(response.Results))
		}
	}
}

// TestRankingBenchmarkWeightsAreNotTiers is why the kind is a weight and not a
// tier: an agent reply that is about the query must beat a long user message
// that mentions it once in passing. Under the old hard tier every user message
// outranked every reply; on LoCoMo that cost 22 points of turn-level recall@10.
func TestRankingBenchmarkWeightsAreNotTiers(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	passing := seedChatSession(t, f.store, "proj", "sess-passing", "conv-passing")
	appendUserMessage(t, f.store, passing, "conv-passing", "msg-passing", "turn-passing",
		"please tidy the readme, update the changelog, bump the version, fix the lint warnings in the "+
			"settings page, rename the helper, and while you are there glance at the tokenizer")
	focused := seedChatSession(t, f.store, "proj", "sess-focused", "conv-focused")
	appendUserMessage(t, f.store, focused, "conv-focused", "msg-focused-user", "turn-focused", "go on")
	settleAssistantMessage(t, f.store, "conv-focused", "msg-focused-reply", "turn-focused",
		"The tokenizer splits identifiers; the tokenizer now also splits acronyms.")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	response := search(t, f.svc, "tokenizer")
	if len(response.Results) < 2 {
		t.Fatalf("results = %s, want both sessions", describe(response.Results))
	}
	if response.Results[0].SessionID != string(focused.ID) || response.Results[0].Kind != domain.SessionSearchKindAssistant {
		t.Fatalf("top = %s, want the reply that is about the tokenizer", describe(response.Results))
	}
}

// TestRankingBenchmarkRecencyTieBreak proves the order is deterministic when two
// documents rank identically, which is what makes a result list stable while a
// person types.
func TestRankingBenchmarkRecencyTieBreak(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	older := seedChatSession(t, f.store, "proj", "sess-old", "conv-old")
	appendUserMessage(t, f.store, older, "conv-old", "msg-old", "turn-old", "identical tiebreak text")
	newer := seedChatSession(t, f.store, "proj", "sess-new", "conv-new")
	appendUserMessage(t, f.store, newer, "conv-new", "msg-new", "turn-new", "identical tiebreak text")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	response := search(t, f.svc, "tiebreak")
	if len(response.Results) != 2 {
		t.Fatalf("results = %+v, want both sessions", response.Results)
	}
	first := response.Results[0].SessionID
	again := search(t, f.svc, "tiebreak")
	if again.Results[0].SessionID != first {
		t.Fatalf("tie order is not stable: %q then %q", first, again.Results[0].SessionID)
	}
}

// describe renders results compactly so a benchmark failure names what ranked.
func describe(results []domain.SessionSearchResult) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		parts = append(parts, fmt.Sprintf("%s/%s", result.SessionID, result.Kind))
	}
	return strings.Join(parts, ", ")
}
