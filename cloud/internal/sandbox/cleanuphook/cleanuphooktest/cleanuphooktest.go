// Package cleanuphooktest helps provider tests check the project Cleanup a
// provider runs before it deletes a sandbox (package cleanuphook).
package cleanuphooktest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// Recorder collects the Cleanup stage and the session events of the provider
// calls made with its Context.
type Recorder struct {
	mu     sync.Mutex
	stages []sandbox.StageReport
	events []worker.ScriptStepEvent
	other  []string
}

// Context returns ctx with the recorder as stage observer and session event
// recorder.
func (r *Recorder) Context(ctx context.Context) context.Context {
	ctx = sandbox.WithStageObserver(ctx, func(_ context.Context, report sandbox.StageReport) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.stages = append(r.stages, report)
	})
	return sandbox.WithSessionEventRecorder(ctx, func(_ context.Context, eventType string, payload any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if event, ok := payload.(worker.ScriptStepEvent); ok && eventType == worker.ScriptStepEventType {
			r.events = append(r.events, event)
			return
		}
		r.other = append(r.other, eventType)
	})
}

// Stage returns the last Cleanup stage reported.
func (r *Recorder) Stage() (sandbox.StageReport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := len(r.stages) - 1; index >= 0; index-- {
		if r.stages[index].Stage == cleanuphook.Stage {
			return r.stages[index], true
		}
	}
	return sandbox.StageReport{}, false
}

// WaitStage waits up to five seconds for the Cleanup stage.
func (r *Recorder) WaitStage(t testing.TB) sandbox.StageReport {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if stage, ok := r.Stage(); ok {
			return stage
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the project cleanup stage")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Events returns the step events appended so far; it fails the test if any
// other session event was appended.
func (r *Recorder) Events(t testing.TB) []worker.ScriptStepEvent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.other) > 0 {
		t.Fatalf("unexpected session events %v", r.other)
	}
	return append([]worker.ScriptStepEvent(nil), r.events...)
}

var (
	payloadPattern = regexp.MustCompile(`ao_payload=\W*([A-Za-z0-9+/=]{16,})`)
	noncePattern   = regexp.MustCompile(`@@ao-step-([0-9a-f]{16}) `)
)

// Script decodes the Cleanup commands script a hook command carries and its
// marker nonce; both are "" when it carries none.
func Script(t testing.TB, command string) (script, nonce string) {
	t.Helper()
	match := payloadPattern.FindStringSubmatch(command)
	if match == nil {
		return "", ""
	}
	compressed, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gunzip payload: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gunzip payload: %v", err)
	}
	script = string(decoded)
	if match := noncePattern.FindStringSubmatch(script); match != nil {
		nonce = match[1]
	}
	return script, nonce
}
