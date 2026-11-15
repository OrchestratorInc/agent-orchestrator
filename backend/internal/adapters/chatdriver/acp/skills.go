package acp

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (c *conversation) ListSkills(ctx context.Context) ([]ports.ChatSkill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneSkills(c.skills), nil
}

func (c *conversation) replaceAvailableCommands(commands []acpsdk.AvailableCommand) {
	skills := make([]ports.ChatSkill, 0, len(commands))
	hasCompact := false
	for _, command := range commands {
		if command.Name == "" {
			continue
		}
		if command.Name == "compact" {
			hasCompact = true
		}
		inputHint := ""
		if command.Input != nil && command.Input.Unstructured != nil {
			inputHint = command.Input.Unstructured.Hint
		}
		skills = append(skills, ports.ChatSkill{
			Name:        command.Name,
			DisplayName: command.Name,
			Description: command.Description,
			InputHint:   inputHint,
			Source:      "agent",
		})
	}

	c.mu.Lock()
	c.skills = skills
	c.skillsKnown = true
	if c.capabilities != nil {
		c.capabilities[ports.ChatCapabilitySkills] = true
		c.capabilities[ports.ChatCapabilityCompaction] = hasCompact
	}
	c.mu.Unlock()
}

func cloneSkills(skills []ports.ChatSkill) []ports.ChatSkill {
	return append([]ports.ChatSkill(nil), skills...)
}

func hasCompactSkill(skills []ports.ChatSkill) bool {
	for _, skill := range skills {
		if skill.Name == "compact" {
			return true
		}
	}
	return false
}
