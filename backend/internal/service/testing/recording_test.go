package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type movieDesktop struct {
	*policyDesktop
	result            ports.TestingRecordingResult
	startErr, stopErr error
	escapePath        string
}

func (d *movieDesktop) StartRecording(_ context.Context, _ domain.TestTargetIdentity, dir string) (ports.TestingRecordingResult, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ports.TestingRecordingResult{}, err
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ports.TestingRecordingResult{}, err
	}
	d.result = ports.TestingRecordingResult{Path: filepath.Join(dir, "window.mov"), MIMEType: "video/quicktime", Width: 2, Height: 2, StartedAt: d.clock.Now(), RecorderPID: 800, StagingCleanup: "pending: recording in progress"}
	if d.startErr != nil {
		d.result.Gap = "owned recorder startup observation failed"
	}
	return d.result, d.startErr
}
func (d *movieDesktop) StopRecording(ctx context.Context, _ domain.TestTargetIdentity) (ports.TestingRecordingResult, error) {
	d.mu.Lock()
	d.cleanupEvents = append(d.cleanupEvents, "recording_stop")
	d.mu.Unlock()
	if ctx.Err() != nil {
		return d.result, ctx.Err()
	}
	d.result.Duration = time.Second
	d.result.StoppedAt = d.result.StartedAt.Add(time.Second)
	d.result.StagingPath = "/native/staging/owned-window.mov"
	d.result.StagingCleanup = "verified absent after final move"
	d.result.Gap = ""
	if d.stopErr != nil {
		d.result.Gap = "owned recorder did not finalize"
		return d.result, d.stopErr
	}
	if d.escapePath != "" {
		d.result.Path = d.escapePath
	}
	if err := os.WriteFile(d.result.Path, []byte("fake-finalized-movie"), 0600); err != nil {
		return d.result, err
	}
	return d.result, nil
}

func movieFixture(t *testing.T, configure func(*movieDesktop)) (*fixture, *movieDesktop) {
	t.Helper()
	var desktop *movieDesktop
	f := newFixture(t, func(deps *Deps) {
		desktop = &movieDesktop{policyDesktop: &policyDesktop{fakeProviders: deps.Desktop.(*fakeProviders), mode: "background"}}
		if configure != nil {
			configure(desktop)
		}
		deps.Desktop = desktop
	})
	return f, desktop
}

func TestRecordingFinishCancelAndDeadlineSaveEvidenceBeforeTargetStop(t *testing.T) {
	for _, finish := range []string{"finish", "cancel", "deadline", "partial_start"} {
		t.Run(finish, func(t *testing.T) {
			f, desktop := movieFixture(t, func(d *movieDesktop) {
				if finish == "partial_start" {
					d.startErr = errors.New("startup birth observation failed")
				}
			})
			switch finish {
			case "finish":
				if _, err := f.call("report", "submit_report", domain.TestSubmitReportRequest{Outcome: domain.TestOutcomePartial, Markdown: "Observed target."}); err != nil {
					t.Fatal(err)
				}
			case "deadline":
				f.clock.advance(time.Minute)
			default:
				if _, err := f.svc.Cancel(context.Background(), f.start.AttemptID); err != nil {
					t.Fatal(err)
				}
			}
			f.wait(t)
			if !reflect.DeepEqual(f.provider.cleanupEvents, []string{"recording_stop", "desktop_release", "target_stop"}) {
				t.Fatal("recording cleanup order", f.provider.cleanupEvents)
			}
			rec, _, err := f.store.GetTestAttempt(context.Background(), f.start.AttemptID)
			if err != nil || rec.RecordingGap != "" || rec.CleanupState != domain.TestCleanupComplete {
				t.Fatal("successful recording retained a gap", rec, err)
			}
			if err := os.Remove(desktop.result.Path); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.dir, "testing", string(f.run.ID), string(rec.ID))
			receipts, err := f.svc.ListEvidence(context.Background(), rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			foundVideo, foundMetadata := false, false
			for _, receipt := range receipts {
				switch receipt.Kind {
				case "recording":
					data, err := os.ReadFile(filepath.Join(dir, receipt.RelativePath))
					if err != nil || string(data) != "fake-finalized-movie" || receipt.MIMEType != "video/quicktime" || !strings.HasSuffix(receipt.RelativePath, ".mov") {
						t.Fatal("recording did not survive target/source stop", err)
					}
					foundVideo = true
				case "recording_metadata":
					foundMetadata = true
				}
			}
			if !foundVideo || !foundMetadata {
				t.Fatal("recording receipts missing", receipts)
			}
			journal, err := os.ReadFile(filepath.Join(dir, "actions.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			foundResult := false
			for _, line := range bytes.Split(bytes.TrimSpace(journal), []byte("\n")) {
				var record domain.TestActionRecord
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if record.Tool != "stop_recording" || record.State != "completed" {
					continue
				}
				var result ports.TestingRecordingResult
				if err := json.Unmarshal(record.Recording, &result); err != nil || !reflect.DeepEqual(result, desktop.result) {
					t.Fatal("full recording/staging result missing from journal", result, err)
				}
				foundResult = true
			}
			if !foundResult || bytes.Contains(journal, []byte(f.worker.binding.Capability)) {
				t.Fatal("recording journal missing or capability leaked")
			}
		})
	}
}

func TestRecordingFailureRetainsGapAndStillStopsTarget(t *testing.T) {
	for _, failure := range []string{"provider_stop", "recording", "recording_metadata", "foreign_path"} {
		t.Run(failure, func(t *testing.T) {
			f, desktop := movieFixture(t, nil)
			switch failure {
			case "provider_stop":
				desktop.stopErr = errors.New("stop failed")
			case "foreign_path":
				desktop.escapePath = filepath.Join(t.TempDir(), "foreign.mov")
			default:
				f.evidence.failKind = failure
			}
			_, _ = f.svc.Cancel(context.Background(), f.start.AttemptID)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := f.svc.WaitCleanup(ctx, f.start.AttemptID); err == nil {
				t.Fatal("recording failure reported successful cleanup")
			}
			rec, _, err := f.store.GetTestAttempt(ctx, f.start.AttemptID)
			if err != nil || rec.RecordingGap == "" || rec.CleanupState != domain.TestCleanupFailed || f.provider.stops != 1 {
				t.Fatal("recording failure lost gap or skipped target stop", rec, err)
			}
		})
	}
}
