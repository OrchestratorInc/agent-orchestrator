package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) UpsertProject(ctx context.Context, r domain.ProjectRecord) error {
	config, err := marshalProjectConfig(r.Config)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return upsertProject(ctx, s.qw, r, config)
}

func (s *Store) UpsertWorkspaceProject(ctx context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	config, err := marshalProjectConfig(r.Config)
	if err != nil {
		return err
	}
	return s.writeWorkspaceProject(ctx, "upsert workspace project", r, repos, func(q *gen.Queries) error {
		return upsertProject(ctx, q, r, config)
	})
}

func (s *Store) ImportWorkspaceProject(ctx context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	config, err := marshalProjectConfig(r.Config)
	if err != nil {
		return err
	}
	return s.writeWorkspaceProject(ctx, "import workspace project", r, repos, func(q *gen.Queries) error {
		return importProject(ctx, q, r, config)
	})
}

func (s *Store) writeWorkspaceProject(ctx context.Context, label string, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord, writeProject func(*gen.Queries) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, label, func(q *gen.Queries) error {
		if err := writeProject(q); err != nil {
			return err
		}
		if err := q.DeleteWorkspaceReposByProject(ctx, domain.ProjectID(r.ID)); err != nil {
			return err
		}
		for _, repo := range repos {
			if err := q.UpsertWorkspaceRepo(ctx, gen.UpsertWorkspaceRepoParams{
				ProjectID:     domain.ProjectID(r.ID),
				Name:          repo.Name,
				RelativePath:  repo.RelativePath,
				RepoOriginURL: repo.RepoOriginURL,
				DefaultBranch: repo.DefaultBranch,
				RegisteredAt:  repo.RegisteredAt,
				GitStatus:     string(repo.GitStatus.WithDefault()),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	rows, err := s.qr.ListWorkspaceRepos(ctx, domain.ProjectID(projectID))
	if err != nil {
		return nil, fmt.Errorf("list workspace repos for %s: %w", projectID, err)
	}
	out := make([]domain.WorkspaceRepoRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.WorkspaceRepoRecord{
			ProjectID:     row.ProjectID,
			Name:          row.Name,
			RelativePath:  row.RelativePath,
			RepoOriginURL: row.RepoOriginURL,
			DefaultBranch: row.DefaultBranch,
			RegisteredAt:  row.RegisteredAt,
			GitStatus:     domain.GitStatus(row.GitStatus),
		})
	}
	return out, nil
}

func upsertProject(ctx context.Context, q *gen.Queries, r domain.ProjectRecord, config sql.NullString) error {
	kind := r.Kind.WithDefault()
	return q.UpsertProject(ctx, gen.UpsertProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		Config:        config,
		Kind:          string(kind),
	})
}

func importProject(ctx context.Context, q *gen.Queries, r domain.ProjectRecord, config sql.NullString) error {
	existingByID, err := q.GetProject(ctx, domain.ProjectID(r.ID))
	if err == nil {
		if existingByID.ArchivedAt.Valid {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSameIDArchivedTarget,
				TargetID:   string(existingByID.ID),
				TargetPath: existingByID.Path,
			}}
		}
		if existingByID.Path != r.Path {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSameIDDifferentActivePath,
				TargetID:   string(existingByID.ID),
				TargetPath: existingByID.Path,
			}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check imported project id conflict: %w", err)
	}

	existingByPath, err := q.FindProjectByPath(ctx, r.Path)
	if err == nil {
		if existingByPath.ID != domain.ProjectID(r.ID) {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSamePathDifferentActiveID,
				TargetID:   string(existingByPath.ID),
				TargetPath: existingByPath.Path,
			}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check imported project path conflict: %w", err)
	}

	kind := r.Kind.WithDefault()
	return q.UpsertImportedProject(ctx, gen.UpsertImportedProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		Config:        config,
		Kind:          string(kind),
	})
}

func (s *Store) GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	return projectRowFromGen(p), true, nil
}

func (s *Store) FindProjectByPath(ctx context.Context, path string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.FindProjectByPath(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	return projectRowFromGen(p), true, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]domain.ProjectRecord, error) {
	rows, err := s.qr.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]domain.ProjectRecord, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectRowFromGen(p))
	}
	return out, nil
}

func (s *Store) UpdateProjectSettings(ctx context.Context, id, displayName string, config domain.ProjectConfig) (bool, error) {
	encodedConfig, err := marshalProjectConfig(config)
	if err != nil {
		return false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateProjectSettings(ctx, gen.UpdateProjectSettingsParams{
		ID:          domain.ProjectID(id),
		DisplayName: displayName,
		Config:      encodedConfig,
	})
	if err != nil {
		return false, fmt.Errorf("update project settings %s: %w", id, err)
	}
	return rows > 0, nil
}

func (s *Store) CountProjectsIncludingArchived(ctx context.Context) (int, error) {
	count, err := s.qr.CountProjectsIncludingArchived(ctx)
	if err != nil {
		return 0, fmt.Errorf("count projects including archived: %w", err)
	}
	return int(count), nil
}

func (s *Store) ArchiveProject(ctx context.Context, id string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ArchiveProject(ctx, gen.ArchiveProjectParams{
		ArchivedAt: nullTime(at),
		ID:         domain.ProjectID(id),
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func projectRowFromGen(p gen.Project) domain.ProjectRecord {
	r := domain.ProjectRecord{
		ID:            string(p.ID),
		Path:          p.Path,
		RepoOriginURL: p.RepoOriginURL,
		DisplayName:   p.DisplayName,
		RegisteredAt:  p.RegisteredAt,
		Kind:          domain.ProjectKind(p.Kind).WithDefault(),
		Config:        unmarshalProjectConfig(p.Config),
	}
	if p.ArchivedAt.Valid {
		r.ArchivedAt = p.ArchivedAt.Time
	}
	return r
}

func marshalProjectConfig(cfg domain.ProjectConfig) (sql.NullString, error) {
	if cfg.IsZero() {
		return sql.NullString{}, nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("marshal project config: %w", err)
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

func unmarshalProjectConfig(s sql.NullString) domain.ProjectConfig {
	if !s.Valid || s.String == "" {
		return domain.ProjectConfig{}
	}
	var cfg domain.ProjectConfig
	if err := json.Unmarshal([]byte(s.String), &cfg); err != nil {
		return domain.ProjectConfig{}
	}
	return cfg
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func (s *Store) SetProjectPermissions(ctx context.Context, id string, permissions domain.PermissionMode) (domain.ProjectRecord, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var row domain.ProjectRecord
	var updated bool
	err := s.inTx(ctx, "set project permissions", func(q *gen.Queries) error {
		stored, err := q.GetProject(ctx, domain.ProjectID(id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		row = projectRowFromGen(stored)
		if !row.ArchivedAt.IsZero() {
			return nil
		}
		worker := row.Config.AgentConfig.Permissions
		if row.Config.Worker.AgentConfig.Permissions != "" {
			worker = row.Config.Worker.AgentConfig.Permissions
		}
		orchestrator := row.Config.AgentConfig.Permissions
		if row.Config.Orchestrator.AgentConfig.Permissions != "" {
			orchestrator = row.Config.Orchestrator.AgentConfig.Permissions
		}
		if worker == "" {
			worker = domain.PermissionModeDefault
		}
		if orchestrator == "" {
			orchestrator = domain.PermissionModeDefault
		}
		for kind, mode := range map[domain.SessionKind]domain.PermissionMode{domain.KindWorker: worker, domain.KindOrchestrator: orchestrator} {
			if err := q.PinProjectSessionPermissions(ctx, gen.PinProjectSessionPermissionsParams{ProjectID: optionalProjectID(domain.ProjectID(id)), Kind: kind, Permissions: string(mode)}); err != nil {
				return err
			}
		}

		row.Config.AgentConfig.Permissions = permissions
		row.Config.Worker.AgentConfig.Permissions = ""
		row.Config.Orchestrator.AgentConfig.Permissions = ""
		config, err := marshalProjectConfig(row.Config)
		if err != nil {
			return err
		}
		rows, err := q.UpdateProjectSettings(ctx, gen.UpdateProjectSettingsParams{ID: domain.ProjectID(id), DisplayName: row.DisplayName, Config: config})
		updated = rows > 0
		return err
	})
	return row, updated, err
}
