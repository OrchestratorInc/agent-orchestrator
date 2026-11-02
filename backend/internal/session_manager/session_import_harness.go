package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// importedHarnessHooks lets a harness add ownership and config-home handling
// without changing the shared restore flow. Each provider implements callbacks
// in session_import_<harness>.go and registers one map entry here. Config env
// values name ConfigDir unless configValue translates a provider home override.
type importedHarnessHooks struct {
	configEnvKey          string
	configValue           func(string) string
	ownedElsewhere        func(context.Context, domain.SessionRecord) bool
	adoptedOwnedElsewhere func(context.Context, domain.SessionRecord) bool
}

var importedHarnesses = map[domain.AgentHarness]importedHarnessHooks{
	domain.HarnessHermes: {configEnvKey: "HERMES_HOME", ownedElsewhere: hermesImportOwnedElsewhere, adoptedOwnedElsewhere: hermesImportOwnedElsewhere},
}

// writableTranscriptOwner also covers providers without a session registry.
// Node-based CLIs normally appear as their binary name or node in lsof output.
// This is best-effort evidence, not a substitute for provider writer locks.
var importedProcessNames = []string{"claude", "codex", "droid", "hermes", "python", "python3"}
