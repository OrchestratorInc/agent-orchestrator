package acp

import (
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// settleOpenItems emits a turn's open items while a late session/update for
// that turn can still write the same maps and tool states. It must read a copy
// taken under the lock: iterating the live maps raced with processUpdate and
// failed CI under -race (TestACPSteerPromptWithoutAnAcknowledgementIsARefusal).
func TestSettleOpenItemsReadsACopyWhileUpdatesStillArrive(t *testing.T) {
	c := &conversation{
		events:         make(chan ports.ChatEvent, 1<<16),
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		messages:       map[string]string{"m": "hello"},
		thoughts:       map[string]string{"r": "thinking"},
		nestedMessages: map[string]nestedMessageState{"n": {text: "sub", parentID: "p"}},
		tools: map[string]*toolState{
			"t": {id: "t", status: acpsdk.ToolCallStatusInProgress},
			"w": {id: "w", status: acpsdk.ToolCallStatusInProgress},
		},
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range c.events {
		}
	}()

	var writers sync.WaitGroup
	writers.Go(func() {
		for i := range 2000 {
			key := strconv.Itoa(i)
			c.mu.Lock()
			c.messages["m"+key] = "late"
			c.thoughts["r"+key] = "late"
			c.nestedMessages["n"+key] = nestedMessageState{text: "late"}
			// Field writes go to "w"; "t" is left alone so a settlement that
			// mutated the live tool would still show in the check below.
			c.tools["w"].status = acpsdk.ToolCallStatusInProgress
			c.tools["t"+key] = &toolState{id: "t" + key, status: acpsdk.ToolCallStatusPending}
			c.mu.Unlock()
		}
	})
	for range 50 {
		c.settleOpenItems("turn-1", nil)
	}
	writers.Wait()
	c.eventMu.Lock()
	c.eventsClosed = true
	close(c.events)
	c.eventMu.Unlock()
	<-drained

	// The live tool keeps its own state: settling reports a copy as failed.
	c.mu.Lock()
	defer c.mu.Unlock()
	if got := c.tools["t"].status; got != acpsdk.ToolCallStatusInProgress {
		t.Fatalf("settling changed the live tool's status to %q", got)
	}
}

// A late session/update can complete a tool after settlement snapshotted it
// as open. Settlement checks the live state before emitting its synthetic
// failure, so a completed tool is not overwritten as failed.
func TestToolStillOpenFollowsTheLiveTool(t *testing.T) {
	c := &conversation{}
	tools := map[string]*toolState{
		"done":    {id: "done", status: acpsdk.ToolCallStatusCompleted},
		"failed":  {id: "failed", status: acpsdk.ToolCallStatusFailed},
		"running": {id: "running", status: acpsdk.ToolCallStatusInProgress},
		"pending": {id: "pending", status: acpsdk.ToolCallStatusPending},
		"unset":   {id: "unset"},
	}
	for id, want := range map[string]bool{"done": false, "failed": false, "running": true, "pending": true, "unset": true} {
		if got := c.toolStillOpen(tools, id); got != want {
			t.Errorf("toolStillOpen(%q) = %v, want %v", id, got, want)
		}
	}
}
