package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SessionPullRequestOpener names the person who started a session, for pull
// requests the AO GitHub App opened on their behalf: their AO display name
// and, when they connected GitHub to AO, their GitHub login and avatar. Only
// the organization's members see who that is; a project share grantee gets
// ErrForbidden.
func (s *Store) SessionPullRequestOpener(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
) (domain.PullRequestOpener, error) {
	var opener domain.PullRequestOpener
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		var userID *string
		err := tx.QueryRow(ctx,
			`SELECT created_by_user_id::text FROM ao_sessions WHERE org_id = $1 AND id = $2`,
			orgID, sessionID,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && userID == nil) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve session creator: %w", err)
		}
		var displayName, avatarURL string
		err = tx.QueryRow(ctx,
			`SELECT display_name, avatar_url FROM ao_users WHERE id = $1`,
			*userID,
		).Scan(&displayName, &avatarURL)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve session creator: %w", err)
		}
		opener.Name = strings.TrimSpace(displayName)
		opener.AvatarURL = strings.TrimSpace(avatarURL)
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, *userID); err != nil {
			return err
		}
		var login, githubAvatar string
		err = tx.QueryRow(ctx,
			`SELECT github_login, github_avatar_url FROM ao_github_user_connections WHERE user_id = $1`,
			*userID,
		).Scan(&login, &githubAvatar)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve session creator GitHub login: %w", err)
		}
		opener.GitHubLogin = strings.TrimSpace(login)
		if githubAvatar = strings.TrimSpace(githubAvatar); githubAvatar != "" {
			opener.AvatarURL = githubAvatar
		}
		return nil
	})
	if err != nil {
		return domain.PullRequestOpener{}, err
	}
	return opener, nil
}
