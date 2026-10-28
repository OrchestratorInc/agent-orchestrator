package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecordActivityEvent appends one row to an organization's support trail. It
// runs under the organization's RLS identity. Callers should go through
// activity.Recorder, which sanitizes the event and never blocks a request.
func (s *Store) RecordActivityEvent(ctx context.Context, event domain.ActivityEvent) error {
	if _, err := uuid.Parse(event.OrgID); err != nil {
		return fmt.Errorf("%w: activity event organization", ErrInvalid)
	}
	return s.withOrg(ctx, event.OrgID, func(tx pgx.Tx) error {
		return insertActivityEvent(ctx, tx, event)
	})
}

// insertActivityEvent writes an activity row inside an existing transaction
// whose RLS identity already covers event.OrgID (ao.org_id or the service
// context). It is used where the event must commit atomically with the change
// it describes, such as the first sign-in that creates an organization.
func insertActivityEvent(ctx context.Context, tx pgx.Tx, event domain.ActivityEvent) error {
	attrs := event.Attrs
	if attrs == nil {
		attrs = map[string]any{}
	}
	encoded, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("encode activity attrs: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO ao_activity_events (org_id, user_id, session_id, kind, provider, attrs)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6::jsonb)`,
		event.OrgID, uuidOrEmpty(event.UserID), uuidOrEmpty(event.SessionID),
		event.Kind, event.Provider, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("insert activity event: %w", err)
	}
	return nil
}

// SetActivityLogger receives the activity rows the store writes inside its own
// transactions (a first sign-in creating an organization), after they commit,
// so they reach the structured log like every recorder event. Set it before
// serving requests.
func (s *Store) SetActivityLogger(log func(context.Context, domain.ActivityEvent)) {
	s.activityLog = log
}

// commitWithActivity writes events inside tx, each under a savepoint so a
// failed activity insert can never fail the change it describes, then commits.
func (s *Store) commitWithActivity(ctx context.Context, tx pgx.Tx, events []domain.ActivityEvent) error {
	written := make([]domain.ActivityEvent, 0, len(events))
	for _, event := range events {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if err := insertActivityEvent(ctx, savepoint, event); err != nil {
			if rollbackErr := savepoint.Rollback(ctx); rollbackErr != nil {
				return rollbackErr
			}
			continue
		}
		if err := savepoint.Commit(ctx); err != nil {
			return err
		}
		written = append(written, event)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.activityLog != nil {
		for _, event := range written {
			s.activityLog(ctx, event)
		}
	}
	return nil
}

func uuidOrEmpty(value string) string {
	if _, err := uuid.Parse(value); err != nil {
		return ""
	}
	return value
}

// ActivityFilter narrows ListActivityEvents. Zero values mean "any".
type ActivityFilter struct {
	SessionID string
	UserID    string
	Since     time.Time
	Limit     int
}

// ListActivityEvents returns an organization's most recent activity, newest
// first.
func (s *Store) ListActivityEvents(
	ctx context.Context,
	orgID string,
	filter ActivityFilter,
) ([]domain.ActivityEvent, error) {
	if _, err := uuid.Parse(orgID); err != nil {
		return nil, fmt.Errorf("%w: organization", ErrInvalid)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var events []domain.ActivityEvent
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		events, err = listActivityEventsTx(ctx, tx, orgID, filter, limit)
		return err
	})
	return events, err
}

func listActivityEventsTx(
	ctx context.Context,
	tx pgx.Tx,
	orgID string,
	filter ActivityFilter,
	limit int,
) ([]domain.ActivityEvent, error) {
	var since *time.Time
	if !filter.Since.IsZero() {
		since = &filter.Since
	}
	rows, err := tx.Query(ctx,
		`SELECT id::text, org_id::text, COALESCE(user_id::text, ''), COALESCE(session_id::text, ''),
			kind, provider, attrs, created_at
		FROM ao_activity_events
		WHERE org_id = $1
			AND ($2 = '' OR session_id = NULLIF($2, '')::uuid)
			AND ($3 = '' OR user_id = NULLIF($3, '')::uuid)
			AND ($4::timestamptz IS NULL OR created_at >= $4)
		ORDER BY created_at DESC, id DESC
		LIMIT $5`,
		orgID, uuidOrEmpty(filter.SessionID), uuidOrEmpty(filter.UserID), since, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list activity events: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ActivityEvent, error) {
		var event domain.ActivityEvent
		var attrs []byte
		if err := row.Scan(
			&event.ID, &event.OrgID, &event.UserID, &event.SessionID,
			&event.Kind, &event.Provider, &attrs, &event.CreatedAt,
		); err != nil {
			return event, err
		}
		if len(attrs) > 0 {
			if err := json.Unmarshal(attrs, &event.Attrs); err != nil {
				return event, err
			}
		}
		return event, nil
	})
}

// PruneActivityEvents deletes activity older than before across every
// organization and returns how many rows it removed.
func (s *Store) PruneActivityEvents(ctx context.Context, before time.Time) (int64, error) {
	var deleted int64
	err := s.withService(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM ao_activity_events WHERE created_at < $1`, before)
		deleted = tag.RowsAffected()
		return err
	})
	return deleted, err
}

// RecordSandboxStartupErrorDetail stores the operator detail behind the
// current startup error: a bounded, redacted excerpt of the provider failure
// and, for Coder, the tail of the workspace build and agent startup logs. It
// is fenced by the reconcile lease like RecordSandboxStartupError and is only
// shown to the organization for its own (bring-your-own) compute.
func (s *Store) RecordSandboxStartupErrorDetail(
	ctx context.Context,
	owner, orgID, sessionID, detail string,
) error {
	if len(detail) > maxStartupErrorDetail {
		// Keep the tail (the most recent output) on a valid UTF-8 boundary.
		detail = strings.ToValidUTF8(detail[len(detail)-maxStartupErrorDetail:], "")
	}
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE ao_sandboxes SET startup_error_detail = $4
			WHERE session_id = $1 AND org_id = $2
				AND reconcile_lease_owner = $3
				AND reconcile_lease_until > now()`,
			sessionID, orgID, owner, detail,
		)
		if err != nil {
			return fmt.Errorf("record sandbox startup error detail: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrSandboxLeaseLost
		}
		return nil
	})
}

// maxStartupErrorDetail matches the column's CHECK constraint.
const maxStartupErrorDetail = 4096

// SupportReport is the operator view of one person's footprint: their
// accounts, the organizations they belong to, each organization's recent
// sessions and activity.
type SupportReport struct {
	Users []SupportUser `json:"users"`
	// Orgs are filled for an organization or session lookup, where there is
	// no user to start from.
	Orgs []SupportOrg `json:"orgs,omitempty"`
}

type SupportUser struct {
	UserID       string       `json:"userId"`
	AuthProvider string       `json:"authProvider"`
	Email        string       `json:"email"`
	DisplayName  string       `json:"displayName"`
	CreatedAt    time.Time    `json:"createdAt"`
	Orgs         []SupportOrg `json:"orgs"`
}

type SupportOrg struct {
	OrgID              string                 `json:"orgId"`
	Slug               string                 `json:"slug"`
	DisplayName        string                 `json:"displayName"`
	Kind               string                 `json:"kind"`
	AuthProvider       string                 `json:"authProvider"`
	ExternalOrgID      string                 `json:"externalOrgId,omitempty"`
	Role               string                 `json:"role,omitempty"`
	Plan               string                 `json:"plan"`
	SubscriptionStatus string                 `json:"subscriptionStatus,omitempty"`
	CreatedAt          time.Time              `json:"createdAt"`
	CoderConnection    *SupportConnection     `json:"coderConnection,omitempty"`
	Sessions           []SupportSession       `json:"sessions"`
	Activity           []domain.ActivityEvent `json:"activity"`
}

type SupportConnection struct {
	ConnectionID    string    `json:"connectionId"`
	ValidationState string    `json:"validationState"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type SupportSession struct {
	SessionID             string     `json:"sessionId"`
	ProjectID             string     `json:"projectId"`
	Kind                  string     `json:"kind"`
	Harness               string     `json:"harness"`
	IsTerminated          bool       `json:"isTerminated"`
	CreatedAt             time.Time  `json:"createdAt"`
	Provider              string     `json:"provider"`
	ProviderEnvironmentID string     `json:"providerEnvironmentId,omitempty"`
	ConnectionID          string     `json:"connectionId,omitempty"`
	DesiredState          string     `json:"desiredState"`
	ObservedState         string     `json:"observedState"`
	WorkerLastSeenAt      *time.Time `json:"workerLastSeenAt,omitempty"`
	StartupAttempts       int        `json:"startupAttempts"`
	StartupErrorCode      string     `json:"startupErrorCode,omitempty"`
	StartupErrorMessage   string     `json:"startupErrorMessage,omitempty"`
	StartupErrorDetail    string     `json:"startupErrorDetail,omitempty"`
	LastError             string     `json:"lastError,omitempty"`
}

// SupportQuery selects what SupportLookup starts from. Exactly one of Email,
// OrgID or SessionID is used, in that order of preference.
type SupportQuery struct {
	Email     string
	OrgID     string
	SessionID string
	// SessionLimit and ActivityLimit bound each organization's lists.
	SessionLimit  int
	ActivityLimit int
}

// SupportLookup assembles an operator report. It is read-only and reads across
// organizations, so it must only be reachable from operator-authenticated
// surfaces (the support endpoint gated by AO_CLOUD_SUPPORT_TOKEN).
func (s *Store) SupportLookup(ctx context.Context, query SupportQuery) (SupportReport, error) {
	if query.SessionLimit <= 0 || query.SessionLimit > 200 {
		query.SessionLimit = 20
	}
	if query.ActivityLimit <= 0 || query.ActivityLimit > 1000 {
		query.ActivityLimit = 100
	}
	email := strings.ToLower(strings.TrimSpace(query.Email))
	switch {
	case email != "":
		return s.supportByEmail(ctx, email, query)
	case query.OrgID != "":
		if _, err := uuid.Parse(query.OrgID); err != nil {
			return SupportReport{}, fmt.Errorf("%w: organization id", ErrInvalid)
		}
		org, err := s.supportOrg(ctx, SupportOrg{OrgID: query.OrgID}, query)
		if err != nil {
			return SupportReport{}, err
		}
		return SupportReport{Users: []SupportUser{}, Orgs: []SupportOrg{org}}, nil
	case query.SessionID != "":
		if _, err := uuid.Parse(query.SessionID); err != nil {
			return SupportReport{}, fmt.Errorf("%w: session id", ErrInvalid)
		}
		var orgID string
		err := s.withService(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT org_id::text FROM ao_sandboxes WHERE session_id = $1`, query.SessionID,
			).Scan(&orgID)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return SupportReport{}, ErrNotFound
		}
		if err != nil {
			return SupportReport{}, err
		}
		org, err := s.supportOrg(ctx, SupportOrg{OrgID: orgID}, query)
		if err != nil {
			return SupportReport{}, err
		}
		return SupportReport{Users: []SupportUser{}, Orgs: []SupportOrg{org}}, nil
	default:
		return SupportReport{}, fmt.Errorf("%w: an email, organization id or session id is required", ErrInvalid)
	}
}

func (s *Store) supportByEmail(ctx context.Context, email string, query SupportQuery) (SupportReport, error) {
	report := SupportReport{Users: []SupportUser{}}
	// ao_users carries no row-level security; the service context documents
	// that this is an operator read.
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id::text, auth_provider, email, display_name, created_at
			FROM ao_users WHERE lower(email) = $1 ORDER BY created_at`, email)
		if err != nil {
			return err
		}
		report.Users, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SupportUser, error) {
			var user SupportUser
			err := row.Scan(&user.UserID, &user.AuthProvider, &user.Email, &user.DisplayName, &user.CreatedAt)
			return user, err
		})
		return err
	})
	if err != nil {
		return SupportReport{}, fmt.Errorf("support lookup users: %w", err)
	}
	for index := range report.Users {
		user := &report.Users[index]
		var orgs []SupportOrg
		err := s.withUser(ctx, user.UserID, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx,
				`SELECT organization.id::text, membership.role
				FROM ao_org_memberships membership
				JOIN ao_organizations organization ON organization.id = membership.org_id
				WHERE membership.user_id = $1
				ORDER BY organization.created_at`, user.UserID)
			if err != nil {
				return err
			}
			orgs, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SupportOrg, error) {
				var org SupportOrg
				err := row.Scan(&org.OrgID, &org.Role)
				return org, err
			})
			return err
		})
		if err != nil {
			return SupportReport{}, fmt.Errorf("support lookup memberships: %w", err)
		}
		user.Orgs = make([]SupportOrg, 0, len(orgs))
		for _, org := range orgs {
			filled, err := s.supportOrg(ctx, org, query)
			if err != nil {
				return SupportReport{}, err
			}
			user.Orgs = append(user.Orgs, filled)
		}
	}
	return report, nil
}

func (s *Store) supportOrg(ctx context.Context, org SupportOrg, query SupportQuery) (SupportOrg, error) {
	err := s.withOrg(ctx, org.OrgID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT slug, display_name, kind, auth_provider, COALESCE(external_org_id, ''),
				plan, COALESCE(subscription_status, ''), created_at
			FROM ao_organizations WHERE id = $1`, org.OrgID,
		).Scan(
			&org.Slug, &org.DisplayName, &org.Kind, &org.AuthProvider, &org.ExternalOrgID,
			&org.Plan, &org.SubscriptionStatus, &org.CreatedAt,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("organization: %w", err)
		}
		var connection SupportConnection
		err := tx.QueryRow(ctx,
			`SELECT id::text, validation_state, updated_at
			FROM ao_provider_connections
			WHERE org_id = $1 AND provider = 'coder'
			ORDER BY updated_at DESC LIMIT 1`, org.OrgID,
		).Scan(&connection.ConnectionID, &connection.ValidationState, &connection.UpdatedAt)
		switch {
		case err == nil:
			org.CoderConnection = &connection
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("coder connection: %w", err)
		}
		rows, err := tx.Query(ctx,
			`SELECT session.id::text, session.project_id::text, session.kind, session.harness,
				session.is_terminated, session.created_at,
				COALESCE(sandbox.provider, ''), COALESCE(sandbox.provider_environment_id, ''),
				COALESCE(sandbox.provider_connection_id::text, ''),
				COALESCE(sandbox.desired_state, ''), COALESCE(sandbox.observed_state, ''),
				sandbox.worker_last_seen_at, COALESCE(sandbox.startup_attempts, 0),
				COALESCE(sandbox.startup_error_code, ''), COALESCE(sandbox.startup_error_message, ''),
				COALESCE(sandbox.startup_error_detail, ''), COALESCE(sandbox.last_error, '')
			FROM ao_sessions session
			LEFT JOIN ao_sandboxes sandbox
				ON sandbox.session_id = session.id AND sandbox.org_id = session.org_id
			WHERE session.org_id = $1
				AND ($2 = '' OR session.id = NULLIF($2, '')::uuid)
			ORDER BY session.created_at DESC
			LIMIT $3`, org.OrgID, uuidOrEmpty(query.SessionID), query.SessionLimit)
		if err != nil {
			return fmt.Errorf("sessions: %w", err)
		}
		org.Sessions, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SupportSession, error) {
			var session SupportSession
			err := row.Scan(
				&session.SessionID, &session.ProjectID, &session.Kind, &session.Harness,
				&session.IsTerminated, &session.CreatedAt,
				&session.Provider, &session.ProviderEnvironmentID, &session.ConnectionID,
				&session.DesiredState, &session.ObservedState,
				&session.WorkerLastSeenAt, &session.StartupAttempts,
				&session.StartupErrorCode, &session.StartupErrorMessage,
				&session.StartupErrorDetail, &session.LastError,
			)
			return session, err
		})
		if err != nil {
			return fmt.Errorf("sessions: %w", err)
		}
		org.Activity, err = listActivityEventsTx(ctx, tx, org.OrgID,
			ActivityFilter{SessionID: query.SessionID}, query.ActivityLimit)
		return err
	})
	if err != nil {
		return SupportOrg{}, fmt.Errorf("support lookup organization %s: %w", org.OrgID, err)
	}
	if org.Sessions == nil {
		org.Sessions = []SupportSession{}
	}
	if org.Activity == nil {
		org.Activity = []domain.ActivityEvent{}
	}
	return org, nil
}

// SandboxReadiness is what the control plane knows about a sandbox at the
// moment its worker registers, used to measure time-to-ready.
type SandboxReadiness struct {
	Provider         string
	ConnectionID     string
	SessionKind      string
	StartupStartedAt *time.Time
	// PreviousWorkerSeenAt is non-nil when a worker checked in before, which
	// makes this registration a resume rather than a first start.
	PreviousWorkerSeenAt *time.Time
	StartupAttempts      int
	SessionCreatedAt     time.Time
}

// SandboxReadiness reads the startup timing of one session's sandbox.
func (s *Store) SandboxReadiness(ctx context.Context, orgID, sessionID string) (SandboxReadiness, error) {
	var readiness SandboxReadiness
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT sandbox.provider, COALESCE(sandbox.provider_connection_id::text, ''), session.kind,
				sandbox.startup_started_at, sandbox.worker_last_seen_at, sandbox.startup_attempts,
				session.created_at
			FROM ao_sandboxes sandbox
			JOIN ao_sessions session ON session.id = sandbox.session_id AND session.org_id = sandbox.org_id
			WHERE sandbox.session_id = $1 AND sandbox.org_id = $2`,
			sessionID, orgID,
		).Scan(
			&readiness.Provider, &readiness.ConnectionID, &readiness.SessionKind,
			&readiness.StartupStartedAt, &readiness.PreviousWorkerSeenAt, &readiness.StartupAttempts,
			&readiness.SessionCreatedAt,
		)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxReadiness{}, ErrNotFound
	}
	return readiness, err
}
