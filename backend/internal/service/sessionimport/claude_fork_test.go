package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A forked or resumed Claude conversation starts its own file with copies of
// the ancestor's records, which keep the ancestor's sessionId. The file is
// still one readable conversation, titled by its own records only.
func TestClaudeTranscriptKeepsForkedAncestorRecords(t *testing.T) {
	parent := "1a842f77-b1fe-49f3-8585-8946df267bae"
	id := "2b842f77-b1fe-49f3-8585-8946df267bae"
	body := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + parent + `","cwd":"/work/repo","message":{"role":"user","content":"plan the migration"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"` + parent + `","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"Here is a plan."}]}}
{"type":"custom-title","customTitle":"Parent's rename","sessionId":"` + parent + `"}
{"type":"user","uuid":"u2","parentUuid":"a1","sessionId":"` + id + `","cwd":"/work/repo","message":{"role":"user","content":"now try the other approach"}}
{"type":"assistant","uuid":"a2","parentUuid":"u2","sessionId":"` + id + `","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"Trying it."}]}}
`
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "projects", "-work-repo", id+".jsonl"), body)
	got, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, path)
	if err != nil {
		t.Fatalf("a forked transcript must stay importable: %v", err)
	}
	wantTexts(t, got.messages, "user: plan the migration", "assistant: Here is a plan.", "user: now try the other approach", "assistant: Trying it.")
	if got.title != "plan the migration" {
		t.Fatalf("title = %q; the ancestor's rename belongs to the ancestor", got.title)
	}
}
