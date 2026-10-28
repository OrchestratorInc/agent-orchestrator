package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	// ErrMergeNeedsApproval refuses a merge by someone a project or session
	// was shared with: they ask its owner to merge instead.
	ErrMergeNeedsApproval = errors.New("merge needs the owner's approval")
	ErrConflict           = errors.New("conflict")
	ErrInvalid            = errors.New("invalid")
	ErrInvalidBranch      = errors.New("invalid session branch")
	// ErrBranchInUse refuses a requested session branch a live session of the
	// project already works on (BranchRefusedError carries which one).
	ErrBranchInUse          = errors.New("session branch in use")
	ErrIdempotencyMismatch  = errors.New("idempotency key belongs to a different operation")
	ErrSandboxQuotaExceeded = errors.New("sandbox quota exceeded")
	ErrWorkerUnavailable    = errors.New("worker unavailable")
	// ErrSessionTerminated refuses to wake a deleted session; only a restore
	// brings it back.
	ErrSessionTerminated = errors.New("session is terminated")
	// ErrTerminalSessionExited means an agent terminal finished and there is no
	// live worker terminal to attach to. It is deliberately distinct from
	// ErrWorkerUnavailable: the latter is expected while a sandbox is booting
	// and callers should retry it, while this one is terminal for this agent
	// session.
	ErrTerminalSessionExited = errors.New("terminal session exited")
	ErrTransportExpired      = errors.New("worker request expired")
	ErrWorkspaceReadOnly     = errors.New("workspace is read-only")
	// ErrTerminalClosed means output arrived for a terminal that is no longer
	// open at the worker's epoch (closed, failed, or never opened there).
	ErrTerminalClosed = errors.New("terminal is closed")
	// ErrTerminalExpired means output arrived for an open terminal whose lease
	// has passed. Its state is left alone: the lease holder decides whether
	// the terminal is reopened or retired.
	ErrTerminalExpired = errors.New("terminal lease expired")
	// ErrProviderConnectionInUse refuses deleting a provider connection that a
	// sandbox not yet fully deleted still runs on. It is a conflict.
	ErrProviderConnectionInUse = fmt.Errorf("provider connection in use: %w", ErrConflict)
	// ErrArtifactLimit means a commit would exceed a session's artifact file
	// count or total size.
	ErrArtifactLimit = errors.New("session artifact limit exceeded")
	// ErrNotOrchestratorChild refuses an orchestrator acting on a session it
	// did not spawn. It is forbidden, but names the reason so `ao kill` and
	// `ao send` can say so instead of reporting an organization access error.
	ErrNotOrchestratorChild = fmt.Errorf("not a session this orchestrator started: %w", ErrForbidden)
	// ErrArtifactShareLimit means the user already holds their maximum number
	// of active artifact share links.
	ErrArtifactShareLimit = errors.New("artifact share limit reached")
	// ErrArtifactShareNameChanged refuses replacing a link's file with one of
	// another name: the name is part of the document a link belongs to.
	ErrArtifactShareNameChanged = errors.New("artifact share file name changed")
)

type Store struct {
	pool    *pgxpool.Pool
	billing billingPolicy
	// activityLog is told about activity rows the store writes itself, inside
	// the transaction they describe (see commitWithActivity).
	activityLog func(context.Context, domain.ActivityEvent)
	// coderReadableNames stores a readable Coder workspace name on each new
	// Coder session (SetCoderReadableWorkspaceNames).
	coderReadableNames bool
}

// SetCoderReadableWorkspaceNames turns readable Coder workspace names on for
// sessions created from now on (config.Config.CoderReadableWorkspaceNames).
// Off, the default, a new session stores no name and keeps the <prefix>-<id>
// name; a stored name is read either way. Call it before serving requests.
func (s *Store) SetCoderReadableWorkspaceNames(enabled bool) {
	s.coderReadableNames = enabled
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) ValidateRuntimeRole(ctx context.Context) error {
	var role string
	var superuser, bypassRLS bool
	if err := s.pool.QueryRow(
		ctx,
		`SELECT rolname, rolsuper, rolbypassrls
		FROM pg_roles
		WHERE rolname = current_user`,
	).Scan(&role, &superuser, &bypassRLS); err != nil {
		return fmt.Errorf("inspect PostgreSQL runtime role: %w", err)
	}
	if superuser || bypassRLS {
		return fmt.Errorf("PostgreSQL runtime role %q bypasses row-level security", role)
	}
	return nil
}

type tenantFn func(pgx.Tx) error

// withService runs fn in a transaction that carries the control-plane service
// context. It is used to scan due sandboxes and to locate registrations for a
// host-authenticated address update. Writes after either lookup run under the
// specific organization or user RLS identity.
func (s *Store) withService(ctx context.Context, fn tenantFn) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`SELECT set_config('ao.service', 'control-plane', true)`,
	); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit service transaction: %w", err)
	}
	return nil
}

// withOrg runs fn scoped to one organization without a user principal. It is
// for control-plane background work — reconciliation, ticket issuance, worker
// registration — where there is no request to authorize but tenant isolation
// must still hold. Request-driven paths use withTenant, which additionally
// verifies the caller's membership.
func (s *Store) withOrg(ctx context.Context, orgID string, fn tenantFn) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`SELECT set_config('ao.org_id', $1, true)`,
		orgID,
	); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit organization transaction: %w", err)
	}
	return nil
}

// withUser runs account-scoped work under the RLS identity of one AO user.
// It is used for credentials that belong to the person rather than an
// organization. Callers must derive userID from an authenticated principal or
// from a hashed, single-use callback route.
func (s *Store) withUser(ctx context.Context, userID string, fn tenantFn) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`SELECT set_config('ao.user_id', $1, true)`,
		userID,
	); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit user transaction: %w", err)
	}
	return nil
}

func (s *Store) withTenant(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
	fn tenantFn,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`SELECT set_config('ao.user_id', $1, true), set_config('ao.org_id', $2, true)`,
		principal.UserID,
		orgID,
	); err != nil {
		return err
	}
	var allowed bool
	if err := tx.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM ao_org_memberships membership
			JOIN ao_organizations organization ON organization.id = membership.org_id
			WHERE membership.org_id = $1
			  AND membership.user_id = $2
			  AND membership.status = 'active'
			  AND organization.status = 'active'
			  AND (
				organization.external_org_id IS NULL
				OR $3 <> 'workos'
				OR (
					$4 <> ''
					AND organization.auth_provider = 'workos'
					AND organization.external_org_id = $4
				)
				OR (
					$4 = ''
					AND organization.external_org_id IS NULL
					AND organization.owner_user_id = $2
				)
			  )
		)`,
		orgID,
		principal.UserID,
		principal.Provider,
		principal.ExternalOrgID,
	).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tenant transaction: %w", err)
	}
	return nil
}
