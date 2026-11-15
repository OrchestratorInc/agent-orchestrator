package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type ProviderOptions struct {
	Client             *Client
	HTTPClient         *http.Client
	Token              TokenSource
	SkipTokenPreflight bool
	RESTBase           string
	UserAgent          string
	Logger             *slog.Logger

	AllowedHosts []string

	HostTokens map[string]TokenSource
}

type Provider struct {
	client *Client
	logger *slog.Logger

	allowedHosts map[string]bool

	hostTokens map[string]TokenSource

	hostClients   map[string]*Client
	hostClientCfg ClientOptions
	hostMu        sync.Mutex

	cache *cache

	hostIdentities map[string]ports.SCMIdentity
	identityMu     sync.Mutex
}

func NewProvider(opts ProviderOptions) (*Provider, error) {
	if opts.Client == nil && opts.Token != nil && !opts.SkipTokenPreflight {
		_, err := opts.Token.Token(context.Background())
		if err != nil {
			return nil, err
		}
	}
	c := opts.Client
	if c == nil {
		c = NewClient(ClientOptions{
			HTTPClient: opts.HTTPClient,
			Token:      opts.Token,
			RESTBase:   opts.RESTBase,
			UserAgent:  opts.UserAgent,
		})
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	allowed := make(map[string]bool, len(opts.AllowedHosts))
	for _, h := range opts.AllowedHosts {
		h = NormalizeHost(h)
		if h != "" {
			allowed[h] = true
		}
	}

	hostTokens := make(map[string]TokenSource, len(opts.HostTokens))
	for h, src := range opts.HostTokens {
		h = NormalizeHost(h)
		if h != "" && src != nil {
			hostTokens[h] = src
		}
	}

	return &Provider{
		client:       c,
		logger:       logger,
		allowedHosts: allowed,
		hostTokens:   hostTokens,
		hostClientCfg: ClientOptions{
			HTTPClient: opts.HTTPClient,
			Token:      opts.Token,
			UserAgent:  opts.UserAgent,
			RESTBase:   opts.RESTBase,
		},
		hostClients: map[string]*Client{},
		cache:       newCache(),
	}, nil
}

func (p *Provider) isHostAllowed(host string) bool {
	host = NormalizeHost(host)
	if host == "" {
		return false
	}
	if IsGitLabDotCom(host) {
		return true
	}
	return p.allowedHosts[host]
}

func (p *Provider) clientForHost(host string) *Client {
	h := NormalizeHost(host)
	if IsGitLabDotCom(h) {
		return p.client
	}
	if !p.allowedHosts[h] {
		return nil
	}
	if p.client != nil && hostMatchesRESTBase(p.client.restBase, h) {
		return p.client
	}

	p.hostMu.Lock()
	defer p.hostMu.Unlock()
	if c, ok := p.hostClients[h]; ok {
		return c
	}

	cfg := p.hostClientCfg
	cfg.RESTBase = "https://" + h + "/api/v4"
	if src, ok := p.hostTokens[h]; ok {
		cfg.Token = src
	}
	c := NewClient(cfg)
	p.hostClients[h] = c
	return c
}

func hostMatchesRESTBase(restBase, host string) bool {
	if restBase == "" {
		return false
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(restBase, "https://"), "http://")
	if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return strings.EqualFold(trimmed, host)
}

var ErrHostNotAllowed = fmt.Errorf("gitlab scm: host not in allowlist")

func (p *Provider) clientForRepoErr(repo ports.SCMRepo) (*Client, error) {
	c := p.clientForHost(repo.Host)
	if c == nil {
		return nil, fmt.Errorf("gitlab scm: host %q not in allowlist: %w", repo.Host, ErrHostNotAllowed)
	}
	return c, nil
}

func (p *Provider) AuthenticatedIdentity(ctx context.Context) (ports.SCMIdentity, error) {
	return p.AuthenticatedIdentityForHost(ctx, "")
}

func (p *Provider) AuthenticatedIdentityForHost(ctx context.Context, host string) (ports.SCMIdentity, error) {
	key := NormalizeHost(host)
	p.identityMu.Lock()
	defer p.identityMu.Unlock()
	if p.hostIdentities == nil {
		p.hostIdentities = make(map[string]ports.SCMIdentity)
	}
	if ident, ok := p.hostIdentities[key]; ok {
		return ident, nil
	}

	client := p.clientForHost(host)
	if client == nil {
		return ports.SCMIdentity{}, fmt.Errorf("gitlab scm: host %q not in allowlist: %w", host, ErrHostNotAllowed)
	}

	resp, err := client.doGET(ctx, "/user", nil)
	if err != nil {
		return ports.SCMIdentity{}, err
	}

	var user struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(resp.Body, &user); err != nil {
		return ports.SCMIdentity{}, fmt.Errorf("gitlab scm: decode authenticated user: %w", err)
	}

	login := strings.TrimSpace(user.Username)
	ident := ports.SCMIdentity{
		Login: login,
		Human: !isBotAuthor(login),
	}
	p.hostIdentities[key] = ident
	return ident, nil
}

func (p *Provider) SCMCredentialsAvailable(ctx context.Context) (bool, error) {
	if p.client != nil && p.client.tokens != nil {
		_, err := p.client.tokens.Token(ctx)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, ErrNoToken) {
			return false, err
		}
	}

	for _, src := range p.hostTokens {
		if src == nil {
			continue
		}
		_, err := src.Token(ctx)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, ErrNoToken) {
			return false, err
		}
	}

	return false, nil
}
