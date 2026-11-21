package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	maxWaitSessions = 20
	maxWaitSeconds  = 110
)

var waitPollInterval = 2 * time.Second

func sessionPath(id string, rest string) string { return "sessions/" + url.PathEscape(id) + rest }

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

func intArg(args map[string]any, name string, fallback int) int {
	if value, ok := args[name].(float64); ok {
		return int(value)
	}
	return fallback
}

func newRequestID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return "mcp-" + hex.EncodeToString(buffer)
}

func callCapabilities(ctx context.Context, s *server, _ map[string]any) (any, error) {
	var agents, accounts map[string]any
	if err := s.daemon.DoJSON(ctx, http.MethodGet, "agents", nil, &agents); err != nil {
		return nil, err
	}
	if err := s.daemon.DoJSON(ctx, http.MethodGet, "agents/accounts", nil, &accounts); err != nil {
		return nil, err
	}
	return map[string]any{
		"agents": each(agents["installed"], func(agent map[string]any) map[string]any {
			return keep(agent, "id", "label", "authStatus")
		}),
		"accounts": each(accounts["accounts"], func(account map[string]any) map[string]any {
			return keep(account, "id", "provider", "plan", "default", "signedIn")
		}),
	}, nil
}

func callSessionRead(ctx context.Context, s *server, args map[string]any) (any, error) {
	query := url.Values{"limit": {fmt.Sprint(intArg(args, "limit", 5))}, "offset": {fmt.Sprint(intArg(args, "offset", 0))}}
	var history struct {
		Offset int              `json:"offset"`
		Total  int              `json:"total"`
		Turns  []map[string]any `json:"turns"`
	}
	if err := s.daemon.DoJSON(ctx, http.MethodGet, sessionPath(stringArg(args, "sessionId"), "/history?"+query.Encode()), nil, &history); err != nil {
		return nil, err
	}
	for _, turn := range history.Turns {
		for _, key := range []string{"userRequest", "agentResponse"} {
			if text, ok := turn[key].(string); ok {
				turn[key] = clip(text)
			}
		}
	}
	return history, nil
}

func sessionSettled(session map[string]any) bool {
	if terminated, _ := session["isTerminated"].(bool); terminated {
		return true
	}
	switch session["provisionState"] {
	case "failed":
		return true
	case "provisioning":
		return false
	}
	return session["activity"] != "active"
}

func callSessionWait(ctx context.Context, s *server, args map[string]any) (any, error) {
	raw, _ := args["sessionIds"].([]any)
	ids := make([]string, 0, len(raw))
	for _, value := range raw {
		if id, ok := value.(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > maxWaitSessions {
		return nil, fmt.Errorf("sessionIds must list 1 to %d sessions", maxWaitSessions)
	}
	timeout := min(max(intArg(args, "timeoutSec", maxWaitSeconds), 1), maxWaitSeconds)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	settledBefore := false
	for {
		sessions := make([]map[string]any, 0, len(ids))
		settled := true
		for _, id := range ids {
			var response struct {
				Session map[string]any `json:"session"`
			}
			if err := s.daemon.DoJSON(ctx, http.MethodGet, sessionPath(id, ""), nil, &response); err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return map[string]any{"timedOut": true, "sessions": sessions}, nil
				}
				return nil, fmt.Errorf("session %s: %w", id, err)
			}
			summary := sessionSummary(response.Session)
			sessions = append(sessions, summary)
			settled = settled && sessionSettled(summary)
		}
		if settled && settledBefore {
			return map[string]any{"timedOut": false, "sessions": sessions}, nil
		}
		settledBefore = settled
		select {
		case <-ctx.Done():
			return map[string]any{"timedOut": true, "sessions": sessions}, nil
		case <-time.After(waitPollInterval):
		}
	}
}

func callPendingList(ctx context.Context, s *server, args map[string]any) (any, error) {
	var conversation struct {
		Activities []map[string]any `json:"activities"`
	}
	if err := s.daemon.DoJSON(ctx, http.MethodGet, sessionPath(stringArg(args, "sessionId"), "/conversation"), nil, &conversation); err != nil {
		return nil, err
	}
	pending := []any{}
	for _, activity := range conversation.Activities {
		kind := activity["activityKind"]
		if activity["status"] == "pending" && (kind == "approval" || kind == "user_input") {
			pending = append(pending, keep(activity, "requestId", "activityKind", "summary", "detail", "createdAt"))
		}
	}
	return map[string]any{"pending": pending}, nil
}

func callPendingRespond(ctx context.Context, s *server, args map[string]any) (any, error) {
	base := sessionPath(stringArg(args, "sessionId"), "/conversation/")
	request := url.PathEscape(stringArg(args, "requestId"))
	var out json.RawMessage
	if decision := stringArg(args, "decisionId"); decision != "" {
		err := s.daemon.DoJSON(ctx, http.MethodPost, base+"approvals/"+request+"/resolve", map[string]any{"decisionId": decision}, &out)
		return map[string]any{"ok": err == nil}, err
	}
	action := stringArg(args, "action")
	if action == "" {
		return nil, errors.New("give decisionId for an approval, or action for a question")
	}
	body := map[string]any{"action": action}
	if content, ok := args["content"]; ok && content != nil {
		body["content"] = content
	}
	err := s.daemon.DoJSON(ctx, http.MethodPost, base+"inputs/"+request+"/resolve", body, &out)
	return map[string]any{"ok": err == nil}, err
}

func callSessionSpawn(ctx context.Context, s *server, args map[string]any) (any, error) {
	caller := callerSession()
	body := map[string]any{"parentSessionId": caller, "async": true}
	for _, name := range []string{"prompt", "projectId", "kind", "harness", "model", "effort", "mode", "branch", "baseBranch"} {
		if value := stringArg(args, name); value != "" {
			body[name] = value
		}
	}
	if name := stringArg(args, "displayName"); name != "" {
		body["displayName"], body["displayNameExplicit"] = name, true
	}
	if id := stringArg(args, "requestId"); id != "" {
		body["clientRequestId"] = id
	}
	if body["projectId"] == nil {
		var own struct {
			Session struct {
				ProjectID string `json:"projectId"`
			} `json:"session"`
		}
		if err := s.daemon.DoJSON(ctx, http.MethodGet, sessionPath(caller, ""), nil, &own); err != nil {
			return nil, fmt.Errorf("could not resolve this session's project; pass projectId: %w", err)
		}
		if own.Session.ProjectID == "" {
			return nil, errors.New("this session has no project; pass projectId")
		}
		body["projectId"] = own.Session.ProjectID
	}
	var response struct {
		Session map[string]any `json:"session"`
	}
	if err := s.daemon.DoJSON(ctx, http.MethodPost, "sessions", body, &response); err != nil {
		return nil, err
	}
	return sessionSummary(response.Session), nil
}

func callSessionFork(ctx context.Context, s *server, args map[string]any) (any, error) {
	id := stringArg(args, "requestId")
	if id == "" {
		id = newRequestID()
	}
	var out json.RawMessage
	err := s.daemon.DoJSON(ctx, http.MethodPost, sessionPath(stringArg(args, "sessionId"), "/fork"), map[string]any{"clientRequestId": id}, &out)
	return out, err
}

func callReport(ctx context.Context, s *server, args map[string]any) (any, error) {
	caller := callerSession()
	if caller == "" {
		return nil, errors.New("report must run inside an Orchestrator session (AO_SESSION_ID is not set)")
	}
	body := map[string]any{"sessionId": caller, "state": stringArg(args, "state")}
	for _, name := range []string{"note", "message"} {
		if value := stringArg(args, name); value != "" {
			body[name] = value
		}
	}
	var out json.RawMessage
	err := s.daemon.DoJSON(ctx, http.MethodPost, "reports", body, &out)
	return out, err
}
