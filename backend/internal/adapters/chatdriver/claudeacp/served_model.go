package claudeacp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// maxAliasCatalogSessions bounds the per-session catalog memory. The driver
// has no session-close hook, so entries are only replaced on relaunch.
const maxAliasCatalogSessions = 1024

// claudeFamilyModel is the newest concrete model of one family in a catalog.
type claudeFamilyModel struct {
	id      string
	version []int
}

// claudeAliasCatalog remembers, per AO session, the newest model of each
// Claude family in the provider catalog the session launched with. An alias
// such as "opus" is expected to resolve to that model; only a resolution to an
// older one is a reroute worth showing.
type claudeAliasCatalog struct {
	mu     sync.Mutex
	newest map[domain.SessionID]map[string]claudeFamilyModel
}

func newClaudeAliasCatalog() *claudeAliasCatalog {
	return &claudeAliasCatalog{newest: map[domain.SessionID]map[string]claudeFamilyModel{}}
}

// record replaces the session's catalog. An empty catalog (discovery failed or
// the user pinned availableModels) forgets it, so no comparison is guessed.
func (c *claudeAliasCatalog) record(sessionID domain.SessionID, models []ports.AgentModelInfo) {
	if sessionID == "" {
		return
	}
	newest := claudeNewestByFamily(models)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(newest) == 0 {
		delete(c.newest, sessionID)
		return
	}
	if _, exists := c.newest[sessionID]; !exists && len(c.newest) >= maxAliasCatalogSessions {
		for stale := range c.newest {
			delete(c.newest, stale)
			break
		}
	}
	c.newest[sessionID] = newest
}

func (c *claudeAliasCatalog) newestFor(sessionID domain.SessionID, family string) (claudeFamilyModel, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	model, ok := c.newest[sessionID][family]
	return model, ok
}

// servedModel reports an alias that the installed Claude Code resolved to an
// older model than the newest of its family in the session's catalog. The ACP
// adapter does not surface the answering model, but Claude's own transcript
// records it on every assistant message. An alias resolving to the newest
// model is the expected outcome and reports nothing, as does any case AO
// cannot compare: "default", an unknown catalog, or an unparseable model ID.
func (c *claudeAliasCatalog) servedModel(ctx context.Context, cfg acpdriver.LaunchConfig, sessionID, selected string) (string, string) {
	family := claudeAliasFamily(selected)
	if family == "" {
		return "", ""
	}
	expected, ok := c.newestFor(cfg.SessionID, family)
	if !ok {
		return "", ""
	}
	served, err := claudecode.LastAssistantModel(ctx, cfg.Env, sessionID)
	if err != nil || served == "" {
		return "", ""
	}
	servedFamily, servedVersion := claudeFamilyVersion(served)
	if servedFamily != family || compareClaudeVersions(servedVersion, expected.version) >= 0 {
		return "", ""
	}
	return served, fmt.Sprintf(
		"The installed Claude Code resolves %q to %s, older than %s. Run `claude update` to get the newest model.",
		selected, served, expected.id)
}

// claudeAliasFamily returns the family a native alias names ("opus[1m]" is
// "opus"); "" for "default" and for concrete model IDs.
func claudeAliasFamily(model string) string {
	if !isClaudeNativeModelAlias(model) {
		return ""
	}
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(model)), "[")
	switch base {
	case "opus", "sonnet", "haiku", "fable":
		return base
	default:
		return ""
	}
}

// claudeFamilyVersion parses "claude-opus-5-5" (optionally with a snapshot
// date) into ("opus", [5 5]).
func claudeFamilyVersion(id string) (string, []int) {
	match := claudeModelFamily.FindStringSubmatch(strings.ToLower(strings.TrimSpace(id)))
	if match == nil {
		return "", nil
	}
	version := make([]int, 0, 2)
	for _, part := range match[2:] {
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return "", nil
		}
		version = append(version, n)
	}
	return match[1], version
}

func claudeNewestByFamily(models []ports.AgentModelInfo) map[string]claudeFamilyModel {
	newest := map[string]claudeFamilyModel{}
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		family, version := claudeFamilyVersion(id)
		if family == "" {
			continue
		}
		if current, ok := newest[family]; !ok || compareClaudeVersions(version, current.version) > 0 {
			newest[family] = claudeFamilyModel{id: id, version: version}
		}
	}
	return newest
}

// compareClaudeVersions orders major.minor versions; a missing minor is 0.
func compareClaudeVersions(a, b []int) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		var left, right int
		if i < len(a) {
			left = a[i]
		}
		if i < len(b) {
			right = b[i]
		}
		if left != right {
			return left - right
		}
	}
	return 0
}
