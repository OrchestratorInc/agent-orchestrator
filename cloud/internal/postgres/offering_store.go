package postgres

import (
	"context"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// OrchestratorPlacement returns where a live orchestrator's sandbox runs: its
// provider, its bring-your-own provider connection (if any), the project config
// that carries the Coder dev-kit settings, and the organization's Coder
// connections. A child the orchestrator spawns uses it to land on the same
// provider and the same Coder deployment rather than the managed default. It
// returns ErrForbidden when the session is not an active orchestrator in the
// organization.
func (s *Store) OrchestratorPlacement(
	ctx context.Context,
	orgID, orchestratorSessionID string,
) (domain.OrchestratorPlacement, error) {
	var placement domain.OrchestratorPlacement
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		projectID, err := requireActiveOrchestrator(ctx, tx, orgID, orchestratorSessionID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(
			ctx,
			`SELECT sandbox.provider,
				COALESCE(sandbox.provider_connection_id::text, ''),
				project.config, project.display_name, project.repository_url
			FROM ao_sandboxes sandbox
			JOIN ao_projects project
			  ON project.org_id = sandbox.org_id AND project.id = $3
			WHERE sandbox.org_id = $1 AND sandbox.session_id = $2`,
			orgID, orchestratorSessionID, projectID,
		).Scan(&placement.Provider, &placement.ConnectionID, &placement.ProjectConfig, &placement.ProjectName,
			&placement.RepositoryURL); err != nil {
			return err
		}
		rows, err := tx.Query(
			ctx,
			`SELECT id, org_id, provider, label, config, validation_state,
				validated_at, created_at, updated_at
			FROM ao_provider_connections
			WHERE org_id = $1 AND provider = 'coder'
			ORDER BY label`,
			orgID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var connection domain.ProviderConnection
			if err := rows.Scan(
				&connection.ID,
				&connection.OrgID,
				&connection.Provider,
				&connection.Label,
				&connection.Config,
				&connection.ValidationState,
				&connection.ValidatedAt,
				&connection.CreatedAt,
				&connection.UpdatedAt,
			); err != nil {
				return err
			}
			placement.CoderConnections = append(placement.CoderConnections, connection)
		}
		return rows.Err()
	})
	return placement, err
}
