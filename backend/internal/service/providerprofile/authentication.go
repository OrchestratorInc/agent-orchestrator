package providerprofile

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// authObservation 仅在进程内保存凭据身份；重启、轮换或配置漂移必须重新验证。
type authObservation struct {
	credential [32]byte
	state      domain.AgentAuthenticationState
	expires    time.Time
}

// Authentication 验证所选路由的认证，不借用原生账号或将公开目录视作凭据证据。
func (r *Registry) Authentication(ctx context.Context, ref string, harness domain.AgentHarness, model, effort string) (domain.AgentAuthenticationState, error) {
	_, state, err := r.authenticated(ctx, ref, harness, model, effort)
	return state, err
}

// Environment 只返回已经验证且仍与当前登记一致的秘密，最后启动回调也走此边界。
func (r *Registry) Environment(ctx context.Context, ref string, harness domain.AgentHarness, model, effort string) (map[string]string, error) {
	secret, state, err := r.authenticated(ctx, ref, harness, model, effort)
	if err != nil {
		return nil, err
	}
	if state != domain.AgentAuthenticationAuthorized {
		return nil, apierr.Conflict("PROVIDER_AUTH_UNVERIFIED", "所选供应商未通过非生成式认证验证，无法启动", nil)
	}
	return map[string]string{ports.ProviderProfileKeyEnv: secret}, nil
}

// authenticated 合并相同配置与凭据的并发探测；轮换后不复用旧成功或失败。
func (r *Registry) authenticated(ctx context.Context, ref string, harness domain.AgentHarness, model, effort string) (string, domain.AgentAuthenticationState, error) {
	p, secret, err := r.credentials(ctx, ref, harness, model, effort)
	if err != nil {
		return "", domain.AgentAuthenticationUnknown, err
	}
	identity := sha256.Sum256([]byte(secret))
	result := r.flight.DoChan(ref+"/"+hex.EncodeToString(identity[:]), func() (any, error) {
		r.mu.Lock()
		cached, found := r.auth[ref]
		r.mu.Unlock()
		if found && cached.credential == identity && time.Now().Before(cached.expires) {
			return cached.state, nil
		}
		state := r.probeAuthentication(ctx, p, secret)
		ttl := 30 * time.Second
		if state == domain.AgentAuthenticationAuthorized {
			ttl = 2 * time.Minute
		}
		r.mu.Lock()
		if r.auth == nil || len(r.auth) >= 100 {
			r.auth = make(map[string]authObservation)
		}
		r.auth[ref] = authObservation{credential: identity, state: state, expires: time.Now().Add(ttl)}
		r.mu.Unlock()
		return state, nil
	})
	select {
	case <-ctx.Done():
		return "", domain.AgentAuthenticationUnknown, ctx.Err()
	case outcome := <-result:
		// 等待期间配置或秘密可能变化；不能把旧证据交给新的启动环境。
		_, current, err := r.credentials(ctx, ref, harness, model, effort)
		if err != nil {
			return "", domain.AgentAuthenticationUnknown, err
		}
		if current != secret {
			return "", domain.AgentAuthenticationUnknown, apierr.Conflict("PROVIDER_CREDENTIAL_CHANGED", "供应商凭据已变化，请重试", nil)
		}
		state, ok := outcome.Val.(domain.AgentAuthenticationState)
		if !ok {
			return "", domain.AgentAuthenticationUnknown, apierr.Internal("PROVIDER_AUTH_UNVERIFIED", "供应商认证未返回有效状态")
		}
		return secret, state, outcome.Err
	}
}

// probeAuthentication 最多三次 GET、总共八秒；对照必须拒绝，成功正文只用于确认目标模型。
// 此证据仅覆盖身份与目录权限，不证明 Responses 推理额度或生成能力。
func (r *Registry) probeAuthentication(ctx context.Context, profile ports.ProviderProfile, secret string) domain.AgentAuthenticationState {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client := &http.Client{Transport: r.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	url := strings.TrimRight(profile.BaseURL, "/") + "/models"
	var invalid [24]byte
	if _, err := rand.Read(invalid[:]); err != nil {
		return domain.AgentAuthenticationUnknown
	}
	for _, key := range []string{"", "ao-invalid-" + hex.EncodeToString(invalid[:])} {
		status, _, err := modelDirectory(ctx, client, url, key, false)
		if err != nil || status != http.StatusUnauthorized && status != http.StatusForbidden {
			return domain.AgentAuthenticationConfigured
		}
	}
	status, raw, err := modelDirectory(ctx, client, url, secret, true)
	if err != nil {
		return domain.AgentAuthenticationConfigured
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return domain.AgentAuthenticationUnauthorized
	}
	if status != http.StatusOK {
		return domain.AgentAuthenticationConfigured
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&models) != nil || decoder.Decode(new(any)) != io.EOF {
		return domain.AgentAuthenticationConfigured
	}
	for _, model := range models.Data {
		if model.ID == profile.Model {
			return domain.AgentAuthenticationAuthorized
		}
	}
	return domain.AgentAuthenticationConfigured
}

// modelDirectory 不跟随重定向、不重试、不传播含 URL 的网络错误或响应正文。
func modelDirectory(ctx context.Context, client *http.Client, url, key string, readBody bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
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
