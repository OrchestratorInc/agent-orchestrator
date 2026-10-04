package codexappserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDiscoverModelsUsesEffectiveConfiguredDefault(t *testing.T) {
	for _, tc := range []struct{ name, config, wantModel, wantEffort string }{
		{"configured model", `{"model":"configured","model_reasoning_effort":"medium"}`, "configured", "medium"},
		{"unsupported configured effort", `{"model":"configured","model_reasoning_effort":"max"}`, "configured", ""},
		{"off catalog", `{"model":"custom/off-catalog"}`, "custom/off-catalog", ""},
		{"no override", `{}`, "recommended", "low"},
		{"effort override only", `{"model_reasoning_effort":"medium"}`, "recommended", "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			spawn := d.spawn
			var childDir string
			var childEnv []string
			d.spawn = func(ctx context.Context, binary, workdir string, env []string) (*process, error) {
				childDir, childEnv = workdir, append([]string(nil), env...)
				return spawn(ctx, binary, workdir, env)
			}
			srv.reply("model/list", `{"data":[{"id":"recommended","isDefault":true,"defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"medium"}]},{"id":"configured","isDefault":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"medium"}]}]}`)
			srv.reply("config/read", `{"config":`+tc.config+`,"origins":{}}`)
			models, err := d.DiscoverModels(context.Background(), "/tmp/project", map[string]string{"CODEX_HOME": "/tmp/scoped-home"})
			if err != nil {
				t.Fatal(err)
			}
			gotModel, gotEffort := "", ""
			for _, model := range models {
				if model.Default {
					gotModel, gotEffort = model.ID, model.DefaultEffort
				}
			}
			if gotModel != tc.wantModel || gotEffort != tc.wantEffort {
				t.Fatalf("default = %q/%q, want %q/%q", gotModel, gotEffort, tc.wantModel, tc.wantEffort)
			}
			if childDir != "/tmp/project" || envValue(childEnv, "CODEX_HOME") != "/tmp/scoped-home" {
				t.Fatalf("discovery process scope = %q/%q", childDir, envValue(childEnv, "CODEX_HOME"))
			}
			wantCount := 2
			if tc.name == "off catalog" {
				wantCount = 3
			}
			if len(models) != wantCount {
				t.Fatalf("choices changed: %#v", models)
			}
			if models[0].ID != "recommended" || models[1].ID != "configured" {
				t.Fatalf("native sequence changed: %#v", models)
			}
			if tc.name == "off catalog" && (models[2].DisplayName != "custom/off-catalog" || models[2].Efforts != nil) {
				t.Fatalf("configured extra inherited native metadata: %#v", models[2])
			}
			request := srv.awaitFrame(func(f frame) bool { return f.Method == "config/read" })
			var params struct {
				Cwd string `json:"cwd"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			if params.Cwd != "/tmp/project" {
				t.Fatalf("config cwd = %q", params.Cwd)
			}
			if srv.sentMethod("thread/start") {
				t.Fatal("discovery opened a thread")
			}
		})
	}
}

func TestDiscoverModelsConfigReadFailureKeepsChoicesWithoutFalseDefault(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.mu.Lock()
	srv.failures["config/read"] = `{"code":-32601,"message":"unsupported"}`
	srv.mu.Unlock()
	models, err := d.DiscoverModels(context.Background(), "/tmp/project", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Default {
		t.Fatalf("unverified default advertised: %#v", models)
	}
}

func TestDiscoverModelsConfigReadCancellationStopsDiscovery(t *testing.T) {
	d, srv := newTestDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := d.DiscoverModels(ctx, "/tmp/project", nil)
	if err == nil {
		t.Fatal("cancelled config read returned a catalog")
	}
	if !srv.sentMethod("config/read") {
		t.Fatal("effective config was not queried")
	}
}

func TestDiscoverModelsConfigReadTimeoutKeepsCatalogWithoutFalseDefault(t *testing.T) {
	d, _ := newTestDriver(t)
	started := time.Now()
	models, err := d.DiscoverModels(context.Background(), "/tmp/project", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Default {
		t.Fatalf("timed out effective default advertised: %#v", models)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("config read was not bounded: %s", elapsed)
	}
}

func TestDiscoverModelsOnlyExposesHiddenModelWhenExplicitlyConfigured(t *testing.T) {
	for _, configured := range []string{"", "hidden"} {
		t.Run(configured, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("model/list", `{"data":[{"id":"visible","displayName":"Visible","isDefault":true},{"id":"hidden","displayName":"Hidden native title","hidden":true,"supportedReasoningEfforts":[{"reasoningEffort":"high"}]}]}`)
			config := `{}`
			if configured != "" {
				config = `{"model":"hidden","model_reasoning_effort":"high"}`
			}
			srv.reply("config/read", `{"config":`+config+`,"origins":{}}`)
			models, err := d.DiscoverModels(context.Background(), "/tmp/project", nil)
			if err != nil {
				t.Fatal(err)
			}
			if configured == "" {
				if len(models) != 1 || models[0].ID != "visible" {
					t.Fatalf("hidden model exposed: %#v", models)
				}
			} else if len(models) != 2 || models[1].ID != "hidden" || models[1].DisplayName != "hidden" || !models[1].Default || models[1].Efforts != nil || models[1].DefaultEffort != "" {
				t.Fatalf("configured hidden choice = %#v", models)
			}
		})
	}
}
