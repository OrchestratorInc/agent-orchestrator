package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// WorkerGitHubCheckoutContext resolves a worker-owned session to the exact
// GitHub App installation that may read its project repository. This is an
// organization-scoped service lookup: no caller-provided repository or
// installation identifier participates in the decision.
func (s *Store) WorkerGitHubCheckoutContext(
	ctx context.Context,
	orgID, sessionID string,
) (domain.GitHubCheckoutContext, error) {
	authorization := domain.GitHubCheckoutContext{OrgID: orgID, SessionID: sessionID}
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var projectGrantID, activeGrantID string
		err := tx.QueryRow(ctx,
			`SELECT project.id, project.github_repository_grant_id, grant_row.id,
				installation.github_installation_id,
				repository.github_repository_id, repository.full_name,
				repository.clone_url, project.default_branch,
				installation.access_scope, COALESCE(installation.scope_user_id::text, '')
			FROM ao_sessions session
			JOIN ao_projects project
			  ON project.org_id = session.org_id AND project.id = session.project_id
			JOIN ao_github_repository_grants grant_row
			  ON grant_row.org_id = project.org_id
			 AND grant_row.github_repository_id = project.github_repository_id
			JOIN ao_github_installations installation
			  ON installation.org_id = grant_row.org_id
			 AND installation.id = grant_row.installation_id
			JOIN ao_github_repositories repository
			  ON repository.github_repository_id = grant_row.github_repository_id
			WHERE session.org_id = $1 AND session.id = $2
			  AND session.is_terminated = false
			  AND project.github_repository_id IS NOT NULL
			  AND project.github_repository_grant_id IS NOT NULL
			  AND project.repository_url = repository.html_url
			  AND grant_row.revoked_at IS NULL
			  AND installation.status = 'active'
			  AND installation.suspended_at IS NULL
			  AND installation.disconnected_at IS NULL
			  AND installation.deleted_at IS NULL
			  AND repository.is_archived = false
			  AND repository.is_disabled = false
			  AND btrim(repository.clone_url) <> ''
			  AND `+githubUserScopeIntactSQL+`
			ORDER BY grant_row.granted_at DESC
			LIMIT 1`,
			orgID, sessionID,
		).Scan(
			&authorization.ProjectID,
			&projectGrantID,
			&activeGrantID,
			&authorization.GitHubInstallationID,
			&authorization.GitHubRepositoryID,
			&authorization.FullName,
			&authorization.CloneURL,
			&authorization.DefaultBranch,
			&authorization.AccessScope,
			&authorization.ScopeUserID,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("resolve worker GitHub checkout context: %w", err)
		}
		if projectGrantID != activeGrantID {
			if _, err := tx.Exec(ctx, `UPDATE ao_projects
				SET github_repository_grant_id = $3
				WHERE org_id = $1 AND id = $2 AND github_repository_grant_id = $4`,
				orgID, authorization.ProjectID, activeGrantID, projectGrantID); err != nil {
				return fmt.Errorf("rebind project GitHub grant: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.GitHubCheckoutContext{}, err
	}
	return authorization, nil
}

// WorkerSessionExtraRepos returns the additional repositories a session's
// project declares (config "repos", or the legacy coder dev-kit "extraRepos"),
// if any. It is a
// best-effort read used only to broaden a checkout token's repository scope, so
// callers must tolerate an empty result and must never fail the primary checkout
// on its account. Repository and installation authority still come exclusively
// from WorkerGitHubCheckoutContext — this only tells the caller which additional
// repositories the project asked to have cloned alongside the primary one.
func (s *Store) WorkerSessionExtraRepos(
	ctx context.Context,
	orgID, sessionID string,
) ([]domain.RepoRef, error) {
	var config json.RawMessage
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT project.config
			FROM ao_sessions session
			JOIN ao_projects project
			  ON project.org_id = session.org_id AND project.id = session.project_id
			WHERE session.org_id = $1 AND session.id = $2
			  AND session.is_terminated = false`,
			orgID, sessionID,
		).Scan(&config)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load session extra repositories: %w", err)
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return domain.ProjectAdditionalRepos(config), nil
}

// WorkerSessionGrantedExtraRepos returns the session's additional
// repositories (WorkerSessionExtraRepos) that still have an active,
// non-revoked grant in the organization through the same GitHub App
// installation as the main repository. It is what an App checkout or push
// token, and an App pull request, may cover: a repository whose grant was
// revoked after the project was set up, or one a project declared without a
// grant (a legacy dev kit list, or a config stored before grants were
// checked), is left out. A project whose main repository has no active grant
// gets none.
func (s *Store) WorkerSessionGrantedExtraRepos(
	ctx context.Context,
	orgID, sessionID string,
) ([]domain.RepoRef, error) {
	var granted []domain.RepoRef
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var config json.RawMessage
		var mainInstallation *int64
		err := tx.QueryRow(ctx,
			`SELECT project.config,
				COALESCE((
					SELECT installation.github_installation_id
					FROM ao_github_repository_grants grant_row
					JOIN ao_github_installations installation
					  ON installation.org_id = grant_row.org_id
					 AND installation.id = grant_row.installation_id
					WHERE grant_row.org_id = project.org_id
					  AND grant_row.github_repository_id = project.github_repository_id
					  AND grant_row.revoked_at IS NULL
					  AND installation.status = 'active'
					  AND installation.suspended_at IS NULL
					  AND installation.disconnected_at IS NULL
					  AND installation.deleted_at IS NULL
					ORDER BY grant_row.granted_at DESC
					LIMIT 1
				), project.github_installation_id)
			FROM ao_sessions session
			JOIN ao_projects project
			  ON project.org_id = session.org_id AND project.id = session.project_id
			WHERE session.org_id = $1 AND session.id = $2
			  AND session.is_terminated = false`,
			orgID, sessionID,
		).Scan(&config, &mainInstallation)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load session extra repositories: %w", err)
		}
		declared := domain.ProjectAdditionalRepos(config)
		if len(declared) == 0 || mainInstallation == nil {
			return nil
		}
		fullNames := make([]string, 0, len(declared))
		for _, repo := range declared {
			if _, fullName, err := domain.CanonicalGitHubRepoURL(repo.URL); err == nil {
				fullNames = append(fullNames, strings.ToLower(fullName))
			}
		}
		rows, err := tx.Query(ctx,
			`SELECT lower(repository.full_name)
			FROM ao_github_repository_grants grant_row
			JOIN ao_github_repositories repository
			  ON repository.github_repository_id = grant_row.github_repository_id
			JOIN ao_github_installations installation
			  ON installation.org_id = grant_row.org_id
			 AND installation.id = grant_row.installation_id
			WHERE grant_row.org_id = $1
			  AND grant_row.revoked_at IS NULL
			  AND installation.github_installation_id = $2
			  AND installation.status = 'active'
			  AND installation.suspended_at IS NULL
			  AND installation.disconnected_at IS NULL
			  AND installation.deleted_at IS NULL
			  AND lower(repository.full_name) = ANY($3::text[])
			  AND `+githubUserScopeIntactSQL,
			orgID, *mainInstallation, fullNames,
		)
		if err != nil {
			return fmt.Errorf("load granted extra repositories: %w", err)
		}
		defer rows.Close()
		active := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			active[name] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, repo := range declared {
			if _, fullName, err := domain.CanonicalGitHubRepoURL(repo.URL); err == nil && active[strings.ToLower(fullName)] {
				granted = append(granted, repo)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return granted, nil
}

// WorkerGitHubPAT resolves the session creator's explicitly configured GitHub
// personal access token. It deliberately derives the repository and token
// owner from the session rather than accepting either from the worker.
func (s *Store) WorkerGitHubPAT(
	ctx context.Context,
	orgID, sessionID, workerID string,
	epoch int64,
) (domain.WorkerGitHubPAT, error) {
	var credential domain.WorkerGitHubPAT
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := requireCurrentWorker(ctx, tx, orgID, sessionID, workerID, epoch); err != nil {
			return err
		}
		var ownerUserID *string
		if err := tx.QueryRow(ctx,
			`SELECT project.repository_url, session.created_by_user_id::text
			FROM ao_sessions session
			JOIN ao_projects project
			  ON project.org_id = session.org_id AND project.id = session.project_id
			WHERE session.org_id = $1 AND session.id = $2
			  AND session.is_terminated = false`,
			orgID, sessionID,
		).Scan(&credential.CloneURL, &ownerUserID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("resolve worker GitHub repository: %w", err)
		}
		if ownerUserID == nil {
			return ErrNotFound
		}
		credential.OwnerUserID = *ownerUserID
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, credential.OwnerUserID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx,
			`SELECT encrypted_secret, secret_nonce
			FROM ao_user_provider_connections
			WHERE user_id = $1
			  AND provider = 'github'
			  AND label = 'default'
			  AND validation_state = 'valid'`,
			credential.OwnerUserID,
		).Scan(&credential.EncryptedSecret, &credential.Nonce)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve worker GitHub PAT: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.WorkerGitHubPAT{}, err
	}
	return credential, nil
}

// WorkerRemoteGitHubCheckoutContext returns only the encrypted production
// capability bound to the worker's own session. Repository and installation
// identifiers are read from the project row and are never supplied by the
// worker.
func (s *Store) WorkerRemoteGitHubCheckoutContext(
	ctx context.Context,
	orgID, sessionID string,
) (domain.RemoteGitHubCheckoutContext, error) {
	authorization := domain.RemoteGitHubCheckoutContext{
		OrgID: orgID, SessionID: sessionID,
	}
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(
			ctx,
			`SELECT project.id, project.github_installation_id,
				project.github_repository_id, project.github_authority_user_id,
				project.github_authority_environment, project.repository_url,
				project.github_capability_ciphertext,
				project.github_capability_nonce
			FROM ao_sessions session
			JOIN ao_projects project
			  ON project.org_id = session.org_id
			 AND project.id = session.project_id
			WHERE session.org_id = $1 AND session.id = $2
			  AND session.is_terminated = false
			  AND project.github_repository_grant_id IS NULL
			  AND project.github_installation_id IS NOT NULL
			  AND project.github_repository_id IS NOT NULL
			  AND project.github_capability_ciphertext IS NOT NULL
			  AND project.github_capability_nonce IS NOT NULL`,
			orgID,
			sessionID,
		).Scan(
			&authorization.ProjectID,
			&authorization.GitHubInstallationID,
			&authorization.GitHubRepositoryID,
			&authorization.UserExternalID,
			&authorization.TargetEnvironment,
			&authorization.RepositoryURL,
			&authorization.CapabilityCiphertext,
			&authorization.CapabilityNonce,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("resolve remote GitHub checkout context: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.RemoteGitHubCheckoutContext{}, err
	}
	return authorization, nil
}
