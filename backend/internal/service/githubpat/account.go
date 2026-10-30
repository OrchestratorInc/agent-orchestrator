package githubpat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Owner is a GitHub account or organization a repository can be created under.
type Owner struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

// Account calls the GitHub API with whichever credential AO uses for GitHub
// (environment token, AO's own sign-in, or the gh CLI), so creating a
// repository needs no gh CLI.
type Account struct {
	token      func(context.Context) (string, error)
	httpClient *http.Client
	apiURL     string
}

// NewAccount returns an Account that authenticates with token.
func NewAccount(token func(context.Context) (string, error)) *Account {
	return &Account{token: token, httpClient: http.DefaultClient, apiURL: "https://api.github.com"}
}

// Token returns the GitHub credential the Account uses.
func (a *Account) Token(ctx context.Context) (string, error) {
	return a.token(ctx)
}

// Owners lists the signed-in user first, then the organizations they belong
// to. An organization lookup failure still returns the user.
func (a *Account) Owners(ctx context.Context) ([]Owner, error) {
	var user struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if _, err := a.call(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return nil, err
	}
	owners := []Owner{{Login: user.Login, AvatarURL: user.AvatarURL}}
	var memberships []struct {
		Organization struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
		} `json:"organization"`
	}
	if status, err := a.call(ctx, http.MethodGet, "/user/memberships/orgs?state=active&per_page=100", nil, &memberships); err == nil && status == http.StatusOK {
		for _, m := range memberships {
			owners = append(owners, Owner{Login: m.Organization.Login, AvatarURL: m.Organization.AvatarURL})
		}
	}
	return owners, nil
}

// RepositoryAvailable reports whether owner/name is free to create.
func (a *Account) RepositoryAvailable(ctx context.Context, owner, name string) (bool, error) {
	status, err := a.call(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil, nil)
	if status == http.StatusNotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// CreateRepository creates an empty repository under the signed-in user, or
// under an organization when owner is not the user.
func (a *Account) CreateRepository(ctx context.Context, owner, name string, private bool) error {
	var user struct {
		Login string `json:"login"`
	}
	if _, err := a.call(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return err
	}
	path := "/user/repos"
	if !strings.EqualFold(owner, user.Login) {
		path = "/orgs/" + url.PathEscape(owner) + "/repos"
	}
	_, err := a.call(ctx, http.MethodPost, path, map[string]any{"name": name, "private": private}, nil)
	return err
}

// call sends one GitHub API request. A non-2xx status is returned with an
// error carrying GitHub's message.
func (a *Account) call(ctx context.Context, method, path string, body, out any) (int, error) {
	token, err := a.token(ctx)
	if err != nil {
		return 0, fmt.Errorf("github: no credential: %w", err)
	}
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.apiURL+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&failure)
		return resp.StatusCode, fmt.Errorf("github %s %s: %d %s", method, path, resp.StatusCode, failure.Message)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("github %s %s: decode: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}
