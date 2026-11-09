// Package providerprofile 将宿主配置限制为可验证的供应商引用，拒绝配置漂移和隐式凭据回退。
package providerprofile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/sync/singleflight"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Filename 是 AO 数据目录内由可信宿主维护的供应商登记文件。
const Filename = "provider-profiles.json"
const maxRegistryBytes = 256 << 10

var identityPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var envPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,127}$`)

// Registry 每次解析重读宿主文件，持久引用在 daemon 重启后仍须匹配相同规范化摘要。
type Registry struct {
	Path      string
	LookupEnv func(string) (string, bool)
	// Transport 仅由宿主装配；测试可注入本地 TLS 服务，HTTP 请求不能设置它。
	Transport http.RoundTripper
	mu        sync.Mutex
	auth      map[string]authObservation
	flight    singleflight.Group
}

// CaptureEnvironment 在 daemon 启动任何子进程前接管专用凭据；秘密只留在只读内存中，不再默认继承。
// 轮换通过修改宿主启动环境并重启 daemon 生效，不改变系统或父进程环境。
func CaptureEnvironment(path string) (*Registry, error) {
	credentials := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if !strings.HasPrefix(upper, ports.ProviderProfileCredentialPrefix) && upper != ports.ProviderProfileKeyEnv {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			return nil, apierr.Invalid("PROVIDER_ENVIRONMENT_UNSAFE", "无法隔离宿主供应商凭据", nil)
		}
		if upper != ports.ProviderProfileKeyEnv {
			if runtime.GOOS == "windows" {
				key = upper
			}
			credentials[key] = value
		}
	}
	return &Registry{Path: path, LookupEnv: func(key string) (string, bool) {
		value, ok := credentials[key]
		return value, ok
	}}, nil
}

// invalid 返回固定的无路径错误，解析器错误和宿主原始内容不能进入 API 或日志。
func invalid() error {
	return apierr.Invalid("PROVIDER_REGISTRY_INVALID", "供应商登记表无效，请检查宿主配置", nil)
}

// read 整体解码并验证登记表；不存在表示空目录，任何坏项都禁止部分接受。
func (r *Registry) read(ctx context.Context) ([]ports.ProviderProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(r.Path)
	if os.IsNotExist(err) {
		return []ports.ProviderProfile{}, nil
	}
	if err != nil {
		return nil, invalid()
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr != nil || !info.Mode().IsRegular() {
		return nil, invalid()
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxRegistryBytes+1))
	if err != nil || len(raw) > maxRegistryBytes {
		return nil, invalid()
	}
	var profiles []ports.ProviderProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&profiles) != nil || decoder.Decode(new(any)) != io.EOF || profiles == nil || len(profiles) > 100 {
		return nil, invalid()
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		if seen[p.ID] || !valid(p) {
			return nil, invalid()
		}
		seen[p.ID] = true
	}
	return profiles, nil
}

// valid 只接受 Codex Responses 明确支持的字段，所有权限和任意配置入口在解码时拒绝。
func valid(p ports.ProviderProfile) bool {
	if !identityPattern.MatchString(p.ID) || p.Harness != domain.HarnessCodex || !envPattern.MatchString(p.CredentialEnv) ||
		!strings.HasPrefix(p.CredentialEnv, ports.ProviderProfileCredentialPrefix) || p.CredentialEnv == ports.ProviderProfileCredentialPrefix ||
		p.Name == "" || len(p.Name) > 128 || p.Model == "" || len(p.Model) > 256 || len(p.BaseURL) > 2048 ||
		strings.IndexFunc(p.Name+p.Model+p.BaseURL, unicode.IsControl) >= 0 || strings.TrimSpace(p.Model) != p.Model ||
		p.ContextWindow < 0 || p.ContextWindow > 10_000_000 {
		return false
	}
	switch p.Effort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return false
	}
	u, err := url.Parse(p.BaseURL)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Opaque == ""
}

// reference 对规范化全部路由字段取摘要；改名、换端点、换凭据引用均使旧任务失效。
func reference(p ports.ProviderProfile) string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return p.ID + "@" + hex.EncodeToString(sum[:])
}

// List 返回不可携带秘密的固定引用；不会访问环境凭据或供应商。
func (r *Registry) List(ctx context.Context) ([]ports.ProviderProfileChoice, error) {
	profiles, err := r.read(ctx)
	if err != nil {
		return nil, err
	}
	choices := make([]ports.ProviderProfileChoice, 0, len(profiles))
	for _, p := range profiles {
		choices = append(choices, ports.ProviderProfileChoice{Reference: reference(p), Name: p.Name, Harness: p.Harness, Model: p.Model, Effort: p.Effort, ContextWindow: p.ContextWindow})
	}
	return choices, nil
}

// Resolve 要求调用者提供目录中的完整固定引用及明确模型，不接受原始 URL 或默认回退。
func (r *Registry) Resolve(ctx context.Context, ref string, harness domain.AgentHarness, model, effort string) (ports.ProviderProfile, error) {
	if harness != domain.HarnessCodex {
		return ports.ProviderProfile{}, apierr.Invalid("PROVIDER_PROFILE_UNSUPPORTED", "该 Adapter 不支持平台供应商引用", nil)
	}
	profiles, err := r.read(ctx)
	if err != nil {
		return ports.ProviderProfile{}, err
	}
	for _, p := range profiles {
		if ref == reference(p) {
			if p.Model != model || p.Effort != effort {
				return ports.ProviderProfile{}, apierr.Invalid("PROVIDER_SELECTION_MISMATCH", "模型或 effort 与固定供应商配置不一致", nil)
			}
			return p, nil
		}
	}
	return ports.ProviderProfile{}, apierr.Conflict("PROVIDER_PROFILE_STALE", "供应商引用不存在或配置已变化，请重新选择", nil)
}

// credentials 只读取登记的凭据，不把秘密写入错误正文。
func (r *Registry) credentials(ctx context.Context, ref string, harness domain.AgentHarness, model, effort string) (ports.ProviderProfile, string, error) {
	p, err := r.Resolve(ctx, ref, harness, model, effort)
	if err != nil {
		return p, "", err
	}
	lookup := r.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	secret, ok := lookup(p.CredentialEnv)
	if !ok || strings.TrimSpace(secret) == "" || strings.IndexFunc(secret, unicode.IsControl) >= 0 {
		return p, "", apierr.Conflict("PROVIDER_CREDENTIAL_UNAVAILABLE", "所选供应商凭据不可用", nil)
	}
	return p, secret, nil
}

// Validate 对未装配解析器的非空引用闭锁；空引用保留原生路径。
func Validate(ctx context.Context, registry ports.ProviderProfiles, ref string, harness domain.AgentHarness, model, effort string) error {
	if ref == "" {
		return nil
	}
	if registry == nil {
		return apierr.Invalid("WORKFLOW_PROFILE_UNSUPPORTED", "供应商引用服务未装配", nil)
	}
	_, err := registry.Resolve(ctx, ref, harness, model, effort)
	return err
}

var _ ports.ProviderProfiles = (*Registry)(nil)
