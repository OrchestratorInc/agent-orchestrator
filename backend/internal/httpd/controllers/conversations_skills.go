package controllers

import (
	"errors"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// skills serves the named skills this session's provider will accept.
//
// A driver that cannot enumerate them answers 200 with an empty list, matching
// how the model catalog reports "no choice here". The composer keys its slash menu
// off an empty list, so an error would leave `/` behaving as though the feature
// were broken rather than absent.
func (c *ConversationsController) skills(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/skills")
		return
	}
	skills, err := c.Svc.SkillsForOwner(r.Context(), conversationOwner(r))
	if err != nil {
		if errors.Is(err, chatsvc.ErrSkillsUnsupported) {
			envelope.WriteJSON(w, http.StatusOK, ConversationSkillsResponse{
				Skills: []ConversationSkillResponse{},
			})
			return
		}
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationSkillsResponse(skills))
}

func conversationSkillsResponse(skills []ports.ChatSkill) ConversationSkillsResponse {
	out := ConversationSkillsResponse{
		Skills: make([]ConversationSkillResponse, 0, len(skills)),
	}
	for _, skill := range skills {
		display := skill.DisplayName
		if display == "" {
			// A skill with no label still has to render, and its own name is the
			// honest fallback.
			display = skill.Name
		}
		out.Skills = append(out.Skills, ConversationSkillResponse{
			Name:        skill.Name,
			DisplayName: display,
			Description: skill.Description,
			InputHint:   skill.InputHint,
			Source:      skill.Source,
		})
	}
	return out
}
