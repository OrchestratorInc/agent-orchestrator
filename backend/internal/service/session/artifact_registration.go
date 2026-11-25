package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/sessionartifacts"
)

// ArtifactRegistration is the outcome of RegisterArtifacts. OtherFiles counts
// the session's files that are not registered; OtherRootFiles names those at
// the root of the artifact directory, the ones most likely to be a deliverable
// the agent has not registered yet.
type ArtifactRegistration struct {
	Registered     []string
	OtherFiles     int
	OtherRootFiles []string
}

// RegisterArtifacts registers the files or bundle directories at references as
// the session's deliverables. Each is an absolute path in the session's
// artifact directory or a path relative to it. workingDir, when set, is the
// folder the agent's shell ran ao artifact add in: a relative reference is
// read from there when that folder is inside the artifact directory (see
// sessionartifacts.FromWorkingDir). Nothing is registered unless every
// reference is valid. Registering a path again changes nothing.
func (s *Service) RegisterArtifacts(ctx context.Context, id domain.SessionID, workingDir string, references []string) (ArtifactRegistration, error) {
	rec, err := s.GetRecord(ctx, id)
	if err != nil {
		return ArtifactRegistration{}, err
	}
	dir := cmp.Or(rec.Metadata.ArtifactDir, sessionartifacts.Dir(s.dataDir, id))
	source := domain.ArtifactSource(rec.Kind, rec.RequestedOutput)
	paths := make([]string, 0, len(references))
	registrations := make([]domain.SessionArtifactRegistration, 0, len(references))
	for _, reference := range references {
		registration, err := sessionartifacts.Registrable(dir, sessionartifacts.FromWorkingDir(dir, workingDir, reference))
		if err != nil {
			s.emitArtifactRegisterFailed(source, artifactRegisterFailure(err))
			return ArtifactRegistration{}, registrationError(dir, reference, err)
		}
		paths = append(paths, registration.Path)
		registrations = append(registrations, registration)
	}
	known, knownOK := s.registeredArtifactPaths(ctx, id)
	registered, found, err := s.store.AddSessionArtifacts(ctx, id, registrations, s.clock())
	if err != nil {
		s.emitArtifactRegisterFailed(source, domain.ArtifactRegisterInternal)
		return ArtifactRegistration{}, err
	}
	if !found {
		// Deleted since its record was read above.
		s.emitArtifactRegisterFailed(source, domain.ArtifactRegisterSessionNotFound)
		return ArtifactRegistration{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	files, err := sessionartifacts.ListRegistered(ctx, dir, registered)
	// Sent before a failed listing returns: the registration itself went through.
	if knownOK {
		s.emitArtifactsRegistered(source, paths, known, files)
	}
	if err != nil {
		return ArtifactRegistration{}, fmt.Errorf("list artifacts of %s: %w", id, err)
	}
	result := ArtifactRegistration{Registered: paths, OtherRootFiles: []string{}}
	for _, file := range files {
		if file.Registered {
			continue
		}
		result.OtherFiles++
		if !strings.Contains(file.Path, "/") {
			result.OtherRootFiles = append(result.OtherRootFiles, file.Path)
		}
	}
	return result, nil
}

// registeredArtifactPaths is the session's registered paths before this
// registration, so registering a path again is not counted as a new artifact.
// It reports false when telemetry is off or the read fails.
func (s *Service) registeredArtifactPaths(ctx context.Context, id domain.SessionID) (map[string]bool, bool) {
	if s.telemetry == nil {
		return nil, false
	}
	rows, err := s.store.ListSessionArtifactsForSessions(ctx, []domain.SessionID{id})
	if err != nil {
		return nil, false
	}
	known := map[string]bool{}
	for _, row := range rows[id] {
		if row.RegisteredAt != nil {
			known[row.Path] = true
		}
	}
	return known, true
}

// emitArtifactsRegistered sends one ao.artifacts.registered per content
// category and size band among the newly registered paths, with how many
// there were and where they came from. A path's size is read from files, the
// listing this registration already made; the band is left out when the
// listing does not have it. Never a path, name or session.
func (s *Service) emitArtifactsRegistered(source string, paths []string, known map[string]bool, files []domain.SessionArtifactFile) {
	type group struct{ category, size string }
	counts := map[group]int{}
	for _, path := range paths {
		if known[path] {
			continue
		}
		known[path] = true
		size, listed := registeredSize(path, files)
		g := group{category: domain.ArtifactContentCategory(path)}
		if listed {
			g.size = domain.ArtifactSizeBucket(size)
		}
		counts[g]++
	}
	groups := slices.SortedFunc(maps.Keys(counts), func(a, b group) int {
		return cmp.Or(cmp.Compare(a.category, b.category), cmp.Compare(a.size, b.size))
	})
	for _, g := range groups {
		payload := map[string]any{"source": source, "content_category": g.category, "count": counts[g]}
		if g.size != "" {
			payload["size_bucket"] = g.size
		}
		s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
			Name:       "ao.artifacts.registered",
			Source:     "session_service",
			OccurredAt: s.now(),
			Level:      ports.TelemetryLevelInfo,
			Payload:    payload,
		})
	}
}

// registeredSize is the size of the file at path, or of every file in the
// bundle directory at path, as files lists them, and whether any was listed.
func registeredSize(path string, files []domain.SessionArtifactFile) (int64, bool) {
	var size int64
	listed := false
	for _, file := range files {
		if file.Path == path || strings.HasPrefix(file.Path, path+"/") {
			size += file.Size
			listed = true
		}
	}
	return size, listed
}

// artifactRegisterFailure is the reason category of a path the daemon
// refused to register.
func artifactRegisterFailure(err error) string {
	switch {
	case errors.Is(err, sessionartifacts.ErrOutsideDir):
		return domain.ArtifactRegisterOutsideDir
	case errors.Is(err, sessionartifacts.ErrNotFound):
		return domain.ArtifactRegisterNotFound
	case errors.Is(err, sessionartifacts.ErrNotRegistrable):
		return domain.ArtifactRegisterNotRegistrable
	}
	return domain.ArtifactRegisterInternal
}

// emitArtifactRegisterFailed sends ao.artifacts.register_failed with why the
// daemon refused a registration: the reason category, never the path.
func (s *Service) emitArtifactRegisterFailed(source, reason string) {
	if s.telemetry == nil {
		return
	}
	s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
		Name:       "ao.artifacts.register_failed",
		Source:     "session_service",
		OccurredAt: s.now(),
		Level:      ports.TelemetryLevelInfo,
		Payload:    map[string]any{"source": source, "reason": reason},
	})
}

// UnregisterArtifact forgets one registered path, after the user deleted it.
func (s *Service) UnregisterArtifact(ctx context.Context, id domain.SessionID, path string) error {
	return s.store.RemoveSessionArtifact(ctx, id, path)
}

// registrationError tells the agent why reference was refused and names the
// artifact directory, so its next try can put the file there or point at it.
func registrationError(dir, reference string, err error) error {
	where := "the session's artifact directory, " + filepath.ToSlash(dir)
	switch {
	case errors.Is(err, sessionartifacts.ErrOutsideDir):
		message := reference + " is not in " + where + ". Copy or move the file there, then run ao artifact add again."
		if sessionartifacts.SplitAtSpace(dir, reference) {
			message += " The path looks cut off at a space: put quotes around a path that contains spaces."
		}
		return apierr.Invalid("ARTIFACT_PATH_OUTSIDE_DIR", message, nil)
	case errors.Is(err, sessionartifacts.ErrNotFound):
		return apierr.NotFound("ARTIFACT_FILE_NOT_FOUND", reference+" does not exist in "+where+
			". A relative path is read from that directory, or from the current folder when that folder is inside it.")
	case errors.Is(err, sessionartifacts.ErrNotRegistrable):
		return apierr.Invalid("ARTIFACT_NOT_REGISTRABLE", reference+" is not a file or a bundle directory.", nil)
	}
	return err
}
