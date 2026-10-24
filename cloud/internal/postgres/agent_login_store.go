package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

var _ agentconnect.Store = (*Store)(nil)

const agentLoginColumns = `id,user_id,org_id,harness,status,idempotency_key,expires_at,
    authorization_url,user_code,requires_code,runner_token_hash,input_ciphertext,input_nonce,job_id,launch_finished`

func scanAgentLogin(row pgx.Row) (agentconnect.Attempt, error) {
	var a agentconnect.Attempt
	err := row.Scan(&a.ID, &a.UserID, &a.OrgID, &a.Harness, &a.Status, &a.IdempotencyKey, &a.ExpiresAt,
		&a.AuthorizationURL, &a.UserCode, &a.RequiresCode, &a.TokenHash, &a.InputCiphertext, &a.InputNonce, &a.JobID, &a.LaunchFinished)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, agentconnect.ErrNotFound
	}
	return a, err
}

func lockAgentLogin(ctx context.Context, tx pgx.Tx, userID, harness string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('agent-login:' || $1 || ':' || $2,0))`, userID, harness)
	return err
}

func expireAgentLogins(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET status='expired',input_ciphertext=NULL,input_nonce=NULL,
        authorization_url='',user_code='',requires_code=false,updated_at=now()
        WHERE user_id=$1 AND status IN ('starting','waiting','connecting') AND expires_at<=clock_timestamp()`, userID)
	return err
}

// agentLoginReservationsTx counts private auth compute for organization
// admission without exposing another user's attempt to the calling principal.
// Admission mutations hold lockOrgTx before reading this shared reservation.
// A known VM counts until cleanup; an unknown Create counts for its lease only.
func agentLoginReservationsTx(ctx context.Context, tx pgx.Tx, orgID string) (int, error) {
	var previous string
	if err := tx.QueryRow(ctx, `SELECT coalesce(current_setting('ao.service',true),'')`).Scan(&previous); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.service','control-plane',true)`); err != nil {
		return 0, err
	}
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_agent_login_attempts WHERE org_id=$1 AND job_id<>''
        AND (launch_finished OR expires_at>clock_timestamp())`, orgID).Scan(&count)
	_, restoreErr := tx.Exec(ctx, `SELECT set_config('ao.service',$1,true)`, previous)
	if err != nil {
		return 0, err
	}
	return count, restoreErr
}

func (s *Store) CreateAgentLogin(ctx context.Context, p domain.Principal, input agentconnect.Attempt, maxSandboxes int) (agentconnect.Attempt, bool, error) {
	var result agentconnect.Attempt
	created := false
	err := s.withTenant(ctx, p, input.OrgID, func(tx pgx.Tx) error {
		if err := lockOrgTx(ctx, tx, input.OrgID); err != nil {
			return err
		}
		var owner bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ao_org_memberships WHERE org_id=$1 AND user_id=$2 AND role='owner' AND status='active')`, input.OrgID, p.UserID).Scan(&owner); err != nil {
			return err
		}
		if !owner || input.UserID != p.UserID {
			return ErrForbidden
		}
		// Serialize idempotency across harnesses as well as the active harness
		// slot, so one key cannot launch two different jobs concurrently.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('agent-login-key:' || $1 || ':' || $2,0))`, p.UserID, input.IdempotencyKey); err != nil {
			return err
		}
		if err := lockAgentLogin(ctx, tx, p.UserID, input.Harness); err != nil {
			return err
		}
		if err := expireAgentLogins(ctx, tx, p.UserID); err != nil {
			return err
		}
		existing, err := scanAgentLogin(tx.QueryRow(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts WHERE user_id=$1 AND idempotency_key=$2`, p.UserID, input.IdempotencyKey))
		if err == nil {
			if existing.OrgID != input.OrgID || existing.Harness != input.Harness {
				return ErrIdempotencyMismatch
			}
			result = existing
			return nil
		}
		if !errors.Is(err, agentconnect.ErrNotFound) {
			return err
		}
		jobCount, err := agentLoginReservationsTx(ctx, tx, input.OrgID)
		if err != nil {
			return err
		}
		var sandboxCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_sandboxes WHERE org_id=$1
            AND observed_state NOT IN ('deleted','deleting','terminated','failed')`, input.OrgID).Scan(&sandboxCount); err != nil {
			return err
		}
		if maxSandboxes < 1 || sandboxCount+jobCount >= maxSandboxes {
			return ErrSandboxQuotaExceeded
		}
		policy := s.billing.forPrincipal(p, input.OrgID)
		if err := policy.planAdmissionTx(ctx, tx, input.OrgID, "worker", admitCreate, time.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET status='cancelled',input_ciphertext=NULL,input_nonce=NULL,
            authorization_url='',user_code='',requires_code=false,updated_at=now()
            WHERE user_id=$1 AND harness=$2 AND status IN ('starting','waiting','connecting')`, p.UserID, input.Harness); err != nil {
			return err
		}
		result, err = scanAgentLogin(tx.QueryRow(ctx, `INSERT INTO ao_agent_login_attempts
            (id,user_id,org_id,harness,status,idempotency_key,runner_token_hash,expires_at,job_id)
            VALUES($1,$2,$3,$4,'starting',$5,$6,now()+interval '10 minutes',$7) RETURNING `+agentLoginColumns,
			input.ID, p.UserID, input.OrgID, input.Harness, input.IdempotencyKey, input.TokenHash, input.JobID))
		created = err == nil
		return err
	})
	return result, created, err
}

func (s *Store) GetAgentLogin(ctx context.Context, userID, id string) (agentconnect.Attempt, error) {
	var a agentconnect.Attempt
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		if err := expireAgentLogins(ctx, tx, userID); err != nil {
			return err
		}
		var err error
		a, err = scanAgentLogin(tx.QueryRow(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts WHERE user_id=$1 AND id=$2`, userID, id))
		return err
	})
	return a, err
}

func (s *Store) FindAgentLoginRunner(ctx context.Context, id string, hash []byte) (agentconnect.Attempt, error) {
	var userID string
	err := s.withService(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT user_id FROM ao_agent_login_attempts WHERE id=$1 AND runner_token_hash=$2`, id, hash).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return agentconnect.ErrNotFound
		}
		return err
	})
	if err != nil {
		return agentconnect.Attempt{}, err
	}
	return s.GetAgentLogin(ctx, userID, id)
}

func (s *Store) ConsumeAgentLoginCode(ctx context.Context, userID, id string, hash []byte) (agentconnect.Attempt, error) {
	var a agentconnect.Attempt
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		if err := expireAgentLogins(ctx, tx, userID); err != nil {
			return err
		}
		var err error
		a, err = scanAgentLogin(tx.QueryRow(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts WHERE user_id=$1 AND id=$2 AND runner_token_hash=$3 FOR UPDATE`, userID, id, hash))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET input_ciphertext=NULL,input_nonce=NULL WHERE id=$1`, id)
		return err
	})
	return a, err
}

func (s *Store) SetAgentLoginCode(ctx context.Context, userID, id string, encrypted, nonce []byte) (agentconnect.Attempt, error) {
	var a agentconnect.Attempt
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		var err error
		a, err = scanAgentLogin(tx.QueryRow(ctx, `UPDATE ao_agent_login_attempts SET input_ciphertext=$3,input_nonce=$4,
            status='connecting',requires_code=false,updated_at=now()
            WHERE user_id=$1 AND id=$2 AND harness='claude-code' AND status='waiting' AND requires_code AND expires_at>clock_timestamp()
            RETURNING `+agentLoginColumns, userID, id, encrypted, nonce))
		if errors.Is(err, agentconnect.ErrNotFound) {
			return agentconnect.ErrConflict
		}
		if err == nil {
			var unexpired bool
			if err := tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM ao_agent_login_attempts WHERE id=$1`, id).Scan(&unexpired); err != nil {
				return err
			}
			if !unexpired {
				return agentconnect.ErrConflict
			}
		}
		return err
	})
	return a, err
}

func (s *Store) CancelAgentLogin(ctx context.Context, userID, id string) (agentconnect.Attempt, error) {
	var a agentconnect.Attempt
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		var err error
		a, err = scanAgentLogin(tx.QueryRow(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts WHERE user_id=$1 AND id=$2`, userID, id))
		if err != nil {
			return err
		}
		if err := lockAgentLogin(ctx, tx, userID, a.Harness); err != nil {
			return err
		}
		if err := expireAgentLogins(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET status='cancelled',input_ciphertext=NULL,input_nonce=NULL,
            authorization_url='',user_code='',requires_code=false,updated_at=now()
            WHERE user_id=$1 AND id=$2 AND status IN ('starting','waiting','connecting')`, userID, id); err != nil {
			return err
		}
		a, err = scanAgentLogin(tx.QueryRow(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts WHERE id=$1`, id))
		return err
	})
	return a, err
}

func (s *Store) UpdateAgentLogin(ctx context.Context, input agentconnect.Attempt, u agentconnect.Update, encrypted, nonce []byte) error {
	return s.withUser(ctx, input.UserID, func(tx pgx.Tx) error {
		if err := lockAgentLogin(ctx, tx, input.UserID, input.Harness); err != nil {
			return err
		}
		a, err := scanAgentLogin(tx.QueryRow(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts
            WHERE user_id=$1 AND id=$2 AND runner_token_hash=$3 AND status IN ('starting','waiting','connecting') AND expires_at>clock_timestamp() FOR UPDATE`, input.UserID, input.ID, input.TokenHash))
		if errors.Is(err, agentconnect.ErrNotFound) {
			return agentconnect.ErrConflict
		}
		if err != nil {
			return err
		}
		if u.Status == "connected" {
			if len(encrypted) == 0 || len(nonce) == 0 {
				return ErrInvalid
			}
			config, _ := json.Marshal(map[string]string{"credentialType": u.CredentialType})
			if _, err := tx.Exec(ctx, `SELECT set_config('ao.agent_login_id',$1,true)`, a.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ao_user_provider_connections (user_id,provider,label,encrypted_secret,secret_nonce,config,validation_state,validated_at)
                VALUES($1,$2,'default',$3,$4,$5,'valid',now()) ON CONFLICT(user_id,provider,label) DO UPDATE
                SET encrypted_secret=EXCLUDED.encrypted_secret,secret_nonce=EXCLUDED.secret_nonce,config=EXCLUDED.config,
                    validation_state='valid',validated_at=now(),updated_at=now()`, a.UserID, a.Harness, encrypted, nonce, config); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET status=$2,
            authorization_url=CASE WHEN $2 IN ('connected','failed') THEN '' WHEN $3<>'' THEN $3 ELSE authorization_url END,
            user_code=CASE WHEN $2 IN ('connected','failed') THEN '' ELSE $4 END,
            requires_code=CASE WHEN $2 IN ('connected','failed') THEN false ELSE $5 END,
            input_ciphertext=CASE WHEN $2 IN ('connected','failed') THEN NULL ELSE input_ciphertext END,
            input_nonce=CASE WHEN $2 IN ('connected','failed') THEN NULL ELSE input_nonce END,updated_at=now()
			WHERE id=$1 AND status IN ('starting','waiting','connecting') AND expires_at>clock_timestamp()`, a.ID, u.Status, u.AuthorizationURL, u.UserCode, u.RequiresCode)
		if err == nil && tag.RowsAffected() != 1 {
			// This check runs after any credential-row/index lock waits. Refusal
			// rolls the preceding encrypted credential upsert back as well.
			return agentconnect.ErrConflict
		}
		return err
	})
}

func (s *Store) SetAgentLoginJob(ctx context.Context, userID, id, job string) (bool, error) {
	var active bool
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE ao_agent_login_attempts SET job_id=$3,launch_finished=true,updated_at=now() WHERE user_id=$1 AND id=$2
            RETURNING status IN ('starting','waiting','connecting') AND expires_at>clock_timestamp()`, userID, id, job).Scan(&active)
	})
	return active, err
}

func (s *Store) FinishAgentLoginJob(ctx context.Context, userID, id, job string) error {
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET job_id='',updated_at=now()
            WHERE user_id=$1 AND id=$2 AND job_id=$3 AND status NOT IN ('starting','waiting','connecting')`, userID, id, job)
		return err
	})
}

func (s *Store) RetryAgentLoginJob(ctx context.Context, userID, id, job string) error {
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET cleanup_after=clock_timestamp()+
            CASE WHEN expires_at<clock_timestamp()-interval '1 day' THEN interval '1 day'
                 WHEN expires_at<clock_timestamp()-interval '1 hour' THEN interval '1 hour'
                 ELSE interval '1 minute' END,updated_at=now()
            WHERE user_id=$1 AND id=$2 AND job_id=$3`, userID, id, job)
		return err
	})
}

func (s *Store) AgentLoginsNeedingCleanup(ctx context.Context) ([]agentconnect.Attempt, error) {
	var candidates []agentconnect.Attempt
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+agentLoginColumns+` FROM ao_agent_login_attempts WHERE job_id<>'' AND cleanup_after<=clock_timestamp()
			AND ((status NOT IN ('starting','waiting','connecting') AND launch_finished) OR expires_at<=clock_timestamp()) ORDER BY cleanup_after,expires_at LIMIT 100`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAgentLogin(rows)
			if err != nil {
				return err
			}
			candidates = append(candidates, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := make([]agentconnect.Attempt, 0, len(candidates))
	for _, a := range candidates {
		current, err := s.GetAgentLogin(ctx, a.UserID, a.ID)
		if errors.Is(err, agentconnect.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !agentconnect.Active(current.Status) && current.JobID != "" {
			result = append(result, current)
		}
	}
	return result, nil
}
