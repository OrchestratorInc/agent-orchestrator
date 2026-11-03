package authprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Connection names an adapter-resolved route. APIKey must never enter diagnostics.
type Connection struct {
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
}

// VerifyConnection makes one bounded, one-token request without tools or a native
// agent session. A model listing alone can be public and cannot verify a key.
func VerifyConnection(ctx context.Context, route Connection, client *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
	started := time.Now()
	step := domain.NewAgentAuthDiagnostic("provider_validation", "provider_unsupported")
	step.Provider = route.Provider
	base, err := url.Parse(route.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) ||
		route.APIKey == "" || route.Model == "" {
		return ports.AgentAuthStatusConfigured, step
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	body, _ := json.Marshal(map[string]any{
		"model": route.Model, "max_tokens": 1, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "Reply OK."}},
	})
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return ports.AgentAuthStatusConfigured, step
	}
	req.Header.Set("Authorization", "Bearer "+route.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// Never forward a credential to a redirect target, including the same host.
	transport := http.DefaultTransport
	if client != nil && client.Transport != nil {
		transport = client.Transport
	}
	probeClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := probeClient.Do(req)
	step.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		step.Code = "provider_unreachable"
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			step.Code = "probe_timeout"
		} else if ctx.Err() != nil {
			step.Code = "probe_cancelled"
		}
		return ports.AgentAuthStatusConfigured, completeConnectionStep(step)
	}
	defer func() { _ = response.Body.Close() }()
	step.HTTPStatus = response.StatusCode
	step.Code = "provider_inconclusive"
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		step.Code = "provider_rejected"
		return ports.AgentAuthStatusUnauthorized, completeConnectionStep(step)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var result struct {
			Choices []struct {
				Message *struct {
					Role string `json:"role"`
				} `json:"message"`
			} `json:"choices"`
			Error json.RawMessage `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&result) == nil && len(result.Choices) > 0 && result.Choices[0].Message != nil && result.Choices[0].Message.Role == "assistant" && (len(result.Error) == 0 || string(result.Error) == "null") {
			step.Code = "provider_accepted"
			return ports.AgentAuthStatusAuthorized, completeConnectionStep(step)
		}
	}
	return ports.AgentAuthStatusConfigured, completeConnectionStep(step)
}

func completeConnectionStep(step domain.AgentAuthDiagnostic) domain.AgentAuthDiagnostic {
	result := domain.NewAgentAuthDiagnostic(step.Method, step.Code)
	result.Provider, result.HTTPStatus, result.DurationMs = step.Provider, step.HTTPStatus, step.DurationMs
	return result
}
