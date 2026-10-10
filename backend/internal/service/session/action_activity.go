package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reqid"
)

// Neutral action names stored on an ao_action activity. Chat maps these. It
// does not infer them from shell text, MCP tool names, or result prose.
const (
	actionSessionSpawned       = "session.spawned"
	actionSessionTerminated    = "session.terminated"
	actionSessionRenamed       = "session.renamed"
	actionSessionRestored      = "session.restored"
	actionSessionAgentSwitched = "session.agent_switched"
	actionPullRequestClaimed   = "pull_request.claimed"
	// actionPullRequestCreated is part of the wire vocabulary. No daemon
	// boundary creates a pull request today, so nothing emits it.
	actionPullRequestCreated = "pull_request.created"
)

const actionTextCap = 160

// conversationActionWriter attaches one confirmed product outcome to the
// session whose chat should show it. Session stores that only implement the
// read model skip the write.
type conversationActionWriter interface {
	ConversationForSession(ctx context.Context, id domain.SessionID) (domain.ConversationRecord, error)
	UpsertActivity(ctx context.Context, conversationID, providerTurnID string, activity domain.ConversationActivity, now time.Time) error
}

type actionDetail struct {
	Action              string `json:"action"`
	OperationID         string `json:"operationId"`
	SourceSessionID     string `json:"sourceSessionId,omitempty"`
	TargetSessionID     string `json:"targetSessionId,omitempty"`
	DisplayName         string `json:"displayName,omitempty"`
	PreviousDisplayName string `json:"previousDisplayName,omitempty"`
	Harness             string `json:"harness,omitempty"`
	PreviousHarness     string `json:"previousHarness,omitempty"`
	Href                string `json:"href,omitempty"`
	PRNumber            int    `json:"prNumber,omitempty"`
	PRTitle             string `json:"prTitle,omitempty"`
	Error               string `json:"error,omitempty"`
}

func (s *Service) recordAction(ctx context.Context, audience domain.SessionID, detail actionDetail, status domain.ActivityStatus) {
	if s == nil || s.store == nil || audience == "" || detail.Action == "" || detail.OperationID == "" {
		return
	}
	writer, ok := s.store.(conversationActionWriter)
	if !ok {
		return
	}
	conversation, err := writer.ConversationForSession(ctx, audience)
	if err != nil {
		if !errors.Is(err, domain.ErrNoConversation) && s.logger != nil {
			s.logger.Warn("action activity: conversation lookup failed", "session", audience, "action", detail.Action, "err", err)
		}
		return
	}
	detail.SourceSessionID = string(audience)
	detail.DisplayName = capActionText(detail.DisplayName)
	detail.PreviousDisplayName = capActionText(detail.PreviousDisplayName)
	detail.Harness = capActionText(detail.Harness)
	detail.PreviousHarness = capActionText(detail.PreviousHarness)
	detail.PRTitle = capActionText(detail.PRTitle)
	detail.Error = capActionText(detail.Error)
	detail.Href = safeActionHref(detail.Href)
	encoded, err := json.Marshal(detail)
	if err != nil {
		return
	}
	providerItemID := "ao-action:" + detail.Action + ":" + detail.OperationID
	activity := domain.ConversationActivity{
		ID:             providerItemID,
		Kind:           domain.ActivityKindAOAction,
		Status:         status,
		Summary:        actionSummary(detail.Action),
		Detail:         encoded,
		ProviderItemID: providerItemID,
	}
	if err := writer.UpsertActivity(ctx, conversation.ID, "", activity, s.now()); err != nil && s.logger != nil {
		s.logger.Warn("action activity: write failed", "session", audience, "action", detail.Action, "err", err)
	}
}

func actionSummary(action string) string {
	switch action {
	case actionSessionSpawned:
		return "Spawned an agent"
	case actionSessionTerminated:
		return "Terminated an agent"
	case actionSessionRenamed:
		return "Renamed a session"
	case actionSessionRestored:
		return "Restored a session"
	case actionSessionAgentSwitched:
		return "Switched the agent"
	case actionPullRequestClaimed:
		return "Claimed a pull request"
	case actionPullRequestCreated:
		return "Created a pull request"
	default:
		return "AO action"
	}
}

func actionStatusForError(err error) domain.ActivityStatus {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.ActivityStatusCancelled
	}
	return domain.ActivityStatusFailed
}

func actionErrorText(err error) string {
	if err == nil {
		return ""
	}
	var api *apierr.Error
	if errors.As(err, &api) && api.Message != "" {
		return api.Message
	}
	return err.Error()
}

func (s *Service) recordSpawnOutcome(ctx context.Context, cfg ports.SpawnConfig, child domain.SessionRecord, status domain.ActivityStatus, err error) {
	audience := cfg.ParentSessionID
	if status == domain.ActivityStatusCompleted && audience == "" {
		audience = child.ID
	}
	if audience == "" {
		return
	}
	operationID := string(child.ID)
	if operationID == "" && cfg.ClientRequestID != "" {
		operationID = "failed:" + cfg.ClientRequestID
	}
	if operationID == "" {
		operationID = failureOperationID(ctx, "spawn")
	}
	if operationID == "" {
		return
	}
	detail := actionDetail{
		Action:          actionSessionSpawned,
		OperationID:     operationID,
		TargetSessionID: string(child.ID),
		DisplayName:     child.DisplayName,
		Harness:         string(child.Harness),
		Href:            sessionActionHref(child.ProjectID, child.ID),
		Error:           actionErrorText(err),
	}
	s.recordAction(ctx, audience, detail, status)
}

func (s *Service) recordTerminated(ctx context.Context, id domain.SessionID, status domain.ActivityStatus, err error) {
	rec, _, _ := s.lookupActionSession(ctx, id)
	operationID := string(id) + ":generation:" + itoa(rec.CleanupGeneration)
	if status != domain.ActivityStatusCompleted {
		failed := failureOperationID(ctx, "terminate")
		if failed == "" {
			return
		}
		operationID = failed
	}
	s.recordAction(ctx, id, actionDetail{
		Action:          actionSessionTerminated,
		OperationID:     operationID,
		TargetSessionID: string(id),
		DisplayName:     rec.DisplayName,
		Harness:         string(rec.Harness),
		Href:            sessionActionHref(rec.ProjectID, id),
		Error:           actionErrorText(err),
	}, status)
}

func (s *Service) recordRenamed(ctx context.Context, id domain.SessionID, previous, next string, status domain.ActivityStatus, err error) {
	if status == domain.ActivityStatusCompleted && previous == next {
		return
	}
	rec, _, _ := s.lookupActionSession(ctx, id)
	operationID := string(id) + ":" + previous + "->" + next
	if status != domain.ActivityStatusCompleted {
		failed := failureOperationID(ctx, "rename")
		if failed == "" {
			return
		}
		operationID = failed
	}
	s.recordAction(ctx, id, actionDetail{
		Action:              actionSessionRenamed,
		OperationID:         operationID,
		TargetSessionID:     string(id),
		DisplayName:         next,
		PreviousDisplayName: previous,
		Href:                sessionActionHref(rec.ProjectID, id),
		Error:               actionErrorText(err),
	}, status)
}

func (s *Service) recordRestored(ctx context.Context, id domain.SessionID, rec domain.SessionRecord, status domain.ActivityStatus, err error) {
	if rec.ID == "" {
		loaded, _, _ := s.lookupActionSession(ctx, id)
		rec = loaded
	}
	operationID := string(id) + ":generation:" + itoa(rec.CleanupGeneration)
	if status != domain.ActivityStatusCompleted {
		failed := failureOperationID(ctx, "restore")
		if failed == "" {
			return
		}
		operationID = failed
	}
	s.recordAction(ctx, id, actionDetail{
		Action:          actionSessionRestored,
		OperationID:     operationID,
		TargetSessionID: string(id),
		DisplayName:     rec.DisplayName,
		Harness:         string(rec.Harness),
		Href:            sessionActionHref(rec.ProjectID, id),
		Error:           actionErrorText(err),
	}, status)
}

func (s *Service) recordAgentSwitched(ctx context.Context, id domain.SessionID, sw domain.AgentSwitch, status domain.ActivityStatus, err error) {
	operationID := string(sw.ID)
	if operationID == "" {
		operationID = sw.IdempotencyKey
	}
	if operationID == "" {
		operationID = failureOperationID(ctx, "switch")
	}
	if operationID == "" {
		return
	}
	rec, _, _ := s.lookupActionSession(ctx, id)
	s.recordAction(ctx, id, actionDetail{
		Action:          actionSessionAgentSwitched,
		OperationID:     operationID,
		TargetSessionID: string(id),
		DisplayName:     rec.DisplayName,
		Harness:         string(sw.TargetHarness),
		PreviousHarness: string(sw.FromHarness),
		Href:            sessionActionHref(rec.ProjectID, id),
		Error:           actionErrorText(err),
	}, status)
}

func (s *Service) recordClaimed(ctx context.Context, id domain.SessionID, pr domain.PullRequest, status domain.ActivityStatus, err error) {
	operationID := string(id) + ":" + pr.URL
	if pr.URL == "" {
		operationID = failureOperationID(ctx, "claim")
	}
	if operationID == "" || operationID == string(id)+":" {
		return
	}
	rec, _, _ := s.lookupActionSession(ctx, id)
	s.recordAction(ctx, id, actionDetail{
		Action:          actionPullRequestClaimed,
		OperationID:     operationID,
		TargetSessionID: string(id),
		DisplayName:     rec.DisplayName,
		Href:            pr.URL,
		PRNumber:        pr.Number,
		PRTitle:         pr.Title,
		Error:           actionErrorText(err),
	}, status)
}

func (s *Service) lookupActionSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if s == nil || s.store == nil || id == "" {
		return domain.SessionRecord{}, false, nil
	}
	return s.store.GetSession(ctx, id)
}

func failureOperationID(ctx context.Context, kind string) string {
	requestID := strings.TrimSpace(reqid.FromContext(ctx))
	if requestID == "" {
		return ""
	}
	return "failed:" + kind + ":" + requestID
}

func sessionActionHref(project domain.ProjectID, id domain.SessionID) string {
	if project == "" || id == "" {
		return ""
	}
	return "ao://sessions/" + url.PathEscape(string(project)) + "/" + url.PathEscape(string(id))
}

func safeActionHref(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "ao://sessions/") {
		remainder := strings.TrimPrefix(raw, "ao://sessions/")
		parts := strings.Split(remainder, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(raw, "?") || strings.Contains(raw, "#") {
			return ""
		}
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

func capActionText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if utf8.RuneCountInString(value) <= actionTextCap {
		return value
	}
	runes := []rune(value)
	return string(runes[:actionTextCap])
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
