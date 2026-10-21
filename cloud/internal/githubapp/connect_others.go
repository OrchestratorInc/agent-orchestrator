package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// After the App installation callback connects the installation the user
// chose, the same GitHub authorization usually reaches other installations
// too: GitHub organizations the user belongs to, including ones where GitHub's
// install page would stop a member who is not an owner. Connecting those is
// offered, never done silently:
//
//   - The installation callback only proves that someone holding the install
//     link finished GitHub's flow. A link forwarded to someone else would
//     otherwise attach that person's GitHub access to the sender's AO account.
//     So the callback page names the GitHub account and the AO account, and
//     nothing is kept until the person in the browser presses Connect.
//   - The page carries the authorization sealed with the state key, so only
//     that browser can confirm it, for connectOthersGrantTTL.
//   - It is offered only in an AO organization whose only member is the
//     initiating user; elsewhere it would share installations nobody chose
//     with teammates.
//   - It never replaces a different GitHub account already linked to the AO
//     user.
//   - The connects run in the background after the confirmation responds, so
//     the browser never waits on a sync of every installation.

const (
	connectOthersGrantTTL = 15 * time.Minute
	// connectOthersOfferTimeout bounds the GitHub and database reads the
	// callback page makes to decide whether to offer anything.
	connectOthersOfferTimeout = 10 * time.Second
	// connectOthersTimeout bounds the background connects after a
	// confirmation.
	connectOthersTimeout = 2 * time.Minute
	// connectOthersShownAccounts caps the account names listed on the page.
	connectOthersShownAccounts = 8
)

var connectOthersAssociatedData = []byte("ao/github/connect-other-installations/v1")

// ErrConnectOthersExpired is a confirmation whose sealed grant is too old.
var ErrConnectOthersExpired = errors.New("this GitHub connection link has expired")

// connectOthersGrant is the sealed payload a callback page posts back.
type connectOthersGrant struct {
	OrgID            string     `json:"o"`
	UserID           string     `json:"u"`
	GitHubUserID     int64      `json:"g"`
	GitHubLogin      string     `json:"l"`
	GitHubAvatarURL  string     `json:"a,omitempty"`
	Skip             int64      `json:"s"`
	AccessToken      string     `json:"t"`
	RefreshToken     string     `json:"r,omitempty"`
	ExpiresAt        *time.Time `json:"te,omitempty"`
	RefreshExpiresAt *time.Time `json:"re,omitempty"`
	IssuedAt         time.Time  `json:"i"`
}

// ConnectOthersOffer is what the installation callback page offers to connect
// next. Grant is opaque and only meaningful to ConfirmConnectOthers.
type ConnectOthersOffer struct {
	GitHubLogin string
	AOAccount   string
	Accounts    []string
	Grant       string
}

// connectOthersOffer decides, after a successful installation callback,
// whether to offer connecting the user's other installations. Any failure
// means no offer; the installation itself is already connected.
func (s *Service) connectOthersOffer(
	ctx context.Context,
	attempt domain.GitHubInstallAttempt,
	token UserAccessToken,
) *ConnectOthersOffer {
	if attempt.OrgID == "" || attempt.InitiatingUserID == "" || token.Token() == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, connectOthersOfferTimeout)
	defer cancel()
	log := s.logger.With("org_id", attempt.OrgID, "user_id", attempt.InitiatingUserID)
	email, onlyMember, err := s.store.GitHubConnectOthersTarget(ctx, attempt.OrgID, attempt.InitiatingUserID)
	if err != nil {
		log.Info("not offering other GitHub installations", "error", err)
		return nil
	}
	if !onlyMember {
		return nil
	}
	user, err := s.client.GetUser(ctx, token.Token())
	if err != nil {
		log.Info("not offering other GitHub installations: read GitHub user", "error", err)
		return nil
	}
	if existing, err := s.store.GitHubUserConnection(ctx, attempt.InitiatingUserID); err == nil &&
		existing.GitHubUserID != user.ID {
		log.Info("not offering other GitHub installations: a different GitHub account is linked",
			"github_user_id", user.ID)
		return nil
	} else if err != nil && !errors.Is(err, postgres.ErrNotFound) {
		log.Info("not offering other GitHub installations: read linked GitHub account", "error", err)
		return nil
	}
	visible, err := s.client.ListUserInstallations(ctx, token.Token())
	if err != nil {
		log.Info("not offering other GitHub installations: list installations", "error", err)
		return nil
	}
	accounts := make([]string, 0, len(visible))
	seen := make(map[string]bool, len(visible))
	for _, installation := range visible {
		login := strings.TrimSpace(installation.Account.Login)
		if installation.ID == attempt.PendingGitHubInstallationID || login == "" || seen[login] {
			continue
		}
		seen[login] = true
		accounts = append(accounts, login)
	}
	if len(accounts) == 0 {
		return nil
	}
	grant, err := s.sealConnectOthersGrant(connectOthersGrant{
		OrgID:            attempt.OrgID,
		UserID:           attempt.InitiatingUserID,
		GitHubUserID:     user.ID,
		GitHubLogin:      strings.TrimSpace(user.Login),
		GitHubAvatarURL:  strings.TrimSpace(user.AvatarURL),
		Skip:             attempt.PendingGitHubInstallationID,
		AccessToken:      token.Token(),
		RefreshToken:     token.RefreshToken(),
		ExpiresAt:        token.ExpiresAt,
		RefreshExpiresAt: token.RefreshExpiresAt,
		IssuedAt:         time.Now().UTC(),
	})
	if err != nil {
		log.Warn("seal GitHub connection offer", "error", err)
		return nil
	}
	return &ConnectOthersOffer{
		GitHubLogin: strings.TrimSpace(user.Login),
		AOAccount:   email,
		Accounts:    accounts,
		Grant:       grant,
	}
}

// ConfirmConnectOthers acts on a confirmation posted from the installation
// callback page: it keeps the user's GitHub authorization and connects the
// other installations it reaches in the background. It returns once that has
// started.
func (s *Service) ConfirmConnectOthers(ctx context.Context, sealed string) error {
	grant, err := s.openConnectOthersGrant(sealed)
	if err != nil {
		return err
	}
	if time.Since(grant.IssuedAt) > connectOthersGrantTTL {
		return ErrConnectOthersExpired
	}
	_, onlyMember, err := s.store.GitHubConnectOthersTarget(ctx, grant.OrgID, grant.UserID)
	if err != nil {
		return err
	}
	if !onlyMember {
		return postgres.ErrGitHubUserScopeNeedsPersonalOrg
	}
	token := grant.AccessToken
	existing, err := s.store.GitHubUserConnection(ctx, grant.UserID)
	switch {
	case err == nil && existing.GitHubUserID != grant.GitHubUserID:
		return postgres.ErrConflict
	case err == nil && existing.UpdatedAt.After(grant.IssuedAt):
		// Already linked and refreshed since this page was shown (a repeated
		// confirmation): keep the newer tokens rather than the page's.
		_, token, err = s.githubUserAccessToken(ctx, grant.UserID)
		if err != nil {
			return err
		}
	case err == nil || errors.Is(err, postgres.ErrNotFound):
		input, err := s.encryptGitHubUserConnection(grant.UserID, User{
			ID: grant.GitHubUserID, Login: grant.GitHubLogin, AvatarURL: grant.GitHubAvatarURL,
		}, UserAccessToken{
			value: grant.AccessToken, refreshValue: grant.RefreshToken,
			ExpiresAt: grant.ExpiresAt, RefreshExpiresAt: grant.RefreshExpiresAt,
		})
		if err != nil {
			return err
		}
		if _, err := s.store.SaveGitHubUserConnection(ctx, grant.UserID, input); err != nil {
			return err
		}
	default:
		return err
	}
	go s.connectOthersInBackground(context.WithoutCancel(ctx), grant, token)
	return nil
}

func (s *Service) connectOthersInBackground(ctx context.Context, grant connectOthersGrant, token string) {
	ctx, cancel := context.WithTimeout(ctx, connectOthersTimeout)
	defer cancel()
	log := s.logger.With("org_id", grant.OrgID, "user_id", grant.UserID)
	connected, err := s.connectVisibleInstallations(ctx, domain.Principal{UserID: grant.UserID},
		grant.OrgID, token, grant.Skip)
	if err != nil {
		log.Info("other GitHub installations not connected", "error", err)
		return
	}
	log.Info("connected other GitHub installations the user confirmed", "count", len(connected))
}

func (s *Service) sealConnectOthersGrant(grant connectOthersGrant) (string, error) {
	plaintext, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	ciphertext, nonce, err := Encrypt(s.stateKey, plaintext, connectOthersAssociatedData)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(nonce) + "." +
		base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func (s *Service) openConnectOthersGrant(sealed string) (connectOthersGrant, error) {
	encodedNonce, encodedCiphertext, ok := strings.Cut(strings.TrimSpace(sealed), ".")
	if !ok {
		return connectOthersGrant{}, postgres.ErrInvalid
	}
	nonce, err := base64.RawURLEncoding.DecodeString(encodedNonce)
	if err != nil {
		return connectOthersGrant{}, postgres.ErrInvalid
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(encodedCiphertext)
	if err != nil {
		return connectOthersGrant{}, postgres.ErrInvalid
	}
	plaintext, err := Decrypt(s.stateKey, ciphertext, nonce, connectOthersAssociatedData)
	if err != nil {
		return connectOthersGrant{}, postgres.ErrInvalid
	}
	defer clear(plaintext)
	var grant connectOthersGrant
	if err := json.Unmarshal(plaintext, &grant); err != nil ||
		grant.OrgID == "" || grant.UserID == "" || grant.GitHubUserID <= 0 || grant.AccessToken == "" {
		return connectOthersGrant{}, postgres.ErrInvalid
	}
	return grant, nil
}

// ConnectOthersURL is where the installation callback page posts a
// confirmation.
func (s *Service) ConnectOthersURL() string {
	return s.client.publicURL + "/api/cloud/v1/github/oauth/connect-others"
}

// InstallationOfferHTML renders the installation callback's success page with
// the offer to connect the user's other GitHub accounts.
func (s *Service) InstallationOfferHTML(offer ConnectOthersOffer) []byte {
	shown := offer.Accounts
	more := 0
	if len(shown) > connectOthersShownAccounts {
		more = len(shown) - connectOthersShownAccounts
		shown = shown[:connectOthersShownAccounts]
	}
	items := make([]string, 0, len(shown)+1)
	for _, account := range shown {
		items = append(items, "<li>"+html.EscapeString(account)+"</li>")
	}
	if more > 0 {
		items = append(items, fmt.Sprintf("<li>and %d more</li>", more))
	}
	return []byte(fmt.Sprintf(
		`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>GitHub connected</title>
<body style="font:15px -apple-system,system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#111">
<main><h1 style="font-size:1.25rem">GitHub connected</h1>
<p style="color:#555">Your repositories will appear in AO's project picker as soon as they finish syncing.</p>
<p>GitHub account <strong>%s</strong> can also reach:</p><ul>%s</ul>
<p>Connect them to the AO account <strong>%s</strong> too? AO will only show the repositories you can access on GitHub.</p>
<form method="post" action="%s"><input type="hidden" name="grant" value="%s">
<button type="submit" style="font:inherit;padding:.5rem 1rem">Connect</button></form>
<p style="color:#555">If this isn't your AO account, or you'd rather not, just close this tab.</p></main></body></html>`,
		html.EscapeString(offer.GitHubLogin),
		strings.Join(items, ""),
		html.EscapeString(offer.AOAccount),
		html.EscapeString(s.ConnectOthersURL()),
		html.EscapeString(offer.Grant),
	))
}

// ConnectOthersStartedHTML renders the page after a confirmation.
func (s *Service) ConnectOthersStartedHTML() []byte {
	return renderCallbackHTML("Connecting your other GitHub accounts",
		"They will appear in AO's project picker within a minute or two. You can close this tab.")
}

// ConnectOthersFailedHTML renders a confirmation that could not be acted on.
func (s *Service) ConnectOthersFailedHTML(err error) []byte {
	message := "AO could not connect your other GitHub accounts. Return to AO and connect them again."
	switch {
	case errors.Is(err, ErrConnectOthersExpired):
		message = "This page has expired. Return to AO and connect GitHub again."
	case errors.Is(err, postgres.ErrConflict):
		message = "A different GitHub account is already linked to this AO account, so nothing was changed."
	case errors.Is(err, postgres.ErrGitHubUserScopeNeedsPersonalOrg):
		message = "Other GitHub accounts can only be connected to an AO account where you are the only member."
	}
	return renderCallbackHTML("Not connected", message)
}
