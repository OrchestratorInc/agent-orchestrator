package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/go-jose/go-jose/v4"
)

const testIssuer = "https://auth.example.test"

type testTokenIssuer struct {
	key     *rsa.PrivateKey
	jwksURL string
}

func newTestTokenIssuer(t *testing.T) *testTokenIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &key.PublicKey, KeyID: "test", Algorithm: string(jose.RS256), Use: "sig",
		}}})
	}))
	t.Cleanup(server.Close)
	return &testTokenIssuer{key: key, jwksURL: server.URL}
}

func (i *testTokenIssuer) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: i.key},
		(&jose.SignerOptions{}).WithHeader("kid", "test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"iss":       testIssuer,
		"exp":       time.Now().Add(time.Hour).Unix(),
		"iat":       time.Now().Unix(),
		"client_id": "client_current",
	}
	for key, value := range claims {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestOIDCVerifierKeepsTheAccountOfACopiedUser(t *testing.T) {
	issuer := newTestTokenIssuer(t)
	profiles := func(_ context.Context, userID string) (WorkOSProfile, error) {
		if userID != "user_production" {
			t.Fatalf("profile lookup for %q, want the token subject", userID)
		}
		return WorkOSProfile{Email: "ada@example.com", DisplayName: "Ada", LegacyID: "user_staging"}, nil
	}
	organizations := func(_ context.Context, organizationID string) (WorkOSOrganization, error) {
		if organizationID != "org_production" {
			t.Fatalf("organization lookup for %q, want the token org", organizationID)
		}
		return WorkOSOrganization{DisplayName: "Ada's team", Capabilities: []string{"coder"}, LegacyID: "org_staging"}, nil
	}
	verifier, err := NewOIDCVerifier(context.Background(), testIssuer, "client_current", issuer.jwksURL, profiles, organizations)
	if err != nil {
		t.Fatal(err)
	}

	principal, err := verifier.Verify(context.Background(), issuer.token(t, map[string]any{
		"sub":    "user_production",
		"org_id": "org_production",
		"role":   "admin",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if principal.ExternalID != "user_staging" || principal.ExternalOrgID != "org_staging" {
		t.Fatalf("principal IDs = %q / %q, want the original staging IDs", principal.ExternalID, principal.ExternalOrgID)
	}
	if principal.Email != "ada@example.com" || principal.OrgName != "Ada's team" || principal.OrgRole != "admin" {
		t.Fatalf("unexpected principal %+v", principal)
	}
}

func TestOIDCVerifierUsesTheSubjectForAUserThatWasNotCopied(t *testing.T) {
	issuer := newTestTokenIssuer(t)
	profiles := func(context.Context, string) (WorkOSProfile, error) {
		return WorkOSProfile{Email: "new@example.com"}, nil
	}
	verifier, err := NewOIDCVerifier(context.Background(), testIssuer, "client_current", issuer.jwksURL, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), issuer.token(t, map[string]any{"sub": "user_new"}))
	if err != nil {
		t.Fatal(err)
	}
	if principal.ExternalID != "user_new" || principal.ExternalOrgID != "" {
		t.Fatalf("principal IDs = %q / %q, want the token subject and no org", principal.ExternalID, principal.ExternalOrgID)
	}
}

type stubVerifier struct {
	principal domain.Principal
	err       error
	calls     int
}

func (v *stubVerifier) Verify(context.Context, string) (domain.Principal, error) {
	v.calls++
	return v.principal, v.err
}

func TestFallbackWorkOSVerifier(t *testing.T) {
	current := domain.Principal{ExternalID: "user_current"}
	previous := domain.Principal{ExternalID: "user_previous"}
	unavailable := errors.New("WorkOS down")
	cases := []struct {
		name         string
		currentErr   error
		previousErr  error
		want         string
		wantErr      error
		previousUsed bool
	}{
		{name: "current token", want: "user_current"},
		{name: "previous token", currentErr: ErrInvalidToken, want: "user_previous", previousUsed: true},
		{name: "neither", currentErr: ErrInvalidToken, previousErr: ErrInvalidToken, wantErr: ErrInvalidToken, previousUsed: true},
		{name: "previous provider down", currentErr: ErrInvalidToken, previousErr: unavailable, wantErr: unavailable, previousUsed: true},
		{name: "current provider down", currentErr: ErrProviderUnavailable, wantErr: ErrProviderUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			currentVerifier := &stubVerifier{principal: current, err: tc.currentErr}
			previousVerifier := &stubVerifier{principal: previous, err: tc.previousErr}
			verifier, err := NewFallbackWorkOSVerifier(currentVerifier, previousVerifier)
			if err != nil {
				t.Fatal(err)
			}
			principal, err := verifier.Verify(context.Background(), "token")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && principal.ExternalID != tc.want {
				t.Fatalf("principal = %q, want %q", principal.ExternalID, tc.want)
			}
			if (previousVerifier.calls > 0) != tc.previousUsed {
				t.Fatalf("previous verifier calls = %d, want used=%v", previousVerifier.calls, tc.previousUsed)
			}
		})
	}
}

func TestWorkOSResolversReadTheCopiedFromID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		records := map[string]map[string]any{
			"/user_management/users/user_copied":     {"id": "user_copied", "email": "a@example.com", "external_id": "user_old"},
			"/user_management/users/user_custom":     {"id": "user_custom", "email": "b@example.com", "external_id": "crm-42"},
			"/user_management/users/user_self":       {"id": "user_self", "email": "c@example.com", "external_id": "user_self"},
			"/organizations/org_copied":              {"id": "org_copied", "name": "Team", "external_id": "org_old"},
			"/organizations/org_plain":               {"id": "org_plain", "name": "Team"},
			"/user_management/users/user_wrong_kind": {"id": "user_wrong_kind", "email": "d@example.com", "external_id": "org_old"},
		}
		record, ok := records[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(record)
	}))
	defer server.Close()
	profiles, err := newWorkOSProfileResolver("sk_test", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for userID, want := range map[string]string{
		"user_copied":     "user_old",
		"user_custom":     "",
		"user_self":       "",
		"user_wrong_kind": "",
	} {
		profile, err := profiles(context.Background(), userID)
		if err != nil {
			t.Fatal(err)
		}
		if profile.LegacyID != want {
			t.Fatalf("%s LegacyID = %q, want %q", userID, profile.LegacyID, want)
		}
	}
	organizations, err := newWorkOSOrganizationResolver("sk_test", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for organizationID, want := range map[string]string{"org_copied": "org_old", "org_plain": ""} {
		organization, err := organizations(context.Background(), organizationID)
		if err != nil {
			t.Fatal(err)
		}
		if organization.LegacyID != want {
			t.Fatalf("%s LegacyID = %q, want %q", organizationID, organization.LegacyID, want)
		}
	}
}
