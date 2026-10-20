package triage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type hostClient struct {
	t         *testing.T
	input     *io.PipeWriter
	responses chan Response
	done      chan error
	cancel    context.CancelFunc
}

func serveForTest(t *testing.T, dir string, deps Dependencies) *hostClient {
	t.Helper()
	in, writer := io.Pipe()
	reader, out := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	c := &hostClient{t: t, input: writer, responses: make(chan Response, 256), done: make(chan error, 1), cancel: cancel}
	go func() {
		defer close(c.responses)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		for scanner.Scan() {
			var response Response
			if json.Unmarshal(scanner.Bytes(), &response) == nil {
				c.responses <- response
			}
		}
	}()
	go func() {
		err := Serve(ctx, in, out, Options{DataDir: dir, Deps: deps})
		_ = out.Close()
		_ = in.Close()
		c.done <- err
		close(c.done)
	}()
	t.Cleanup(func() {
		_ = writer.Close()
		cancel()
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Error("triage host did not stop")
		}
		_ = reader.Close()
	})
	return c
}

func (c *hostClient) send(cmd Command) {
	c.t.Helper()
	if err := json.NewEncoder(c.input).Encode(cmd); err != nil {
		c.t.Fatal(err)
	}
}

func (c *hostClient) stop() {
	c.t.Helper()
	c.cancel()
	select {
	case err := <-c.done:
		if err != nil {
			c.t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		c.t.Fatal("triage host did not stop")
	}
}

func (c *hostClient) wait(predicate func(Response) bool) Response {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case response, ok := <-c.responses:
			if !ok {
				c.t.Fatal("triage host ended before expected response")
			}
			if response.Version != ProtocolVersion {
				c.t.Fatalf("unexpected response version: %d", response.Version)
			}
			if predicate(response) {
				return response
			}
		case <-deadline:
			c.t.Fatal("timed out waiting for triage response")
		}
	}
}

func (c *hostClient) response(id string) Response {
	return c.wait(func(response Response) bool { return response.ID == id })
}

func (c *hostClient) state(state string) Run {
	return *c.wait(func(response Response) bool { return response.Run != nil && response.Run.State == state }).Run
}

func (c *hostClient) responseAndState(id, state string) (Response, Run) {
	return c.responseAndRun(id, func(run Run) bool { return run.State == state })
}

func (c *hostClient) responseAndRun(id string, matches func(Run) bool) (Response, Run) {
	c.t.Helper()
	var acknowledged *Response
	var settled *Run
	c.wait(func(response Response) bool {
		if response.ID == id {
			if response.Type == "error" {
				c.t.Fatalf("%s failed: %s", id, response.Error)
			}
			acknowledged = &response
		}
		if response.Run != nil && matches(*response.Run) {
			settled = response.Run
		}
		return acknowledged != nil && settled != nil
	})
	return *acknowledged, *settled
}

func TestHostClientRetainsTerminalUpdateBeforeCancelAcknowledgement(t *testing.T) {
	c := &hostClient{t: t, responses: make(chan Response, 2)}
	c.responses <- Response{Version: ProtocolVersion, Type: "run", Run: &Run{State: "interrupted"}}
	c.responses <- Response{Version: ProtocolVersion, ID: "cancel", Type: "run", Run: &Run{State: "starting"}}
	acknowledged, settled := c.responseAndState("cancel", "interrupted")
	if acknowledged.ID != "cancel" || settled.State != "interrupted" {
		t.Fatalf("lost interleaved cancellation result: %#v, %#v", acknowledged, settled)
	}
}

func configuredDeps() Dependencies {
	return Dependencies{Catalog: func(context.Context) ([]Choice, error) {
		return []Choice{{Harness: "test", Available: true,
			Models:   []ModelChoice{{ID: "chosen-model", Efforts: []string{"xhigh"}}},
			Accounts: []Account{{ID: "chosen-account"}}}}, nil
	}}
}

func startCommand(id string, authorized bool) Command {
	return Command{ID: id, Type: "start", Start: &StartRequest{
		Selection:   Selection{Harness: "test", Model: "chosen-model", Effort: "xhigh", AccountID: "chosen-account"},
		Observation: "The session stays blank after opening it.", PublishAuthorized: authorized,
	}}
}

type fakeTriageConversation struct {
	events        chan ports.ChatEvent
	started       chan ports.ChatUserMessage
	terminated    chan struct{}
	terminateOnce sync.Once
	turns         atomic.Int32
	closeCalls    atomic.Int32
	sendHook      func(ports.ChatUserMessage) error
	deferredHook  func(string) error
	ackHook       func(string) error
	resolveHook   func(string, ports.ChatDecision) error
	inputHook     func(string, ports.ChatInputResponse) error
}

func fakeConversation() *fakeTriageConversation {
	return &fakeTriageConversation{events: make(chan ports.ChatEvent, 32), started: make(chan ports.ChatUserMessage, 4), terminated: make(chan struct{})}
}

func (c *fakeTriageConversation) ProviderConversationID() string { return "provider-conversation" }
func (c *fakeTriageConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityApprovals: true, ports.ChatCapabilityInterrupt: true, ports.ChatCapabilityResume: true}
}
func (c *fakeTriageConversation) SendTurn(_ context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if c.sendHook != nil {
		if err := c.sendHook(msg); err != nil {
			return ports.ChatTurnRef{}, err
		}
	}
	turn := c.turns.Add(1)
	c.started <- msg
	return ports.ChatTurnRef{ProviderTurnID: "turn-" + string('0'+turn)}, nil
}
func (c *fakeTriageConversation) Interrupt(context.Context, string) error { return nil }
func (c *fakeTriageConversation) ResolveRequest(_ context.Context, id string, decision ports.ChatDecision) error {
	if c.resolveHook != nil {
		return c.resolveHook(id, decision)
	}
	return nil
}
func (c *fakeTriageConversation) ResolveInput(_ context.Context, id string, response ports.ChatInputResponse) error {
	if c.inputHook != nil {
		return c.inputHook(id, response)
	}
	return nil
}
func (c *fakeTriageConversation) Events() <-chan ports.ChatEvent { return c.events }
func (c *fakeTriageConversation) Close() error                   { c.closeCalls.Add(1); return nil }
func (c *fakeTriageConversation) Terminate() error {
	c.terminateOnce.Do(func() { close(c.terminated) })
	return nil
}
func (c *fakeTriageConversation) StartDeferredTurn(id string) error {
	if c.deferredHook != nil {
		return c.deferredHook(id)
	}
	return nil
}
func (c *fakeTriageConversation) DiscardDeferredTurn(string) {}
func (c *fakeTriageConversation) AcknowledgeProviderEvent(_ context.Context, id string) error {
	if c.ackHook != nil {
		return c.ackHook(id)
	}
	return nil
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for provider signal")
	}
}

func TestHostReadOnlyCommandsNeedNoDaemonConfigOrProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_RUN_FILE", filepath.Join(dir, "does-not-exist", "running.json"))
	c := serveForTest(t, dir, Dependencies{})
	for _, kind := range []string{"catalog", "list"} {
		c.send(Command{ID: kind, Type: kind})
		if response := c.response(kind); response.Type == "error" {
			t.Fatalf("%s failed: %s", kind, response.Error)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "running.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only triage touched daemon run file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only triage created main config: %v", err)
	}
}

func TestSecondHelperCannotRecoverLiveInvestigation(t *testing.T) {
	dir := t.TempDir()
	deps := configuredDeps()
	deps.Collect = func(ctx context.Context, _ Origin) ([]Evidence, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c := serveForTest(t, dir, deps)
	request := handoffRequest(t, c, Origin{})
	var recovered atomic.Int32
	err := Serve(context.Background(), strings.NewReader(""), io.Discard, Options{DataDir: dir, Deps: Dependencies{
		RecoverConversation: func(context.Context, Run) error { recovered.Add(1); return nil },
	}})
	if !errors.Is(err, errRunOwned) || recovered.Load() != 0 {
		t.Fatalf("second helper reached recovery: %v, calls=%d", err, recovered.Load())
	}
	c.send(Command{ID: "get-live", Type: "get", RunID: request.PreparationID})
	if run := c.response("get-live").Run; run == nil || run.State != "collecting" {
		t.Fatalf("second helper changed the live preparation: %#v", run)
	}
}

func TestHostMalformedAndOversizedCommands(t *testing.T) {
	c := serveForTest(t, t.TempDir(), Dependencies{})
	if _, err := io.WriteString(c.input, "\nnot-json\n{\"id\":\"unknown\",\"type\":\"wat\"}\n{\"id\":\"extra\",\"type\":\"list\",\"secret\":true}\n"); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4; n++ {
		response := c.wait(func(response Response) bool { return response.Type == "error" })
		if response.Error == "" {
			t.Fatal("malformed command returned no error")
		}
	}
	c.send(Command{ID: "long-reply", Type: "start", Start: &StartRequest{Observation: strings.Repeat("x", maxReplyBytes+1)}})
	if response := c.response("long-reply"); response.Type != "error" {
		t.Fatal("oversized observation was accepted")
	}
	if _, err := io.WriteString(c.input, strings.Repeat("x", maxCommandBytes+4)+"\n"); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	c.wait(func(response Response) bool {
		return response.Type == "error" && strings.Contains(response.Error, "1 MiB")
	})
}
