package mimocode

import "testing"

func TestExportProbeEnvDropsCaseVariantOfImportOverride(t *testing.T) {
	caller := map[string]string{"mimocode_disable_claude_import": "0", "OTHER": "kept"}
	got := exportProbeEnv(caller, true)
	if len(got) != 2 || got[disableClaudeImportEnv] != "1" || got["OTHER"] != "kept" {
		t.Fatalf("probe env = %v, want only %s=1 and OTHER", got, disableClaudeImportEnv)
	}
	if caller["mimocode_disable_claude_import"] != "0" || len(caller) != 2 {
		t.Fatalf("caller env mutated: %v", caller)
	}
}
