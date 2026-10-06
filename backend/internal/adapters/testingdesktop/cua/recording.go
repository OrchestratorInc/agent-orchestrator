package cua

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// RecordingResult declares the window-only recording gap. Cua 0.34.0's
// start_recording records the main display and cannot satisfy this boundary.
type RecordingResult struct {
	Gap string `json:"gap"`
}

// StartRecording returns an explicit gap without invoking display recording.
// evidenceDir is daemon-owned; no file is written while this gap applies.
func (a *Adapter) StartRecording(ctx context.Context, target domain.TestTargetIdentity, _ string) (RecordingResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.bound(ctx, target); err != nil {
		return RecordingResult{}, err
	}
	return RecordingResult{Gap: "Cua Driver 0.34.0 video captures the main display only; no window or region option. Window-only video is unavailable."}, nil
}

// StopRecording retains the explicit gap; this adapter never starts a recorder.
func (a *Adapter) StopRecording(ctx context.Context, target domain.TestTargetIdentity) (RecordingResult, error) {
	return a.StartRecording(ctx, target, "")
}
