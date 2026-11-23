package connectors

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	callbackPath     = "/oauth/mcp/callback"
	clientName       = "Orchestrator"
	signInLifetime   = 10 * time.Minute
	signInFinishWait = 45 * time.Second
	maxAddressBytes  = 16 << 10
	initializeBody   = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"orchestrator","version":"1"}}}`
)

type signIn struct {
	connectorID string
	result      chan *auth.AuthorizationResult
	done        chan struct{}
	err         error
	cancel      context.CancelFunc
	cancelled   bool
}

// CallbackURL is the address a sign-in server sends the browser back to.
func (s *Service) CallbackURL() string { return s.base() + callbackPath }

func errNotConnected(checked Connector) error {
	return apierr.Unavailable("CONNECTOR_UNAVAILABLE", checked.Name+" did not connect: "+checked.StatusError)
}

func asksForSignIn(status int, header http.Header) bool {
	return status == http.StatusUnauthorized ||
		(status == http.StatusForbidden && strings.Contains(header.Get("WWW-Authenticate"), "insufficient_scope"))
}

// StartSignIn starts a sign-in and returns the page the user must open. It returns "" when the server asks for no sign-in.
func (s *Service) StartSignIn(ctx context.Context, ref string) (string, error) {
	rec, err := s.snapshot(ref)
	if err != nil {
		return "", err
	}
	if rec.Transport != TransportHTTP || rec.Auth != AuthOAuth {
		return "", invalid("CONNECTOR_SIGNIN_NOT_SUPPORTED", "Only a server URL with automatic sign-in can sign in")
	}
	s.cancelSignIns(rec.ID)
	if rec.OAuth != nil {
		checked, err := s.Test(ctx, rec.ID)
		if err != nil {
			return "", err
		}
		if checked.Status == StatusConnected {
			return "", nil
		}
		if checked.Status != StatusNeedsSignIn {
			return "", errNotConnected(checked)
		}
		if _, err := s.SignOut(ctx, rec.ID); err != nil {
			return "", err
		}
	}
	req, status, header, err := s.challenge(ctx, &rec)
	if err != nil {
		_, _ = s.setStatus(rec.ID, StatusUnavailable, "reach", safeError(err, rec.URL))
		return "", apierr.Unavailable("CONNECTOR_UNREACHABLE", "The server did not answer: "+safeError(err, rec.URL))
	}
	if status == http.StatusForbidden && !asksForSignIn(status, header) {
		_, _ = s.setStatus(rec.ID, StatusUnavailable, "initialize", "the server refused the request and asked for no sign-in")
		return "", apierr.Unavailable("CONNECTOR_REFUSED", "The server refused the request and asked for no sign-in")
	}
	if !asksForSignIn(status, header) {
		checked, err := s.Test(ctx, rec.ID)
		if err == nil && checked.Status != StatusConnected {
			err = errNotConnected(checked)
		}
		return "", err
	}
	if _, err := s.setStatus(rec.ID, StatusNeedsSignIn, "", ""); err != nil {
		return "", err
	}
	refusal := &http.Response{StatusCode: status, Header: header, Body: http.NoBody, Request: req}

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signInLifetime)
	pending := &signIn{connectorID: rec.ID, result: make(chan *auth.AuthorizationResult, 1), done: make(chan struct{}), cancel: cancel}
	pageReady := make(chan string, 1)
	handler, err := auth.NewAuthorizationCodeHandler(s.handlerConfig(&rec, pending, pageReady))
	if err != nil {
		cancel()
		return "", apierr.InternalCause("CONNECTOR_SIGNIN_FAILED", "The sign-in could not start", err)
	}
	go func() {
		defer cancel()
		s.endSignIn(pending, handler.Authorize(runCtx, req, refusal))
	}()
	select {
	case page := <-pageReady:
		return page, nil
	case <-pending.done:
		if pending.err != nil {
			return "", signInFailed(pending.err)
		}
		return "", nil
	case <-ctx.Done():
		cancel()
		return "", ctx.Err()
	}
}

// CompleteSignIn gives a waiting sign-in the code its server sent back.
func (s *Service) CompleteSignIn(ctx context.Context, state, code, iss, refusal string) (Connector, error) {
	s.mu.Lock()
	pending := s.signIns[state]
	s.mu.Unlock()
	if pending == nil || state == "" {
		return Connector{}, apierr.NotFound("CONNECTOR_SIGNIN_NOT_FOUND", "This sign-in is not open on this machine")
	}
	if refusal != "" || code == "" {
		pending.cancel()
		<-pending.done
		return Connector{}, invalid("CONNECTOR_SIGNIN_REFUSED", "The server refused the sign-in: "+refusal)
	}
	select {
	case pending.result <- &auth.AuthorizationResult{Code: code, State: state, Iss: iss}:
	default:
	}
	select {
	case <-pending.done:
	case <-time.After(signInFinishWait):
		return Connector{}, apierr.Unavailable("CONNECTOR_SIGNIN_SLOW", "The server did not complete the sign-in in time")
	case <-ctx.Done():
		return Connector{}, ctx.Err()
	}
	if pending.err != nil {
		return Connector{}, signInFailed(pending.err)
	}
	return s.Get(ctx, pending.connectorID)
}

// FinishFromAddress completes a sign-in from the address the user's browser opened on another device.
func (s *Service) FinishFromAddress(ctx context.Context, ref, address string) (Connector, error) {
	rec, err := s.snapshot(ref)
	if err != nil {
		return Connector{}, err
	}
	wrong := invalid("CONNECTOR_SIGNIN_ADDRESS", "That address is not from this sign-in. Paste the address your browser opened after you approved.")
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	pasted, err := url.Parse(address)
	want, wantErr := url.Parse(s.CallbackURL())
	if err != nil || wantErr != nil || len(address) > maxAddressBytes {
		return Connector{}, wrong
	}
	query := pasted.Query()
	if pasted.Host != want.Host || pasted.Path != want.Path || pasted.User != nil || pasted.Fragment != "" ||
		len(query["state"]) != 1 || len(query["code"])+len(query["error"]) != 1 {
		return Connector{}, wrong
	}
	state := query.Get("state")
	s.mu.Lock()
	pending := s.signIns[state]
	s.mu.Unlock()
	if pending == nil || pending.connectorID != rec.ID {
		return Connector{}, wrong
	}
	return s.CompleteSignIn(ctx, state, query.Get("code"), query.Get("iss"), query.Get("error"))
}

// SignOut forgets the tokens of a connector.
func (s *Service) SignOut(_ context.Context, ref string) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Connector{}, err
	}
	i := s.index(ref)
	if i < 0 {
		return Connector{}, errNotFound()
	}
	next := slices.Clone(s.records)
	next[i].OAuth, next[i].Status, next[i].StatusStep, next[i].StatusError = nil, StatusNeedsSignIn, "", ""
	if err := s.save(next); err != nil {
		return Connector{}, err
	}
	delete(s.sources, next[i].ID)
	return next[i].public(), nil
}

func (s *Service) handlerConfig(rec *record, pending *signIn, pageReady chan<- string) *auth.AuthorizationCodeHandlerConfig {
	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:           s.CallbackURL(),
		RequestRefreshToken:   true,
		AcceptUnadvertisedIss: true,
		Client:                s.http,
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			page, err := url.Parse(args.URL)
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			s.signIns[page.Query().Get("state")] = pending
			s.mu.Unlock()
			pageReady <- args.URL
			select {
			case result := <-pending.result:
				return result, nil
			case <-ctx.Done():
				return nil, errors.New("the sign-in was not completed")
			}
		},
		NewTokenSource: func(_ context.Context, cfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			if err := s.saveToken(rec.ID, rec.URL, cfg, token); err != nil {
				return nil, err
			}
			return s.tokenSource(rec.ID), nil
		},
	}
	if rec.OAuthClientID != "" {
		cfg.PreregisteredClient = &oauthex.ClientCredentials{ClientID: rec.OAuthClientID}
		if rec.OAuthClientSecret != "" {
			cfg.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: rec.OAuthClientSecret}
		}
		return cfg
	}
	cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
		ClientName:              clientName,
		RedirectURIs:            []string{s.CallbackURL()},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	}}
	return cfg
}

func (s *Service) challenge(ctx context.Context, rec *record) (*http.Request, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rec.URL, strings.NewReader(initializeBody))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range rec.Headers {
		req.Header.Set(name, value)
	}
	resp, err := s.http.Do(req) // #nosec G107 -- the user chose this server URL.
	if err != nil {
		return nil, 0, nil, err
	}
	_ = resp.Body.Close()
	return req, resp.StatusCode, resp.Header, nil
}

func (s *Service) endSignIn(pending *signIn, err error) {
	s.mu.Lock()
	for state, open := range s.signIns {
		if open == pending {
			delete(s.signIns, state)
		}
	}
	cancelled := pending.cancelled
	s.mu.Unlock()
	if err != nil {
		pending.err = err
		if !cancelled {
			_, _ = s.setStatus(pending.connectorID, StatusNeedsSignIn, "sign-in", safeError(err, ""))
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		_, _ = s.Test(ctx, pending.connectorID)
		cancel()
	}
	close(pending.done)
}

func (s *Service) cancelSignIns(connectorID string) {
	s.mu.Lock()
	var open []*signIn
	for _, pending := range s.signIns {
		if pending.connectorID == connectorID && !slices.Contains(open, pending) {
			pending.cancelled = true
			open = append(open, pending)
		}
	}
	s.mu.Unlock()
	for _, pending := range open {
		pending.cancel()
		<-pending.done
	}
}

func (s *Service) saveToken(id, address string, cfg *oauth2.Config, token *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return errNotFound()
	}
	if s.records[i].URL != address {
		return invalid("CONNECTOR_SIGNIN_STALE", "The connector's address changed during the sign-in")
	}
	next := slices.Clone(s.records)
	next[i].OAuth = &oauthToken{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		TokenURL: cfg.Endpoint.TokenURL, AuthStyle: int(cfg.Endpoint.AuthStyle),
		AccessToken: token.AccessToken, TokenType: token.TokenType, RefreshToken: token.RefreshToken, Expiry: token.Expiry,
	}
	delete(s.sources, id)
	return s.save(next)
}

func (s *Service) tokenSource(id string) oauth2.TokenSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source, ok := s.sources[id]; ok {
		return source
	}
	if err := s.load(); err != nil {
		return nil
	}
	i := s.index(id)
	if i < 0 || s.records[i].OAuth == nil {
		return nil
	}
	saved := s.records[i].OAuth
	cfg := &oauth2.Config{
		ClientID: saved.ClientID, ClientSecret: saved.ClientSecret,
		Endpoint: oauth2.Endpoint{TokenURL: saved.TokenURL, AuthStyle: oauth2.AuthStyle(saved.AuthStyle)},
	}
	base := cfg.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, s.http),
		&oauth2.Token{AccessToken: saved.AccessToken, TokenType: saved.TokenType, RefreshToken: saved.RefreshToken, Expiry: saved.Expiry})
	source := &savingSource{service: s, id: id, base: base, last: saved.AccessToken}
	s.sources[id] = source
	return source
}

type savingSource struct {
	service *Service
	id      string
	base    oauth2.TokenSource
	last    string
}

func (t *savingSource) Token() (*oauth2.Token, error) {
	token, err := t.base.Token()
	if err != nil {
		var refused *oauth2.RetrieveError
		t.service.mu.Lock()
		current := t.service.sources[t.id] == oauth2.TokenSource(t)
		t.service.mu.Unlock()
		if current && errors.As(err, &refused) && refused.ErrorCode == "invalid_grant" {
			_, _ = t.service.SignOut(context.Background(), t.id)
		}
		return nil, err
	}
	t.service.mu.Lock()
	defer t.service.mu.Unlock()
	if token.AccessToken == t.last {
		return token, nil
	}
	i := t.service.index(t.id)
	if i < 0 || t.service.records[i].OAuth == nil || t.service.sources[t.id] != oauth2.TokenSource(t) {
		return token, nil
	}
	next := slices.Clone(t.service.records)
	renewed := *next[i].OAuth
	renewed.AccessToken, renewed.TokenType, renewed.Expiry = token.AccessToken, token.TokenType, token.Expiry
	if token.RefreshToken != "" {
		renewed.RefreshToken = token.RefreshToken
	}
	next[i].OAuth = &renewed
	if err := t.service.save(next); err != nil {
		return nil, err
	}
	t.last = token.AccessToken
	return token, nil
}

func (s *Service) authorize(req *http.Request, rec *record) error {
	for name, value := range rec.Headers {
		req.Header.Set(name, value)
	}
	if rec.Auth != AuthOAuth {
		return nil
	}
	source := s.tokenSource(rec.ID)
	if source == nil {
		return nil
	}
	token, err := source.Token()
	if err != nil {
		return err
	}
	token.SetAuthHeader(req)
	return nil
}

func signInFailed(err error) error {
	return invalid("CONNECTOR_SIGNIN_FAILED", "The sign-in did not complete: "+safeError(err, ""))
}
