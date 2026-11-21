package modelcatalog

import "testing"

func TestApplyEnvOverridesDropsCaseVariantsWhenCaseInsensitive(t *testing.T) {
	env := map[string]string{"mimocode_disable_claude_import": "0", "OTHER": "kept"}
	applyEnvOverrides(env, map[string]string{"MIMOCODE_DISABLE_CLAUDE_IMPORT": "1"}, true)
	want := map[string]string{"MIMOCODE_DISABLE_CLAUDE_IMPORT": "1", "OTHER": "kept"}
	if len(env) != len(want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("env = %v, want %v", env, want)
		}
	}
}

func TestApplyEnvOverridesKeepsCaseVariantsWhenCaseSensitive(t *testing.T) {
	env := map[string]string{"mimocode_disable_claude_import": "0"}
	applyEnvOverrides(env, map[string]string{"MIMOCODE_DISABLE_CLAUDE_IMPORT": "1"}, false)
	if env["mimocode_disable_claude_import"] != "0" || env["MIMOCODE_DISABLE_CLAUDE_IMPORT"] != "1" || len(env) != 2 {
		t.Fatalf("env = %v, want both spellings kept", env)
	}
}
