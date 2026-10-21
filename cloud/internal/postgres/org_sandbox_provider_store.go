package postgres

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// settingsDefaultSandboxProvider is the ao_organizations.settings key holding
// the sandbox provider the organization chose in the desktop's settings.
const settingsDefaultSandboxProvider = "defaultSandboxProvider"

var sandboxProviderName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// OrgDefaultSandboxProvider returns the sandbox provider the organization chose
// for sessions nothing else places, or "" when it has not chosen one. Callers
// have already authorized access to the organization; whether it may still use
// the provider is theirs to check.
func (s *Store) OrgDefaultSandboxProvider(ctx context.Context, orgID string) (string, error) {
	var provider *string
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT settings->>'`+settingsDefaultSandboxProvider+`' FROM ao_organizations WHERE id = $1`, orgID,
		).Scan(&provider)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil || provider == nil {
		return "", err
	}
	return *provider, nil
}

// SetOrgDefaultSandboxProvider stores the organization's default sandbox
// provider; "" clears it. Only organization admins may change it.
func (s *Store) SetOrgDefaultSandboxProvider(
	ctx context.Context, principal domain.Principal, orgID, provider string,
) error {
	if provider != "" && !sandboxProviderName.MatchString(provider) {
		return ErrInvalid
	}
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if err := requireOrgAdmin(ctx, tx, orgID, principal.UserID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ao_organizations
			SET settings = CASE WHEN $2 = '' THEN settings - $3::text
				ELSE settings || jsonb_build_object($3::text, $2::text) END,
			    updated_at = now()
			WHERE id = $1`,
			orgID, provider, settingsDefaultSandboxProvider)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}
