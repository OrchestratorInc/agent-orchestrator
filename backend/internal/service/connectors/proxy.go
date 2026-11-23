package connectors

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	startGrace         = 5 * time.Minute
	maxTrackedSessions = 256
	proxyPrefix        = "/mcp/s/"
	launchSuffix       = "/launch"
	keyFileName        = "mcp-connectors.key"
	maxProxyBody       = 32 << 20
	rpcServerError     = -32001
)

// SessionInput names the session that asks for its connectors.
type SessionInput struct {
	SessionID string
	ProjectID string
	Native    func() map[string]bool
}

// SessionConnector is one connector a session got, with its use in that session.
type SessionConnector struct {
	Key       string
	Name      string
	Transport Transport
	Status    Status
	Enabled   bool
	Calls     int
	LastTool  string
}

type sessionUse struct {
	keys   []string
	calls  map[string]int
	last   map[string]string
	issued time.Time
}

// SetSessionLookup sets how the proxy learns the project of a session and whether the session is open.
func (s *Service) SetSessionLookup(lookup func(ctx context.Context, sessionID string) (projectID string, open bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionLookup = lookup
}

// ForSession returns the tool servers a new or restored session gets, and records them for the session.
func (s *Service) ForSession(ctx context.Context, in SessionInput) []ports.ChatMCPServerConfig {
	s.forgetClosed(ctx)
	var native map[string]bool
	if in.Native != nil && s.offers(in.ProjectID) {
		native = in.Native()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.SessionID == "" || s.load() != nil {
		return nil
	}
	token, err := s.sessionToken(in.SessionID)
	if err != nil {
		return nil
	}
	var out []ports.ChatMCPServerConfig
	use := s.sessions[in.SessionID]
	if use == nil {
		use = &sessionUse{calls: map[string]int{}, last: map[string]string{}}
		s.sessions[in.SessionID] = use
	}
	use.keys, use.issued = use.keys[:0], s.now()
	for i := range s.records {
		rec := &s.records[i]
		if !rec.Enabled || (rec.ProjectID != "" && rec.ProjectID != in.ProjectID) {
			continue
		}
		if native[rec.Key] {
			continue
		}
		use.keys = append(use.keys, rec.Key)
		entry := ports.ChatMCPServerConfig{
			Name: rec.Key, Type: "http",
			URL:     s.base() + proxyPrefix + url.PathEscape(in.SessionID) + "/connectors/" + url.PathEscape(rec.Key),
			Headers: map[string]string{"Authorization": "Bearer " + token},
		}
		if rec.Transport == TransportStdio {
			entry.Type, entry.URL = "stdio", entry.URL+launchSuffix
		}
		out = append(out, entry)
	}
	return out
}

// ServeLaunch gives a session the command and the environment values of one of its command connectors.
func (s *Service) ServeLaunch(w http.ResponseWriter, r *http.Request, sessionID, key string) {
	rec, problem := s.admit(r, sessionID, key, TransportStdio)
	if problem != "" {
		status := http.StatusForbidden
		if rec == nil {
			status = http.StatusUnauthorized
		}
		http.Error(w, problem, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"command": rec.Command, "args": rec.Args, "env": rec.Env})
}

// SessionConnectors returns the connectors a session got and how it used them.
func (s *Service) SessionConnectors(_ context.Context, sessionID string) ([]SessionConnector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	use := s.sessions[sessionID]
	if use == nil {
		return []SessionConnector{}, nil
	}
	out := make([]SessionConnector, 0, len(use.keys))
	for _, key := range use.keys {
		one := SessionConnector{Key: key, Name: key, Status: StatusUnavailable, Calls: use.calls[key], LastTool: use.last[key]}
		if i := s.index(key); i >= 0 {
			one.Name, one.Transport, one.Status, one.Enabled = s.records[i].Name, s.records[i].Transport, s.records[i].Status, s.records[i].Enabled
		}
		out = append(out, one)
	}
	return out, nil
}

// ServeProxy passes one request of a session to a connector's server and adds the connector's sign-in.
func (s *Service) ServeProxy(w http.ResponseWriter, r *http.Request, sessionID, key string) {
	rec, problem := s.admit(r, sessionID, key, TransportHTTP)
	if rec == nil {
		http.Error(w, problem, http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
	if err != nil || len(body) > maxProxyBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var call struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &call)
	fail := func(message string) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeRPCError(w, call.ID, message)
	}
	if problem != "" {
		fail(problem)
		return
	}
	target, err := url.Parse(rec.URL)
	if err != nil {
		fail(rec.Name + " has a bad address. Ask the user to open Settings, Connectors.")
		return
	}
	signed, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rec.URL, http.NoBody)
	if err == nil {
		err = s.authorize(signed, rec)
	}
	var refused *oauth2.RetrieveError
	if err != nil && (!errors.As(err, &refused) || refused.ErrorCode != "invalid_grant") {
		s.observe(rec.ID, StatusUnavailable, "reach", safeError(err, rec.URL))
		fail(rec.Name + " did not answer: its sign-in could not be renewed now. Try again.")
		return
	}
	if err != nil {
		s.observe(rec.ID, StatusNeedsSignIn, "sign-in", safeError(err, rec.URL))
		fail(signInAdvice(rec))
		return
	}
	s.count(sessionID, rec.Key, call.Method, call.Params.Name)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	base := s.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	(&httputil.ReverseProxy{
		Transport: roundTrip(func(out *http.Request) (*http.Response, error) {
			return s.send(base, rec, out, func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil })
		}),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL = target
			p.Out.Host = target.Host
			p.Out.Header.Del("Authorization")
			p.Out.Header.Del("Cookie")
			for name, values := range signed.Header {
				p.Out.Header[name] = values
			}
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			refused := resp.StatusCode == http.StatusUnauthorized ||
				(resp.StatusCode == http.StatusForbidden && resp.Header.Get("WWW-Authenticate") != "")
			switch {
			case refused:
				s.observe(rec.ID, StatusNeedsSignIn, "sign-in", "the server refused the sign-in")
				_ = resp.Body.Close()
				if r.Method != http.MethodPost {
					resp.StatusCode, resp.Status = http.StatusMethodNotAllowed, "405 Method Not Allowed"
					resp.Header, resp.Body, resp.ContentLength = http.Header{}, http.NoBody, 0
					return nil
				}
				answer := rpcError(call.ID, signInAdvice(rec))
				resp.StatusCode, resp.Status = http.StatusOK, "200 OK"
				resp.Header = http.Header{"Content-Type": {"application/json"}}
				resp.Body, resp.ContentLength = io.NopCloser(bytes.NewReader(answer)), int64(len(answer))
			case resp.StatusCode >= http.StatusInternalServerError:
				s.observe(rec.ID, StatusUnavailable, "reach", resp.Status)
			case resp.StatusCode < http.StatusMultipleChoices:
				s.answered(rec.ID, call.Method == "initialize" || call.Method == "tools/list")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				s.observe(rec.ID, StatusUnavailable, "reach", safeError(err, rec.URL))
			}
			fail(rec.Name + " did not answer: " + safeError(err, rec.URL))
		},
	}).ServeHTTP(w, r)
}

func (s *Service) admit(r *http.Request, sessionID, key string, transport Transport) (*record, string) {
	s.mu.Lock()
	lookup := s.sessionLookup
	want, tokenErr := s.sessionToken(sessionID)
	loadErr := s.load()
	var rec *record
	if i := s.index(key); loadErr == nil && i >= 0 && s.records[i].Key == key {
		found := s.records[i]
		rec = &found
	}
	s.mu.Unlock()
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenErr != nil || sessionID == "" || !hmac.Equal([]byte(want), []byte(given)) {
		return nil, "unknown session"
	}
	projectID := ""
	if lookup != nil {
		var open bool
		if projectID, open = lookup(r.Context(), sessionID); !open && !s.starting(sessionID) {
			return nil, "unknown session"
		}
	}
	switch {
	case rec == nil || rec.Transport != transport || (lookup != nil && rec.ProjectID != "" && rec.ProjectID != projectID):
		return &record{}, "This session has no connector named " + key + "."
	case !rec.Enabled:
		return rec, rec.Name + " is turned off. Ask the user to open Settings, Connectors, and turn it on."
	}
	return rec, ""
}

func (s *Service) offers(projectID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load() == nil && slices.ContainsFunc(s.records, func(rec record) bool {
		return rec.Enabled && (rec.ProjectID == "" || rec.ProjectID == projectID)
	})
}

func (s *Service) starting(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	use := s.sessions[sessionID]
	return use != nil && !use.issued.IsZero() && s.now().Sub(use.issued) < startGrace
}

func (s *Service) forgetClosed(ctx context.Context) {
	s.mu.Lock()
	lookup := s.sessionLookup
	ids := slices.Collect(maps.Keys(s.sessions))
	s.mu.Unlock()
	if lookup == nil || len(ids) < maxTrackedSessions {
		return
	}
	for _, id := range ids {
		if _, open := lookup(ctx, id); !open && !s.starting(id) {
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
		}
	}
}

func (s *Service) count(sessionID, key, method, tool string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if method != "tools/call" {
		return
	}
	use := s.sessions[sessionID]
	if use == nil {
		use = &sessionUse{calls: map[string]int{}, last: map[string]string{}}
		s.sessions[sessionID] = use
	}
	if !slices.Contains(use.keys, key) {
		use.keys = append(use.keys, key)
	}
	use.calls[key]++
	use.last[key] = tool
}

func (s *Service) answered(id string, listed bool) {
	s.mu.Lock()
	i := s.index(id)
	stale := i >= 0 && (s.records[i].Status == StatusUnavailable || (listed && s.records[i].Status != StatusConnected))
	s.mu.Unlock()
	if stale {
		_, _ = s.setStatus(id, StatusConnected, "", "")
	}
}

func (s *Service) observe(id string, status Status, step, detail string) {
	s.mu.Lock()
	i := s.index(id)
	same := i < 0 || s.records[i].Status == status
	s.mu.Unlock()
	if !same {
		_, _ = s.setStatus(id, status, step, detail)
	}
}

func (s *Service) sessionToken(sessionID string) (string, error) {
	if s.tokenKey == nil {
		path := filepath.Join(filepath.Dir(s.path), keyFileName)
		key, err := os.ReadFile(path) // #nosec G304 -- path is the daemon's own data file.
		if errors.Is(err, fs.ErrNotExist) || (err == nil && len(key) < sha256.Size) {
			key = make([]byte, sha256.Size)
			if _, err := rand.Read(key); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return "", err
			}
			err = os.WriteFile(path, key, 0o600)
		}
		if err != nil {
			return "", err
		}
		s.tokenKey = key
	}
	mac := hmac.New(sha256.New, s.tokenKey)
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func signInAdvice(rec *record) string {
	if rec.Auth != AuthOAuth {
		return rec.Name + " refused the saved sign-in value. Ask the user to open Settings, Connectors, and edit " + rec.Name + "."
	}
	return rec.Name + " needs sign-in. Ask the user to open Settings, Connectors, and press Sign in."
}

func rpcError(id json.RawMessage, message string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": rpcServerError, "message": message},
	})
	return body
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rpcError(id, message))
}
