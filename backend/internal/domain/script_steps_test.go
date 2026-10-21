package domain

import (
	"strings"
	"testing"
)

func TestTimedScriptCommand(t *testing.T) {
	if got := TimedScriptCommand(0, "npm ci"); got != "npm ci" {
		t.Fatalf("no limit = %q", got)
	}
	timed := TimedScriptCommand(45, "npm ci")
	if timed != "# ao-timeout: 45m\nnpm ci" {
		t.Fatalf("timed = %q", timed)
	}
	if command, minutes := ScriptStepMinutes(timed); command != "npm ci" || minutes != 45 {
		t.Fatalf("minutes = %q %d", command, minutes)
	}
	if command, minutes := ScriptStepMinutes("# ao-timeout: 30m\r\nnpm ci"); command != "npm ci" || minutes != 30 {
		t.Fatalf("CRLF = %q %d", command, minutes)
	}
	if command, minutes := ScriptStepMinutes("# install\nnpm ci"); command != "# install\nnpm ci" || minutes != 0 {
		t.Fatalf("a plain comment = %q %d", command, minutes)
	}
	if _, minutes := ScriptStepMinutes("# ao-timeout: 900m\nnpm ci"); minutes != MaxScriptStepTimeoutMinutes {
		t.Fatalf("an out-of-range limit runs capped, got %d", minutes)
	}
}

func TestProjectConfigValidatesScriptStepTimeouts(t *testing.T) {
	for _, cfg := range []ProjectConfig{
		{PostCreate: []string{"npm ci", "# ao-timeout: 1m\nmake"}},
		{PreRemove: []string{"# ao-timeout: 120m\nmake down"}},
	} {
		if err := cfg.Validate(); err != nil {
			t.Errorf("%+v: %v", cfg, err)
		}
	}
	for _, cfg := range []ProjectConfig{
		{PostCreate: []string{"# ao-timeout: 0m\nnpm ci"}},
		{PostCreate: []string{"ok", "# ao-timeout: 121m\nnpm ci"}},
		{PreRemove: []string{"# ao-timeout: 500m\nmake down"}},
		{PreRemove: []string{"# ao-timeout: 5m\n  "}},
	} {
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "step") {
			t.Errorf("%+v: err = %v, want the step refused", cfg, err)
		}
	}
}
