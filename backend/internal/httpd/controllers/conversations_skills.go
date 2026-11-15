package controllers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func (c *ConversationsController) skills(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/skills")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	skills, err := c.Svc.Skills(r.Context(), session)
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
