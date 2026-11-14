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

type ArtifactRegistration struct {
	Registered     []string
	OtherFiles     int
	OtherRootFiles []string
}

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
	registered, found, err := s.store.AddSessionArtifacts(ctx, id, registrations, s.clock())
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
