package acp

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ListSkills exposes ACP's available commands through AO's existing named-skill
// boundary. Invocation needs no protocol-specific method: ACP commands are sent
// as ordinary prompt text beginning with /name, which is already how AO's
// composer inserts a skill.
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
	hasCompact, compactTakesInput := false, false
	for _, command := range commands {
		if command.Name == "" {
			continue
		}
		if command.Name == "compact" {
			hasCompact = true
			compactTakesInput = command.Input != nil && command.Input.Unstructured != nil
		}
		// Hiding is presentation only, so it runs after the capability checks:
		// an agent's compaction still works when its command stays off the menu.
		if c.hideCommand != nil && c.hideCommand(command) {
			continue
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
			// ACP does not report command provenance. "agent" tells the user this
			// came from the live provider without leaking protocol jargon into UI.
			Source: "agent",
		})
	}

	c.mu.Lock()
	c.skills = skills
	c.skillsKnown = true
	c.compactAdvertised = hasCompact
	c.compactTakesInput = compactTakesInput
	if c.capabilities != nil {
		c.capabilities[ports.ChatCapabilitySkills] = true
		c.capabilities[ports.ChatCapabilityCompaction] = hasCompact
	}
	c.mu.Unlock()
}

// HideCommands returns a Config.HideCommand that hides the named built-ins.
//
// Matching is by name because ACP carries no command kind. That is safe only for
// an agent whose built-ins win over a same-named user command (the user's copy
// is unreachable anyway). An agent that lets a user command replace a built-in
// needs a HideCommand that also checks the agent's own provenance metadata.
func HideCommands(names ...string) func(acpsdk.AvailableCommand) bool {
	hidden := make(map[string]struct{}, len(names))
	for _, name := range names {
		hidden[name] = struct{}{}
	}
	return func(command acpsdk.AvailableCommand) bool {
		_, ok := hidden[command.Name]
		return ok
	}
}

func cloneSkills(skills []ports.ChatSkill) []ports.ChatSkill {
	return append([]ports.ChatSkill(nil), skills...)
}
