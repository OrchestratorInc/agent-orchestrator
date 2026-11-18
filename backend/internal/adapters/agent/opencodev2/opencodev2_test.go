package opencodev2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func fakeBinary(t *testing.T, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '" + version + "'; exit; fi\nprintf '%s\\n' \"$@\"\nprintf '%s\\n' \"$OPENCODE_CONFIG\" \"$OPENCODE_CONFIG_CONTENT\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	return binary
}

func sharedWorkspace(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX git fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the shared-server fixture")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	configDir := filepath.Join(dir, ".opencode")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".gitignore"), []byte("/"+projectConfigFileName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLaunchAndRestoreV2ArgumentsAndOverlay(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, tc := range []struct {
			mode        ports.PermissionMode
			flag        string
			permissions string
		}{
			{ports.PermissionModeDefault, "", `null`},
			{ports.PermissionModeAcceptEdits, "", `[{"action":"edit","resource":"*","effect":"allow"}]`},
			{ports.PermissionModeAuto, "--auto", `null`},
			{ports.PermissionModeBypassPermissions, "--dangerously-skip-permissions", `[{"action":"*","resource":"*","effect":"allow"}]`},
		} {
			t.Run(string(tc.mode)+map[bool]string{false: "/fresh", true: "/restore"}[restore], func(t *testing.T) {
				binary := fakeBinary(t, "2.0.0")
				custom := filepath.Join(t.TempDir(), "custom.jsonc")
				original := "{ // caller config\n\"model\": \"user/model\"\n}\n"
				if err := os.WriteFile(custom, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("OPENCODE_CONFIG", custom)
				t.Setenv("OPENCODE_CONFIG_CONTENT", `{"providers":{"local":{"name":"mine"}},"permissions":[{"action":"shell","resource":"git push *","effect":"deny"}],"agents":{"user-agent":{"system":"user instructions"},"ao-sess-1":{"description":"preserve me","system":"old"}}}`)
				cfg := ports.LaunchConfig{Config: ports.AgentConfig{Model: " provider/model "}, SessionID: "sess/1", SystemPrompt: "AO rules", Permissions: tc.mode, Prompt: "--help\n'quoted' $HOME"}
				var cmd []string
				var err error
				if restore {
					var ok bool
					cmd, ok, err = New().GetRestoreCommand(context.Background(), ports.RestoreConfig{Config: cfg.Config, SystemPrompt: cfg.SystemPrompt, Permissions: cfg.Permissions, Prompt: cfg.Prompt, Session: ports.SessionRef{ID: cfg.SessionID, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ses_native"}}})
					if !ok {
						t.Fatalf("restore not supported: %v", err)
					}
				} else {
					cmd, err = New().GetLaunchCommand(context.Background(), cfg)
				}
				if err != nil {
					t.Fatal(err)
				}
				cmd = withoutDataHome(cmd)
				if len(cmd) < 4 || cmd[0] != "env" || !strings.HasPrefix(cmd[1], "OPENCODE_CONFIG_CONTENT=") || cmd[2] != binary {
					t.Fatalf("command = %#v", cmd)
				}
				want := []string{binary, "--standalone"}
				if tc.flag != "" {
					want = append(want, tc.flag)
				}
				if restore {
					want = append(want, "--session", "ses_native")
				}
				want = append(want, "--prompt="+cfg.Prompt)
				if !reflect.DeepEqual(cmd[2:], want) {
					t.Fatalf("argv = %#v, want %#v", cmd[2:], want)
				}
				var config struct {
					DefaultAgent string              `json:"default_agent"`
					Providers    map[string]any      `json:"providers"`
					Permissions  []map[string]string `json:"permissions"`
					Agents       map[string]struct {
						System, Mode, Model, Description string
						Permissions                      json.RawMessage
					} `json:"agents"`
				}
				content := strings.TrimPrefix(cmd[1], "OPENCODE_CONFIG_CONTENT=")
				if err := json.Unmarshal([]byte(content), &config); err != nil {
					t.Fatal(err)
				}
				agent := config.Agents["ao-sess-1"]
				if config.DefaultAgent != "ao-sess-1" || agent.System != "AO rules" || agent.Model != "provider/model" || agent.Mode != "primary" || agent.Description != "preserve me" {
					t.Fatalf("AO config = %+v", config)
				}
				var gotPerm, wantPerm any
				if len(agent.Permissions) > 0 {
					if err := json.Unmarshal(agent.Permissions, &gotPerm); err != nil {
						t.Fatal(err)
					}
				}
				if err := json.Unmarshal([]byte(tc.permissions), &wantPerm); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotPerm, wantPerm) {
					t.Fatalf("permissions=%s, want %s", agent.Permissions, tc.permissions)
				}
				if config.Agents["user-agent"].System != "user instructions" || config.Providers["local"] == nil || len(config.Permissions) != 1 || config.Permissions[0]["effect"] != "deny" {
					t.Fatalf("caller config lost: %s", content)
				}
				out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
				if err != nil {
					t.Fatalf("execute argv: %v: %s", err, out)
				}
				if !strings.Contains(string(out), "--prompt="+cfg.Prompt+"\n"+custom+"\n"+content) {
					t.Fatalf("prompt/config changed at exec boundary: %s", out)
				}
				data, err := os.ReadFile(custom)
				if err != nil || string(data) != original {
					t.Fatalf("custom config changed: %q %v", data, err)
				}
			})
		}
	}
}

func TestV2PromptFileAndNoOverlay(t *testing.T) {
	binary := fakeBinary(t, "2.0.0")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	cmd = withoutDataHome(cmd)
	if err != nil || !reflect.DeepEqual(cmd, []string{binary, "--standalone"}) {
		t.Fatalf("empty launch=%#v %v", cmd, err)
	}
	file := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(file, []byte("file instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, err = New().GetLaunchCommand(context.Background(), ports.LaunchConfig{SystemPromptFile: file, SessionID: "sess"})
	cmd = withoutDataHome(cmd)
	if err != nil {
		t.Fatal(err)
	}
	var overlay map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(cmd[1], "OPENCODE_CONFIG_CONTENT=")), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay["agents"].(map[string]any)["ao-sess"].(map[string]any)["system"] != "file instructions\n" {
		t.Fatalf("overlay=%#v", overlay)
	}
}

func TestV2ClearsPreviousAOAgentModelAndPermissions(t *testing.T) {
	fakeBinary(t, "2.0.0")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"agents":{"ao-sess":{"model":"old/model","permissions":[{"action":"*","resource":"*","effect":"allow"}],"description":"preserved"}}}`)
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{SessionID: "sess", SystemPrompt: "new rules"})
	cmd = withoutDataHome(cmd)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Agents map[string]map[string]any `json:"agents"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(cmd[1], "OPENCODE_CONFIG_CONTENT=")), &config); err != nil {
		t.Fatal(err)
	}
	agent := config.Agents["ao-sess"]
	if _, ok := agent["model"]; ok {
		t.Fatalf("stale model retained: %#v", agent)
	}
	if _, ok := agent["permissions"]; ok {
		t.Fatalf("stale bypass retained: %#v", agent)
	}
	if agent["description"] != "preserved" {
		t.Fatalf("unrelated field lost: %#v", agent)
	}
}

func TestPrepareACPConfigContentPreservesUserConfigAndConstrainsTheBuiltInAgent(t *testing.T) {
	existing := `{"providers":{"local":{"name":"mine"}},"permissions":[{"action":"shell","resource":"git push *","effect":"deny"}],"agents":{"mine":{"mode":"primary","system":"user rules"},"build":{"description":"preserve me"}}}`
	for _, test := range []struct {
		mode ports.PermissionMode
	}{
		{ports.PermissionModeDefault},
		{ports.PermissionModeAcceptEdits},
		{ports.PermissionModeAuto},
		{ports.PermissionModeBypassPermissions},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			content, err := PrepareACPConfigContent(existing, "AO standing rules", test.mode)
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				DefaultAgent string              `json:"default_agent"`
				Providers    map[string]any      `json:"providers"`
				Permissions  []map[string]string `json:"permissions"`
				Agents       map[string]struct {
					Mode, System, Description string
					Permissions               []permissionRule
				} `json:"agents"`
			}
			if err := json.Unmarshal([]byte(content), &config); err != nil {
				t.Fatal(err)
			}
			if config.DefaultAgent != "build" || config.Providers["local"] == nil ||
				len(config.Permissions) != 1 || config.Agents["mine"].System != "user rules" {
				t.Fatalf("user/v2 config = %#v", config)
			}
			build := config.Agents["build"]
			if build.Mode != "primary" || build.System != "AO standing rules" || build.Description != "preserve me" {
				t.Fatalf("build agent = %#v", build)
			}
			var raw struct {
				Agents map[string]map[string]json.RawMessage `json:"agents"`
			}
			if err := json.Unmarshal([]byte(content), &raw); err != nil {
				t.Fatal(err)
			}
			if _, ok := raw.Agents["build"]["permissions"]; ok {
				t.Fatal("agents.build.permissions must stay absent so file-defined rules survive the array-replacing merge")
			}
		})
	}
}

func TestV2RejectsFailuresWithoutSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name, version, content, native string
		canceled                       bool
		wantErr                        string
	}{
		{"wrong major", "1.18.33", "", "ses_1", false, "requires OpenCode 2"},
		{"malformed version", "bad", "", "ses_1", false, "version"},
		{"canceled", "2.0.0", "", "ses_1", true, "canceled"},
		{"bad content", "2.0.0", "{invalid", "ses_1", false, "OPENCODE_CONFIG_CONTENT"},
		{"bad agents", "2.0.0", `{"agents":[]}`, "ses_1", false, "agents"},
		{"bad agent", "2.0.0", `{"agents":{"ao-sess":false}}`, "ses_1", false, "ao-sess"},
		{"missing native", "1.18.33", "{invalid", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeBinary(t, tc.version)
			t.Setenv("OPENCODE_CONFIG_CONTENT", tc.content)
			dir := t.TempDir()
			file := filepath.Join(dir, "system.md")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			cmd, ok, err := New().GetRestoreCommand(ctx, ports.RestoreConfig{SystemPrompt: "rules", SystemPromptFile: file, Session: ports.SessionRef{ID: "sess", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: tc.native}}})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error=%v want %q", err, tc.wantErr)
			}
			if cmd != nil || ok {
				t.Fatalf("failure returned command: %#v %v", cmd, ok)
			}
			if tc.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failure wrote files: %v %v", entries, err)
			}
			if os.Getenv("OPENCODE_CONFIG_CONTENT") != tc.content {
				t.Fatal("caller environment mutated")
			}
			if tc.native != "" {
				cmd, err = New().GetLaunchCommand(ctx, ports.LaunchConfig{SessionID: "sess", SystemPrompt: "rules", SystemPromptFile: file})
				if err == nil || cmd != nil {
					t.Fatalf("launch failure returned %#v %v", cmd, err)
				}
			}
		})
	}
}

func TestV2ResolvesAgainBetweenAttempts(t *testing.T) {
	first := fakeBinary(t, "2.0.0")
	plugin := New()
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	cmd = withoutDataHome(cmd)
	if err != nil || cmd[0] != first {
		t.Fatalf("first=%#v %v", cmd, err)
	}
	fakeBinary(t, "1.18.33")
	if _, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{}); err == nil {
		t.Fatal("reused previous binary after PATH changed")
	}
}

func TestV2AuthStatusUsesPrivateServerAndConnections(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   ports.AgentAuthStatus
	}{
		{`[]`, ports.AgentAuthStatusUnknown},
		{`[{"id":"anthropic","name":"Anthropic","connections":[{"type":"credential","label":"personal"}]}]`, ports.AgentAuthStatusAuthorized},
		{`[{"id":"anthropic","name":"Anthropic","connections":[]}]`, ports.AgentAuthStatusUnknown},
		{`bad output`, ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.output, func(t *testing.T) {
			binary := fakeBinary(t, "2.0.0")
			script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 2.0.0; exit; fi\n[ \"$*\" = 'auth list --standalone --format json' ] || exit 99\nprintf '%s\\n' '" + tc.output + "'\n"
			if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			status, err := New().AuthStatus(context.Background())
			if err != nil || status != tc.want {
				t.Fatalf("auth=%q %v, want %q", status, err, tc.want)
			}
		})
	}
}

func TestV2AuthStatusAllowsMigrationBeyondCommandTimeout(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	t.Setenv("HOME", t.TempDir())
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(legacy, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"CREATE TABLE sessions (id TEXT PRIMARY KEY)", "BEGIN EXCLUSIVE"} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _, _ = db.Exec("ROLLBACK") }()
	binary := fakeBinary(t, "2.0.0")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 2.0.0; exit; fi\n" +
		"[ \"$*\" = 'auth list --standalone --format json' ] || exit 99\n" +
		"printf '%s\\n' '[{\"connections\":[{\"type\":\"credential\"}]}]'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		status, err := New().AuthStatus(ctx)
		if err == nil && status != ports.AgentAuthStatusAuthorized {
			err = fmt.Errorf("auth status = %q, want authorized", status)
		}
		done <- err
	}()
	for {
		staging, _ := filepath.Glob(filepath.Join(parent, "opencode-v2-home", ".opencode-migration-*"))
		if len(staging) > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("auth returned before migration started: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
		time.Sleep(time.Millisecond)
	}
	timer := time.NewTimer(3200 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-done:
		t.Fatalf("auth returned before the legacy database was released: %v", err)
	case <-timer.C:
	}
	if _, err := db.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestSemanticMessageAcceptanceIsNotAdvertised(t *testing.T) {
	var agent ports.Agent = New()
	signaler, ok := agent.(ports.SemanticMessageAcceptanceSignaler)
	if !ok || signaler.EmitsSemanticMessageAcceptance() {
		t.Fatal("OpenCode 2 must not advertise semantic acceptance without a post-admission signal")
	}
}

// withoutDataHome drops the XDG_DATA_HOME isolation entry (covered in
// datahome_test.go) so these tests can assert the rest of the launch argv.
func withoutDataHome(cmd []string) []string {
	out := make([]string, 0, len(cmd))
	for _, arg := range cmd {
		if !strings.HasPrefix(arg, "XDG_DATA_HOME=") {
			out = append(out, arg)
		}
	}
	if len(out) > 1 && out[0] == "env" && !strings.Contains(out[1], "=") {
		out = out[1:]
	}
	return out
}

func TestV2LaunchUsesSharedServerAndWritesProjectConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	fakeBinary(t, "2.0.0")
	const password = "shared-server-password"
	var gotUser, gotPassword string
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/info" {
			http.NotFound(w, r)
			return
		}
		gotUser, gotPassword, _ = r.BasicAuth()
		if gotUser != "opencode" || gotPassword != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer info.Close()

	dataDir := t.TempDir()
	persistServer(dataDir, sharedServer{url: info.URL, password: password})
	workspace := sharedWorkspace(t)
	plugin := New()
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		WorkspacePath: workspace,
		SessionID:     "sess/shared",
		SystemPrompt:  "AO shared rules",
		Prompt:        "do the task",
		Env:           map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotUser != "opencode" || gotPassword != password {
		t.Fatalf("health probe auth = %q/%q", gotUser, gotPassword)
	}
	if !containsArg(cmd, "--server", info.URL) {
		t.Fatalf("argv = %#v, want --server %s", cmd, info.URL)
	}
	if hasEnv(cmd, "OPENCODE_PASSWORD="+password) || strings.Contains(strings.Join(cmd, "\n"), password) {
		t.Fatalf("argv = %#v, must not contain the server password", cmd)
	}
	env := map[string]string{}
	plugin.AugmentRuntimeLaunchEnv(env, dataDir, "sess/shared", "launch-1")
	if env["OPENCODE_PASSWORD"] != password {
		t.Fatalf("runtime env = %#v, want OPENCODE_PASSWORD", env)
	}
	again := map[string]string{}
	plugin.AugmentRuntimeLaunchEnv(again, dataDir, "sess/shared", "launch-2")
	if _, ok := again["OPENCODE_PASSWORD"]; ok {
		t.Fatalf("runtime env = %#v, password must be consumed once", again)
	}
	if strings.Contains(strings.Join(cmd, "\n"), "--standalone") {
		t.Fatalf("argv = %#v, must not contain --standalone", cmd)
	}
	if hasEnvPrefix(cmd, "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("argv = %#v, must not pass OPENCODE_CONFIG_CONTENT in shared mode", cmd)
	}

	configPath := filepath.Join(workspace, ".opencode", projectConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		DefaultAgent string `json:"default_agent"`
		Agents       map[string]struct {
			Mode   string `json:"mode"`
			System string `json:"system"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.DefaultAgent != "ao-sess-shared" {
		t.Fatalf("default_agent = %q", config.DefaultAgent)
	}
	agent := config.Agents["ao-sess-shared"]
	if agent.Mode != "primary" || agent.System != "AO shared rules" {
		t.Fatalf("agent = %#v", agent)
	}
}

func TestV2SharedServerFallsBackWhenWorkspaceConfigExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	fakeBinary(t, "2.0.0")
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer info.Close()

	dataDir := t.TempDir()
	persistServer(dataDir, sharedServer{url: info.URL, password: "pw"})
	workspace := sharedWorkspace(t)
	configPath := filepath.Join(workspace, ".opencode", projectConfigFileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	original := "{\n  \"default_agent\": \"user-agent\"\n}\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		WorkspacePath: workspace,
		SessionID:     "sess/fallback",
		SystemPrompt:  "AO rules",
		Env:           map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmd, "\n"), "--standalone") {
		t.Fatalf("argv = %#v, want standalone fallback", cmd)
	}
	if !hasEnvPrefix(cmd, "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("argv = %#v, want OPENCODE_CONFIG_CONTENT fallback", cmd)
	}
	if containsArg(cmd, "--server", info.URL) {
		t.Fatalf("argv = %#v, must not use the shared server", cmd)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("workspace config changed to %q", got)
	}
}

func TestPrepareSharedServerDoesNotWriteConfigWhenServerFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	binary := fakeBinary(t, "2.0.0")
	workspace := sharedWorkspace(t)
	_, err := prepareSharedServer(context.Background(), binary, ports.LaunchConfig{
		DataDir:       t.TempDir(),
		WorkspacePath: workspace,
		SessionID:     "sess/failed",
	}, t.TempDir(), `{"default_agent":"ao-failed"}`)
	if err == nil {
		t.Fatal("prepareSharedServer succeeded with a server that exits during startup")
	}
	if _, statErr := os.Stat(filepath.Join(workspace, ".opencode", projectConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("project config written before the server became ready: %v", statErr)
	}
}

func TestV2ProviderEnvDriftKeepsPrivateServer(t *testing.T) {
	fakeBinary(t, "2.0.0")
	t.Setenv("AWS_PROFILE", "work")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer info.Close()
	dataDir := t.TempDir()
	persistServer(dataDir, sharedServer{url: info.URL, password: "pw"})

	matching := sharedWorkspace(t)
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		WorkspacePath: matching,
		SessionID:     "sess/matching",
		SystemPrompt:  "AO rules",
		Env:           map[string]string{"AWS_PROFILE": "work"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsArg(cmd, "--server", info.URL) {
		t.Fatalf("matching environment argv = %#v, want the shared server", cmd)
	}
	if _, statErr := os.Stat(filepath.Join(matching, ".opencode", projectConfigFileName)); statErr != nil {
		t.Fatalf("matching launch did not write project config: %v", statErr)
	}

	drifted := sharedWorkspace(t)
	cmd, err = New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		WorkspacePath: drifted,
		SessionID:     "sess/drifted",
		SystemPrompt:  "AO rules",
		Env:           map[string]string{"AWS_PROFILE": "personal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmd, "\n"), "--standalone") {
		t.Fatalf("drifted argv = %#v, want the private-server path", cmd)
	}
	if _, statErr := os.Stat(filepath.Join(drifted, ".opencode", projectConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("project config written for a drifted launch: %v", statErr)
	}
}

func TestV2SharedServerNeedsKnownLaunchEnvironment(t *testing.T) {
	fakeBinary(t, "2.0.0")
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer info.Close()
	dataDir := t.TempDir()
	persistServer(dataDir, sharedServer{url: info.URL, password: "pw"})
	workspace := sharedWorkspace(t)
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		WorkspacePath: workspace,
		SessionID:     "sess/unknown-env",
		SystemPrompt:  "AO rules",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmd, "\n"), "--standalone") {
		t.Fatalf("argv = %#v, want the private-server path without an environment", cmd)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, ".opencode", projectConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("project config written without an environment: %v", statErr)
	}
	_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: dataDir,
		Session: ports.SessionRef{
			ID:            "sess/unknown-env",
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "ses_native"},
		},
		Env: nil,
	})
	if err != nil || !ok {
		t.Fatalf("restore = %v %v", ok, err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, ".opencode", projectConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("project config written for a restore without an environment: %v", statErr)
	}
}

func TestV2RestoreForwardsLaunchEnvironmentToSharedServer(t *testing.T) {
	fakeBinary(t, "2.0.0")
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer info.Close()
	dataDir := t.TempDir()
	persistServer(dataDir, sharedServer{url: info.URL, password: "pw"})
	workspace := sharedWorkspace(t)
	cmd, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: dataDir,
		Session: ports.SessionRef{
			ID:            "sess/restore",
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "ses_native"},
		},
		SystemPrompt: "AO rules",
		Env:          map[string]string{},
	})
	if err != nil || !ok {
		t.Fatalf("restore = %v %v", ok, err)
	}
	if !containsArg(cmd, "--server", info.URL) {
		t.Fatalf("argv = %#v, want the shared server", cmd)
	}
	if !containsArg(cmd, "--session", "ses_native") {
		t.Fatalf("argv = %#v, want the native session", cmd)
	}
}

func TestPrepareSharedServerRequiresEffectiveGitignore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX git fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	binary := fakeBinary(t, "2.0.0")
	workspace := t.TempDir()
	if out, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	configDir := filepath.Join(workspace, ".opencode")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".gitignore"), []byte("*.log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareSharedServer(context.Background(), binary, ports.LaunchConfig{
		DataDir:       t.TempDir(),
		WorkspacePath: workspace,
		SessionID:     "sess/untracked",
	}, t.TempDir(), `{"default_agent":"ao-untracked"}`)
	if err == nil {
		t.Fatal("prepareSharedServer accepted a workspace config Git does not ignore")
	}
	if _, statErr := os.Stat(filepath.Join(configDir, projectConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unignored project config was written: %v", statErr)
	}
}

func TestLockWorkspacePreparationSerializes(t *testing.T) {
	workspace := t.TempDir()
	release := lockWorkspacePreparation(workspace)
	acquired := make(chan struct{})
	go func() {
		unlock := lockWorkspacePreparation(workspace)
		close(acquired)
		unlock()
	}()
	select {
	case <-acquired:
		t.Fatal("second preparation acquired the workspace lock while the first held it")
	default:
	}
	release()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second preparation did not acquire the released workspace lock")
	}
}

func TestV2SharedServerBusyReturnsErrorInsteadOfPrivateFallback(t *testing.T) {
	fakeBinary(t, "2.0.0")
	dataDir := t.TempDir()
	workspace := sharedWorkspace(t)
	handle := &serverHandle{server: sharedServer{url: "http://127.0.0.1:1", password: "pw"}, cmd: &exec.Cmd{}}
	sharedServerRegistry.mu.Lock()
	sharedServerRegistry.byDir[dataDir] = handle
	sharedServerRegistry.mu.Unlock()
	t.Cleanup(func() {
		sharedServerRegistry.mu.Lock()
		delete(sharedServerRegistry.byDir, dataDir)
		sharedServerRegistry.mu.Unlock()
	})

	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		WorkspacePath: workspace,
		SessionID:     "sess/busy",
		SystemPrompt:  "AO rules",
		Env:           map[string]string{},
	})
	if !errors.Is(err, errSharedServerUnavailable) {
		t.Fatalf("error = %v, want errSharedServerUnavailable", err)
	}
	if cmd != nil {
		t.Fatalf("busy shared server produced a private fallback argv: %#v", cmd)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, ".opencode", projectConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("project config written for a busy server: %v", statErr)
	}
}

func TestEnsureSharedServerKeepsReattachedHandleWhenProbeFails(t *testing.T) {
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer info.Close()
	key := t.TempDir()
	handle := &serverHandle{server: sharedServer{url: info.URL, password: "pw"}}
	sharedServerRegistry.mu.Lock()
	sharedServerRegistry.byDir[key] = handle
	sharedServerRegistry.mu.Unlock()
	t.Cleanup(func() {
		sharedServerRegistry.mu.Lock()
		delete(sharedServerRegistry.byDir, key)
		sharedServerRegistry.mu.Unlock()
	})

	if _, err := ensureSharedServer(context.Background(), "unused", key, t.TempDir()); err == nil {
		t.Fatal("ensureSharedServer accepted a listening but unhealthy reattached server")
	}
	sharedServerRegistry.mu.Lock()
	got := sharedServerRegistry.byDir[key]
	sharedServerRegistry.mu.Unlock()
	if got != handle {
		t.Fatal("listening reattached server was dropped, so a duplicate could start")
	}
}

func TestWriteProjectConfigDoesNotReplaceExistingFile(t *testing.T) {
	workspace := t.TempDir()
	if created, err := writeProjectConfig(workspace, "one"); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if created, err := writeProjectConfig(workspace, "one"); err != nil || created {
		t.Fatalf("identical config: created=%v err=%v", created, err)
	}
	if _, err := writeProjectConfig(workspace, "two"); err == nil {
		t.Fatal("differing config was accepted")
	}
	data, err := os.ReadFile(filepath.Join(workspace, ".opencode", projectConfigFileName))
	if err != nil || string(data) != "one" {
		t.Fatalf("config = %q, %v", data, err)
	}
	temps, err := filepath.Glob(filepath.Join(workspace, ".opencode", ".ao-tmp-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary config files left behind: %v %v", temps, err)
	}
}

func TestEnsureSharedServerKeepsOwnedHandleWhenProbeFails(t *testing.T) {
	key := t.TempDir()
	handle := &serverHandle{
		server: sharedServer{url: "http://127.0.0.1:1", password: "pw"},
		cmd:    &exec.Cmd{},
	}
	sharedServerRegistry.mu.Lock()
	sharedServerRegistry.byDir[key] = handle
	sharedServerRegistry.mu.Unlock()
	t.Cleanup(func() {
		sharedServerRegistry.mu.Lock()
		delete(sharedServerRegistry.byDir, key)
		sharedServerRegistry.mu.Unlock()
	})

	if _, err := ensureSharedServer(context.Background(), "unused", key, t.TempDir()); err == nil {
		t.Fatal("ensureSharedServer accepted an unhealthy owned server")
	}
	sharedServerRegistry.mu.Lock()
	got := sharedServerRegistry.byDir[key]
	sharedServerRegistry.mu.Unlock()
	if got != handle {
		t.Fatal("running server handle was dropped, so a replacement could leak a second server")
	}
}

func TestGetAgentHooksIgnoresProjectConfigInItsOwnDir(t *testing.T) {
	workspace := t.TempDir()
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err != nil {
		t.Fatal(err)
	}
	configIgnore, err := os.ReadFile(filepath.Join(workspace, ".opencode", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configIgnore), "/"+projectConfigFileName) {
		t.Fatalf(".opencode/.gitignore = %q", configIgnore)
	}
	pluginIgnore, err := os.ReadFile(filepath.Join(workspace, ".opencode", "plugins", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pluginIgnore), "/"+v2PluginFileName) || strings.Contains(string(pluginIgnore), "/"+projectConfigFileName) {
		t.Fatalf(".opencode/plugins/.gitignore = %q", pluginIgnore)
	}
}

func containsArg(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasEnv(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasEnvPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func TestWaitForSharedServerReadsPasswordAndProbesHealth(t *testing.T) {
	const password = "probe-password"
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer info.Close()

	var output serverOutput
	if _, err := output.Write([]byte("server listening on http://127.0.0.1:1\nserver password " + password + "\n")); err != nil {
		t.Fatal(err)
	}
	got, err := waitForSharedServer(context.Background(), info.URL, &output, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	if got != password {
		t.Fatalf("password = %q, want %q", got, password)
	}
}
