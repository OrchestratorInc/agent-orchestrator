package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// GitHubRepositoryAccess reads, for setting up a project's repositories, the
// granted main repository (by GitHub repository id; 0 skips it) and the
// GitHub App installation each named repository (owner/name) is granted
// through in the organization. A repository without an active grant is left
// out of Installations; a main repository without one leaves MainURL empty.
func (s *Store) GitHubRepositoryAccess(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
	mainRepositoryID int64,
	fullNames []string,
) (domain.GitHubRepositoryAccess, error) {
	access := domain.GitHubRepositoryAccess{Installations: map[string]string{}}
	lowered := make([]string, 0, len(fullNames))
	for _, name := range fullNames {
		lowered = append(lowered, strings.ToLower(strings.Trim(strings.TrimSpace(name), "/")))
	}
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if mainRepositoryID > 0 {
			err := tx.QueryRow(ctx,
				`SELECT repository.html_url, grant_row.installation_id::text
				FROM ao_github_repository_grants grant_row
				JOIN ao_github_repositories repository
				  ON repository.github_repository_id = grant_row.github_repository_id
				JOIN ao_github_installations installation
				  ON installation.org_id = grant_row.org_id
				 AND installation.id = grant_row.installation_id
				WHERE grant_row.org_id = $1
				  AND grant_row.github_repository_id = $2
				  AND grant_row.revoked_at IS NULL
				  AND `+githubUserScopeIntactSQL,
				orgID, mainRepositoryID,
			).Scan(&access.MainURL, &access.MainInstallation)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if len(lowered) == 0 {
			return nil
		}
		rows, err := tx.Query(ctx,
			`SELECT lower(repository.full_name), grant_row.installation_id::text
			FROM ao_github_repository_grants grant_row
			JOIN ao_github_repositories repository
			  ON repository.github_repository_id = grant_row.github_repository_id
			JOIN ao_github_installations installation
			  ON installation.org_id = grant_row.org_id
			 AND installation.id = grant_row.installation_id
			WHERE grant_row.org_id = $1
			  AND lower(repository.full_name) = ANY($2::text[])
			  AND grant_row.revoked_at IS NULL
			  AND `+githubUserScopeIntactSQL,
			orgID, lowered,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name, installation string
			if err := rows.Scan(&name, &installation); err != nil {
				return err
			}
			access.Installations[name] = installation
		}
		return rows.Err()
	})
	return access, err
}

// projectDeclaresRepositorySQL is a condition that the project row aliased
// project declares the GitHub repository row aliased repository as one of its
// additional repositories: config "repos", or the legacy coder dev kit
// "extraRepos" when "repos" is absent (domain.ProjectAdditionalRepos).
const projectDeclaresRepositorySQL = `EXISTS (
	SELECT 1 FROM jsonb_array_elements(
		CASE
			WHEN jsonb_typeof(project.config->'repos') = 'array' THEN project.config->'repos'
			WHEN project.config ? 'repos' THEN '[]'::jsonb
			WHEN jsonb_typeof(project.config->'coder'->'extraRepos') = 'array' THEN project.config->'coder'->'extraRepos'
			ELSE '[]'::jsonb
		END
	) AS extra
	WHERE jsonb_typeof(extra) = 'object'
	  AND lower(regexp_replace(rtrim(coalesce(extra->>'url', ''), '/'), '\.git$', '')) = lower(rtrim(repository.html_url, '/'))
)`
