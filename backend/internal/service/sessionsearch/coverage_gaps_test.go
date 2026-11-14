package sessionsearch_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
)

func filler(bytes int) string {
	const word = "progress steady "
	return strings.Repeat(word, bytes/len(word)+1)[:bytes]
}

func TestLongCommandOutputKeepsItsFirst3072Characters(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-long-output", domain.HarnessClaudeCode)
	output := "zzHeadNeedle started the suite " + filler(12<<10) + " zzMiddleNeedle " + filler(12<<10) + " zzTailNeedle failed"
	writeTranscript(t, root, filepath.Join("-tmp-proj", string(session.ID)+".jsonl"),
		claudeToolUseLine(t, "Bash", `{"command":"go test ./..."}`, "2026-08-09T12:00:00Z"),
		claudeToolResultLine(t, output, "2026-08-09T12:00:01Z"),
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzHeadNeedle").Results; len(got) != 1 {
		t.Fatalf("results = %s, want the start of the long output", describe(got))
	}
	for _, needle := range []string{"zzMiddleNeedle", "zzTailNeedle"} {
		if got := search(t, f.svc, needle).Results; len(got) != 0 {
			t.Fatalf("%s: output past its first 3,072 characters is not kept, got %s", needle, describe(got))
		}
	}
}

func TestLongMessageIsSearchableToItsEndAndCountsOnce(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-long-prompt", domain.HarnessClaudeCode)
	prompt := "zzRepeatedNeedle at the start " + filler(18<<10) + " zzRepeatedNeedle again and zzPromptEndNeedle at the end"
	writeTranscript(t, root, filepath.Join("-tmp-proj", string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", prompt, "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzPromptEndNeedle").Results; len(got) != 1 {
		t.Fatalf("results = %s, want the end of the 18 KiB prompt", describe(got))
	}
	got := search(t, f.svc, "zzRepeatedNeedle").Results
	if len(got) != 1 || got[0].SessionMatches != 1 {
		t.Fatalf("results = %s (matches %v), want one session with one match", describe(got), matchCounts(got))
	}
}

func TestChatLongMessageToItsEndAndLongOutputToItsStart(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-chat-long", "conv-chat-long")
	appendUserMessage(t, f.store, session, "conv-chat-long", "msg-long", "turn-long",
		"please look "+filler(18<<10)+" zzChatPromptEndNeedle")
	addCommandActivity(t, f.store, "conv-chat-long", "turn-long", "act-long", "go test ./...",
		"zzChatOutputHeadNeedle "+filler(12<<10)+" zzChatDeepTailNeedle "+filler(3800)+" FAIL zzChatOutputTailNeedle", domain.ActivityStatusFailed)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	for _, needle := range []string{"zzChatPromptEndNeedle", "zzChatOutputHeadNeedle"} {
		got := search(t, f.svc, needle).Results
		if len(got) != 1 {
			t.Fatalf("%s: results = %s", needle, describe(got))
		}
		if needle == "zzChatPromptEndNeedle" && got[0].MessageID != "msg-long" {
			t.Fatalf("a later chunk must open the message it belongs to, got %q", got[0].MessageID)
		}
	}
	for _, needle := range []string{"zzChatDeepTailNeedle", "zzChatOutputTailNeedle"} {
		if got := search(t, f.svc, needle).Results; len(got) != 0 {
			t.Fatalf("%s: output past its first 3,072 characters is not kept, got %s", needle, describe(got))
		}
	}
}

const phraseAcrossABreak = "alpha beta"

func textWithAPhraseAcrossABreak() string {
	return strings.Repeat("x ", 4092) + phraseAcrossABreak
}

func TestChatQuotedPhraseAcrossAChunkBreakIsFound(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-chat-phrase", "conv-chat-phrase")
	appendUserMessage(t, f.store, session, "conv-chat-phrase", "msg-phrase", "turn-phrase", textWithAPhraseAcrossABreak())
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	got := search(t, f.svc, `"`+phraseAcrossABreak+`"`).Results
	if len(got) != 1 || got[0].MessageID != "msg-phrase" {
		t.Fatalf("results = %s, want the message holding the phrase", describe(got))
	}
	if !strings.Contains(got[0].Snippet, phraseAcrossABreak) {
		t.Fatalf("snippet %q does not show the phrase", got[0].Snippet)
	}
	word := search(t, f.svc, "alpha").Results
	if len(word) != 1 || word[0].SessionMatches != 1 {
		t.Fatalf("results = %s (matches %v), want one session with one match", describe(word), matchCounts(word))
	}
	if miss := search(t, f.svc, `"beta alpha"`).Results; len(miss) != 0 {
		t.Fatalf("results = %s, want none for the words in the other order", describe(miss))
	}
}

func TestNativeQuotedPhraseAcrossAChunkBreakIsFound(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	session := seedTUI(t, f.store, "proj", "sess-native-phrase", domain.HarnessClaudeCode)
	writeTranscript(t, root, filepath.Join("-tmp-proj", string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", textWithAPhraseAcrossABreak(), "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	got := search(t, f.svc, `"`+phraseAcrossABreak+`"`).Results
	if len(got) != 1 || got[0].SessionID != string(session.ID) {
		t.Fatalf("results = %s, want the session holding the phrase", describe(got))
	}
	word := search(t, f.svc, "alpha").Results
	if len(word) != 1 || word[0].SessionMatches != 1 {
		t.Fatalf("results = %s (matches %v), want one session with one match", describe(word), matchCounts(word))
	}
}

func matchCounts(results []domain.SessionSearchResult) []int {
	out := make([]int, len(results))
	for index, result := range results {
		out[index] = result.SessionMatches
	}
	return out
}
