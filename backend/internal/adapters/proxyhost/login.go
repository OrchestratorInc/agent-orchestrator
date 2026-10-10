package proxyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Relays exist only for the current OAuth attempt. They never bind LAN addresses
// or put callback codes into the renderer or logs.
var relayOwners sync.Map

// StartAccountLogin reserves the local callback and requests an upstream login link.
func (c *Client) StartAccountLogin(ctx context.Context, provider, id string) (ports.ProviderLogin, error) {
	var port int
	var path string
	switch provider {
	case "codex":
		port = 1455
		path = "/auth/callback"
	case "claude":
		port = 54545
		path = "/callback"
	default:
		return ports.ProviderLogin{}, errors.New("unsupported account provider")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return ports.ProviderLogin{}, ports.ErrProviderLoginCallbackBusy
	}
	var login ports.ProviderLogin
	if err = c.management(ctx, http.MethodGet, "/v8/management/oauth/auth-url?provider="+provider, nil, &login, id); err != nil {
		_ = listener.Close()
		return login, err
	}
	login.ID = id
	login.Provider = provider
	login.Mode = "browser"
	login.Status = "waiting"
	if login.State == "" || login.URL == "" {
		_ = listener.Close()
		return login, errors.New("provider did not return a login link")
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != path || r.URL.Query().Get("state") != login.State {
			http.Error(w, "Unknown login attempt", http.StatusBadRequest)
			return
		}
		callbackCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		body := map[string]string{"provider": provider, "state": login.State, "code": r.URL.Query().Get("code"), "error": r.URL.Query().Get("error")}
		if err := c.management(callbackCtx, http.MethodPost, "/v8/management/oauth/callback", body, nil, ""); err != nil {
			http.Error(w, "Login could not be completed. Return to AO and retry.", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Login received. You can return to AO."))
	})}
	relayOwners.Store(c.root+"/"+id, server)
	go func() { _ = server.Serve(listener) }()
	time.AfterFunc(6*time.Minute, func() { closeRelay(c.root, id) })
	return login, nil
}
func closeRelay(root, id string) {
	if server, ok := relayOwners.LoadAndDelete(root + "/" + id); ok {
		if callback, valid := server.(*http.Server); valid {
			_ = callback.Close()
		}
	}
}

// AccountLoginStatus polls upstream completion and releases terminal callback listeners.
func (c *Client) AccountLoginStatus(ctx context.Context, login ports.ProviderLogin) (string, error) {
	if login.Mode == "import" || login.Mode == "api_key" {
		if _, err := c.VerifiedAccountLogin(ctx, login.ID); err == nil {
			return "complete", nil
		} else {
			var status statusError
			if !errors.As(err, &status) || status.status != http.StatusNotFound {
				return "", err
			}
		}
		return "waiting", nil
	}
	if login.Mode != "" && login.Mode != "browser" {
		var result ports.ProviderLogin
		if err := c.call(ctx, http.MethodGet, "/ao/login/status?id="+url.QueryEscape(login.ID), nil, &result, nil); err != nil {
			return "", err
		}
		return result.Status, nil
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := c.management(ctx, http.MethodGet, "/v8/management/oauth/status?state="+url.QueryEscape(login.State), nil, &result, ""); err != nil {
		return "", err
	}
	switch result.Status {
	case "wait":
		return "waiting", nil
	case "ok":
		closeRelay(c.root, login.ID)
		return "complete", nil
	case "error":
		closeRelay(c.root, login.ID)
		return "failed", nil
	}
	return "", errors.New("unknown login status")
}

// CancelAccountLogin cancels upstream login and closes its callback listener.
func (c *Client) CancelAccountLogin(ctx context.Context, login ports.ProviderLogin) error {
	if login.Mode == "import" {
		return c.DeleteCredential(ctx, "ao-"+login.ID+".json")
	}
	if login.Mode == "api_key" {
		return nil
	}
	if login.Mode != "" && login.Mode != "browser" {
		return c.call(ctx, http.MethodDelete, "/ao/login/status?id="+url.QueryEscape(login.ID), nil, nil, nil)
	}
	err := c.management(ctx, http.MethodDelete, "/v8/management/oauth/session?state="+url.QueryEscape(login.State), nil, nil, "")
	closeRelay(c.root, login.ID)
	return err
}

// VerifiedAccountLogin reads the verified identity for the exact login attempt.
func (c *Client) VerifiedAccountLogin(ctx context.Context, id string) (ports.VerifiedProviderLogin, error) {
	var result ports.VerifiedProviderLogin
	err := c.call(ctx, http.MethodGet, "/ao/login-result/"+url.QueryEscape(id), nil, &result, nil)
	// A helper that does not name the kind still names the credential: an API
	// key lives in its configuration, a sign-in in a file.
	if err == nil && result.Kind == "" && strings.HasPrefix(result.CredentialRef, "config-index:") {
		result.Kind = "api_key"
	}
	return result, err
}

// StartAccountLoginMode uses CLIProxy's native credential management APIs.
func (c *Client) StartAccountLoginMode(ctx context.Context, provider, id, mode string, input ports.ProviderLoginInput) (ports.ProviderLogin, error) {
	if provider != "codex" && provider != "claude" {
		return ports.ProviderLogin{}, ports.ErrProviderAccountIncompatible
	}
	switch mode {
	case "device":
		if provider != "codex" {
			return ports.ProviderLogin{}, ports.ErrProviderAccountIncompatible
		}
		if err := c.Ensure(ctx); err != nil {
			return ports.ProviderLogin{}, err
		}
		var login ports.ProviderLogin
		err := c.call(ctx, http.MethodPost, "/ao/login/device/start", map[string]string{"id": id}, &login, nil)
		return login, err
	case "import":
		if len(input.CredentialJSON) > 1<<20 {
			return ports.ProviderLogin{}, errors.New("credential JSON exceeds 1 MiB")
		}
		var value map[string]any
		if json.Unmarshal([]byte(input.CredentialJSON), &value) != nil || value == nil {
			return ports.ProviderLogin{}, errors.New("credential JSON is invalid")
		}
		if strings.TrimSpace(fmt.Sprint(value["type"])) != provider {
			return ports.ProviderLogin{}, ports.ErrProviderAccountIncompatible
		}
		name := "ao-" + id + ".json"
		if err := c.management(ctx, http.MethodPost, "/v8/management/credentials?name="+url.QueryEscape(name), json.RawMessage(input.CredentialJSON), nil, id); err != nil {
			return ports.ProviderLogin{}, err
		}
	case "api_key":
		if strings.TrimSpace(input.APIKey) == "" {
			return ports.ProviderLogin{}, errors.New("API key is required")
		}
		base := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ports.ProviderLogin{}, errors.New("a valid HTTP(S) base URL is required")
		}
		field := provider + "-api-key"
		entries, err := c.apiKeys(ctx, field)
		if err != nil {
			return ports.ProviderLogin{}, err
		}
		for _, entry := range entries {
			if fmt.Sprint(entry["api-key"]) == input.APIKey && strings.TrimRight(fmt.Sprint(entry["base-url"]), "/") == base {
				return ports.ProviderLogin{}, errors.New("API key already exists")
			}
		}
		entries = append(entries, map[string]any{"api-key": input.APIKey, "base-url": base})
		if err := c.management(ctx, http.MethodPut, "/v0/management/"+field, entries, nil, id); err != nil {
			return ports.ProviderLogin{}, err
		}
		if err := c.call(ctx, http.MethodPost, "/ao/tag-api-key", map[string]string{"id": id, "provider": provider, "api_key": input.APIKey, "base_url": base, "label": input.Label}, nil, nil); err != nil {
			_ = c.management(ctx, http.MethodDelete, "/v0/management/"+field+"?api-key="+url.QueryEscape(input.APIKey)+"&base-url="+url.QueryEscape(base), nil, nil, "")
			return ports.ProviderLogin{}, err
		}
	default:
		return ports.ProviderLogin{}, ports.ErrProviderAccountIncompatible
	}
	return ports.ProviderLogin{ID: id, Provider: provider, Mode: mode, Status: "waiting", ExpiresIn: 60}, nil
}
