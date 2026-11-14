package policyauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/google/uuid"
)

const FileName = "telemetry_policy.json"

type Snapshot struct {
	Present           bool
	EventsEnabled     bool
	ConsentGeneration string
}

type Reader struct{ path string }

func New(path string) *Reader { return &Reader{path: path} }

func (r *Reader) Read(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	info, err := os.Lstat(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, nil
		}
		return Snapshot{}, fmt.Errorf("inspect telemetry policy authority: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return Snapshot{}, errors.New("telemetry policy authority is unsafe")
	}
	file, err := os.Open(r.path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open telemetry policy authority: %w", err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var keys map[string]json.RawMessage
	if err := decoder.Decode(&keys); err != nil || (len(keys) != 4 && len(keys) != 5) || keys["schema_version"] == nil || keys["events_enabled"] == nil || keys["consent_generation"] == nil || keys["updated_at"] == nil {
		return Snapshot{}, errors.New("telemetry policy authority is malformed")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Snapshot{}, errors.New("telemetry policy authority has trailing data")
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return Snapshot{}, fmt.Errorf("normalize telemetry policy authority: %w", err)
	}
	decoder = json.NewDecoder(io.LimitReader(bytes.NewReader(raw), 4097))
	decoder.DisallowUnknownFields()
	var record diskRecord
	if err := decoder.Decode(&record); err != nil || !record.versionShapeValid() || uuid.Validate(record.ConsentGeneration) != nil || !validTimestamp(record.UpdatedAt) {
		return Snapshot{}, errors.New("telemetry policy authority fields are invalid")
	}
	return Snapshot{
		Present: true, EventsEnabled: record.EventsEnabled, ConsentGeneration: record.ConsentGeneration,
	}, nil
}

type diskRecord struct {
	SchemaVersion            int    `json:"schema_version"`
	EventsEnabled            bool   `json:"events_enabled"`
	ConsentGeneration        string `json:"consent_generation"`
	ConsentProductionEnabled *bool  `json:"consent_production_enabled"`
	UpdatedAt                string `json:"updated_at"`
}

func (r diskRecord) versionShapeValid() bool {
	switch r.SchemaVersion {
	case 1:
		return r.ConsentProductionEnabled == nil
	case 2:
		return r.ConsentProductionEnabled != nil
	default:
		return false
	}
}

func validTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Location() == time.UTC
}

func (r *Reader) EventsAllowed(ctx context.Context, envEvents, envExplicit bool) bool {
	if !envEvents || !envExplicit {
		return false
	}
	snapshot, err := r.Read(ctx)
	if err != nil {
		return false
	}
	return !snapshot.Present || snapshot.EventsEnabled
}
