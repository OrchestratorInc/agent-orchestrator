package codexappserver

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatSkillLister = (*conversation)(nil)

type skillsListResponse struct {
	Data []struct {
		Cwd    string `json:"cwd"`
		Skills []struct {
			Name             string `json:"name"`
			Description      string `json:"description"`
			ShortDescription string `json:"shortDescription"`
			Enabled          bool   `json:"enabled"`
			Scope            string `json:"scope"`
			Interface        *struct {
				DisplayName      string `json:"displayName"`
				ShortDescription string `json:"shortDescription"`
			} `json:"interface"`
		} `json:"skills"`
	} `json:"data"`
}

func (c *conversation) ListSkills(ctx context.Context) ([]ports.ChatSkill, error) {
	var resp skillsListResponse
	if err := c.conn.request(ctx, "skills/list", map[string]any{}, &resp); err != nil {
		return nil, fmt.Errorf("skills/list: %w", err)
	}

	skills := make([]ports.ChatSkill, 0)
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
