package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/pkg/tasknaming"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/jackc/pgx/v5"
)

// NameWorkerTask reserves a project-wide branch namespace before the worker
// moves its workspace, then commits the adapter's actual name and an event.
func (s *Store) NameWorkerTask(
	ctx context.Context,
	orgID, sessionID, workerID string,
	epoch int64,
	input domain.TaskNameRequest,
) (domain.TaskNameResult, error) {
	flags := 0
	for _, flag := range []bool{input.Claim, input.Skip, input.Complete, input.Retry} {
		if flag {
			flags++
		}
	}
	if flags > 1 {
		return domain.TaskNameResult{}, ErrInvalid
	}
	var result domain.TaskNameResult
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		// Serialize generation admission across projects in this organization.
		if input.Claim {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "ao-task-generation:"+orgID); err != nil {
				return err
			}
		}
		var projectID string
		if err := tx.QueryRow(ctx, `SELECT project_id FROM ao_sessions WHERE org_id=$1 AND id=$2`, orgID, sessionID).Scan(&projectID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		// All naming claims for this project's refs share one transaction lock.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "ao-task-name:"+orgID+":"+projectID); err != nil {
			return err
		}
		// Hold the connection through commit so replacement cannot invalidate
		// an epoch between its check and the session update.
		var lockedEpoch int64
		if err := tx.QueryRow(ctx, `SELECT epoch FROM ao_worker_connections
			WHERE org_id=$1 AND session_id=$2 AND worker_id=$3 AND epoch=$4 AND disconnected_at IS NULL
			FOR UPDATE`, orgID, sessionID, workerID, epoch).Scan(&lockedEpoch); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrStaleWorker
			}
			return err
		}
		current, err := workerEpochCurrent(ctx, tx, orgID, sessionID, epoch)
		if err != nil {
			return err
		}
		if !current {
			return ErrStaleWorker
		}
		var kind, prompt, agentSessionID string
		var terminated bool
		var config json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT se.kind, se.prompt, se.agent_session_id, se.is_terminated,
			se.task_naming_state, se.display_name, se.branch, se.task_name_slug, project.config
			FROM ao_sessions se JOIN ao_projects project ON project.id=se.project_id AND project.org_id=se.org_id
			WHERE se.org_id=$1 AND se.id=$2 FOR UPDATE OF se`, orgID, sessionID).Scan(
			&kind, &prompt, &agentSessionID, &terminated, &result.State, &result.Title, &result.Branch, &result.Slug, &config,
		); err != nil {
			return err
		}
		if kind != "worker" || terminated {
			return ErrConflict
		}
		if result.State == domain.TaskNamingDone {
			return nil
		}
		if agentSessionID != "" {
			return ErrConflict
		}
		if input.Skip {
			if result.State != domain.TaskNamingPending {
				return ErrConflict
			}
			result.State, result.Slug = domain.TaskNamingDone, ""
			if _, err := tx.Exec(ctx, `UPDATE ao_sessions SET task_naming_state='done', task_name_slug='', task_name_generation_at=NULL, updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, sessionID); err != nil {
				return err
			}
			return appendTypedEvent(ctx, tx, orgID, sessionID, "workspace.changed", result)
		}
		if input.Claim {
			if result.State != domain.TaskNamingPending {
				return nil
			}
			var alreadyClaimed bool
			if err := tx.QueryRow(ctx, `SELECT COALESCE(task_name_generation_at > clock_timestamp()-make_interval(secs => $3), false) FROM ao_sessions WHERE org_id=$1 AND id=$2`, orgID, sessionID, tasknaming.Timeout.Seconds()).Scan(&alreadyClaimed); err != nil {
				return err
			}
			if alreadyClaimed {
				return nil
			}
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_sessions WHERE org_id=$1 AND task_naming_state='pending' AND NOT is_terminated AND agent_session_id='' AND task_name_generation_at > clock_timestamp()-make_interval(secs => $2)`, orgID, tasknaming.Timeout.Seconds()).Scan(&count); err != nil {
				return err
			}
			if count >= tasknaming.MaxConcurrent {
				return nil
			}
			_, err := tx.Exec(ctx, `UPDATE ao_sessions SET task_name_generation_at=clock_timestamp() WHERE org_id=$1 AND id=$2`, orgID, sessionID)
			result.Generate = err == nil
			return err
		}
		prefix, err := domain.ProjectSessionPrefix(config)
		if err != nil {
			return ErrInvalid
		}
		if result.State == domain.TaskNamingReserved {
			// A settings edit cannot change an in-flight reservation's prefix.
			var ok bool
			prefix, ok = strings.CutSuffix(result.Branch, "/"+result.Slug+"/root")
			if !ok || prefix == "" {
				return ErrConflict
			}
		}
		occupied, err := taskNameBranches(ctx, tx, orgID, projectID, sessionID)
		if err != nil {
			return err
		}
		// Extra repositories are cloned beside the primary task worktree.
		// Reserve their existing directory names before choosing the primary.
		if cfg, ok := domain.DecodeProjectCoderConfig(config); ok {
			for _, repo := range cfg.ExtraRepos {
				occupied = append(occupied, worker.ExtraRepoDirName(repo.URL))
			}
		}
		if input.Complete {
			if result.State != domain.TaskNamingReserved {
				return ErrConflict
			}
			actualSlug, ok := completedTaskSlug(prefix, result.Slug, input.Branch)
			if !ok {
				return ErrInvalid
			}
			if taskNameOccupied(prefix, actualSlug, occupied) {
				return ErrConflict
			}
			result.State, result.Branch, result.Slug = domain.TaskNamingDone, input.Branch, actualSlug
			if _, err := tx.Exec(ctx, `UPDATE ao_sessions SET task_naming_state=$3, branch=$4, task_name_slug=$5, task_name_generation_at=NULL, updated_at=now()
				WHERE org_id=$1 AND id=$2`, orgID, sessionID, result.State, result.Branch, result.Slug); err != nil {
				return err
			}
			return appendTypedEvent(ctx, tx, orgID, sessionID, "workspace.changed", result)
		}
		if result.State == domain.TaskNamingReserved && !input.Retry {
			return nil
		}
		if input.Retry && result.State != domain.TaskNamingReserved {
			return ErrConflict
		}
		encoded, err := json.Marshal(tasknaming.Name{Title: input.Title, Slug: input.Slug})
		if err != nil {
			return err
		}
		fallback := tasknaming.Fallback(prompt, sessionID)
		if input.Retry {
			fallback.Slug = result.Slug
		}
		name := tasknaming.Parse(string(encoded), fallback)
		occupied = append(occupied, input.Occupied...)
		if input.Retry {
			occupied = append(occupied, result.Branch)
		}
		candidate := ""
		for suffix := 1; suffix <= 100; suffix++ {
			slug := name.Slug
			if suffix > 1 {
				slug = fmt.Sprintf("%s-%d", slug, suffix)
			}
			if !taskNameOccupied(prefix, slug, occupied) {
				candidate = slug
				break
			}
		}
		if candidate == "" {
			return ErrConflict
		}
		result.State, result.Slug, result.Branch = domain.TaskNamingReserved, candidate, prefix+"/"+candidate+"/root"
		if err := tx.QueryRow(ctx, `UPDATE ao_sessions
			SET task_naming_state=$3, task_name_slug=$4, branch=$5, task_name_generation_at=NULL,
				display_name=CASE WHEN task_name_initial_title IS NOT NULL AND display_name=task_name_initial_title AND $6<>'' THEN $6 ELSE display_name END,
				updated_at=now()
			WHERE org_id=$1 AND id=$2 RETURNING display_name`, orgID, sessionID, result.State, result.Slug, result.Branch, name.Title).Scan(&result.Title); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return domain.TaskNameResult{}, err
	}
	return result, nil
}

func taskNameBranches(ctx context.Context, tx pgx.Tx, orgID, projectID, sessionID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT branch FROM ao_sessions WHERE org_id=$1 AND project_id=$2 AND id<>$3`, orgID, projectID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var branches []string
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch); err != nil {
			return nil, err
		}
		branches = append(branches, "refs/heads/"+branch)
	}
	return branches, rows.Err()
}

func completedTaskSlug(prefix, reserved, branch string) (string, bool) {
	slug, ok := strings.CutPrefix(branch, prefix+"/")
	if !ok {
		return "", false
	}
	slug, ok = strings.CutSuffix(slug, "/root")
	if !ok {
		return "", false
	}
	if slug == reserved {
		return slug, true
	}
	suffix, ok := strings.CutPrefix(slug, reserved+"-")
	if !ok {
		return "", false
	}
	n, err := strconv.Atoi(suffix)
	return slug, err == nil && n >= 2 && n <= 100 && strconv.Itoa(n) == suffix
}

func taskNameOccupied(prefix, slug string, occupied []string) bool {
	namespace := prefix + "/" + slug
	for _, value := range occupied {
		value = strings.TrimSpace(value)
		isRef := strings.HasPrefix(value, "refs/")
		if strings.HasPrefix(value, "refs/remotes/") {
			_, value, _ = strings.Cut(strings.TrimPrefix(value, "refs/remotes/"), "/")
		} else {
			value = strings.TrimPrefix(value, "refs/heads/")
			value = strings.TrimPrefix(value, "origin/")
		}
		value = strings.TrimSuffix(value, "/")
		// Workers can report either refs or sibling workspace directory names.
		if !isRef && (value == slug || path.IsAbs(value) && path.Base(value) == slug) ||
			value == namespace || strings.HasPrefix(value, namespace+"/") ||
			(isRef || strings.Contains(value, "/")) && value != "" && strings.HasPrefix(namespace, value+"/") {
			return true
		}
	}
	return false
}
