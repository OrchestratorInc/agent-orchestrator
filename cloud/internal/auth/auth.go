package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken        = errors.New("invalid access token")
	ErrProviderUnavailable = errors.New("authentication provider unavailable")
)

var dummyPasswordHash, _ = bcrypt.GenerateFromPassword(
	[]byte("ao-cloud-invalid-password"),
	bcrypt.DefaultCost,
)

type WorkOSVerifier interface {
	Verify(ctx context.Context, token string) (domain.Principal, error)
}

// WorkOSProfile is a WorkOS user as AO needs it. LegacyID is the user's ID in
// the WorkOS environment the user was copied from, read from external_id.
type WorkOSProfile struct {
	Email       string
	DisplayName string
	LegacyID    string
}

// WorkOSOrganization is a WorkOS organization as AO needs it. LegacyID is the
// organization's ID in the WorkOS environment it was copied from.
type WorkOSOrganization struct {
	DisplayName  string
	Capabilities []string
	LegacyID     string
}

type ProfileResolver func(ctx context.Context, userID string) (WorkOSProfile, error)

type OrganizationResolver func(ctx context.Context, organizationID string) (WorkOSOrganization, error)

type OIDCVerifier struct {
	verifier      *oidc.IDTokenVerifier
	clientID      string
	profiles      ProfileResolver
	organizations OrganizationResolver
}

func NewOIDCVerifier(
	ctx context.Context,
	issuer string,
	clientID string,
	jwksURL string,
	profiles ProfileResolver,
	organizations OrganizationResolver,
) (*OIDCVerifier, error) {
	if strings.TrimSpace(issuer) == "" ||
		strings.TrimSpace(clientID) == "" ||
		strings.TrimSpace(jwksURL) == "" {
		return nil, errors.New("WorkOS issuer, client ID, and JWKS URL are required")
	}
	return &OIDCVerifier{
		verifier: oidc.NewVerifier(
			strings.TrimSpace(issuer),
			oidc.NewRemoteKeySet(ctx, jwksURL),
			&oidc.Config{SkipClientIDCheck: true},
		),
		clientID:      strings.TrimSpace(clientID),
		profiles:      profiles,
		organizations: organizations,
	}, nil
}

func (v *OIDCVerifier) Verify(ctx context.Context, token string) (domain.Principal, error) {
	idToken, err := v.verifier.Verify(ctx, token)
	if err != nil {
		return domain.Principal{}, ErrInvalidToken
	}
	var claims struct {
		Subject    string `json:"sub"`
		Email      string `json:"email"`
		Name       string `json:"name"`
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
		ClientID   string `json:"client_id"`
		OrgID      string `json:"org_id"`
		Role       string `json:"role"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return domain.Principal{}, ErrInvalidToken
	}
	if strings.TrimSpace(claims.Subject) == "" ||
		strings.TrimSpace(claims.ClientID) != v.clientID {
		return domain.Principal{}, ErrInvalidToken
	}
	displayName := strings.TrimSpace(claims.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(strings.Join(
			[]string{claims.GivenName, claims.FamilyName},
			" ",
		))
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	// AO keys accounts by the ID a user had when AO first saw them. A user
	// copied from an earlier WorkOS environment carries that ID as external_id,
	// so they keep their account, organizations, and repository grants.
	externalID := claims.Subject
	if v.profiles != nil {
		profile, err := v.profiles(ctx, claims.Subject)
		if err != nil {
			return domain.Principal{}, fmt.Errorf("%w: resolve WorkOS user: %v", ErrProviderUnavailable, err)
		}
		if profile.Email != "" {
			email = strings.ToLower(strings.TrimSpace(profile.Email))
		}
		if profile.DisplayName != "" {
			displayName = strings.TrimSpace(profile.DisplayName)
		}
		if profile.LegacyID != "" {
			externalID = profile.LegacyID
		}
	}
	if email == "" {
		return domain.Principal{}, ErrInvalidToken
	}
	if displayName == "" {
		displayName = email
	}
	orgID := strings.TrimSpace(claims.OrgID)
	var organization WorkOSOrganization
	if orgID != "" && v.organizations != nil {
		organization, err = v.organizations(ctx, orgID)
		if err != nil {
			return domain.Principal{}, fmt.Errorf("%w: resolve WorkOS organization: %v", ErrProviderUnavailable, err)
		}
	}
	if organization.LegacyID != "" {
		orgID = organization.LegacyID
	}
	return domain.Principal{
		Provider:        "workos",
		ExternalID:      externalID,
		Email:           email,
		DisplayName:     displayName,
		ExternalOrgID:   orgID,
		OrgName:         strings.TrimSpace(organization.DisplayName),
		OrgRole:         normalizeOrganizationRole(claims.Role),
		OrgCapabilities: organization.Capabilities,
	}, nil
}

// FallbackWorkOSVerifier accepts tokens from the current WorkOS environment
// and, while users move between environments, from the previous one. Copied
// users resolve to the same account through either environment, so desktop
// builds that still sign in with the previous environment keep working.
type FallbackWorkOSVerifier struct {
	current  WorkOSVerifier
	previous WorkOSVerifier
}

func NewFallbackWorkOSVerifier(current, previous WorkOSVerifier) (*FallbackWorkOSVerifier, error) {
	if current == nil || previous == nil {
		return nil, errors.New("current and previous WorkOS verifiers are required")
	}
	return &FallbackWorkOSVerifier{current: current, previous: previous}, nil
}

func (v *FallbackWorkOSVerifier) Verify(ctx context.Context, token string) (domain.Principal, error) {
	principal, err := v.current.Verify(ctx, token)
	if err == nil || !errors.Is(err, ErrInvalidToken) {
		return principal, err
	}
	principal, previousErr := v.previous.Verify(ctx, token)
	if errors.Is(previousErr, ErrInvalidToken) {
		return domain.Principal{}, err
	}
	return principal, previousErr
}

func normalizeOrganizationRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner":
		return "owner"
	case "admin":
		return "admin"
	default:
		return "member"
	}
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) bool {
	if hash == "" {
		hash = string(dummyPasswordHash)
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func NewOpaqueToken() (string, []byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", nil, err
	}
	token := "ao_local_" + base64.RawURLEncoding.EncodeToString(value)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
