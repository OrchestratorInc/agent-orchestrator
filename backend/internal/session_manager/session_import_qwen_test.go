package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/qwen"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestQwenSessionOwnership(t *testing.T) {
	const id = "807b463e-6160-40ef-aa01-eb87dcc061e1"
	const pid = 12345
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		runtime bool
		cwd     string
		id      string
		live    bool
		want    bool
	}{
		{"registry owner", false, "external", id, true, true},
		{"registry own workspace", false, "own", id, true, false},
		{"registry stale pid", false, "external", id, false, false},
		{"registry other session", false, "external", "different", true, false},
		{"runtime owner", true, "external", id, true, true},
		{"runtime own workspace", true, "own", id, true, false},
		{"runtime stale pid", true, "external", id, false, false},
		{"runtime other session", true, "external", "different", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, own, external := t.TempDir(), t.TempDir(), t.TempDir()
			cwd := external
			if tc.cwd == "own" {
				cwd = filepath.Join(own, "subdir")
			}
			record := map[string]any{"pid": pid, "sessionId": tc.id, "cwd": cwd, "startedAt": 1234000, "procStart": "boot:123"}
			path := filepath.Join(root, "sessions", "12345.json")
			if tc.runtime {
				host, err := os.Hostname()
				if err != nil {
					t.Fatal(err)
				}
				record = map[string]any{"schema_version": 1, "pid": pid, "session_id": tc.id, "work_dir": cwd, "started_at": 1234.0, "hostname": host}
				path = filepath.Join(root, "projects", "-external", "chats", id+".runtime.json")
			}
			body, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			probe := func(_ context.Context, gotPID int, at int64, token string) bool {
				if gotPID != pid || at != 1234000 {
					t.Fatalf("unexpected pid/start: %d/%d", gotPID, at)
				}
				if !tc.runtime && token != "boot:123" {
					t.Fatalf("procStart lost: %q", token)
				}
				return tc.live
			}
			if got := qwenSessionLiveWithProbe(ctx, root, id, own, probe); got != tc.want {
				t.Fatalf("owned = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQwenOwnershipMissingAndMalformedRecords(t *testing.T) {
	root := t.TempDir()
	if qwenSessionLive(context.Background(), root, "session", "") {
		t.Fatal("missing records are not ownership")
	}
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{torn", `{"pid":0,"sessionId":"session"}`, `{"pid":2147483646,"sessionId":"session","startedAt":1}`} {
		if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if qwenSessionLive(context.Background(), root, "session", "") {
			t.Fatal("malformed or dead record counted as an owner")
		}
	}
}

func TestQwenProcessIdentity(t *testing.T) {
	started := time.UnixMilli(100000)
	for _, tc := range []struct {
		name, token, current string
		registered           int64
		want                 bool
	}{
		{"node", "boot:1", "boot:1", 101000, true},
		{"qwen", "", "", 101000, true},
		{"node", "boot:1", "boot:2", 101000, false},
		{"node", "boot:1", "", 101000, false},
		{"node", "", "", 99000, false},
		{"unrelated", "", "", 101000, false},
		{"node", "", "", 0, false},
	} {
		if got := qwenProcessIdentityMatches(tc.name, started, tc.registered, tc.token, tc.current); got != tc.want {
			t.Errorf("identity %#v: %v, want %v", tc, got, tc.want)
		}
	}
}

func TestQwenOwnershipDispatchRejectsLiveUnrelatedPID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "native", "cwd": t.TempDir(), "startedAt": time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "live.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{Harness: domain.HarnessQwen, Metadata: domain.SessionMetadata{ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: root}}}
	if importOwnedElsewhere(context.Background(), rec) || adoptedOwnedElsewhere(context.Background(), rec) {
		t.Fatal("an unrelated live test process must not count as Qwen")
	}
}

type qwenHomeAgents struct{}

func (qwenHomeAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return qwen.New(), true }

func TestImportedConfigEnvQwen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QWEN_HOME", "")
	m := &Manager{agents: qwenHomeAgents{}}
	source := filepath.Join(home, ".qwen")
	rec := domain.SessionRecord{Harness: domain.HarnessQwen, Metadata: domain.SessionMetadata{
		ProviderConversationID: "native", ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: source},
	}}
	env := map[string]string{}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if _, forced := env["QWEN_HOME"]; forced {
		t.Fatal("default Qwen home should not need an override")
	}
	env["QWEN_HOME"] = filepath.Join(home, "managed")
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if env["QWEN_HOME"] != source {
		t.Fatalf("QWEN_HOME = %q, want %q", env["QWEN_HOME"], source)
	}
}
