package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// `ao mcp serve` is a personal remote MCP connector: claude.ai adds it as a
// custom connector by URL and drives this machine's Orchestrator with the
// tools in mcp_serve_tools.go. It is its own small OAuth authorization server
// for Claude (dynamic registration, PKCE S256), hands the actual sign-in to
// Google, and admits only allowlisted, verified emails. It binds loopback
// only; a tunnel the user runs exposes it at --public-url.
//
// ponytail: dogfood branch. AGENTS.md forbids new network-facing binds, so
// upstreaming needs an ADR or the AO Cloud relay design; see the MCP access
// assessment.

const (
	mcpServeProtocolVersion = "2025-11-25"
	mcpServeMaxBody         = 1 << 20
	mcpServePendingTTL      = 10 * time.Minute
	mcpServeCodeTTL         = 5 * time.Minute
	mcpServeAccessTTL       = time.Hour
	mcpServeRefreshTTL      = 30 * 24 * time.Hour
	mcpServeMaxClients      = 100
)

var mcpServeProtocolVersions = []string{mcpServeProtocolVersion, "2025-06-18", "2025-03-26"}

type mcpServeOptions struct {
	addr        string
	publicURL   string
	allowEmails []string
}

func newMCPServeCommand(ctx *commandContext) *cobra.Command {
	var opts mcpServeOptions
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve Orchestrator as a remote MCP connector for claude.ai (personal use)",
		Long: "Serve Orchestrator's agent tools over MCP Streamable HTTP with Google sign-in.\n\n" +
			"Binds a loopback address only. Expose it through your own HTTPS tunnel at --public-url,\n" +
			"then add <public-url>/mcp as a custom connector in Claude.\n\n" +
			"Environment: AO_MCP_GOOGLE_CLIENT_ID and AO_MCP_GOOGLE_CLIENT_SECRET (a Google OAuth web client\n" +
			"whose authorized redirect URI is <public-url>/oauth/google/callback).",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv, err := newMCPServeServer(ctx, opts)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", opts.addr)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ao mcp serve: listening on http://%s, connector URL %s\n", ln.Addr(), srv.resource)
			httpSrv := &http.Server{Handler: srv.handler(), ReadHeaderTimeout: 10 * time.Second}
			go func() {
				<-cmd.Context().Done()
				_ = httpSrv.Close()
			}()
			if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.addr, "addr", "127.0.0.1:8787", "Loopback address to listen on")
	f.StringVar(&opts.publicURL, "public-url", "", "Public HTTPS origin the tunnel serves, e.g. https://ao-mcp.example.dev (required)")
	f.StringSliceVar(&opts.allowEmails, "allow-email", nil, "Google account email allowed to sign in (required, repeatable)")
	return cmd
}

type mcpServeServer struct {
	c         *commandContext
	public    string
	resource  string
	allowed   map[string]bool
	statePath string
	log       io.Writer
	now       func() time.Time
	http      *http.Client

	googleID, googleSecret                   string
	googleAuthURL, googleTokenURL, googleUserURL string

	mu      sync.Mutex
	st      mcpServeState
	pending map[string]mcpServePending
	codes   map[string]mcpServeCode
	access  map[string]mcpServeAccess
	// done caches each write tool's result by request id, so a client retry
	// after a dropped response returns the first outcome instead of acting again.
	// ponytail: in memory, lost on restart; the daemon's own replay keys still
	// cover create_agent and chat send_message across restarts.
	done map[string]mcpServeDone
}

type mcpServeState struct {
	Clients map[string]mcpServeClient  `json:"clients"`
	Refresh map[string]mcpServeRefresh `json:"refresh"`
}

type mcpServeClient struct {
	RedirectURIs []string  `json:"redirectUris"`
	Name         string    `json:"name,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type mcpServeRefresh struct {
	ClientID  string    `json:"clientId"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type mcpServePending struct {
	clientID, redirectURI, challenge, clientState string
	expires                                       time.Time
}

type mcpServeCode struct {
	clientID, redirectURI, challenge, email string
	expires                                 time.Time
}

type mcpServeAccess struct {
	clientID, email string
	expires         time.Time
}

type mcpServeDone struct {
	argsHash string
	result   map[string]any
}

func newMCPServeServer(c *commandContext, opts mcpServeOptions) (*mcpServeServer, error) {
	host, _, err := net.SplitHostPort(opts.addr)
	if err != nil {
		return nil, usageError{fmt.Errorf("--addr: %w", err)}
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, usageError{errors.New("--addr must be a loopback address; expose it through a tunnel")}
	}
	public := strings.TrimRight(strings.TrimSpace(opts.publicURL), "/")
	u, err := url.Parse(public)
	if public == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return nil, usageError{errors.New("--public-url must be an https origin with no path, e.g. https://ao-mcp.example.dev")}
	}
	allowed := map[string]bool{}
	for _, e := range opts.allowEmails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			allowed[e] = true
		}
	}
	if len(allowed) == 0 {
		return nil, usageError{errors.New("--allow-email is required")}
	}
	id, secret := strings.TrimSpace(os.Getenv("AO_MCP_GOOGLE_CLIENT_ID")), strings.TrimSpace(os.Getenv("AO_MCP_GOOGLE_CLIENT_SECRET"))
	if id == "" || secret == "" {
		return nil, usageError{errors.New("AO_MCP_GOOGLE_CLIENT_ID and AO_MCP_GOOGLE_CLIENT_SECRET must be set")}
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	s := &mcpServeServer{
		c: c, public: public, resource: public + "/mcp", allowed: allowed,
		statePath: filepath.Join(cfg.DataDir, "mcp-remote", "state.json"),
		log:       c.deps.Err, now: time.Now, http: &http.Client{Timeout: 15 * time.Second},
		googleID: id, googleSecret: secret,
		googleAuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		googleTokenURL: "https://oauth2.googleapis.com/token",
		googleUserURL:  "https://openidconnect.googleapis.com/v1/userinfo",
	}
	s.reset()
	if raw, err := os.ReadFile(s.statePath); err == nil {
		if err := json.Unmarshal(raw, &s.st); err != nil {
			return nil, fmt.Errorf("read %s: %w", s.statePath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.st.Clients == nil {
		s.st.Clients = map[string]mcpServeClient{}
	}
	if s.st.Refresh == nil {
		s.st.Refresh = map[string]mcpServeRefresh{}
	}
	return s, nil
}

func (s *mcpServeServer) reset() {
	s.st = mcpServeState{Clients: map[string]mcpServeClient{}, Refresh: map[string]mcpServeRefresh{}}
	s.pending = map[string]mcpServePending{}
	s.codes = map[string]mcpServeCode{}
	s.access = map[string]mcpServeAccess{}
	s.done = map[string]mcpServeDone{}
}

func (s *mcpServeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", s.authServerMetadata)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("GET /oauth/google/callback", s.googleCallback)
	mux.HandleFunc("POST /token", s.token)
	mux.HandleFunc("/mcp", s.mcp)
	return mux
}

func (s *mcpServeServer) protectedResource(w http.ResponseWriter, _ *http.Request) {
	writeMCPServeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.resource,
		"authorization_servers":    []string{s.public},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Agent Orchestrator",
	})
}

func (s *mcpServeServer) authServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeMCPServeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.public,
		"authorization_endpoint":                s.public + "/authorize",
		"token_endpoint":                        s.public + "/token",
		"registration_endpoint":                 s.public + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"authorization_response_iss_parameter_supported": true,
	})
}

// register is RFC 7591 dynamic registration for public clients. Redirects are
// limited to Claude's hosted callback and loopback (Claude Code, MCP
// Inspector), so a stranger's registration cannot send a code anywhere else.
func (s *mcpServeServer) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, uri := range in.RedirectURIs {
		if !mcpServeRedirectAllowed(uri) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URI not allowed: "+uri)
			return
		}
	}
	name := in.ClientName
	if len(name) > 200 {
		name = name[:200]
	}
	id := mcpServeRandom()
	s.mu.Lock()
	if len(s.st.Clients) >= mcpServeMaxClients {
		oldest := ""
		for k, v := range s.st.Clients {
			if oldest == "" || v.CreatedAt.Before(s.st.Clients[oldest].CreatedAt) {
				oldest = k
			}
		}
		delete(s.st.Clients, oldest)
	}
	s.st.Clients[id] = mcpServeClient{RedirectURIs: in.RedirectURIs, Name: name, CreatedAt: s.now()}
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not save registration")
		return
	}
	writeMCPServeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        s.now().Unix(),
		"redirect_uris":              in.RedirectURIs,
		"client_name":                name,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

func (s *mcpServeServer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID, redirectURI := q.Get("client_id"), q.Get("redirect_uri")
	s.mu.Lock()
	client, ok := s.st.Clients[clientID]
	s.mu.Unlock()
	// Until the redirect is validated, errors are shown here, never redirected.
	if !ok {
		http.Error(w, "unknown client_id; remove and re-add the connector", http.StatusBadRequest)
		return
	}
	if !mcpServeRedirectMatches(client.RedirectURIs, redirectURI) {
		http.Error(w, "redirect_uri is not registered for this client", http.StatusBadRequest)
		return
	}
	fail := func(code, desc string) { redirectOAuthError(w, r, redirectURI, q.Get("state"), s.public, code, desc) }
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "response_type must be code")
		return
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 {
		fail("invalid_request", "PKCE with S256 is required")
		return
	}
	if res := q.Get("resource"); res != "" && strings.TrimRight(res, "/") != s.resource {
		fail("invalid_target", "resource must be "+s.resource)
		return
	}
	gstate := mcpServeRandom()
	s.mu.Lock()
	s.sweepLocked()
	s.pending[gstate] = mcpServePending{clientID: clientID, redirectURI: redirectURI, challenge: q.Get("code_challenge"), clientState: q.Get("state"), expires: s.now().Add(mcpServePendingTTL)}
	s.mu.Unlock()
	http.Redirect(w, r, s.googleAuthURL+"?"+url.Values{
		"client_id":     {s.googleID},
		"redirect_uri":  {s.public + "/oauth/google/callback"},
		"response_type": {"code"},
		"scope":         {"openid email"},
		"state":         {gstate},
		"prompt":        {"select_account"},
	}.Encode(), http.StatusFound)
}

func (s *mcpServeServer) googleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	p, ok := s.pending[q.Get("state")]
	delete(s.pending, q.Get("state"))
	s.mu.Unlock()
	if !ok || s.now().After(p.expires) {
		http.Error(w, "sign-in expired; start again from Claude", http.StatusBadRequest)
		return
	}
	fail := func(code, desc string) { redirectOAuthError(w, r, p.redirectURI, p.clientState, s.public, code, desc) }
	if q.Get("error") != "" {
		fail("access_denied", "Google sign-in was cancelled")
		return
	}
	email, err := s.googleEmail(r.Context(), q.Get("code"))
	if err != nil {
		_, _ = fmt.Fprintf(s.log, "ao mcp serve: google sign-in failed: %v\n", err)
		fail("access_denied", "Google sign-in failed")
		return
	}
	if !s.allowed[email] {
		_, _ = fmt.Fprintf(s.log, "ao mcp serve: refused sign-in for %s\n", email)
		fail("access_denied", "this Google account is not allowed")
		return
	}
	code := mcpServeRandom()
	s.mu.Lock()
	s.codes[mcpServeHash(code)] = mcpServeCode{clientID: p.clientID, redirectURI: p.redirectURI, challenge: p.challenge, email: email, expires: s.now().Add(mcpServeCodeTTL)}
	s.mu.Unlock()
	v := url.Values{"code": {code}, "iss": {s.public}}
	if p.clientState != "" {
		v.Set("state", p.clientState)
	}
	http.Redirect(w, r, mcpServeAppendQuery(p.redirectURI, v), http.StatusFound)
}

// googleEmail redeems Google's code and reads the verified email from the
// userinfo endpoint over TLS, so no ID token signature check is needed.
func (s *mcpServeServer) googleEmail(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"code": {code}, "client_id": {s.googleID}, "client_secret": {s.googleSecret},
		"redirect_uri": {s.public + "/oauth/google/callback"}, "grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := s.doGoogle(req, &tok); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, s.googleUserURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := s.doGoogle(req, &info); err != nil {
		return "", fmt.Errorf("userinfo: %w", err)
	}
	if !info.EmailVerified || info.Email == "" {
		return "", errors.New("email is not verified")
	}
	return strings.ToLower(info.Email), nil
}

func (s *mcpServeServer) doGoogle(req *http.Request, out any) error {
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func (s *mcpServeServer) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	clientID := r.PostForm.Get("client_id")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	if _, ok := s.st.Clients[clientID]; !ok {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "unknown client_id")
		return
	}
	var email string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		key := mcpServeHash(r.PostForm.Get("code"))
		code, ok := s.codes[key]
		delete(s.codes, key)
		if !ok || code.clientID != clientID || code.redirectURI != r.PostForm.Get("redirect_uri") {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
			return
		}
		if !mcpServePKCEMatches(code.challenge, r.PostForm.Get("code_verifier")) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match")
			return
		}
		email = code.email
	case "refresh_token":
		key := mcpServeHash(r.PostForm.Get("refresh_token"))
		rt, ok := s.st.Refresh[key]
		delete(s.st.Refresh, key)
		if !ok || rt.ClientID != clientID || !s.allowed[rt.Email] {
			_ = s.saveLocked()
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
			return
		}
		email = rt.Email
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	access, refresh := mcpServeRandom(), mcpServeRandom()
	now := s.now()
	s.access[mcpServeHash(access)] = mcpServeAccess{clientID: clientID, email: email, expires: now.Add(mcpServeAccessTTL)}
	s.st.Refresh[mcpServeHash(refresh)] = mcpServeRefresh{ClientID: clientID, Email: email, ExpiresAt: now.Add(mcpServeRefreshTTL)}
	if err := s.saveLocked(); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not save token")
		return
	}
	writeMCPServeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(mcpServeAccessTTL.Seconds()),
		"refresh_token": refresh,
	})
}

// mcp serves Streamable HTTP with JSON responses only: every tool answers in
// one response, so no SSE stream or session id is needed.
func (s *mcpServeServer) mcp(w http.ResponseWriter, r *http.Request) {
	email, ok := s.bearer(r)
	if !ok {
		challenge := fmt.Sprintf(`Bearer resource_metadata=%q`, s.public+"/.well-known/oauth-protected-resource/mcp")
		if r.Header.Get("Authorization") != "" {
			challenge += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token", "sign in required")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpServeMaxBody))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	resp := s.handleMCPServe(r.Context(), email, bytes.TrimSpace(body))
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeMCPServeJSON(w, http.StatusOK, resp)
}

func (s *mcpServeServer) bearer(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.access[mcpServeHash(tok)]
	if !ok || s.now().After(a.expires) || !s.allowed[a.email] {
		return "", false
	}
	return a.email, true
}

func (s *mcpServeServer) handleMCPServe(ctx context.Context, email string, line []byte) *mcpResponse {
	if len(line) > 0 && line[0] == '[' {
		return &mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32600, Message: "batch requests are not supported"}}
	}
	var req mcpRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "parse error: " + err.Error()}}
	}
	if req.ID == nil {
		return nil
	}
	resp := &mcpResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpServeProtocolVersion
		if slices.Contains(mcpServeProtocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agent-orchestrator", "title": "Agent Orchestrator", "version": Version},
			"instructions":    mcpServeInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpServeTools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &mcpError{Code: -32602, Message: "invalid params"}
			break
		}
		if !mcpServeToolExists(p.Name) {
			resp.Error = &mcpError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", p.Name)}
			break
		}
		start := s.now()
		resp.Result = s.callTool(ctx, p.Name, p.Arguments)
		status := "ok"
		if m, _ := resp.Result.(map[string]any); m["isError"] == true {
			status = "error"
		}
		// Tool name and outcome only: arguments and results stay out of logs.
		_, _ = fmt.Fprintf(s.log, "ao mcp serve: %s %s %s (%s)\n", email, p.Name, status, s.now().Sub(start).Round(time.Millisecond))
	default:
		resp.Error = &mcpError{Code: -32601, Message: fmt.Sprintf("method %q not found", req.Method)}
	}
	return resp
}

// sweepLocked drops expired grants. ponytail: linear scan per request; fine
// for one user's handful of tokens.
func (s *mcpServeServer) sweepLocked() {
	now := s.now()
	for k, v := range s.pending {
		if now.After(v.expires) {
			delete(s.pending, k)
		}
	}
	for k, v := range s.codes {
		if now.After(v.expires) {
			delete(s.codes, k)
		}
	}
	for k, v := range s.access {
		if now.After(v.expires) {
			delete(s.access, k)
		}
	}
	for k, v := range s.st.Refresh {
		if now.After(v.ExpiresAt) {
			delete(s.st.Refresh, k)
		}
	}
}

func (s *mcpServeServer) saveLocked() error {
	raw, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o700); err != nil {
		return err
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath)
}

func mcpServeRedirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil {
		return false
	}
	switch raw {
	case "https://claude.ai/api/mcp/auth_callback", "https://claude.com/api/mcp/auth_callback":
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
}

// mcpServeRedirectMatches is exact, except loopback redirects match on any
// port (RFC 8252 section 7.3): Claude Code picks a fresh port each sign-in.
func mcpServeRedirectMatches(registered []string, got string) bool {
	gu, err := url.Parse(got)
	if err != nil {
		return false
	}
	for _, r := range registered {
		if r == got {
			return true
		}
		ru, err := url.Parse(r)
		if err == nil && ru.Scheme == "http" && gu.Scheme == "http" && mcpServeRedirectAllowed(r) &&
			ru.Hostname() == gu.Hostname() && ru.Path == gu.Path && ru.RawQuery == gu.RawQuery {
			return true
		}
	}
	return false
}

func mcpServePKCEMatches(challenge, verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func mcpServeRandom() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func mcpServeHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mcpServeAppendQuery(raw string, v url.Values) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for k, vals := range v {
		q[k] = vals
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func redirectOAuthError(w http.ResponseWriter, r *http.Request, redirectURI, state, iss, code, desc string) {
	v := url.Values{"error": {code}, "error_description": {desc}, "iss": {iss}}
	if state != "" {
		v.Set("state", state)
	}
	http.Redirect(w, r, mcpServeAppendQuery(redirectURI, v), http.StatusFound)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeMCPServeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func writeMCPServeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
