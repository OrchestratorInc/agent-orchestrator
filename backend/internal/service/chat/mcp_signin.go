package chat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrMCPSignInUnsupported reports a driver whose provider cannot sign in to its own tool servers.
var ErrMCPSignInUnsupported = errors.New("chat driver cannot sign in to MCP servers")

// ErrMCPSignInAddress reports an address that is not the return page of the session's open sign-in.
var ErrMCPSignInAddress = errors.New("address is not the return page of this sign-in")

// ErrMCPSignInClosed reports a sign-in whose listener no longer takes the return page.
var ErrMCPSignInClosed = errors.New("the sign-in is no longer open")

var mcpSignInClient = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// MCPSignIn is the page of a provider's own sign-in and the address that page returns to.
type MCPSignIn struct {
	AuthorizationURL string
	CallbackURL      string
}

// SignInMCPServer starts the provider's sign-in for one of the session's own tool servers.
func (s *Service) SignInMCPServer(ctx context.Context, id domain.SessionID, name string) (MCPSignIn, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return MCPSignIn{}, err
	}
	defer release()
	return controller.SignInMCPServer(ctx, name)
}

// FinishMCPSignIn passes the page the browser opened after approval to the provider's listener.
func (s *Service) FinishMCPSignIn(ctx context.Context, id domain.SessionID, name, address string) error {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	return controller.FinishMCPSignIn(ctx, name, address)
}

// SignInMCPServer starts the provider's sign-in for one of its own tool servers.
func (c *Controller) SignInMCPServer(ctx context.Context, name string) (MCPSignIn, error) {
	signer, ok := c.conv.(ports.ChatMCPSigner)
	if !ok {
		return MCPSignIn{}, ErrMCPSignInUnsupported
	}
	page, err := signer.SignInMCPServer(ctx, name)
	if err != nil {
		return MCPSignIn{}, err
	}
	back := mcpSignInReturn(page)
	c.mu.Lock()
	if c.mcpSignIns == nil {
		c.mcpSignIns = map[string]string{}
	}
	c.mcpSignIns[name] = back
	c.mu.Unlock()
	return MCPSignIn{AuthorizationURL: page, CallbackURL: back}, nil
}

// FinishMCPSignIn requests the return page of an open sign-in from this machine, where the provider listens for it.
func (c *Controller) FinishMCPSignIn(ctx context.Context, name, address string) error {
	c.mu.Lock()
	back := c.mcpSignIns[name]
	c.mu.Unlock()
	got, err := url.Parse(strings.TrimSpace(address))
	if back == "" || err != nil || got.RawQuery == "" || got.Scheme+"://"+got.Host+got.Path != back {
		return ErrMCPSignInAddress
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, back+"?"+got.RawQuery, http.NoBody)
	if err != nil {
		return ErrMCPSignInAddress
	}
	resp, err := mcpSignInClient.Do(req)
	if err != nil {
		return ErrMCPSignInClosed
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%w: the listener answered %d", ErrMCPSignInClosed, resp.StatusCode)
	}
	return nil
}

func mcpSignInReturn(page string) string {
	parsed, err := url.Parse(page)
	if err != nil {
		return ""
	}
	back, err := url.Parse(parsed.Query().Get("redirect_uri"))
	if err != nil || back.Scheme != "http" {
		return ""
	}
	if ip := net.ParseIP(back.Hostname()); (ip == nil || !ip.IsLoopback()) && back.Hostname() != "localhost" {
		return ""
	}
	return back.Scheme + "://" + back.Host + back.Path
}
