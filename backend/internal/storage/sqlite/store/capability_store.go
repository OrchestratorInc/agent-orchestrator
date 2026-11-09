package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Harness capability evidence. Two scopes, two tables: a harness-scoped
// observation is keyed by the harness, a session-scoped one by the session. See
// migration 20261104020830811 for why the natural key was not expressed as one
// table with a COALESCE primary key.
//
// Nothing here grades or filters: the store persists what a recorder observed and
// returns it verbatim. Whether an observation may authorize a feature is the
// capability package's decision, and splitting that across two layers is how a
// store ends up silently upgrading evidence.

// UpsertHarnessCapabilitySnapshot stores one harness-scoped observation, replacing
// any previous one for the same harness.
func (s *Store) UpsertHarnessCapabilitySnapshot(ctx context.Context, snapshot domain.HarnessCapabilitySnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("record capability snapshot for %s: %w", snapshot.Harness, err)
	}
	if snapshot.SessionScoped() {
		return fmt.Errorf("record capability snapshot for %s: a session-scoped observation belongs on its session",
			snapshot.Harness)
	}
	params, err := harnessCapabilityParams(snapshot)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertHarnessCapabilitySnapshot(ctx, params); err != nil {
		return fmt.Errorf("record capability snapshot for %s: %w", snapshot.Harness, err)
	}
	return nil
}

// GetHarnessCapabilitySnapshot loads the stored harness-scoped observation.
func (s *Store) GetHarnessCapabilitySnapshot(
	ctx context.Context,
	harness domain.AgentHarness,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	row, err := s.qr.GetHarnessCapabilitySnapshot(ctx, string(harness))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.HarnessCapabilitySnapshot{}, false, nil
	}
	if err != nil {
		return domain.HarnessCapabilitySnapshot{}, false,
			fmt.Errorf("load capability snapshot for %s: %w", harness, err)
	}
	return decodeHarnessCapability(row), true, nil
}

// UpsertSessionCapabilitySnapshot stores one session-scoped observation,
// replacing the previous one for that session.
func (s *Store) UpsertSessionCapabilitySnapshot(ctx context.Context, snapshot domain.HarnessCapabilitySnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("record session capability snapshot for %s: %w", snapshot.ObservedSessionID, err)
	}
	if !snapshot.SessionScoped() {
		return fmt.Errorf("record session capability snapshot for %s: a harness-scoped observation names no session",
			snapshot.Harness)
	}
	params, err := sessionCapabilityParams(snapshot)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSessionCapabilitySnapshot(ctx, params); err != nil {
		return fmt.Errorf("record session capability snapshot for %s: %w", snapshot.ObservedSessionID, err)
	}
	return nil
}

// GetSessionCapabilitySnapshot loads the stored observation for one session.
func (s *Store) GetSessionCapabilitySnapshot(
	ctx context.Context,
	sessionID domain.SessionID,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	row, err := s.qr.GetSessionCapabilitySnapshot(ctx, string(sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.HarnessCapabilitySnapshot{}, false, nil
	}
	if err != nil {
		return domain.HarnessCapabilitySnapshot{}, false,
			fmt.Errorf("load session capability snapshot for %s: %w", sessionID, err)
	}
	return decodeSessionCapability(row), true, nil
}

func harnessCapabilityParams(
	snapshot domain.HarnessCapabilitySnapshot,
) (gen.UpsertHarnessCapabilitySnapshotParams, error) {
	models, err := encodeCapabilityModels(snapshot)
	if err != nil {
		return gen.UpsertHarnessCapabilitySnapshotParams{}, err
	}
	return gen.UpsertHarnessCapabilitySnapshotParams{
		Harness:    string(snapshot.Harness),
		Source:     string(snapshot.Source),
		AuthState:  string(snapshot.AuthState),
		Readiness:  string(snapshot.Readiness),
		Surfaces:   string(snapshot.Surfaces),
		Streaming:  string(snapshot.Streaming),
		Tools:      string(snapshot.Tools),
		Interrupt:  string(snapshot.Interrupt),
		Steer:      string(snapshot.Steer),
		Queue:      string(snapshot.Queue),
		Resume:     string(snapshot.Resume),
		Approval:   string(snapshot.Approval),
		ApplyAt:    string(snapshot.ApplyAt),
		Usage:      string(snapshot.Usage),
		ModelsJson: models,
		Executable: snapshot.Executable,
		Version:    snapshot.Version,
		ReasonCode: snapshot.ReasonCode,
		ObservedAt: snapshot.ObservedAt.UTC(),
		ExpiresAt:  snapshot.ExpiresAt.UTC(),
	}, nil
}

func sessionCapabilityParams(
	snapshot domain.HarnessCapabilitySnapshot,
) (gen.UpsertSessionCapabilitySnapshotParams, error) {
	models, err := encodeCapabilityModels(snapshot)
	if err != nil {
		return gen.UpsertSessionCapabilitySnapshotParams{}, err
	}
	return gen.UpsertSessionCapabilitySnapshotParams{
		SessionID:  string(snapshot.ObservedSessionID),
		Harness:    string(snapshot.Harness),
		Generation: snapshot.Generation,
		Source:     string(snapshot.Source),
		AuthState:  string(snapshot.AuthState),
		Readiness:  string(snapshot.Readiness),
		Surfaces:   string(snapshot.Surfaces),
		Streaming:  string(snapshot.Streaming),
		Tools:      string(snapshot.Tools),
		Interrupt:  string(snapshot.Interrupt),
		Steer:      string(snapshot.Steer),
		Queue:      string(snapshot.Queue),
		Resume:     string(snapshot.Resume),
		Approval:   string(snapshot.Approval),
		ApplyAt:    string(snapshot.ApplyAt),
		Usage:      string(snapshot.Usage),
		ModelsJson: models,
		Executable: snapshot.Executable,
		Version:    snapshot.Version,
		ReasonCode: snapshot.ReasonCode,
		ObservedAt: snapshot.ObservedAt.UTC(),
		ExpiresAt:  snapshot.ExpiresAt.UTC(),
	}, nil
}

// encodeCapabilityModels stores the advertised catalog as JSON. An absent catalog
// is NULL rather than '[]' or 'null': the column is display metadata, and a null
// keeps "no models were advertised" distinguishable from "the set was empty".
func encodeCapabilityModels(snapshot domain.HarnessCapabilitySnapshot) (sql.NullString, error) {
	if len(snapshot.Models) == 0 {
		return sql.NullString{}, nil
	}
	encoded, err := json.Marshal(snapshot.Models)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encode capability models: %w", err)
	}
	return sql.NullString{String: string(encoded), Valid: true}, nil
}

func decodeHarnessCapability(row gen.HarnessCapabilitySnapshot) domain.HarnessCapabilitySnapshot {
	snapshot := domain.HarnessCapabilitySnapshot{Models: decodeCapabilityModels(row.ModelsJson)}
	snapshot.Harness = domain.AgentHarness(row.Harness)
	snapshot.Source = domain.CapabilitySource(row.Source)
	snapshot.AuthState = domain.AgentAuthenticationState(row.AuthState)
	snapshot.Readiness = domain.CapabilityReadiness(row.Readiness)
	snapshot.Surfaces = domain.CapabilitySurface(row.Surfaces)
	snapshot.Streaming = domain.CapabilityState(row.Streaming)
	snapshot.Tools = domain.CapabilityState(row.Tools)
	snapshot.Interrupt = domain.CapabilityState(row.Interrupt)
	snapshot.Steer = domain.CapabilityState(row.Steer)
	snapshot.Queue = domain.CapabilityState(row.Queue)
	snapshot.Resume = domain.CapabilityResume(row.Resume)
	snapshot.Approval = domain.CapabilityApproval(row.Approval)
	snapshot.ApplyAt = domain.CapabilityApplyAt(row.ApplyAt)
	snapshot.Usage = domain.CapabilityUsage(row.Usage)
	snapshot.Executable = row.Executable
	snapshot.Version = row.Version
	snapshot.ReasonCode = row.ReasonCode
	snapshot.ObservedAt = row.ObservedAt.UTC()
	snapshot.ExpiresAt = row.ExpiresAt.UTC()
	return snapshot
}

func decodeSessionCapability(row gen.SessionCapabilitySnapshot) domain.HarnessCapabilitySnapshot {
	snapshot := domain.HarnessCapabilitySnapshot{Models: decodeCapabilityModels(row.ModelsJson)}
	snapshot.Harness = domain.AgentHarness(row.Harness)
	snapshot.Source = domain.CapabilitySource(row.Source)
	snapshot.AuthState = domain.AgentAuthenticationState(row.AuthState)
	snapshot.Readiness = domain.CapabilityReadiness(row.Readiness)
	snapshot.Surfaces = domain.CapabilitySurface(row.Surfaces)
	snapshot.Streaming = domain.CapabilityState(row.Streaming)
	snapshot.Tools = domain.CapabilityState(row.Tools)
	snapshot.Interrupt = domain.CapabilityState(row.Interrupt)
	snapshot.Steer = domain.CapabilityState(row.Steer)
	snapshot.Queue = domain.CapabilityState(row.Queue)
	snapshot.Resume = domain.CapabilityResume(row.Resume)
	snapshot.Approval = domain.CapabilityApproval(row.Approval)
	snapshot.ApplyAt = domain.CapabilityApplyAt(row.ApplyAt)
	snapshot.Usage = domain.CapabilityUsage(row.Usage)
	snapshot.Executable = row.Executable
	snapshot.Version = row.Version
	snapshot.ReasonCode = row.ReasonCode
	snapshot.ObservedSessionID = domain.SessionID(row.SessionID)
	snapshot.Generation = row.Generation
	snapshot.ObservedAt = row.ObservedAt.UTC()
	snapshot.ExpiresAt = row.ExpiresAt.UTC()
	return snapshot
}

// decodeCapabilityModels reads the display catalog. Undecodable metadata must not
// smuggle a capability claim in: the row still answers for every other column, so
// a bad catalog becomes no catalog rather than a failed read.
func decodeCapabilityModels(encoded sql.NullString) []domain.CapabilityModelChoice {
	if !encoded.Valid || encoded.String == "" {
		return nil
	}
	var models []domain.CapabilityModelChoice
	if err := json.Unmarshal([]byte(encoded.String), &models); err != nil {
		return nil
	}
	return models
}
