package kilocode

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPrepareACPConfigContentMergesExistingConfig(t *testing.T) {
	got, err := PrepareACPConfigContent(
		`{"theme":"dark","agent":{"existing":{"prompt":"keep"}}}`,
		"Follow AO rules.", "sess-1", ports.PermissionModeDefault)
	if err != nil {
		t.Fatalf("PrepareACPConfigContent: %v", err)
	}
	var config struct {
		Theme        string `json:"theme"`
		DefaultAgent string `json:"default_agent"`
		Agent        map[string]struct {
			Prompt string `json:"prompt"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(got), &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Theme != "dark" {
		t.Fatalf("existing config was not preserved: %#v", config)
	}
	if config.Agent["existing"].Prompt != "keep" {
		t.Fatalf("existing agent was not preserved: %#v", config.Agent)
	}
	if config.Agent["ao-sess-1"].Prompt != "Follow AO rules." {
		t.Fatalf("AO agent prompt = %#v", config.Agent["ao-sess-1"])
	}
	if config.DefaultAgent != "ao-sess-1" {
		t.Fatalf("default_agent = %q", config.DefaultAgent)
	}
}

func TestPrepareACPConfigContentAddsPermissionMap(t *testing.T) {
	got, err := PrepareACPConfigContent("", "", "sess-1", ports.PermissionModeBypassPermissions)
	if err != nil {
		t.Fatalf("PrepareACPConfigContent: %v", err)
	}
	var config struct {
		Permission map[string]string `json:"permission"`
	}
	if err := json.Unmarshal([]byte(got), &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Permission["*"] != "allow" {
		t.Fatalf("permission = %#v", config.Permission)
	}
}

func TestPrepareACPConfigContentPreservesInheritedPermissions(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		t.Run(string(mode), func(t *testing.T) {
			got, err := PrepareACPConfigContent(`{"theme":"dark","permission":{"webfetch":"deny","read":{"*.env":"deny"},"edit":"deny","bash":"deny"}}`, "", "sess-1", mode)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(got), &config); err != nil {
				t.Fatal(err)
			}
			permission := config["permission"].(map[string]any)
			if config["theme"] != "dark" || permission["webfetch"] != "deny" || !reflect.DeepEqual(permission["read"], map[string]any{"*.env": "deny"}) {
				t.Fatalf("inherited config lost: %s", got)
			}
			wantEdit, wantBash := "ask", "ask"
			switch mode {
			case ports.PermissionModeAcceptEdits:
				wantEdit = "allow"
			case ports.PermissionModeAuto:
				wantEdit, wantBash = "allow", "allow"
			case ports.PermissionModeBypassPermissions:
				if permission["*"] != "allow" {
					t.Fatalf("bypass permission = %#v", permission)
				}
				return
			}
			if permission["edit"] != wantEdit || permission["bash"] != wantBash {
				t.Fatalf("permission = %#v, want edit=%s bash=%s", permission, wantEdit, wantBash)
			}
		})
	}
}

func TestPrepareACPConfigContentPreservesWildcardAction(t *testing.T) {
	got, err := PrepareACPConfigContent(`{"permission":"deny"}`, "", "sess-1", ports.PermissionModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Permission map[string]string `json:"permission"`
	}
	if err := json.Unmarshal([]byte(got), &config); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"*": "deny", "edit": "ask", "bash": "ask"}
	if !reflect.DeepEqual(config.Permission, want) {
		t.Fatalf("permission = %#v, want %#v", config.Permission, want)
	}
}

func TestPrepareACPConfigContentRejectsInvalidPermissionConfig(t *testing.T) {
	for _, existing := range []string{`null`, `{"permission":false}`, `{"permission":[]}`} {
		if _, err := PrepareACPConfigContent(existing, "", "sess-1", ports.PermissionModeDefault); err == nil {
			t.Fatalf("accepted invalid config: %s", existing)
		}
	}
}

func TestPrepareACPConfigContentRejectsNonObjectAgent(t *testing.T) {
	if _, err := PrepareACPConfigContent(`{"agent":"nope"}`, "rules", "sess-1", ports.PermissionModeDefault); err == nil {
		t.Fatal("expected an error for a non-object agent config")
	}
}
