package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/sessionartifacts"
)

// keptArtifactStore records the artifacts a person keeps when they delete
// their session. It is optional so focused store fakes need not implement it.
type keptArtifactStore interface {
	KeepSessionArtifacts(ctx context.Context, kept domain.KeptArtifactSession) error
	ListKeptArtifactSessions(ctx context.Context) ([]domain.KeptArtifactSession, error)
	GetKeptArtifactSession(ctx context.Context, id domain.SessionID) (domain.KeptArtifactSession, bool, error)
	ListKeptArtifacts(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactRecord, error)
	RemoveKeptArtifact(ctx context.Context, id domain.SessionID, path string) error
	ForgetKeptArtifactSession(ctx context.Context, id domain.SessionID) error
	ListRemovedProjectSessionIDs(ctx context.Context) ([]domain.SessionID, error)
}

// KeptArtifacts is one set of artifacts no session or project lists: those of
// a session deleted with its artifacts kept, or of a session in a removed
// project. They are view only.
type KeptArtifacts struct {
	SessionID domain.SessionID
	// ProjectID is the session's project, empty when it has none any more.
	ProjectID domain.ProjectID
	Title     string
	// SessionDeleted is set when the session itself is gone; otherwise its
	// project was removed and the session row remains.
	SessionDeleted bool
	// At is when the session was deleted, or last updated.
	At    time.Time
	Files []domain.SessionArtifactFile
}

// DeleteKeepingArtifacts deletes a terminated session the way Delete does but
// keeps its artifact files, view only, until they are deleted one by one. A
// session without artifact files is simply deleted.
func (s *Service) DeleteKeepingArtifacts(ctx context.Context, id domain.SessionID, cleanup bool) error {
	dir := sessionartifacts.Dir(s.dataDir, id)
	hasFiles, err := sessionartifacts.HasFiles(ctx, dir)
	if err != nil {
		return fmt.Errorf("delete %s: check artifacts: %w", id, err)
	}
	if !hasFiles {
		return s.Delete(ctx, id, cleanup)
	}
	keeper, ok := s.store.(keptArtifactStore)
	deleter, canDelete := s.manager.(sessionDeleteCommander)
	if !ok || !canDelete {
		return apierr.NotImplemented("SESSION_DELETE_UNSUPPORTED", "Keeping artifacts is not supported by this daemon")
	}
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return toAPIError(err)
	}
	if !found {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	// Kept files are read from <dataDir>/artifacts/<id>; ones written
	// elsewhere could not be found again once the session is gone.
	if stored := rec.Metadata.ArtifactDir; stored != "" && filepath.Clean(stored) != dir {
		return fmt.Errorf("delete %s: artifact dir %q is outside the data dir", id, stored)
	}
	title := presentedTerminalTitle(rec, s.storedTerminalTitles(ctx, []domain.SessionID{id})[id])
	if err := keeper.KeepSessionArtifacts(ctx, domain.KeptArtifactSession{
		SessionID: id, ProjectID: rec.ProjectID, Title: cmp.Or(title, recordTitle(rec)), KeptAt: s.now(),
	}); err != nil {
		return err
	}
	if err := deleter.DeleteSession(ctx, id, cleanup); err != nil {
		return toAPIError(err)
	}
	s.cancelTitleRefinement(id)
	// Version history is a session's, and its rows went with it.
	if err := os.RemoveAll(filepath.Join(dir, sessionartifacts.VersionsDir)); err != nil && s.logger != nil {
		s.logger.Warn("delete version history of kept artifacts", "session", id, "err", err)
	}
	return nil
}

// forgetKeptArtifacts drops a kept record for a session whose files are gone.
func (s *Service) forgetKeptArtifacts(ctx context.Context, id domain.SessionID) {
	keeper, ok := s.store.(keptArtifactStore)
	if !ok {
		return
	}
	if err := keeper.ForgetKeptArtifactSession(ctx, id); err != nil && s.logger != nil {
		s.logger.Warn("forget kept artifacts", "session", id, "err", err)
	}
}

// keptTitle is the title a session's kept artifacts are listed under.
func keptTitle(sess domain.Session) string {
	return cmp.Or(sess.TerminalTitle, recordTitle(sess.SessionRecord))
}

// recordTitle is a session's title without its terminal program's.
func recordTitle(rec domain.SessionRecord) string {
	return cmp.Or(rec.DisplayName, string(rec.IssueID), string(rec.ID))
}

// ListKeptArtifacts lists the artifacts of deleted sessions that kept them,
// most recently deleted first, then those of sessions in removed projects.
// A set with no files left is left out.
func (s *Service) ListKeptArtifacts(ctx context.Context) ([]KeptArtifacts, error) {
	keeper, ok := s.store.(keptArtifactStore)
	if !ok {
		return nil, nil
	}
	kept, err := keeper.ListKeptArtifactSessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []KeptArtifacts
	for _, k := range kept {
		files, err := s.keptFiles(ctx, keeper, k.SessionID)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("list kept artifacts", "session", k.SessionID, "err", err)
			}
			continue
		}
		if len(files) == 0 {
			continue
		}
		out = append(out, KeptArtifacts{SessionID: k.SessionID, ProjectID: k.ProjectID, Title: k.Title, SessionDeleted: true, At: k.KeptAt, Files: files})
	}
	removed, err := keeper.ListRemovedProjectSessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range removed {
		sess, err := s.Get(ctx, id)
		if err != nil {
			if s.logger != nil && !isNotFound(err) {
				s.logger.Warn("list artifacts of removed project session", "session", id, "err", err)
			}
			continue
		}
		if len(sess.ArtifactFiles) == 0 {
			continue
		}
		out = append(out, KeptArtifacts{SessionID: id, Title: keptTitle(sess), At: sess.UpdatedAt, Files: sess.ArtifactFiles})
	}
	return out, nil
}

// keptFiles lists the files kept with a deleted session, with their links.
func (s *Service) keptFiles(ctx context.Context, keeper keptArtifactStore, id domain.SessionID) ([]domain.SessionArtifactFile, error) {
	rows, err := keeper.ListKeptArtifacts(ctx, id)
	if err != nil {
		return nil, err
	}
	observation, err := sessionartifacts.Observe(ctx, sessionartifacts.Dir(s.dataDir, id), sessionartifacts.RegisteredPaths(rows))
	if err != nil {
		return nil, err
	}
	files := observation.Apply(rows)
	s.shareLinks.Apply(id, files)
	return files, nil
}

// ArtifactSession returns the session whose artifact files a request reads:
// the session itself, or, for a session deleted with its artifacts kept, a
// terminated stand-in holding only its artifact directory and files.
func (s *Service) ArtifactSession(ctx context.Context, id domain.SessionID) (sess domain.Session, kept bool, err error) {
	sess, err = s.Get(ctx, id)
	if err == nil || !isNotFound(err) {
		return sess, false, err
	}
	keeper, ok := s.store.(keptArtifactStore)
	if !ok {
		return domain.Session{}, false, err
	}
	record, found, lookupErr := keeper.GetKeptArtifactSession(ctx, id)
	if lookupErr != nil {
		return domain.Session{}, false, lookupErr
	}
	if !found {
		return domain.Session{}, false, err
	}
	files, filesErr := s.keptFiles(ctx, keeper, id)
	if filesErr != nil {
		return domain.Session{}, false, filesErr
	}
	stand := domain.Session{ArtifactFiles: files}
	stand.ID = id
	stand.ProjectID = record.ProjectID
	stand.DisplayName = record.Title
	stand.IsTerminated = true
	stand.Metadata.ArtifactDir = sessionartifacts.Dir(s.dataDir, id)
	return stand, true, nil
}

// ForgetKeptArtifact forgets a kept artifact whose file was just deleted.
// Once the session has no files left, its directory and record go too.
func (s *Service) ForgetKeptArtifact(ctx context.Context, id domain.SessionID, path string) error {
	keeper, ok := s.store.(keptArtifactStore)
	if !ok {
		return nil
	}
	if err := keeper.RemoveKeptArtifact(ctx, id, path); err != nil {
		return err
	}
	dir := sessionartifacts.Dir(s.dataDir, id)
	hasFiles, err := sessionartifacts.HasFiles(ctx, dir)
	if err != nil || hasFiles {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove kept artifact dir of %s: %w", id, err)
	}
	return keeper.ForgetKeptArtifactSession(ctx, id)
}

func isNotFound(err error) bool {
	var apiErr *apierr.Error
	return errors.As(err, &apiErr) && apiErr.Kind == apierr.KindNotFound
}
