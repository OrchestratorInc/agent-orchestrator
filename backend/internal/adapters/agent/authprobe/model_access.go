package authprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// VerifyModelAccess 用匿名、无效凭据和实际凭据对照验证目录权限，不发送生成请求。
// 对照必须拒绝且目标模型必须存在；证据只证明认证与目录权限，不证明推理额度。
func VerifyModelAccess(ctx context.Context, route Connection, transport http.RoundTripper) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
	step := domain.NewAgentAuthDiagnostic("provider_validation", "provider_inconclusive")
	step.Provider = route.Provider
	base, err := url.Parse(route.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) ||
		route.APIKey == "" || route.Model == "" {
		step.Code = "provider_unsupported"
		return ports.AgentAuthStatusConfigured, completeConnectionStep(step)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := strings.TrimRight(route.BaseURL, "/") + "/models"
	var invalid [24]byte
	if _, err := rand.Read(invalid[:]); err != nil {
		return ports.AgentAuthStatusUnknown, step
	}
	for _, key := range []string{"", "ao-invalid-" + hex.EncodeToString(invalid[:])} {
		status, _, err := modelDirectory(ctx, client, endpoint, key, false)
		if err != nil || status != http.StatusUnauthorized && status != http.StatusForbidden {
			return ports.AgentAuthStatusConfigured, step
		}
	}
	status, raw, err := modelDirectory(ctx, client, endpoint, route.APIKey, true)
	step.HTTPStatus = status
	if err != nil {
		return ports.AgentAuthStatusConfigured, step
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		step.Code = "provider_rejected"
		return ports.AgentAuthStatusUnauthorized, completeConnectionStep(step)
	}
	if status != http.StatusOK {
		return ports.AgentAuthStatusConfigured, step
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&models) != nil || decoder.Decode(new(any)) != io.EOF {
		return ports.AgentAuthStatusConfigured, step
	}
	for _, model := range models.Data {
		if model.ID == route.Model {
			step.Code = "provider_accepted"
			return ports.AgentAuthStatusAuthorized, completeConnectionStep(step)
		}
	}
	return ports.AgentAuthStatusConfigured, step
}

// modelDirectory 不重试，不传播含 URL 的网络错误或响应正文，并限制目录大小。
func modelDirectory(ctx context.Context, client *http.Client, endpoint, key string, readBody bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return 0, nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if !readBody || resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, nil
	}
	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if len(raw) > maxBody {
		return 0, nil, io.ErrShortBuffer
	}
	return resp.StatusCode, raw, err
}
