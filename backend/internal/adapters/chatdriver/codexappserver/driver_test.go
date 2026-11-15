package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePlugin struct {
	bin        string
	binErr     error
	authStatus ports.AgentAuthStatus
	authErr    error
}

func (f fakePlugin) ResolveBinary(context.Context) (string, error) { return f.bin, f.binErr }
func (f fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return f.authStatus, f.authErr
}

type scriptedServer struct {
	t        *testing.T
	toClient io.WriteCloser

	mu                sync.Mutex
	responses         map[string]string
	responseSequences map[string][]string
	failures          map[string]string
	seen              []frame
	seenCh            chan frame
}

func (s *scriptedServer) replyError(method string, code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[method] = `{"code":` + strconv.Itoa(code) + `,"message":` + strconv.Quote(message) + `}`
}

func (s *scriptedServer) respondTo(method, resultJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[method] = resultJSON
}

func (s *scriptedServer) respondSequence(method string, results ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseSequences[method] = append([]string(nil), results...)
}

func (s *scriptedServer) push(raw string) {
	s.t.Helper()
	if _, err := io.WriteString(s.toClient, raw+"\n"); err != nil {
		s.t.Fatalf("push: %v", err)
	}
}

func (s *scriptedServer) reply(method, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[method] = result
}

func (s *scriptedServer) sentMethod(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.seen {
		if f.Method == method {
			return true
		}
	}
	return false
}

func (s *scriptedServer) awaitFrame(pred func(frame) bool) frame {
	s.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		s.mu.Lock()
		for _, f := range s.seen {
			if pred(f) {
				s.mu.Unlock()
				return f
			}
		}
		s.mu.Unlock()
		select {
		case <-s.seenCh:
		case <-deadline:
			s.t.Fatal("timed out waiting for an expected client frame")
			return frame{}
		}
	}
}

func newTestDriver(t *testing.T) (*Driver, *scriptedServer) {
	t.Helper()

	clientReads, serverWrites := io.Pipe()
	serverReads, clientWrites := io.Pipe()

	srv := &scriptedServer{
		t:        t,
		toClient: serverWrites,
		responses: map[string]string{
			"initialize":     `{"userAgent":"ao/test","codexHome":"/tmp/.codex"}`,
			"model/list":     `{"data":[{"id":"gpt-test","displayName":"GPT Test","isDefault":true}]}`,
			"thread/start":   `{"thread":{"id":"thread-1"},"model":"gpt-test","cwd":"/tmp/ws"}`,
			"turn/start":     `{"turn":{"id":"turn-1","status":"inProgress","items":[]}}`,
			"turn/interrupt": `{}`,
			"thread/resume":  `{"thread":{"id":"thread-1"}}`,
			"config/read":    `{"config":{}}`,
		},
		responseSequences: map[string][]string{},
		failures:          map[string]string{},
		seenCh:            make(chan frame, 64),
	}

	go func() {
		br := bufio.NewReader(serverReads)
		for {
			line, err := readFrame(br)
			if err != nil {
				return
			}
			if len(line) == 0 {
				continue
			}
			var f frame
			if err := json.Unmarshal(line, &f); err != nil {
				continue
			}

			srv.mu.Lock()
			srv.seen = append(srv.seen, f)
			reply, known := srv.responses[f.Method]
			if sequence := srv.responseSequences[f.Method]; len(sequence) > 0 {
				reply, known = sequence[0], true
				srv.responseSequences[f.Method] = sequence[1:]
			}
			failure, refused := srv.failures[f.Method]
			srv.mu.Unlock()

			select {
			case srv.seenCh <- f:
			default:
			}

			switch {
			case f.ID == nil || f.Method == "":
			case refused:
				srv.push(`{"id":` + string(*f.ID) + `,"error":` + failure + `}`)
			case known:
				srv.push(`{"id":` + string(*f.ID) + `,"result":` + reply + `}`)
			}
		}
	}()

	d := &Driver{
		plugin: fakePlugin{bin: "codex", authStatus: ports.AgentAuthStatusAuthorized},
		log:    slog.New(slog.DiscardHandler),
		versionProbe: func(context.Context, string) (string, error) {
			return "codex-cli 0.146.0", nil
		},
		spawn: func(context.Context, string, string, []string) (*process, error) {
			return &process{
				stdin:  clientWrites,
				stdout: clientReads,
				stop:   func() error { return serverWrites.Close() },
			}, nil
		},
	}
	t.Cleanup(func() { _ = serverWrites.Close() })
	return d, srv
}

func nextEvent(t *testing.T, events <-chan ports.ChatEvent, want ports.ChatEventKind) ports.ChatEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed while waiting for %q", want)
			}
			if ev.Kind == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %q", want)
			return ports.ChatEvent{}
		}
	}
}

func TestStartCompletesHandshakeAndOpensThread(t *testing.T) {
	d, srv := newTestDriver(t)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{
		SessionID:     "ao-1",
		WorkspacePath: "/tmp/ws",
		Model:         "gpt-test",
		Effort:        "high",
		Permissions:   ports.PermissionModeDefault,
		SystemPrompt:  "standing rules",
		Ephemeral:     true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	if got := conv.ProviderConversationID(); got != "thread-1" {
		t.Fatalf("provider conversation id = %q, want thread-1", got)
	}

	srv.awaitFrame(func(f frame) bool { return f.Method == "initialized" && f.ID == nil })

	start := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	var params struct {
		Cwd                   string            `json:"cwd"`
		ApprovalPolicy        string            `json:"approvalPolicy"`
		Sandbox               string            `json:"sandbox"`
		DeveloperInstructions string            `json:"developerInstructions"`
		Model                 string            `json:"model"`
		Config                map[string]string `json:"config"`
		Ephemeral             bool              `json:"ephemeral"`
	}
	if err := json.Unmarshal(start.Params, &params); err != nil {
		t.Fatalf("thread/start params: %v", err)
	}
	if params.Cwd != "/tmp/ws" {
		t.Errorf("cwd = %q", params.Cwd)
	}
	if params.DeveloperInstructions != "standing rules" {
		t.Errorf("developerInstructions = %q", params.DeveloperInstructions)
	}
	if params.Model != "gpt-test" || params.Config["model_reasoning_effort"] != "high" {
		t.Errorf("model tuning = %#v", params)
	}
	if !params.Ephemeral {
		t.Error("thread/start did not mark the background conversation ephemeral")
	}
	var rawParams map[string]json.RawMessage
	if err := json.Unmarshal(start.Params, &rawParams); err != nil {
		t.Fatalf("thread/start raw params: %v", err)
	}
	if _, ok := rawParams["reasoningEffort"]; ok {
		t.Fatal("thread/start sent unsupported top-level reasoningEffort")
	}
	if params.ApprovalPolicy != "never" || params.Sandbox != "danger-full-access" {
		t.Errorf("default posture = %q/%q, want never/danger-full-access", params.ApprovalPolicy, params.Sandbox)
	}
}

func TestReconnectAdoptsInitializedHostWithoutNativeResume(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("initialize", -32600, "Already initialized")
	prepareCalls := 0
	proc, err := d.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.persistent = true
	d.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		if !cfg.ReconnectOnly {
			t.Fatal("startup reconnect allowed a provider launch")
		}
		return &persistenthost.Transport{
			Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41,
		}, nil
	}

	conv, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-reconnect", ProviderConversationID: "thread-survived",
		DataDir: t.TempDir(), WorkspacePath: "/tmp/ws",
		PrepareEnv: func(context.Context) (map[string]string, error) {
			prepareCalls++
			return map[string]string{"AO_BROWSER_CAPABILITY": "rotated"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = conv.Close() }()
	if got := conv.ProviderConversationID(); got != "thread-survived" {
		t.Fatalf("provider conversation id = %q", got)
	}
	if prepareCalls != 0 {
		t.Fatalf("live Codex reconnect prepared launch-only environment %d times", prepareCalls)
	}
	if srv.sentMethod("thread/resume") || srv.sentMethod("initialized") {
		t.Fatal("reconnect to an initialized app-server repeated its handshake")
	}
	if !conv.(*conversation).ReconnectedLive() {
		t.Fatal("reconnect to an initialized app-server was not reported live")
	}

	lister := conv.(ports.ChatModelLister)
	if _, err := lister.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	request := srv.awaitFrame(func(f frame) bool { return f.Method == "model/list" })
	if request.ID == nil || string(*request.ID) != "43" {
		t.Fatalf("first request id after reconnect = %v, want 43", request.ID)
	}
}

func TestReconnectInitializesAHostThatNeverWas(t *testing.T) {
	d, srv := newTestDriver(t)
	proc, err := d.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.persistent = true
	d.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{
			Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41,
		}, nil
	}

	conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-uninitialized", ProviderConversationID: "thread-1",
		DataDir: t.TempDir(), WorkspacePath: "/tmp/ws",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = conv.Close() }()
	if !srv.sentMethod("initialize") || !srv.sentMethod("thread/resume") {
		t.Fatalf("handshake: initialize=%v resume=%v, want both",
			srv.sentMethod("initialize"), srv.sentMethod("thread/resume"))
	}
	if conv.(*conversation).ReconnectedLive() {
		t.Fatal("an app-server initialized just now was reported as a live reconnect")
	}
}

func TestCodexHibernateStopsAppServerAndNativeResumesThread(t *testing.T) {
	d, firstServer := newTestDriver(t)
	workspace := t.TempDir()
	first, err := d.Start(context.Background(), ports.ChatStartConfig{
		SessionID: "hibernate-codex", WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	threadID := first.ProviderConversationID()
	if _, err := first.SendTurn(context.Background(), ports.ChatUserMessage{Text: "first turn"}); err != nil {
		t.Fatal(err)
	}
	firstServer.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"inProgress","items":[]}}}`)
	firstServer.push(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}}`)
	if completed := nextEvent(t, first.Events(), ports.ChatEventTurnCompleted); completed.TurnState != domain.TurnStateCompleted {
		t.Fatalf("first turn state = %q", completed.TurnState)
	}
	stopped := false
	provider := first.(*conversation)
	provider.proc.terminate = func() error {
		stopped = true
		return provider.proc.stop()
	}
	if err := first.(ports.ChatProviderHibernator).Hibernate(); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("hibernate detached the controller but left app-server alive")
	}

	replacement, server := newTestDriver(t)
	server.reply("turn/start", `{"turn":{"id":"turn-2","status":"inProgress","items":[]}}`)
	resumed, err := replacement.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "hibernate-codex", WorkspacePath: workspace, ProviderConversationID: threadID,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if resumed.ProviderConversationID() != threadID || !server.sentMethod("thread/resume") {
		t.Fatal("replacement app-server did not resume the same Codex thread")
	}
	second, err := resumed.SendTurn(context.Background(), ports.ChatUserMessage{Text: "second turn", ClientMessageID: "after-hibernate"})
	if err != nil || second.ProviderTurnID != "turn-2" {
		t.Fatalf("second turn = %+v, %v", second, err)
	}
	request := server.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params struct {
		ThreadID            string `json:"threadId"`
		ClientUserMessageID string `json:"clientUserMessageId"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.ThreadID != threadID || params.ClientUserMessageID != "after-hibernate" || server.sentMethod("thread/start") {
		t.Fatalf("second turn targeted %q with key %q; fresh thread started=%v",
			params.ThreadID, params.ClientUserMessageID, server.sentMethod("thread/start"))
	}
}

func TestResumeDoesNotCompeteWithAttachedHostDuringDaemonOverlap(t *testing.T) {
	d, srv := newTestDriver(t)
	d.persistent = true
	d.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
		return nil, persistenthost.ErrAttached
	}
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-overlap", ProviderConversationID: "thread-live",
		DataDir: t.TempDir(), WorkspacePath: "/tmp/ws",
	})
	if err == nil || !errors.Is(err, persistenthost.ErrAttached) {
		t.Fatalf("Resume error = %v, want attached-host refusal", err)
	}
	if !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("Resume error = %v, want recovery-inconclusive classification", err)
	}
	if srv.sentMethod("initialize") || srv.sentMethod("thread/resume") {
		t.Fatal("daemon overlap launched a competing direct provider")
	}
}

func TestStartRejectsRelativeWorkspacePath(t *testing.T) {
	d, _ := newTestDriver(t)
	_, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "workspace"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want a rejection naming the absolute-path requirement", err)
	}
}

func TestSendTurnCarriesIdempotencyKey(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:            "what changed?",
		ClientMessageID: "client-msg-7",
		Origin:          domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if ref.ProviderTurnID != "turn-1" {
		t.Fatalf("turn id = %q", ref.ProviderTurnID)
	}

	sent := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params struct {
		ThreadID            string `json:"threadId"`
		ClientUserMessageID string `json:"clientUserMessageId"`
		Input               []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
	}
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("turn/start params: %v", err)
	}
	if params.ThreadID != "thread-1" {
		t.Errorf("threadId = %q", params.ThreadID)
	}
	if params.ClientUserMessageID != "client-msg-7" {
		t.Errorf("clientUserMessageId = %q, want the caller's key", params.ClientUserMessageID)
	}
	if len(params.Input) != 1 || params.Input[0].Text != "what changed?" {
		t.Errorf("input = %+v", params.Input)
	}
}

func TestSendTurnRejectsEmptyText(t *testing.T) {
	d, _ := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "  "}); err == nil {
		t.Fatal("expected empty text to be rejected")
	}
}

func TestApprovalIsParkedUntilResolved(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"id":0,"method":"item/commandExecution/requestApproval","params":{` +
		`"threadId":"thread-1","turnId":"turn-1","itemId":"exec-1",` +
		`"command":"/bin/zsh -lc 'date -u'","cwd":"/tmp/ws",` +
		`"availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["date","-u"]}},"cancel"]}}`)

	ev := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if ev.RequestID != "0" {
		t.Fatalf("request id = %q, want the JSON-RPC id 0", ev.RequestID)
	}
	if ev.ProviderTurnID != "turn-1" {
		t.Fatalf("provider turn id = %q, want turn-1", ev.ProviderTurnID)
	}
	if ev.ActivityStatus != domain.ActivityStatusPending {
		t.Errorf("status = %q, want pending", ev.ActivityStatus)
	}
	if ev.Summary != "Run date -u" {
		t.Errorf("summary = %q, want the shell wrapper stripped", ev.Summary)
	}

	var ids []string
	for _, opt := range ev.Decisions {
		ids = append(ids, opt.ID)
	}
	want := []string{"accept", "acceptWithExecpolicyAmendment", "cancel"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("decisions = %v, want %v (from the provider's own list)", ids, want)
	}
	wantKinds := []ports.ChatDecisionKind{
		ports.ChatDecisionAllowOnce, ports.ChatDecisionAllowAlways, ports.ChatDecisionRejectOnce,
	}
	for i, option := range ev.Decisions {
		if option.Kind != wantKinds[i] {
			t.Fatalf("decision %q kind = %q, want %q", option.ID, option.Kind, wantKinds[i])
		}
	}

	srv.mu.Lock()
	for _, f := range srv.seen {
		if f.ID != nil && string(*f.ID) == "0" {
			srv.mu.Unlock()
			t.Fatal("approval was answered before the user decided")
		}
	}
	srv.mu.Unlock()

	if err := conv.ResolveRequest(context.Background(), "0", ports.ChatDecision{ID: "accept"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}

	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "0" && f.Method == "" })
	var payload struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(reply.Result, &payload); err != nil {
		t.Fatalf("reply not decodable: %v (%s)", err, reply.Result)
	}
	if payload.Decision != "accept" {
		t.Fatalf("decision sent = %q", payload.Decision)
	}
}

func TestStructuredDecisionIsAnsweredWithTheProvidersOwnPayload(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"id":3,"method":"item/commandExecution/requestApproval","params":{"command":"ls","availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["ls"]}},"cancel"]}}`)
	ev := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)

	if err := conv.ResolveRequest(context.Background(), ev.RequestID, ports.ChatDecision{
		ID: "acceptWithExecpolicyAmendment",
	}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}

	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "3" && f.Method == "" })
	if !strings.Contains(string(reply.Result), "execpolicy_amendment") {
		t.Fatalf("the provider's own decision payload was not echoed: %s", reply.Result)
	}
}

func TestDecisionNotOfferedIsRefusedAndLeavesTheRequestPending(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"id":4,"method":"item/commandExecution/requestApproval","params":{"command":"rm -rf /","availableDecisions":["accept","cancel"]}}`)
	ev := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)

	err = conv.ResolveRequest(context.Background(), ev.RequestID, ports.ChatDecision{ID: "decline"})
	if !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("err = %v, want ErrChatDecisionNotOffered", err)
	}

	if err := conv.ResolveRequest(context.Background(), ev.RequestID, ports.ChatDecision{ID: "cancel"}); err != nil {
		t.Fatalf("a refused decision consumed the request: %v", err)
	}
	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "4" && f.Method == "" })
	if !strings.Contains(string(reply.Result), "cancel") {
		t.Fatalf("reply did not carry the offered decision: %s", reply.Result)
	}
}

func TestResolvingAnUnknownRequestIsTyped(t *testing.T) {
	d, _ := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	err = conv.ResolveRequest(context.Background(), "no-such-request", ports.ChatDecision{ID: "accept"})
	if !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("err = %v, want ErrChatRequestNotPending", err)
	}
}

func TestResolveUnknownRequestIsRefused(t *testing.T) {
	d, _ := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	err = conv.ResolveRequest(context.Background(), "999", ports.ChatDecision{ID: "accept"})
	if err == nil {
		t.Fatal("expected resolving an unknown request to fail")
	}
}

func TestUnmodelledServerRequestIsRefused(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"id":11,"method":"mcpServer/elicitation/request","params":{}}`)

	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "11" && f.Method == "" })
	if reply.Error == nil {
		t.Fatalf("unmodelled request was answered with a result: %s", reply.Result)
	}
}

func TestNotificationsBecomeNeutralEvents(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/agentMessage/delta","params":{"turnId":"turn-1","itemId":"m1","delta":"hello"}}`)
	srv.push(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}}`)

	if ev := nextEvent(t, conv.Events(), ports.ChatEventMessageDelta); ev.Delta != "hello" {
		t.Fatalf("delta = %q", ev.Delta)
	}
	if ev := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted); ev.TurnState != domain.TurnStateCompleted {
		t.Fatalf("turn state = %q", ev.TurnState)
	}
}

func TestReloadMCPServersReturnsCompletePagedInventory(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo(codexproto.MethodConfigMcpServerReload, `{}`)
	srv.respondSequence(codexproto.MethodMcpServerStatusList,
		`{"data":[{"name":"github","authStatus":"notLoggedIn","serverInfo":{"name":"github","version":"1"},"resourceTemplates":[],"resources":[],"tools":{}},{"name":"failed","authStatus":"unknown","resourceTemplates":[],"resources":[],"tools":{}}],"nextCursor":"page-2"}`,
		`{"data":[{"name":"playwright","authStatus":"notLoggedIn","serverInfo":{"name":"playwright","version":"1"},"resourceTemplates":[],"resources":[],"tools":{}}]}`,
	)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	result, err := conv.(ports.ChatMCPReloader).ReloadMCPServers(context.Background())
	if err != nil {
		t.Fatalf("ReloadMCPServers: %v", err)
	}
	if !result.Authoritative {
		t.Fatal("successful complete inventory was not authoritative")
	}
	if len(result.Servers) != 2 || result.Servers[0].Name != "github" || result.Servers[1].Name != "playwright" {
		t.Fatalf("servers = %+v, want both pages in order", result.Servers)
	}

	srv.mu.Lock()
	var requests []frame
	for _, request := range srv.seen {
		if request.Method == codexproto.MethodMcpServerStatusList {
			requests = append(requests, request)
		}
	}
	srv.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("inventory requests = %d, want 2", len(requests))
	}
	var first, second codexproto.ListMcpServerStatusParams
	if err := json.Unmarshal(requests[0].Params, &first); err != nil {
		t.Fatalf("decode first inventory params: %v", err)
	}
	if err := json.Unmarshal(requests[1].Params, &second); err != nil {
		t.Fatalf("decode second inventory params: %v", err)
	}
	if first.Detail == nil || *first.Detail != codexproto.McpServerStatusDetailToolsAndAuthOnly {
		t.Fatalf("detail = %v, want toolsAndAuthOnly", first.Detail)
	}
	if first.ThreadID == nil || *first.ThreadID != "thread-1" {
		t.Fatalf("threadId = %v, want thread-1", first.ThreadID)
	}
	if first.Cursor != nil {
		t.Fatalf("first cursor = %v, want absent", first.Cursor)
	}
	if second.Cursor == nil || *second.Cursor != "page-2" {
		t.Fatalf("second cursor = %v, want page-2", second.Cursor)
	}
}

func TestReloadMCPServersMarksFailedInventoryUnavailable(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo(codexproto.MethodConfigMcpServerReload, `{}`)
	srv.replyError(codexproto.MethodMcpServerStatusList, -32602, "inventory unavailable")
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	result, err := conv.(ports.ChatMCPReloader).ReloadMCPServers(context.Background())
	if err != nil {
		t.Fatalf("ReloadMCPServers: %v", err)
	}
	if result.Authoritative || len(result.Servers) != 0 {
		t.Fatalf("result = %+v, want unavailable inventory", result)
	}
}

func TestNestedThreadDoesNotReplaceRootActiveTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"root-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"turn/started","params":{"threadId":"child-thread-1","turn":{"id":"child-turn","status":"inProgress","items":[]}}}`)
	root := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	child := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if root.ProviderConversationID != "thread-1" || child.ProviderConversationID != "child-thread-1" {
		t.Fatalf("normalized lifecycle threads = root %q child %q", root.ProviderConversationID, child.ProviderConversationID)
	}

	if err := conv.Interrupt(context.Background(), ""); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	sent := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/interrupt" })
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("decode turn/interrupt params: %v", err)
	}
	if params.ThreadID != "thread-1" || params.TurnID != "root-turn" {
		t.Fatalf("interrupt target = %q/%q, want thread-1/root-turn", params.ThreadID, params.TurnID)
	}
}

func TestResumeFailureDoesNotFallBackToStart(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.mu.Lock()
	delete(srv.responses, "thread/resume")
	srv.mu.Unlock()

	go func() {
		f := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/resume" })
		srv.push(`{"id":` + string(*f.ID) + `,"error":{"code":-32602,"message":"unknown thread"}}`)
	}()

	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID:              "ao-1",
		ProviderConversationID: "thread-gone",
		WorkspacePath:          "/tmp/ws",
	})
	if !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("err = %v, want ErrChatResumeFailed", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, f := range srv.seen {
		if f.Method == "thread/start" {
			t.Fatal("driver fell back to thread/start after a failed resume")
		}
	}
}

func TestResumeReappliesWorkspaceAndStandingInstructions(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID:              "ao-1",
		ProviderConversationID: "thread-1",
		WorkspacePath:          "/tmp/ws",
		Model:                  "selected-resume-model",
		Effort:                 "high",
		SystemPrompt:           "current AO standing instructions",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = conv.Close() }()

	resume := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/resume" })
	var params struct {
		ThreadID              string            `json:"threadId"`
		Cwd                   string            `json:"cwd"`
		Model                 string            `json:"model"`
		Config                map[string]string `json:"config"`
		DeveloperInstructions string            `json:"developerInstructions"`
	}
	if err := json.Unmarshal(resume.Params, &params); err != nil {
		t.Fatalf("thread/resume params: %v", err)
	}
	if params.ThreadID != "thread-1" || params.Cwd != "/tmp/ws" {
		t.Fatalf("thread resume identity = %#v", params)
	}
	if params.Model != "selected-resume-model" {
		t.Fatalf("thread resume model = %q, want selected-resume-model", params.Model)
	}
	if params.Config["model_reasoning_effort"] != "high" {
		t.Fatalf("thread resume effort config = %q, want high", params.Config["model_reasoning_effort"])
	}
	var rawParams map[string]json.RawMessage
	if err := json.Unmarshal(resume.Params, &rawParams); err != nil {
		t.Fatalf("thread/resume raw params: %v", err)
	}
	if _, ok := rawParams["reasoningEffort"]; ok {
		t.Fatal("thread/resume sent unsupported top-level reasoningEffort")
	}
	if params.DeveloperInstructions != "current AO standing instructions" {
		t.Fatalf("developerInstructions = %q", params.DeveloperInstructions)
	}
}

func TestResumeRequiresStoredThreadID(t *testing.T) {
	d, _ := newTestDriver(t)
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{WorkspacePath: "/tmp/ws"})
	if !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("err = %v, want ErrChatResumeFailed", err)
	}
}

func TestProbeIgnoresAmbientAuthStatus(t *testing.T) {
	d, _ := newTestDriver(t)
	d.plugin = fakePlugin{bin: "codex", authStatus: ports.AgentAuthStatusUnauthorized}
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if missing := ports.MissingProductionCapabilities(caps); len(missing) != 0 {
		t.Fatalf("codex is missing production capabilities: %v", missing)
	}
}

func TestProbeTreatsUnknownAuthAsUsable(t *testing.T) {
	d, _ := newTestDriver(t)
	d.plugin = fakePlugin{bin: "codex", authStatus: ports.AgentAuthStatusUnknown, authErr: errors.New("probe timed out")}
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if missing := ports.MissingProductionCapabilities(caps); len(missing) != 0 {
		t.Fatalf("codex is missing production capabilities: %v", missing)
	}
}

func TestProbeRejectsIncompatibleProtocolBeforeCreation(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.mu.Lock()
	delete(srv.responses, "model/list")
	srv.failures["model/list"] = `{"code":-32601,"message":"method not found"}`
	srv.mu.Unlock()

	if _, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("err = %v, want ErrChatDriverIncompatible", err)
	}
}

func TestProbeRejectsCodexOlderThanTheTestedProtocolFloor(t *testing.T) {
	d, _ := newTestDriver(t)
	d.versionProbe = func(context.Context, string) (string, error) {
		return "codex-cli 0.145.9", nil
	}

	if _, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("err = %v, want ErrChatDriverIncompatible", err)
	}
}

func TestProbeAcceptsNewerCodexVersion(t *testing.T) {
	d, _ := newTestDriver(t)
	d.versionProbe = func(context.Context, string) (string, error) {
		return "codex-cli 1.2.3", nil
	}

	if _, err := d.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

func TestInstalledCodexVersionAugmentsNodePATHForNPMLauncher(t *testing.T) {
	launcher, _ := npmCodexLauncherWithNodeOutsidePATH(t)

	directOutput, directErr := exec.Command(launcher, "--version").CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(directErr, &exitErr) || exitErr.ExitCode() != 127 {
		t.Fatalf("unaugmented npm launcher = %q, %v; want exit 127", directOutput, directErr)
	}

	got, err := installedCodexVersion(context.Background(), launcher)
	if err != nil {
		t.Fatalf("installedCodexVersion: %v", err)
	}
	if got != "codex-cli 0.149.1\n" {
		t.Fatalf("installedCodexVersion = %q, want Codex version", got)
	}

	d, _ := newTestDriver(t)
	d.plugin = fakePlugin{bin: launcher, authStatus: ports.AgentAuthStatusAuthorized}
	d.versionProbe = installedCodexVersion
	if _, err := d.Probe(context.Background()); err != nil {
		t.Fatalf("Probe with augmented npm launcher: %v", err)
	}
}

func TestCodexProcessEnvPreservesDaemonPATH(t *testing.T) {
	if os.Getenv("AO_TEST_CODEX_PATH_PIN") == "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(exe)
		launcher := filepath.Join(t.TempDir(), "codex")
		env := codexProcessEnv(context.Background(), launcher, map[string]string{
			"PATH": dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		})
		if got := strings.Split(envValue(env, "PATH"), string(os.PathListSeparator))[0]; got != dir {
			t.Fatalf("first PATH directory = %q, want daemon directory %q", got, dir)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "ao"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	copyPath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(copyPath, binary, 0o700); err != nil { //nolint:gosec // executable test fixture
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), copyPath, "-test.run=^TestCodexProcessEnvPreservesDaemonPATH$")
	cmd.Env = append(os.Environ(), "AO_TEST_CODEX_PATH_PIN=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("AO-named test process: %v\n%s", err, output)
	}
}

func TestStartAndResumeAugmentNodePATHForNPMLauncher(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *Driver) (ports.ChatConversation, error)
	}{
		{
			name: "initial Chat launch",
			run: func(ctx context.Context, d *Driver) (ports.ChatConversation, error) {
				return d.Start(ctx, ports.ChatStartConfig{SessionID: "ao-1", WorkspacePath: "/tmp/ws"})
			},
		},
		{
			name: "Chat restore",
			run: func(ctx context.Context, d *Driver) (ports.ChatConversation, error) {
				return d.Resume(ctx, ports.ChatResumeConfig{
					SessionID: "ao-1", ProviderConversationID: "thread-1", WorkspacePath: "/tmp/ws",
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher, nodeDir := npmCodexLauncherWithNodeOutsidePATH(t)
			d, _ := newTestDriver(t)
			d.plugin = fakePlugin{bin: launcher, authStatus: ports.AgentAuthStatusAuthorized}
			spawn := d.spawn
			var childEnv []string
			d.spawn = func(ctx context.Context, bin, workdir string, env []string) (*process, error) {
				childEnv = append([]string(nil), env...)
				return spawn(ctx, bin, workdir, env)
			}

			conv, err := tc.run(context.Background(), d)
			if err != nil {
				t.Fatalf("launch: %v", err)
			}
			defer func() { _ = conv.Close() }()

			path := envValue(childEnv, "PATH")
			if !pathContainsDir(path, nodeDir) {
				t.Fatalf("child PATH = %q, want Node runtime dir %q", path, nodeDir)
			}
		})
	}
}

func npmCodexLauncherWithNodeOutsidePATH(t *testing.T) (launcher, nodeDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("npm env-node launcher is Unix-specific")
	}
	home := t.TempDir()
	if canonical, err := filepath.EvalSymlinks(home); err == nil {
		home = canonical
	}
	nodeDir = filepath.Join(home, ".nvm", "versions", "node", "v22.11.0", "bin")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(nodeDir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nscript=$1\nshift\nexec /bin/sh \"$script\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(home, ".local", "lib", "node_modules", "@openai", "codex")
	js := filepath.Join(packageRoot, "bin", "codex.js")
	if err := os.MkdirAll(filepath.Dir(js), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(js, []byte("#!/usr/bin/env node\nif [ \"$1\" = \"--version\" ]; then printf 'codex-cli 0.149.1\\n'; fi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher = filepath.Join(home, ".local", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "lib", "node_modules", "@openai", "codex", "bin", "codex.js"), launcher); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	return launcher, nodeDir
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func pathContainsDir(path, dir string) bool {
	for _, part := range filepath.SplitList(path) {
		if part == dir {
			return true
		}
	}
	return false
}

func TestProbeRejectsUnparseableCodexVersion(t *testing.T) {
	d, _ := newTestDriver(t)
	d.versionProbe = func(context.Context, string) (string, error) {
		return "codex development build", nil
	}

	if _, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("err = %v, want ErrChatDriverIncompatible", err)
	}
}

func TestProbeReportsMissingBinary(t *testing.T) {
	d := &Driver{
		plugin: fakePlugin{binErr: errors.New("codex not found on PATH")},
		log:    slog.New(slog.DiscardHandler),
	}
	if _, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverUnavailable) {
		t.Fatalf("err = %v, want ErrChatDriverUnavailable", err)
	}
}

func TestSandboxAllowsNetworkFollowsTheCodexSandbox(t *testing.T) {
	for _, tc := range []struct {
		name           string
		readOnly       bool
		disableNetwork bool
		launchMode     ports.PermissionMode
		turnMode       ports.PermissionMode
		want           bool
	}{
		{"full access at launch", false, false, ports.PermissionModeDefault, "", true},
		{"accept edits at launch", false, false, ports.PermissionModeAcceptEdits, "", false},
		{"auto at launch", false, false, ports.PermissionModeAuto, "", false},
		{"turn narrows full access", false, false, ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, false},
		{"turn widens accept edits", false, false, ports.PermissionModeAcceptEdits, ports.PermissionModeDefault, true},
		{"read-only reviewer", true, false, ports.PermissionModeAcceptEdits, "", true},
		{"read-only background", true, true, ports.PermissionModeAcceptEdits, "", false},
		{"background cannot widen access", true, true, ports.PermissionModeAcceptEdits, ports.PermissionModeBypassPermissions, false},
	} {
		conv := &conversation{readOnly: tc.readOnly, disableNetwork: tc.disableNetwork, launchMode: tc.launchMode}
		if got := conv.SandboxAllowsNetwork(tc.turnMode); got != tc.want {
			t.Errorf("%s: SandboxAllowsNetwork = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestApprovalSettingsMirrorTUIPosture(t *testing.T) {
	for _, tc := range []struct {
		readOnly                  bool
		mode                      ports.PermissionMode
		policy, sandbox, reviewer string
	}{
		{false, ports.PermissionModeDefault, "never", "danger-full-access", "user"},
		{false, ports.PermissionModeBypassPermissions, "never", "danger-full-access", "user"},
		{false, ports.PermissionModeAcceptEdits, "on-request", "workspace-write", "user"},
		{false, ports.PermissionModeAuto, "on-request", "workspace-write", "auto_review"},
		{false, ports.PermissionMode("nonsense"), "never", "danger-full-access", "user"},
		{true, ports.PermissionModeAuto, "never", "read-only", "user"},
	} {
		policy, sandbox, reviewer := launchApprovalSettings(tc.mode, tc.readOnly)
		if policy != tc.policy || sandbox != tc.sandbox || reviewer != tc.reviewer {
			t.Errorf("approval settings(%q, readOnly=%t) = %q/%q/%q, want %q/%q/%q", tc.mode, tc.readOnly, policy, sandbox, reviewer, tc.policy, tc.sandbox, tc.reviewer)
		}
	}
}

func TestReadOnlyTurnCannotOverrideSandbox(t *testing.T) {
	params := map[string]any{}
	applyTurnSettings(params, ports.ChatTurnSettings{Approval: ports.PermissionModeAuto}, true, false)
	if params["approvalPolicy"] != "never" || params["approvalsReviewer"] != "user" ||
		!reflect.DeepEqual(params["sandboxPolicy"], map[string]any{"type": "readOnly", "networkAccess": true}) {
		t.Fatalf("read-only turn settings = %#v", params)
	}
}

func TestBackgroundReadOnlySandboxPersistsAcrossTurnOverrides(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: "/tmp/ws", Permissions: ports.PermissionModeAuto,
		ReadOnly: true, DisableNetwork: true, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	start := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	var startParams map[string]any
	if err := json.Unmarshal(start.Params, &startParams); err != nil {
		t.Fatal(err)
	}
	if startParams["sandbox"] != "read-only" || startParams["approvalPolicy"] != "never" || startParams["approvalsReviewer"] != "user" || startParams["ephemeral"] != true {
		t.Fatalf("background thread settings = %#v", startParams)
	}
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "Write a title", Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions}}); err != nil {
		t.Fatal(err)
	}
	turn := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var turnParams map[string]any
	if err := json.Unmarshal(turn.Params, &turnParams); err != nil {
		t.Fatal(err)
	}
	if turnParams["approvalPolicy"] != "never" || turnParams["approvalsReviewer"] != "user" || !reflect.DeepEqual(turnParams["sandboxPolicy"], map[string]any{"type": "readOnly", "networkAccess": false}) {
		t.Fatalf("background turn settings = %#v", turnParams)
	}
}

func TestBackgroundRefusesNetworkPermissionAndApprovalRequests(t *testing.T) {
	for _, method := range []string{
		"item/permissions/requestApproval",
		"item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/tool/requestUserInput",
	} {
		t.Run(method, func(t *testing.T) {
			conv := &conversation{readOnly: true, disableNetwork: true}
			result, err := conv.handleServerRequest(context.Background(), serverRequest{
				Method: method, ID: json.RawMessage(`42`),
				Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","permissions":{"network":{"enabled":true}},"availableDecisions":["accept","cancel"]}`),
			})
			if result != nil || err == nil || !strings.Contains(err.Error(), "does not permit") || len(conv.pending) != 0 {
				t.Fatalf("background request = %#v, %v, pending = %#v", result, err, conv.pending)
			}
		})
	}
}

func TestEnvSliceIsSortedForReproducibleRelaunch(t *testing.T) {
	got := envSlice(map[string]string{"ZZ_LAST": "z", "AA_FIRST": "a"})
	var previous string
	for _, entry := range got {
		if previous != "" && entry < previous {
			t.Fatalf("env not sorted: %q came after %q", entry, previous)
		}
		previous = entry
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"AA_FIRST=a", "ZZ_LAST=z"} {
		if !strings.Contains(joined, want) {
			t.Errorf("overlay entry %q missing from %v", want, got)
		}
	}
}

func TestControllerStopIsAnnounced(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	_ = srv.toClient.Close()

	ev := nextEvent(t, conv.Events(), ports.ChatEventControllerState)
	if ev.ControllerState != ports.ChatControllerStopped {
		t.Fatalf("controller state = %q, want stopped", ev.ControllerState)
	}
	_ = conv.Close()
}

func TestTurnSettingsUseTheTurnLevelWireShapes(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "go",
		Settings: ports.ChatTurnSettings{
			Model: "gpt-5.6-terra", Effort: "high", Approval: ports.PermissionModeAcceptEdits,
		},
	}); err != nil {
		t.Fatalf("SendTurn: %v", err)
	}

	sent := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params struct {
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		ApprovalPolicy string `json:"approvalPolicy"`
		SandboxPolicy  struct {
			Type string `json:"type"`
		} `json:"sandboxPolicy"`
	}
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("decode turn/start params: %v: %s", err, sent.Params)
	}
	if params.Model != "gpt-5.6-terra" || params.Effort != "high" {
		t.Errorf("model/effort not forwarded: %+v", params)
	}
	if params.ApprovalPolicy != "on-request" {
		t.Errorf("approvalPolicy = %q, want on-request", params.ApprovalPolicy)
	}
	if params.SandboxPolicy.Type != "workspaceWrite" {
		t.Errorf("sandboxPolicy.type = %q; a turn needs the tagged shape", params.SandboxPolicy.Type)
	}
}

func TestNoTurnSettingsSendsNoSettingsFields(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "go"}); err != nil {
		t.Fatalf("SendTurn: %v", err)
	}

	sent := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params map[string]json.RawMessage
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("decode turn/start params: %v", err)
	}
	for _, key := range []string{"model", "effort", "approvalPolicy", "sandboxPolicy"} {
		if _, present := params[key]; present {
			t.Errorf("unset setting %q was sent anyway", key)
		}
	}
}

func TestListModelsKeepsCatalogAndUsesThreadEffort(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/start", `{"thread":{"id":"thread-1"},"model":"a","reasoningEffort":"xhigh","cwd":"/tmp/ws"}`)
	srv.reply("model/list", `{"data":[{"id":"a","displayName":"Model A","description":"first","isDefault":true,"hidden":false,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"xhigh"}]},{"id":"secret","displayName":"Hidden","isDefault":false,"hidden":true,"defaultReasoningEffort":"low","supportedReasoningEfforts":[]},{"id":"b","displayName":"Model B","isDefault":false,"hidden":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[]}]}`)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	lister, ok := conv.(ports.ChatModelLister)
	if !ok {
		t.Fatal("conversation does not implement ChatModelLister")
	}

	models, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want the 2 visible ones: %+v", len(models), models)
	}
	if models[0].ID != "a" || models[1].ID != "b" {
		t.Errorf("provider order not preserved: %+v", models)
	}
	if !models[0].Default {
		t.Error("the provider's default was not carried through")
	}
	if got := models[0].Efforts; len(got) != 2 || got[0] != "low" || got[1] != "xhigh" {
		t.Errorf("efforts = %v, want [low xhigh]", got)
	}
	if models[0].DefaultEffort != "xhigh" {
		t.Errorf("default effort = %q, want the thread's xhigh", models[0].DefaultEffort)
	}
}

func TestListModelsUsesConfiguredThreadDefault(t *testing.T) {
	for _, configured := range []string{"nano", "custom-model"} {
		t.Run(configured, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("thread/start", `{"thread":{"id":"thread-1"},"model":"`+configured+`","cwd":"/tmp/ws"}`)
			srv.reply("model/list", `{"data":[{"id":"astra","isDefault":true},{"id":"nano","isDefault":false}]}`)
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			models, err := conv.(ports.ChatModelLister).ListModels(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, model := range models {
				if model.Default != (model.ID == configured) {
					t.Errorf("model %q default = %v, configured thread model = %q", model.ID, model.Default, configured)
				}
			}
		})
	}
}

func TestDiscoverModelsReadsCatalogWithoutOpeningThread(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("model/list", `{"data":[{"id":"gpt-visible","displayName":"GPT Visible","isDefault":true,"hidden":false},{"id":"gpt-hidden","displayName":"GPT Hidden","hidden":true}]}`)

	models, err := d.DiscoverModels(context.Background(), "/tmp/ws", map[string]string{"CODEX_HOME": "/tmp/codex-home"})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-visible" || models[0].DisplayName != "GPT Visible" || !models[0].Default {
		t.Fatalf("models = %#v", models)
	}
	if srv.sentMethod("thread/start") {
		t.Fatal("model discovery opened a provider thread")
	}
}

func TestDiscoverModelsDrainsEveryModelListPage(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondSequence("model/list",
		`{"data":[{"id":"gpt-first","displayName":"First"}],"nextCursor":"page-2"}`,
		`{"data":[{"id":"gpt-second","displayName":"Second"}]}`,
	)

	models, err := d.DiscoverModels(context.Background(), "/tmp/ws", nil)
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %#v, want two pages", models)
	}
	if got := []string{models[0].ID, models[1].ID}; !reflect.DeepEqual(got, []string{"gpt-first", "gpt-second"}) {
		t.Fatalf("model ids = %v, want both pages", got)
	}
	second := srv.awaitFrame(func(f frame) bool {
		if f.Method != "model/list" {
			return false
		}
		var params struct {
			Cursor string `json:"cursor"`
		}
		return json.Unmarshal(f.Params, &params) == nil && params.Cursor == "page-2"
	})
	if second.Method != "model/list" {
		t.Fatalf("second page request = %#v", second)
	}
}

func TestReadRateLimitsFromProviderResult(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("account/rateLimits/read", `{"rateLimits":{"limitId":"codex","limitName":null,"primary":{"usedPercent":71,"windowDurationMins":10080,"resetsAt":4102444800},"secondary":null,"credits":{"hasCredits":false,"unlimited":false,"balance":"0"},"individualLimit":null,"spendControlReached":false,"planType":"pro","rateLimitReachedType":null},"rateLimitsByLimitId":{"codex_bengalfox":{"limitId":"codex_bengalfox","limitName":"GPT-5.3-Codex-Spark","primary":{"usedPercent":0,"windowDurationMins":10080,"resetsAt":4102444800},"secondary":null,"credits":null,"individualLimit":null,"spendControlReached":null,"planType":"pro","rateLimitReachedType":null}},"rateLimitResetCredits":{"availableCount":0,"credits":[]}}`)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	reporter, ok := conv.(ports.ChatUsageReporter)
	if !ok {
		t.Fatal("conversation does not implement ChatUsageReporter")
	}

	limits, err := reporter.ReadRateLimits(context.Background())
	if err != nil {
		t.Fatalf("ReadRateLimits: %v", err)
	}
	if limits.PrimaryUsedPercent != 71 {
		t.Errorf("primary = %v, want 71", limits.PrimaryUsedPercent)
	}
	if limits.SecondaryUsedPercent >= 0 {
		t.Errorf("secondary = %v, want negative for a window this account lacks",
			limits.SecondaryUsedPercent)
	}
	if limits.PlanLabel != "pro" {
		t.Errorf("plan = %q, want pro", limits.PlanLabel)
	}
	if limits.PrimaryResetsInSeconds <= 0 {
		t.Errorf("primary resets in %d, want a positive remaining duration",
			limits.PrimaryResetsInSeconds)
	}
}

func TestCapabilitiesAdvertiseUsageAndRateLimits(t *testing.T) {
	caps := capabilities()
	if !caps.Has(ports.ChatCapabilityUsage) {
		t.Error("usage capability not advertised")
	}
	if !caps.Has(ports.ChatCapabilityRateLimits) {
		t.Error("rate limit capability not advertised")
	}
}

func TestCompactSendsOnlyTheThreadID(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/compact/start", `{}`)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	compactor, ok := conv.(ports.ChatCompactor)
	if !ok {
		t.Fatal("conversation does not implement ChatCompactor")
	}
	if _, err := compactor.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	sent := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/compact/start" })
	var params map[string]any
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("compact params: %v", err)
	}
	if params["threadId"] != "thread-1" {
		t.Errorf("threadId = %v, want thread-1", params["threadId"])
	}
	if len(params) != 1 {
		t.Errorf("compact sent %v; the method takes threadId alone", params)
	}
}

func TestCompactionReportsWhatItReclaimed(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"thread/tokenUsage/updated","params":{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":15650},"last":{"totalTokens":15650},"modelContextWindow":258400}}}`)
	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"compact-turn","status":"inProgress","items":[]}}}`)
	srv.push(`{"method":"item/started","params":{"item":{"type":"contextCompaction","id":"cc-1"},"threadId":"thread-1","turnId":"compact-turn"}}`)
	srv.push(`{"method":"thread/tokenUsage/updated","params":{"threadId":"thread-1","turnId":"compact-turn","tokenUsage":{"total":{"totalTokens":15650},"last":{"totalTokens":4632},"modelContextWindow":258400}}}`)
	srv.push(`{"method":"item/completed","params":{"item":{"type":"contextCompaction","id":"cc-1"},"threadId":"thread-1","turnId":"compact-turn"}}`)

	ev := nextEvent(t, conv.Events(), ports.ChatEventCompacted)
	if ev.ProviderItemID != "cc-1" {
		t.Errorf("provider item id = %q, want cc-1", ev.ProviderItemID)
	}
	var detail struct {
		TokensBefore    int64 `json:"tokensBefore"`
		TokensAfter     int64 `json:"tokensAfter"`
		TokensReclaimed int64 `json:"tokensReclaimed"`
		ContextWindow   int64 `json:"contextWindow"`
	}
	if err := json.Unmarshal(ev.Detail, &detail); err != nil {
		t.Fatalf("compaction detail: %v", err)
	}
	if detail.TokensBefore != 15650 || detail.TokensAfter != 4632 {
		t.Errorf("bracket = %d -> %d, want 15650 -> 4632", detail.TokensBefore, detail.TokensAfter)
	}
	if detail.TokensReclaimed != 11018 {
		t.Errorf("reclaimed = %d, want 11018", detail.TokensReclaimed)
	}
	if detail.ContextWindow != 258400 {
		t.Errorf("context window = %d, want 258400", detail.ContextWindow)
	}
	if !strings.Contains(ev.Summary, "11.0k") {
		t.Errorf("summary = %q, want the reclaimed amount named", ev.Summary)
	}
}

func TestCompactionIsReportedOncePerTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"method":"item/completed","params":{"item":{"type":"contextCompaction","id":"cc-1"},"threadId":"thread-1","turnId":"compact-turn"}}`)
	srv.push(`{"method":"thread/compacted","params":{"threadId":"thread-1","turnId":"compact-turn"}}`)
	srv.push(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"compact-turn","status":"completed","items":[]}}}`)

	nextEvent(t, conv.Events(), ports.ChatEventCompacted)
	for {
		ev, ok := <-conv.Events()
		if !ok {
			t.Fatal("stream closed before the marker arrived")
		}
		if ev.Kind == ports.ChatEventCompacted {
			t.Fatal("one compaction was reported twice")
		}
		if ev.Kind == ports.ChatEventTurnCompleted {
			return
		}
	}
}

func TestCompactionClaimsNoFiguresItDoesNotHave(t *testing.T) {
	if got := compactionSummary(0, 0); got != "Compacted the conversation history" {
		t.Errorf("summary with no figures = %q", got)
	}
	if got := compactionSummary(1000, 4702); got != "Compacted the conversation history" {
		t.Errorf("summary with no reclaim = %q, want no invented saving", got)
	}
	if got := compactionSummary(15650, 4632); !strings.Contains(got, "11.0k tokens") {
		t.Errorf("summary = %q", got)
	}
}

func TestCompactionIsAdvertised(t *testing.T) {
	if !capabilities().Has(ports.ChatCapabilityCompaction) {
		t.Fatal("compaction capability is not advertised")
	}
}

func TestEnvSliceMergesOverTheDaemonEnvironment(t *testing.T) {
	t.Setenv("HOME", "/Users/someone")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("AO_SESSION", "stale-from-the-shell-that-started-the-daemon")

	got := map[string]string{}
	for _, entry := range envSlice(map[string]string{
		"AO_SESSION": "p1-1",
		"PATH":       "/pinned/bin:/usr/bin",
	}) {
		key, value, _ := strings.Cut(entry, "=")
		got[key] = value
	}

	if got["HOME"] != "/Users/someone" {
		t.Errorf("HOME = %q, want it inherited from the daemon", got["HOME"])
	}
	if got["SSH_AUTH_SOCK"] != "/tmp/agent.sock" {
		t.Errorf("SSH_AUTH_SOCK = %q; without it the agent cannot push over SSH", got["SSH_AUTH_SOCK"])
	}
	if got["AO_SESSION"] != "p1-1" {
		t.Errorf("AO_SESSION = %q, want the session's own id to win", got["AO_SESSION"])
	}
	if got["PATH"] != "/pinned/bin:/usr/bin" {
		t.Errorf("PATH = %q, want the HookPATH-pinned value to win", got["PATH"])
	}
}

func TestEnvSliceWithNoOverlayStillInheritsTheEnvironment(t *testing.T) {
	t.Setenv("HOME", "/Users/someone")
	entries := envSlice(nil)
	var sawHome bool
	for _, entry := range entries {
		if entry == "HOME=/Users/someone" {
			sawHome = true
		}
	}
	if !sawHome {
		t.Error("an empty overlay produced an environment with no HOME")
	}
}

func TestReconnectMissingHostNeverLaunchesProvider(t *testing.T) {
	driver := New(fakePlugin{binErr: errors.New("provider installation is unavailable")}, nil)
	_, err := driver.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "stopped", ProviderConversationID: "thread", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		PrepareEnv: func(context.Context) (map[string]string, error) {
			t.Fatal("health check rotated launch credentials")
			return nil, nil
		},
	})
	if !errors.Is(err, ports.ErrChatHostNotRunning) {
		t.Fatalf("error=%v", err)
	}
}

func TestStartAndResumePassAOToolServersInThreadConfig(t *testing.T) {
	servers := []ports.ChatMCPServerConfig{{
		Name: "ao", Type: "stdio", Command: "/opt/ao/bin/ao", Args: []string{"mcp"},
		Env: map[string]string{"AO_SESSION_ID": "ao-1"},
	}, {
		Name: "docs", Type: "http", URL: "http://127.0.0.1:9/mcp",
	}}
	want := `{"mcp_servers":{"ao":{"args":["mcp"],"command":"/opt/ao/bin/ao",` +
		`"default_tools_approval_mode":"approve","env":{"AO_SESSION_ID":"ao-1"},"tool_timeout_sec":150},` +
		`"docs":{"url":"http://127.0.0.1:9/mcp"}},"model_reasoning_effort":"high"}`
	for _, method := range []string{"thread/start", "thread/resume"} {
		t.Run(method, func(t *testing.T) {
			d, srv := newTestDriver(t)
			var conv ports.ChatConversation
			var err error
			if method == "thread/start" {
				conv, err = d.Start(context.Background(), ports.ChatStartConfig{
					SessionID: "ao-1", WorkspacePath: "/tmp/ws", Effort: "high", MCPServers: servers,
				})
			} else {
				conv, err = d.Resume(context.Background(), ports.ChatResumeConfig{
					SessionID: "ao-1", ProviderConversationID: "thread-1", WorkspacePath: "/tmp/ws", Effort: "high", MCPServers: servers,
				})
			}
			if err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			defer func() { _ = conv.Close() }()

			f := srv.awaitFrame(func(f frame) bool { return f.Method == method })
			var params struct {
				Config json.RawMessage `json:"config"`
			}
			if err := json.Unmarshal(f.Params, &params); err != nil {
				t.Fatal(err)
			}
			if string(params.Config) != want {
				t.Fatalf("config = %s\nwant     %s", params.Config, want)
			}
		})
	}
}

func TestCodexResumedThreadLeavesTheFastTierItMayHaveBeenLeftOn(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo("thread/resume", `{"thread":{"id":"thread-1"},"serviceTier":"priority"}`)
	resumed, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "fast-resume", WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if _, err := resumed.SendTurn(context.Background(), ports.ChatUserMessage{Text: "not fast"}); err != nil {
		t.Fatal(err)
	}
	request := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if tier, ok := params["serviceTier"]; !ok || string(tier) != "null" {
		t.Fatalf("serviceTier = %s (sent %v), want an explicit null", tier, ok)
	}
}

func TestCodexResumedThreadKeepsTheUsersConfiguredServiceTier(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo("thread/resume", `{"thread":{"id":"thread-1"},"serviceTier":"priority"}`)
	srv.respondTo("config/read", `{"config":{"service_tier":"fast"}}`)
	resumed, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "fast-config", WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if _, err := resumed.SendTurn(context.Background(), ports.ChatUserMessage{Text: "as configured"}); err != nil {
		t.Fatal(err)
	}
	request := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if tier, ok := params["serviceTier"]; ok {
		t.Fatalf("serviceTier = %s, want it left to the user's config", tier)
	}
}
