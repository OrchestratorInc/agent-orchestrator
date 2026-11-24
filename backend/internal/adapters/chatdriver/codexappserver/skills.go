package codexappserver

import (
	"context"
	"fmt"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Asserted here so a refactor cannot silently drop skill listing: the service
// feature-detects this interface, and a missed method would read to a client as
// "this agent has no skills" with nothing to notice.
var _ ports.ChatSkillLister = (*conversation)(nil)

// skillsListResponse is the subset of `skills/list` AO reads.
//
// The provider groups results by working directory because a client may ask about
// several at once. AO asks about one, so the groups are flattened.
type skillsListResponse struct {
	Data []struct {
		Cwd    string `json:"cwd"`
		Skills []struct {
			Name string `json:"name"`
			// Description is the full trigger text from SKILL.md. It is written for
			// the model, not for a menu, and routinely runs to a paragraph.
			Description string `json:"description"`
			// ShortDescription is the legacy SKILL.md field; SKILL.json's
			// interface.shortDescription supersedes it when both are present.
			ShortDescription string `json:"shortDescription"`
			Enabled          bool   `json:"enabled"`
			// Scope is where the skill came from: user, repo, system, admin.
			Scope     string `json:"scope"`
			Interface *struct {
				DisplayName      string `json:"displayName"`
				ShortDescription string `json:"shortDescription"`
			} `json:"interface"`
		} `json:"skills"`
	} `json:"data"`
}

// ListSkills asks the provider which named skills this agent can be pointed at.
//
// `cwds` is deliberately omitted. app-server defaults its scan to the session
// working directory, and the driver already launches app-server with the session
// worktree as its cwd — so the default is already the right tree, and naming it
// again would be a second place that has to stay in sync with the spawn.
//
// Disabled skills are dropped. `enabled: false` is a skill the user switched off
// in their Codex config; offering it would put a command in the menu that the
// provider will not run.
func (c *conversation) ListSkills(ctx context.Context) ([]ports.ChatSkill, error) {
	var resp skillsListResponse
	if err := c.conn.request(ctx, "skills/list", map[string]any{}, &resp); err != nil {
		return nil, fmt.Errorf("skills/list: %w", err)
	}

	skills := make([]ports.ChatSkill, 0)
	// A skill reachable from more than one root is still one command. Deduplicating
	// by name keeps the menu from listing the same thing twice.
	seen := make(map[string]bool)
	for _, group := range resp.Data {
		for _, entry := range group.Skills {
			if entry.Name == "" || !entry.Enabled || seen[entry.Name] {
				continue
			}
			seen[entry.Name] = true

			display := entry.Name
			description := entry.Description
			if entry.ShortDescription != "" {
				description = entry.ShortDescription
			}
			if entry.Interface != nil {
				if entry.Interface.DisplayName != "" {
					display = entry.Interface.DisplayName
				}
				// Preferred over the legacy field per the provider's own note on
				// shortDescription.
				if entry.Interface.ShortDescription != "" {
					description = entry.Interface.ShortDescription
				}
			}

			skills = append(skills, ports.ChatSkill{
				Name:        entry.Name,
				DisplayName: display,
				Description: description,
				Source:      entry.Scope,
			})
		}
	}
	return skills, nil
}
