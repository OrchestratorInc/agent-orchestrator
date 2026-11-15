package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	pathpkg "path"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
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
// artifact directory or a path relative to it. Nothing is registered unless
// every reference is valid. Registering a path again changes nothing.
func (s *Service) RegisterArtifacts(ctx context.Context, id domain.SessionID, references []string) (ArtifactRegistration, error) {
	rec, err := s.GetRecord(ctx, id)
	if err != nil {
		return ArtifactRegistration{}, err
	}
	dir := cmp.Or(rec.Metadata.ArtifactDir, sessionartifacts.Dir(s.dataDir, id))
	paths := make([]string, 0, len(references))
	registrations := make([]domain.SessionArtifactRegistration, 0, len(references))
	for _, reference := range references {
		registration, err := sessionartifacts.Registrable(dir, reference)
		if err != nil {
			return ArtifactRegistration{}, registrationError(reference, err)
		}
		paths = append(paths, registration.Path)
		registrations = append(registrations, registration)
	}
	known, knownOK := s.registeredArtifactPaths(ctx, id)
	registered, found, err := s.store.AddSessionArtifacts(ctx, id, registrations, s.clock())
	if err != nil {
		return ArtifactRegistration{}, err
	}
	if !found {
		return ArtifactRegistration{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if knownOK {
		s.emitArtifactsRegistered(paths, known)
	}
	files, err := sessionartifacts.ListRegistered(ctx, dir, registered)
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

// emitArtifactsRegistered sends one ao.artifacts.registered per file type
// among the newly registered paths, with how many there were. Only the type
// bucket and the count are sent, never a path, name or session.
func (s *Service) emitArtifactsRegistered(paths []string, known map[string]bool) {
	counts := map[string]int{}
	for _, path := range paths {
		if known[path] {
			continue
		}
		known[path] = true
		counts[telemetrymeta.ArtifactFileType(path, sessionartifacts.IsBundleDir(pathpkg.Base(path)))]++
	}
	for _, fileType := range slices.Sorted(maps.Keys(counts)) {
		s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
			Name:       "ao.artifacts.registered",
			Source:     "session_service",
			OccurredAt: s.now(),
			Level:      ports.TelemetryLevelInfo,
			Payload:    map[string]any{"file_type": fileType, "count": counts[fileType]},
		})
	}
}

// UnregisterArtifact forgets one registered path, after the user deleted it.
func (s *Service) UnregisterArtifact(ctx context.Context, id domain.SessionID, path string) error {
	return s.store.RemoveSessionArtifact(ctx, id, path)
}

func registrationError(reference string, err error) error {
	switch {
	case errors.Is(err, sessionartifacts.ErrOutsideDir):
		return apierr.Invalid("ARTIFACT_PATH_OUTSIDE_DIR", reference+" is not in the session's artifact directory.", nil)
	case errors.Is(err, sessionartifacts.ErrNotFound):
		return apierr.NotFound("ARTIFACT_FILE_NOT_FOUND", reference+" does not exist in the session's artifact directory.")
	case errors.Is(err, sessionartifacts.ErrNotRegistrable):
		return apierr.Invalid("ARTIFACT_NOT_REGISTRABLE", reference+" is not a file or a bundle directory.", nil)
	}
	return err
}
