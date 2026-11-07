package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
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
	for _, reference := range references {
		path, err := sessionartifacts.Registrable(dir, reference)
		if err != nil {
			return ArtifactRegistration{}, registrationError(reference, err)
		}
		paths = append(paths, path)
	}
	registered, found, err := s.store.AddSessionArtifacts(ctx, id, paths, s.clock())
	if err != nil {
		return ArtifactRegistration{}, err
	}
	if !found {
		return ArtifactRegistration{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
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
