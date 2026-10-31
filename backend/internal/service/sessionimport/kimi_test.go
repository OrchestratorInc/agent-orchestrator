package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const kimiSession = "session_11111111-2222-3333-4444-555555555555"

// kimiFixture writes a synthetic Kimi session (state.json + main-agent
// wire.jsonl) under root and returns the wire.jsonl path. The shapes mirror
// real Kimi Code 2.3 transcripts but hold only invented content.
func kimiFixture(t *testing.T, root, workdir, sessionID, state, wire string) string {
	t.Helper()
	base := filepath.Join(root, "sessions", workdir, sessionID)
	if state != "" {
		writeFile(t, filepath.Join(base, "state.json"), state)
	}
	return writeFile(t, filepath.Join(base, "agents", "main", "wire.jsonl"), wire)
}

// A full turn: a typed prompt, injected context AO must drop, streamed
// reasoning and text, then a second turn. Assistant text streams as several
// content.part events within one turn and must join into one message.
const kimiWire = `{"type":"metadata","protocol_version":2,"created_at":1700000000000}
{"type":"profile.bind","agentId":"main","modelAlias":"kimi-for-coding","time":1700000000100}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"add a readme"}],"origin":{"kind":"user"},"id":"m1"},"time":1700000001000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"<environment_context>\n<cwd>/work/repo</cwd>\n</environment_context>"}],"origin":{"kind":"injection"}},"time":1700000001100}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"step.begin","turnId":0,"step":1}}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","turnId":0,"step":1,"part":{"type":"think","think":"planning the readme"}}}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","turnId":0,"step":1,"part":{"type":"text","text":"Sure, "}},"time":1700000002000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","turnId":0,"step":1,"part":{"type":"text","text":"adding it now."}},"time":1700000002100}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"step.end","turnId":0,"step":1,"finishReason":"end_turn"}}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"REVIEW SKILL EXPANSION TEXT"}],"origin":{"kind":"skill_activation","skillName":"review"},"id":"m2"},"time":1700000003000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"now run tests"}],"origin":"user","id":"m3"},"time":1700000004000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","turnId":1,"step":1,"part":{"type":"text","text":"Tests pass."}},"time":1700000005000}
`

const kimiStateJSON = `{"id":"` + kimiSession + `","version":2,"cwd":"/work/repo","createdAt":1700000000000,"updatedAt":1700000005000,"title":"Add a README","isCustomTitle":false}`

func TestKimiDiscoverAndParse(t *testing.T) {
	root := t.TempDir()
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, kimiStateJSON, kimiWire)

	files, err := new(Service).discover(context.Background(), domain.HarnessKimi, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("discover found %d sessions, want 1", len(files))
	}
	if got := files[0].nativeID(); got != kimiSession {
		t.Fatalf("nativeID = %q, want %q", got, kimiSession)
	}
	if files[0].paths[0] != path {
		t.Fatalf("path = %q, want %q", files[0].paths[0], path)
	}

	got, err := readTranscript(context.Background(), domain.HarnessKimi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages,
		"user: add a readme",
		"assistant: Sure, adding it now.",
		"user: now run tests",
		"assistant: Tests pass.",
	)
	if got.title != "Add a README" || got.cwd != "/work/repo" || got.nativeID != kimiSession {
		t.Fatalf("title=%q cwd=%q id=%q", got.title, got.cwd, got.nativeID)
	}
}

func TestKimiDropsInjectedContext(t *testing.T) {
	root := t.TempDir()
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, kimiStateJSON, kimiWire)
	got, err := readTranscript(context.Background(), domain.HarnessKimi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.messages {
		if m.Text == "REVIEW SKILL EXPANSION TEXT" || m.Role == domain.MessageRoleUser && m.Text != "add a readme" && m.Text != "now run tests" {
			t.Fatalf("injected content leaked: %q", m.Text)
		}
	}
}

func TestKimiTitleFallsBackToPromptWithoutState(t *testing.T) {
	root := t.TempDir()
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, "", kimiWire)
	got, err := readTranscript(context.Background(), domain.HarnessKimi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "add a readme" {
		t.Fatalf("title = %q, want first prompt", got.title)
	}
	if got.cwd != "" {
		t.Fatalf("cwd = %q, want empty without state", got.cwd)
	}
}

func TestKimiRejectsConversationWithoutTypedPrompt(t *testing.T) {
	root := t.TempDir()
	onlyInjected := `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"<environment_context/>"}],"origin":{"kind":"injection"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","turnId":0,"step":1,"part":{"type":"text","text":"ignored"}},"time":1700000002000}
`
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, "", onlyInjected)
	if _, err := readTranscript(context.Background(), domain.HarnessKimi, root, path); err == nil {
		t.Fatal("expected a session with no typed prompt to be rejected")
	}
}

func TestKimiDiscoverIgnoresSubagentsAndStrays(t *testing.T) {
	root := t.TempDir()
	kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, kimiStateJSON, kimiWire)
	// A subagent transcript sits beside the main agent and is not a conversation.
	writeFile(t, filepath.Join(root, "sessions", "wd_repo_abc123def456", kimiSession, "agents", "research", "wire.jsonl"), kimiWire)
	// A directory that is not a session_<uuid> must be ignored entirely.
	writeFile(t, filepath.Join(root, "sessions", "wd_repo_abc123def456", "not_a_session", "agents", "main", "wire.jsonl"), kimiWire)
	// A stray index cache file must not be read as a conversation.
	writeFile(t, filepath.Join(root, "sessions", ".index-cache", "whatever.jsonl"), "{}\n")

	files, err := new(Service).discover(context.Background(), domain.HarnessKimi, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("discover found %d sessions, want exactly the main conversation", len(files))
	}
	if files[0].nativeID() != kimiSession {
		t.Fatalf("nativeID = %q", files[0].nativeID())
	}
}

func TestKimiStateIdentityMismatchRejected(t *testing.T) {
	root := t.TempDir()
	wrong := `{"id":"session_99999999-8888-7777-6666-555555555555","cwd":"/work/repo","title":"x","updatedAt":1700000005000}`
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, wrong, kimiWire)
	if _, err := readTranscript(context.Background(), domain.HarnessKimi, root, path); err == nil {
		t.Fatal("expected a state.json id mismatch to be rejected")
	}
}

// context.clear resets the conversation, so a /clear-and-restart imports only
// what followed, and the title fallback cannot reach a pre-clear prompt.
func TestKimiContextClearResetsHistory(t *testing.T) {
	root := t.TempDir()
	wire := `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"first idea"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"ok first"}},"time":1700000002000}
{"type":"context.clear","agentId":"main","time":1700000003000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"second idea"}],"origin":{"kind":"user"}},"time":1700000004000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"ok second"}},"time":1700000005000}
`
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, "", wire)
	got, err := readTranscript(context.Background(), domain.HarnessKimi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: second idea", "assistant: ok second")
	if got.title != "second idea" {
		t.Fatalf("title = %q, want post-clear prompt", got.title)
	}
}

// context.undo count=N retracts the last N typed-prompt turns (prompt plus the
// assistant text after it); undoing every turn leaves no conversation.
func TestKimiContextUndoRetractsTurns(t *testing.T) {
	twoTurns := `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"idea one"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"reply one"}},"time":1700000002000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"idea two"}],"origin":{"kind":"user"}},"time":1700000003000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"reply two"}},"time":1700000004000}
`
	t.Run("drops the last turn", func(t *testing.T) {
		root := t.TempDir()
		wire := twoTurns + `{"type":"context.undo","agentId":"main","count":1,"time":1700000005000}` + "\n"
		path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, "", wire)
		got, err := readTranscript(context.Background(), domain.HarnessKimi, root, path)
		if err != nil {
			t.Fatal(err)
		}
		wantTexts(t, got.messages, "user: idea one", "assistant: reply one")
	})
	t.Run("undoing every turn leaves no conversation", func(t *testing.T) {
		root := t.TempDir()
		wire := twoTurns + `{"type":"context.undo","agentId":"main","count":2,"time":1700000005000}` + "\n"
		path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, "", wire)
		if _, err := readTranscript(context.Background(), domain.HarnessKimi, root, path); err == nil {
			t.Fatal("expected a fully undone session to be rejected")
		}
	})
}

// Undo parity with Kimi's isUndoAnchor: origin-less prompts count and show,
// in-turn steers show but are not anchors, user-slash skill/plugin activations
// are hidden anchors, and undo never crosses a compaction.
func TestKimiUndoAnchorParity(t *testing.T) {
	read := func(t *testing.T, wire string) (transcript, error) {
		root := t.TempDir()
		path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, "", wire)
		return readTranscript(context.Background(), domain.HarnessKimi, root, path)
	}

	t.Run("origin-less prompt shows and is a typed prompt", func(t *testing.T) {
		got, err := read(t, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"no origin prompt"}]},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"a reply"}},"time":1700000002000}
`)
		if err != nil {
			t.Fatal(err)
		}
		wantTexts(t, got.messages, "user: no origin prompt", "assistant: a reply")
	})

	t.Run("in-turn steer shows but is not an undo anchor", func(t *testing.T) {
		got, err := read(t, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"task A"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"a1"}},"time":1700000002000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"steer S"}],"origin":{"kind":"user","inTurn":true}},"time":1700000003000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"a2"}},"time":1700000004000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"task B"}],"origin":{"kind":"user"}},"time":1700000005000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"b1"}},"time":1700000006000}
{"type":"context.undo","agentId":"main","count":1,"time":1700000007000}
`)
		if err != nil {
			t.Fatal(err)
		}
		// Undo 1 retracts only task B; the steer is not counted, so it survives.
		wantTexts(t, got.messages, "user: task A", "assistant: a1", "user: steer S", "assistant: a2")
	})

	t.Run("user-slash skill is a hidden anchor undo retracts", func(t *testing.T) {
		got, err := read(t, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"task P"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"p1"}},"time":1700000002000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"EXPANDED REVIEW SKILL"}],"origin":{"kind":"skill_activation","trigger":"user-slash","skillName":"review"}},"time":1700000003000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"s1"}},"time":1700000004000}
{"type":"context.undo","agentId":"main","count":1,"time":1700000005000}
`)
		if err != nil {
			t.Fatal(err)
		}
		// Undo 1 retracts the skill turn (hidden anchor), leaving the prior prompt.
		wantTexts(t, got.messages, "user: task P", "assistant: p1")
	})

	t.Run("undo stops at a compaction boundary", func(t *testing.T) {
		got, err := read(t, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"before compaction"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"a reply"}},"time":1700000002000}
{"type":"context.apply_compaction","agentId":"main","keptUserMessageCount":1,"time":1700000003000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"after compaction"}],"origin":{"kind":"user"}},"time":1700000004000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"b reply"}},"time":1700000005000}
{"type":"context.undo","agentId":"main","count":5,"time":1700000006000}
`)
		if err != nil {
			t.Fatal(err)
		}
		// Undo 5 removes the post-compaction turn then stops; pre-compaction stays.
		wantTexts(t, got.messages, "user: before compaction", "assistant: a reply")
	})

	t.Run("empty typed prompt still anchors undo", func(t *testing.T) {
		got, err := read(t, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"task A"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"a reply"}},"time":1700000002000}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"image","source":{"data":"x"}}],"origin":{"kind":"user"}},"time":1700000003000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"b reply"}},"time":1700000004000}
{"type":"context.undo","agentId":"main","count":1,"time":1700000005000}
`)
		if err != nil {
			t.Fatal(err)
		}
		// The image-only prompt carries no text to show but is still an anchor, so
		// undo 1 retracts it and its reply, leaving the earlier typed prompt.
		wantTexts(t, got.messages, "user: task A", "assistant: a reply")
	})

	t.Run("undo past every anchor empties the conversation", func(t *testing.T) {
		if _, err := read(t, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"only prompt"}],"origin":{"kind":"user"}},"time":1700000001000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"only reply"}},"time":1700000002000}
{"type":"context.undo","agentId":"main","count":3,"time":1700000003000}
`); err == nil {
			t.Fatal("expected an over-count undo with no compaction to be rejected")
		}
	})
}
