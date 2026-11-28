package hermesacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The live Hermes tests run the operator's own `hermes` against a scripted
// OpenAI-compatible model served by the test, through AO's driver and
// persistent chat host. Run them explicitly with AO_LIVE_HERMES=1. Hermes gets
// a private HERMES_HOME holding only a config.yaml that points at the scripted
// model, and no provider keys, so nothing reaches a real model or the
// operator's ~/.hermes. CI never depends on them.

// liveSteerRunTime is how long the scripted model takes to answer guidance that
// Hermes runs as a prompt of its own. It is longer than the 10 s the client
// waits for a steer command's answer once the turn's own prompt has answered.
const liveSteerRunTime = 12 * time.Second

// liveHermesCommand is the slow command the scripted model asks Hermes to run.
const liveHermesCommand = "sleep 6; echo slow-check-done"

// TestLiveHermesACPSteersARunningTurn steers while Hermes runs a tool. Hermes
// queues `/steer` for the running turn and hands it to the model after the tool
// result. AO must report the steer taken and see one turn whose reply comes
// from the steered request.
func TestLiveHermesACPSteersARunningTurn(t *testing.T) {
	model := startLiveHermesModel(t)
	model.script = func(r modelRequest) modelReply {
		switch {
		case r.mentions("STEER-ME"):
			return modelReply{text: "STEERED-OK"}
		case r.lastRole == "tool":
			return modelReply{text: "NOT-STEERED"}
		default:
			return modelReply{command: liveHermesCommand}
		}
	}
	ctx, conversation := startLiveHermes(t, model)

	ref := sendLiveTurn(ctx, t, conversation, "Run the slow check.")
	steered := make(chan error, 1)
	steering := false
	var reply strings.Builder
	for done := false; !done; {
		event := nextLiveEvent(ctx, t, conversation)
		switch event.Kind {
		case ports.ChatEventMessageDelta:
			reply.WriteString(event.Delta)
		case ports.ChatEventApprovalRequested:
			resolveLiveApproval(ctx, t, conversation, event)
		case ports.ChatEventActivityStarted:
			t.Logf("tool started: %s %s", event.ActivityKind, event.Summary)
			if !steering && event.ActivityKind == domain.ActivityKindCommand {
				steering = true
				go func() {
					time.Sleep(2 * time.Second)
					_, err := conversation.(ports.ChatSteerer).Steer(ctx, ref.ProviderTurnID, ports.ChatUserMessage{
						Text: "STEER-ME: use staging", ClientMessageID: "live-steer", Origin: domain.MessageOriginHuman,
					})
					steered <- err
				}()
			}
		case ports.ChatEventTurnCompleted:
			if event.TurnState != domain.TurnStateCompleted {
				t.Fatalf("turn state = %q; reply=%q; err=%v", event.TurnState, reply.String(), event.Err)
			}
			done = true
		}
	}
	if !steering {
		t.Fatalf("the command never started; reply=%q; model saw %v", reply.String(), model.seen())
	}
	if err := <-steered; err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if !strings.Contains(reply.String(), "STEERED-OK") {
		t.Fatalf("the turn did not take the guidance: reply=%q; model saw %v", reply.String(), model.seen())
	}
	t.Logf("reply: %q", reply.String())
	expectNoMoreTurns(ctx, t, conversation)
}

// TestLiveHermesACPKeepsALateSteerInTheTurn sends `/steer` just as Hermes'
// turn ends, sweeping the delay over several turns. Hermes then takes the text
// one of three ways: into the running turn; as a prompt of its own, run from
// its queue ("No active turn") or in place of the command; or, when it
// acknowledged the steer after the turn's last step, in a later turn. Whatever
// it does, AO must see one completion per turn and nothing after it, and a
// prompt of its own must finish inside the turn. Which request runs that
// prompt is up to Hermes' timing; the host's unit tests pin down the case where
// the `/steer` request runs it after the turn's own prompt has answered.
func TestLiveHermesACPKeepsALateSteerInTheTurn(t *testing.T) {
	model := startLiveHermesModel(t)
	var mu sync.Mutex
	release := map[string]chan struct{}{}
	model.script = func(r modelRequest) modelReply {
		round := r.round()
		switch {
		case round != "" && r.lastUserIs("STEER-ME-"+round+": use staging"):
			// Outlast the client's wait for a steer command's answer, so only the
			// host keeping the turn open keeps this run in it.
			time.Sleep(liveSteerRunTime)
			return modelReply{text: "PROMPT-RAN-" + round}
		case round != "" && r.mentions("STEER-ME-"+round):
			return modelReply{text: "STEERED-OK-" + round}
		case round != "":
			mu.Lock()
			gate := release[round]
			mu.Unlock()
			if gate != nil {
				<-gate
			}
			return modelReply{text: "DONE-" + round} // never "ROOT-": round() reads that
		default:
			return modelReply{text: "lab"}
		}
	}
	ctx, conversation := startLiveHermes(t, model)

	paths := map[string]int{}
	var acknowledgedLate []string
	for i, delay := range []time.Duration{0, 0, 2, 5, 10, 20, 40, 80} {
		round := fmt.Sprintf("%d", i+1)
		gate := make(chan struct{})
		mu.Lock()
		release[round] = gate
		mu.Unlock()
		asked := model.waitFor("ROOT-" + round)
		ref := sendLiveTurn(ctx, t, conversation, "ROOT-"+round+": reply briefly.")
		select {
		case <-asked:
		case <-ctx.Done():
			t.Fatalf("round %s: the model never got the turn's request", round)
		}
		steered := make(chan error, 1)
		go func() {
			_, err := conversation.(ports.ChatSteerer).Steer(ctx, ref.ProviderTurnID, ports.ChatUserMessage{
				Text: "STEER-ME-" + round + ": use staging", ClientMessageID: "live-steer-" + round,
				Origin: domain.MessageOriginHuman,
			})
			steered <- err
		}()
		time.Sleep(delay * time.Millisecond)
		close(gate)

		var reply strings.Builder
		for done := false; !done; {
			event := nextLiveEvent(ctx, t, conversation)
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				reply.WriteString(event.Delta)
			case ports.ChatEventApprovalRequested:
				resolveLiveApproval(ctx, t, conversation, event)
			case ports.ChatEventTurnCompleted:
				if event.ProviderTurnID != ref.ProviderTurnID {
					t.Fatalf("round %s: a turn AO did not start completed: %+v", round, event)
				}
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("round %s: turn state = %q; reply=%q; err=%v", round, event.TurnState, reply.String(), event.Err)
				}
				done = true
			}
		}
		steerErr := <-steered
		answer := reply.String()
		var path string
		switch {
		case strings.Contains(answer, "PROMPT-RAN-"+round):
			path = "ran as a prompt of its own, inside the turn"
		case strings.Contains(answer, "STEERED-OK-"+round):
			path = "queued into the running turn"
		case errors.Is(steerErr, ports.ErrChatNoSteerableTurn) || isLiveRefusal(steerErr):
			path = "refused"
		default:
			path = "not seen by the model"
		}
		if path == "not seen by the model" && steerErr == nil {
			// Hermes acknowledged it ("queued for the active turn") after the
			// model's last answer, when no step was left to take it.
			path = "acknowledged, not in this turn"
			acknowledgedLate = append(acknowledgedLate, round)
		}
		paths[path]++
		t.Logf("round %s (delay %dms): %s; Steer=%v; reply=%q", round, delay, path, steerErr, answer)
		expectNoMoreTurns(ctx, t, conversation)
	}
	t.Logf("paths: %v", paths)
	for _, round := range acknowledgedLate {
		if later := model.mentionedAfter("STEER-ME-"+round, "ROOT-"+round); later != "" {
			t.Logf("round %s's steer reached the model later: %s", round, later)
		} else {
			t.Logf("round %s's steer never reached the model", round)
		}
	}
}

// stopLiveHermesGateway ends the gateway Hermes started for hermesHome, and
// only that one: its state file must name hermesHome as its home.
func stopLiveHermesGateway(t *testing.T, hermesHome string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(hermesHome, "gateway_state.json"))
	if err != nil {
		return
	}
	var state struct {
		PID        int    `json:"pid"`
		HermesHome string `json:"hermes_home"`
		State      string `json:"gateway_state"`
	}
	if json.Unmarshal(data, &state) != nil || state.PID <= 0 || state.State != "running" {
		return
	}
	resolved, _ := filepath.EvalSymlinks(hermesHome)
	if state.HermesHome != hermesHome && state.HermesHome != resolved {
		t.Logf("gateway %d belongs to %s, not %s; leaving it", state.PID, state.HermesHome, hermesHome)
		return
	}
	// The state file can outlive the gateway, and its pid can be reused.
	out, err := exec.Command("ps", "-p", fmt.Sprint(state.PID), "-o", "command=").Output()
	if err != nil || !strings.Contains(string(out), "hermes") || !strings.Contains(string(out), "gateway") {
		return
	}
	process, err := os.FindProcess(state.PID)
	if err != nil || process.Signal(os.Interrupt) != nil {
		return
	}
	// It writes its exit state on the way out, which would recreate the folder
	// after it is removed, so wait for it to go.
	for range 60 {
		if exec.Command("ps", "-p", fmt.Sprint(state.PID)).Run() != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("gateway %d did not exit after an interrupt", state.PID)
}

func isLiveRefusal(err error) bool {
	var refusal interface{ ChatRefusal() bool }
	return errors.As(err, &refusal) && refusal.ChatRefusal()
}

// startLiveHermes starts AO's Hermes driver against model and returns the
// conversation. The provider is terminated when the test ends.
func startLiveHermes(t *testing.T, model *liveHermesModel) (context.Context, ports.ChatConversation) {
	t.Helper()
	if os.Getenv("AO_LIVE_HERMES") != "1" {
		t.Skip("set AO_LIVE_HERMES=1 to run against the local hermes with a scripted model")
	}
	// `hermes acp` auto-starts a gateway for its HERMES_HOME, which outlives
	// the test unless stopped. The cleanup stops the one this home recorded,
	// then removes the folder, retrying while Hermes finishes writing to it.
	hermesHome, err := os.MkdirTemp("", "ao-live-hermes-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopLiveHermesGateway(t, hermesHome)
		for range 40 {
			if os.RemoveAll(hermesHome) == nil {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Errorf("remove HERMES_HOME %s: %v", hermesHome, os.RemoveAll(hermesHome))
	})
	config := fmt.Sprintf("model:\n  default: \"lab-model\"\n  provider: \"custom\"\n  base_url: %q\n"+
		"  api_key: \"lab-dummy\"\n  streaming: false\n  context_length: 128000\n"+
		"gateway:\n  unmanaged_idle_exit_seconds: 10\n", model.server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(hermesHome, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", hermesHome)
	env := liveHermesEnv()
	env["HERMES_HOME"] = hermesHome

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	t.Cleanup(cancel)
	driver := New(hermes.New(), nil)
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-hermes-steer", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Env: env, Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A closed conversation leaves its persistent host and Hermes running for a
	// later resume, so end the provider too.
	t.Cleanup(func() {
		if terminator, ok := conversation.(ports.ChatProviderTerminator); ok {
			_ = terminator.Terminate()
		}
		_ = conversation.Close()
	})
	if !conversation.Capabilities()[ports.ChatCapabilitySteer] {
		t.Skip("the installed hermes does not offer Steer (needs 0.22.4 or newer)")
	}
	return ctx, conversation
}

// liveHermesEnv is the test's environment without provider credentials, so a
// misconfigured Hermes cannot fall back to a real model.
func liveHermesEnv() map[string]string {
	out := make(map[string]string)
	for _, pair := range os.Environ() {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "API_KEY") || strings.Contains(upper, "TOKEN") || strings.HasSuffix(upper, "_BASE_URL") {
			continue
		}
		out[name] = value
	}
	return out
}

func sendLiveTurn(ctx context.Context, t *testing.T, conversation ports.ChatConversation, text string) ports.ChatTurnRef {
	t.Helper()
	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: text, ClientMessageID: "live-" + time.Now().Format("150405.000000"), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if starter, ok := conversation.(ports.ChatDeferredTurnStarter); ok {
		if err := starter.StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatalf("StartDeferredTurn: %v", err)
		}
	}
	return ref
}

func nextLiveEvent(ctx context.Context, t *testing.T, conversation ports.ChatConversation) ports.ChatEvent {
	t.Helper()
	select {
	case event, ok := <-conversation.Events():
		if !ok {
			t.Fatal("controller closed")
		}
		if event.Kind == ports.ChatEventError {
			t.Fatalf("controller error: %v", event.Err)
		}
		// The daemon acknowledges each persisted event; the persistent host
		// refuses the next prompt until the previous one is acknowledged.
		if event.ProviderEventID != "" {
			if acknowledger, ok := conversation.(ports.ChatProviderEventAcknowledger); ok {
				if err := acknowledger.AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
					t.Fatalf("AcknowledgeProviderEvent: %v", err)
				}
			}
		}
		return event
	case <-ctx.Done():
		t.Fatalf("live test timed out: %v", ctx.Err())
	}
	return ports.ChatEvent{}
}

// resolveLiveApproval allows the scripted command only as written.
func resolveLiveApproval(ctx context.Context, t *testing.T, conversation ports.ChatConversation, event ports.ChatEvent) {
	t.Helper()
	want := ports.ChatDecisionRejectOnce
	if strings.TrimSpace(event.Summary) == liveHermesCommand {
		want = ports.ChatDecisionAllowOnce
	}
	var decision ports.ChatDecision
	for _, option := range event.Decisions {
		if option.Kind == want {
			decision = ports.ChatDecision{ID: option.ID}
		}
	}
	t.Logf("approval %q -> %s", event.Summary, decision.ID)
	if err := conversation.ResolveRequest(ctx, event.RequestID, decision); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
}

// expectNoMoreTurns fails if anything completes, or the agent keeps talking,
// in the quiet window after a turn.
func expectNoMoreTurns(ctx context.Context, t *testing.T, conversation ports.ChatConversation) {
	t.Helper()
	quiet := time.After(3 * time.Second)
	for {
		select {
		case event := <-conversation.Events():
			if event.Kind == ports.ChatEventTurnCompleted || event.Kind == ports.ChatEventMessageDelta {
				t.Fatalf("output after the turn ended: %+v", event)
			}
		case <-quiet:
			return
		case <-ctx.Done():
			t.Fatalf("live test timed out: %v", ctx.Err())
		}
	}
}

// liveHermesModel is a scripted OpenAI-compatible chat model.
type liveHermesModel struct {
	server   *httptest.Server
	script   func(modelRequest) modelReply
	mu       sync.Mutex
	log      []string
	requests []modelRequest
	waiters  map[string]chan struct{}
}

type modelReply struct {
	text    string
	command string // a terminal tool call instead of text
}

type modelRequest struct {
	hasTools bool
	lastRole string
	lastUser string
	all      string
}

func (r modelRequest) mentions(s string) bool { return strings.Contains(r.all, s) }

func (r modelRequest) lastUserIs(s string) bool { return strings.TrimSpace(r.lastUser) == s }

// round returns n for the newest prompt "ROOT-<n>: …", or "". Replies must
// not contain "ROOT-".
func (r modelRequest) round() string {
	i := strings.LastIndex(r.all, "ROOT-")
	if i < 0 {
		return ""
	}
	rest := r.all[i+len("ROOT-"):]
	end := strings.IndexByte(rest, ':')
	if end <= 0 {
		return ""
	}
	return rest[:end]
}

func startLiveHermesModel(t *testing.T) *liveHermesModel {
	t.Helper()
	m := &liveHermesModel{waiters: map[string]chan struct{}{}}
	m.server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.server.Close)
	return m
}

// waitFor returns a channel closed once a chat request mentions s.
func (m *liveHermesModel) waitFor(s string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan struct{})
	m.waiters[s] = ch
	return ch
}

// mentionedAfter returns the first logged request, after the first one whose
// newest user message mentions after, that mentions s anywhere, or "".
func (m *liveHermesModel) mentionedAfter(s, after string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := -1
	for i, req := range m.requests {
		if start < 0 && strings.Contains(req.lastUser, after) {
			start = i
			continue
		}
		if start >= 0 && strings.Contains(req.all, s) {
			return m.log[i]
		}
	}
	return ""
}

func (m *liveHermesModel) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.log...)
}

func (m *liveHermesModel) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{
			{"id": "lab-model", "object": "model", "context_length": 128000},
		}})
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools  []json.RawMessage `json:"tools"`
		Stream bool              `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := modelRequest{hasTools: len(body.Tools) > 0}
	var all strings.Builder
	for _, message := range body.Messages {
		text := messageText(message.Content)
		all.WriteString(text + "\n")
		req.lastRole = message.Role
		if message.Role == "user" {
			req.lastUser = text
		}
	}
	req.all = all.String()
	// Waiters hear of the request before the script runs: a script can hold
	// its answer until the test, having seen the request, lets it go.
	m.mu.Lock()
	for s, ch := range m.waiters {
		if strings.Contains(req.all, s) {
			close(ch)
			delete(m.waiters, s)
		}
	}
	m.mu.Unlock()
	reply := modelReply{text: "lab"}
	if req.hasTools && m.script != nil {
		reply = m.script(req)
	}
	m.mu.Lock()
	m.log = append(m.log, fmt.Sprintf("last %s %.60q -> %+v", req.lastRole, req.lastUser, reply))
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	writeModelReply(w, reply, body.Stream)
}

func messageText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var out strings.Builder
	for _, part := range parts {
		out.WriteString(part.Text)
	}
	return out.String()
}

func writeModelReply(w http.ResponseWriter, reply modelReply, stream bool) {
	message := map[string]any{"role": "assistant", "content": reply.text}
	finish := "stop"
	if reply.command != "" {
		args, _ := json.Marshal(map[string]any{"command": reply.command})
		message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
			"index": 0, "id": "call_lab", "type": "function",
			"function": map[string]any{"name": "terminal", "arguments": string(args)},
		}}}
		finish = "tool_calls"
	}
	usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "lab", "object": "chat.completion", "created": time.Now().Unix(), "model": "lab-model",
			"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
			"usage":   usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, finish any) {
		data, _ := json.Marshal(map[string]any{
			"id": "lab", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "lab-model",
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	chunk(message, nil)
	chunk(map[string]any{}, finish)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}
