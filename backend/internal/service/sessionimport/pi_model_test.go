package sessionimport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An imported Pi chat resumes on the model and thinking level its active
// branch last chose, not on AO's or Pi's default, which may be a provider the
// chat never used. A model chosen on an abandoned branch does not count.
func TestPiTranscriptKeepsTheActiveBranchModel(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, piPath(root, piSession), `{"type":"session","version":1,"id":"`+piSession+`","timestamp":"2026-10-08T09:11:21.828Z","cwd":"/work/repo"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-10-08T09:11:28.742Z","provider":"zai-glm","modelId":"glm-4.5"}
{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-10-08T09:11:28.742Z","thinkingLevel":"low"}
{"type":"message","id":"u1","parentId":"t1","timestamp":"2026-10-08T09:11:30.000Z","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-08T09:11:40.000Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}
{"type":"model_change","id":"m2","parentId":"a1","timestamp":"2026-10-08T09:12:00.000Z","provider":"zai-coding-cn","modelId":"glm-5.3-flash"}
{"type":"thinking_level_change","id":"t2","parentId":"m2","timestamp":"2026-10-08T09:12:00.000Z","thinkingLevel":"high"}
{"type":"message","id":"u2","parentId":"t2","timestamp":"2026-10-08T09:12:10.000Z","message":{"role":"user","content":[{"type":"text","text":"second prompt"}]}}
{"type":"model_change","id":"m3","parentId":"u2","timestamp":"2026-10-08T09:12:20.000Z","provider":"openai","modelId":"gpt-5.4"}
{"type":"message","id":"a2","parentId":"u2","timestamp":"2026-10-08T09:12:30.000Z","message":{"role":"assistant","content":[{"type":"text","text":"second answer"}]}}
`)
	got, err := readTranscript(context.Background(), domain.HarnessPi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.model != "zai-coding-cn/glm-5.3-flash" || got.effort != "high" {
		t.Fatalf("model=%q effort=%q, want the active branch's zai-coding-cn/glm-5.3-flash at high", got.model, got.effort)
	}
}

func TestPiImportPersistsModelAndEffort(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	cwd, _ := json.Marshal(project)
	raw := `{"type":"session","version":1,"id":"` + piSession + `","cwd":CWD}
{"type":"model_change","id":"m1","parentId":null,"provider":"zai-coding-cn","modelId":"glm-5.3-flash"}
{"type":"thinking_level_change","id":"t1","parentId":"m1","thinkingLevel":"high"}
` + piMsg("u1", "t1", "user", "keep my model") + "\n" + piMsg("a1", "u1", "assistant", "ready") + "\n"
	writeFile(t, piPath(root, piSession), strings.Replace(raw, "CWD", string(cwd), 1))
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "")
	svc.roots = map[domain.AgentHarness]string{domain.HarnessPi: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	if len(st.sessions) != 1 || st.sessions[0].Metadata.Model != "zai-coding-cn/glm-5.3-flash" || st.sessions[0].Metadata.Effort != "high" {
		t.Fatalf("import lost model/effort: %#v", st.sessions)
	}
}
