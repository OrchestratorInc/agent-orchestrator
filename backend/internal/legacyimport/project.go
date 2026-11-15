package legacyimport

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func mapPermission(legacy string) (mode domain.PermissionMode, ok, lossy bool) {
	switch legacy {
	case "":
		return "", false, false
	case "permissionless", "skip":
		return domain.PermissionModeBypassPermissions, true, false
	case "auto-edit":
		return domain.PermissionModeAcceptEdits, true, false
	case "default":
		return domain.PermissionModeDefault, true, false
	case "suggest":
		return domain.PermissionModeDefault, true, true
	default:
		return domain.PermissionModeDefault, true, true
	}
}

func mapHarness(agent string) (domain.AgentHarness, bool) {
	if agent == "" {
		return "", false
	}
	h := domain.AgentHarness(agent)
	if h.IsKnown() {
		return h, true
	}
	return "", false
}

func buildAgentConfig(src *legacyAgentConfig, notes *[]string, label string) domain.AgentConfig {
	var out domain.AgentConfig
	if src == nil {
		return out
	}
	if m := strings.TrimSpace(src.Model); m != "" {
		out.Model = m
	}
	if mode, ok, lossy := mapPermission(src.Permissions); ok {
		out.Permissions = mode
		if lossy {
			*notes = append(*notes, fmt.Sprintf("%s permission %q mapped lossily to %q", label, src.Permissions, mode))
		}
	}
	return out
}

func buildRoleOverride(src *legacyRole, notes *[]string, label string) domain.RoleOverride {
	var out domain.RoleOverride
	if src == nil {
		return out
	}
	if src.Agent != "" {
		if h, ok := mapHarness(src.Agent); ok {
			out.Harness = h
		} else {
			*notes = append(*notes, fmt.Sprintf("%s agent %q has no rewrite harness — dropped", label, src.Agent))
		}
	}
	out.AgentConfig = buildAgentConfig(src.AgentConfig, notes, label+" agent")
	return out
}

func buildProjectConfig(pc legacyProjectConfig, notes *[]string) domain.ProjectConfig {
	var cfg domain.ProjectConfig

	if b := strings.TrimSpace(pc.DefaultBranch); b != "" {
		cfg.DefaultBranch = b
	}
	if pc.SessionPrefix != "" {
		cfg.SessionPrefix = pc.SessionPrefix
	}
	if len(pc.Env) > 0 {
		cfg.Env = make(map[string]string, len(pc.Env))
		for k, v := range pc.Env {
			cfg.Env[k] = v
		}
	}
	if len(pc.Symlinks) > 0 {
		cfg.Symlinks = append([]string(nil), pc.Symlinks...)
	}
	if len(pc.PostCreate) > 0 {
		cfg.PostCreate = append([]string(nil), pc.PostCreate...)
	}
	cfg.AgentConfig = buildAgentConfig(pc.AgentConfig, notes, "agentConfig")
	cfg.Worker = buildRoleOverride(pc.Worker, notes, "worker")
	cfg.Orchestrator = buildRoleOverride(pc.Orchestrator, notes, "orchestrator")
	var droppedRules bool
	if v, ok := legacyStringValue(pc.AgentRules); ok {
		cfg.AgentRules = v
	} else if pc.AgentRules != nil {
		droppedRules = true
	}
	if v, ok := legacyStringValue(pc.AgentRulesFile); ok {
		cfg.AgentRulesFile = strings.TrimSpace(v)
	} else if pc.AgentRulesFile != nil {
		droppedRules = true
	}
	if v, ok := legacyStringValue(pc.OrchestratorRule); ok {
		cfg.OrchestratorRules = v
	} else if pc.OrchestratorRule != nil {
		droppedRules = true
	}

	var dropped []string
	if pc.Tracker != nil {
		dropped = append(dropped, "tracker")
	}
	if pc.SCM != nil {
		dropped = append(dropped, "scm")
	}
	if droppedRules {
		dropped = append(dropped, "rules")
	}
	if pc.Runtime != nil {
		dropped = append(dropped, "runtime")
	}
	if pc.Workspace != nil {
		dropped = append(dropped, "workspace")
	}
	if pc.Reactions != nil {
		dropped = append(dropped, "reactions")
	}
	if len(dropped) > 0 {
		*notes = append(*notes, "dropped project fields with no rewrite home: "+strings.Join(dropped, ", "))
	}
	return cfg
}

func legacyStringValue(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	if node.Tag != "" && node.Tag != "!!str" {
		return "", false
	}
	return node.Value, true
}

type projectRowDeps struct {
	repoOriginURL func(path string) string
	configMtime   string
	now           time.Time
}

func buildProjectRecord(id string, pc legacyProjectConfig, prefs preferences, reg registeredManifest, deps projectRowDeps) (domain.ProjectRecord, []string) {
	var notes []string
	cfg := buildProjectConfig(pc, &notes)

	path := normalizePath(pc.Path)

	displayName := ""
	if p, ok := prefs.Projects[id]; ok && p.DisplayName != "" {
		displayName = p.DisplayName
	} else if pc.Name != "" && pc.Name != id {
		displayName = pc.Name
	}

	registeredAt := deps.now
	if iso := reg.addedAt(id, pc.Path); iso != "" {
		if t, err := time.Parse(time.RFC3339, iso); err == nil {
			registeredAt = t
		}
	} else if deps.configMtime != "" {
		if t, err := time.Parse(time.RFC3339, deps.configMtime); err == nil {
			registeredAt = t
		}
	}

	origin := ""
	if deps.repoOriginURL != nil {
		origin = deps.repoOriginURL(path)
	}

	return domain.ProjectRecord{
		ID:            id,
		Path:          path,
		RepoOriginURL: origin,
		DisplayName:   displayName,
		RegisteredAt:  registeredAt,
		Kind:          domain.ProjectKindSingleRepo,
		Config:        cfg,
	}, notes
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := userHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
