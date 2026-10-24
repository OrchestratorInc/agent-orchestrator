package zcodeappserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/zcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakePlugin is kimiacp's fake extended with a no-op AugmentRuntimeLaunchEnv
// that only records its arguments. It never writes ZCODE_DATA_BASE_DIR, so the
// one test that asserts on that key uses the real plugin.
type fakePlugin struct {
	binary  string
	binErr  error
	status  ports.AgentAuthStatus
	authErr error

	mu       sync.Mutex
	augments []augmentCall
}

type augmentCall struct {
	dataDir   string
	sessionID domain.SessionID
}

func (p *fakePlugin) ResolveBinary(context.Context) (string, error) { return p.binary, p.binErr }
func (p *fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return p.status, p.authErr
}
func (p *fakePlugin) AugmentRuntimeLaunchEnv(_ map[string]string, dataDir string, sessionID domain.SessionID, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.augments = append(p.augments, augmentCall{dataDir: dataDir, sessionID: sessionID})
}

// testSnapshot is a session snapshot in the PROVEN shape of the p1/p7
// fixtures (two models with reasoning levels, current with
// options.reasoningLevel, idle projection), used as the canned
// session/create, session/resume and session/read result.
const testSnapshot = `{"protocol":{"name":"ZCode Protocol","version":1},"session":{"sessionId":"sess-1","mode":"build","model":{"providerId":"zai-api-ao","modelId":"GLM-5.3"}},` +
	`"settings":{"mode":{"current":"edit"},"model":{"available":[` +
	`{"ref":{"providerId":"zai-api-ao","modelId":"GLM-5.3"},"label":"GLM-5.3","providerLabel":"Z.ai Coding Plan (AO)","contextWindow":200000,"maxOutputTokens":128000,"reasoning":{"levels":[{"value":"low","label":"low"},{"value":"high","label":"high"},{"value":"max","label":"max"}],"defaultLevel":"max"}},` +
	`{"ref":{"providerId":"zai-api-ao","modelId":"GLM-5.3-Flash"},"label":"GLM-5.3-Flash","providerLabel":"Z.ai Coding Plan (AO)","contextWindow":200000,"reasoning":{"levels":[{"value":"low","label":"low"},{"value":"high","label":"high"}],"defaultLevel":"high"}}],` +
	`"current":{"providerId":"zai-api-ao","modelId":"GLM-5.3","options":{"reasoningLevel":"max"}},"lastUsed":{"providerId":"zai-api-ao","modelId":"GLM-5.3"}},"permission":{"mode":"edit"}},` +
	`"projection":{"activeToolCalls":[],"backgroundJobs":[],"contextUsed":1200,"contextWindow":200000,"pendingPermissions":[],"status":"idle","turnCount":0},` +
	`"runtime":{"eventSeq":0,"pendingRequestIds":[],"stateRevision":0},"messages":[]}`

// scriptedServer stands in for `zcode app-server` over in-memory pipes. It
// answers client requests from a canned table, lets a test push notifications
// and server->client requests, and — unlike codex's fixture — can hold a
// request's reply until the driver has answered a server request the handler
// raised in the meantime (onRequest + awaitReply), which is the proven create
// exchange.
type scriptedServer struct {
	t        *testing.T
	toClient io.WriteCloser

	mu        sync.Mutex
	responses map[string]string
	failures  map[string]string
	handlers  map[string]func(id, params json.RawMessage)
	seen      []frame
	changed   chan struct{}

	envMu sync.Mutex
	env   []string

	// proc is what the injected spawn or host hands the driver; a test may
	// prepend frames to its stdout to stand in for a host replay.
	proc *process
}

func (s *scriptedServer) respondTo(method, resultJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[method] = resultJSON
	delete(s.failures, method)
	delete(s.handlers, method)
}

func (s *scriptedServer) replyError(method string, code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[method] = `{"code":` + strconv.Itoa(code) + `,"message":` + strconv.Quote(message) + `}`
	delete(s.handlers, method)
}

// onRequest installs a handler that runs on its own goroutine when the client
// sends the method. The handler owns the reply (see respond).
func (s *scriptedServer) onRequest(method string, fn func(id, params json.RawMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
}

func (s *scriptedServer) push(raw string) {
	s.t.Helper()
	if _, err := io.WriteString(s.toClient, raw+"\n"); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		// A closed pipe is teardown racing a scripted handler's trailing
		// frames (noise after a create result, say), not a failed expectation.
		s.t.Errorf("push: %v", err)
	}
}

func (s *scriptedServer) respond(id json.RawMessage, resultJSON string) {
	s.push(`{"id":` + string(id) + `,"result":` + resultJSON + `}`)
}

func (s *scriptedServer) record(f frame) {
	s.mu.Lock()
	s.seen = append(s.seen, f)
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *scriptedServer) frames() []frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]frame(nil), s.seen...)
}

func (s *scriptedServer) sentMethod(method string) bool {
	for _, f := range s.frames() {
		if f.Method == method {
			return true
		}
	}
	return false
}

func (s *scriptedServer) countFrames(pred func(frame) bool) int {
	n := 0
	for _, f := range s.frames() {
		if pred(f) {
			n++
		}
	}
	return n
}

// awaitFrame waits for a frame matching pred among everything the client sent.
func (s *scriptedServer) awaitFrame(pred func(frame) bool) frame {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		s.mu.Lock()
		for _, f := range s.seen {
			if pred(f) {
				s.mu.Unlock()
				return f
			}
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			s.t.Fatal("timed out waiting for an expected client frame")
			return frame{}
		}
	}
}

// awaitReply waits for the driver's reply to a server->client request id.
func (s *scriptedServer) awaitReply(id string) frame {
	s.t.Helper()
	return s.awaitFrame(func(f frame) bool {
		return f.Method == "" && f.ID != nil && rawID(*f.ID) == id
	})
}

func (s *scriptedServer) awaitRequest(method string) frame {
	s.t.Helper()
	return s.awaitFrame(func(f frame) bool { return f.Method == method && f.ID != nil })
}

func (s *scriptedServer) serve(serverReads io.Reader) {
	// The real app-server exits on stdin EOF (U10), which ends the driver's
	// read loop; mirror that by closing the server->client pipe too.
	defer func() { _ = s.toClient.Close() }()
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
		s.record(f)
		if f.ID == nil || f.Method == "" {
			continue
		}
		s.mu.Lock()
		handler := s.handlers[f.Method]
		reply, known := s.responses[f.Method]
		failure, refused := s.failures[f.Method]
		s.mu.Unlock()
		id := *f.ID
		if f.Method == zcodeproto.MethodSessionRead && !positiveMessageLimit(f.Params) {
			// The installed bundle's session/read schema (`KXt`, strict) declares
			// messageLimit int().positive().optional(), so a zero limit is refused
			// exactly as the live server refuses it; every captured read carried
			// 1 or 10.
			s.push(`{"id":` + string(id) + `,"error":{"code":-32602,"message":"messageLimit: Number must be greater than 0"}}`)
			continue
		}
		switch {
		case handler != nil:
			go handler(id, f.Params)
		case refused:
			s.push(`{"id":` + string(id) + `,"error":` + failure + `}`)
		case known:
			s.respond(id, reply)
		}
	}
}

// positiveMessageLimit mirrors the server's session/read rule: messageLimit
// is optional, and when present it must be a positive integer.
func positiveMessageLimit(params json.RawMessage) bool {
	var p struct {
		MessageLimit *float64 `json:"messageLimit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return false
	}
	return p.MessageLimit == nil || (*p.MessageLimit >= 1 && *p.MessageLimit == float64(int64(*p.MessageLimit)))
}

// messageLimitOf returns the messageLimit a session/read frame carried, or -1
// when the key was absent.
func messageLimitOf(t *testing.T, read frame) int {
	t.Helper()
	limit, ok := paramsMap(t, read)["messageLimit"].(float64)
	if !ok {
		return -1
	}
	return int(limit)
}

func (s *scriptedServer) capturedEnv() []string {
	s.envMu.Lock()
	defer s.envMu.Unlock()
	return append([]string(nil), s.env...)
}

// newScriptedServer wires the pipes and the default canned table. The returned
// process is what the injected spawn hands the driver.
func newScriptedServer(t *testing.T) (*scriptedServer, *process) {
	t.Helper()
	clientReads, serverWrites := io.Pipe()
	serverReads, clientWrites := io.Pipe()
	srv := &scriptedServer{
		t:        t,
		toClient: serverWrites,
		responses: map[string]string{
			zcodeproto.MethodSessionSubscribe: `{"sessionId":"sess-1"}`,
			zcodeproto.MethodSessionSend:      `{"accepted":true,"sessionId":"sess-1","stateRevision":1}`,
			zcodeproto.MethodSessionStop:      `{}`,
			zcodeproto.MethodSessionResume:    testSnapshot,
			zcodeproto.MethodSessionRead:      testSnapshot,
			zcodeproto.MethodSessionSetModel:  testSnapshot,
		},
		failures: map[string]string{},
		handlers: map[string]func(id, params json.RawMessage){},
		changed:  make(chan struct{}),
	}
	// The default create exchange: raise the runtime-preferences request, hold
	// the create result until it is answered, then write the snapshot.
	srv.onRequest(zcodeproto.MethodSessionCreate, func(id, _ json.RawMessage) {
		srv.push(`{"id":"server-1","method":"session/requestRuntimePreferences","params":{"sessionId":"sess-1"}}`)
		srv.awaitReply("server-1")
		srv.respond(id, testSnapshot)
	})
	go srv.serve(serverReads)
	proc := &process{
		stdin:  clientWrites,
		stdout: clientReads,
		stop:   func() error { return serverWrites.Close() },
	}
	srv.proc = proc
	t.Cleanup(func() { _ = serverWrites.Close() })
	return srv, proc
}

// activateLive does what the chat service does right after a live reconnect:
// hand the driver the durable running turn (or none) so it releases the replay.
func activateLive(t *testing.T, conv ports.ChatConversation, providerTurnID string) {
	t.Helper()
	activator, ok := conv.(ports.ChatLiveReconnectActivator)
	if !ok {
		t.Fatal("live reconnect needs ports.ChatLiveReconnectActivator: the minted turn id lives only in daemon memory")
	}
	if err := activator.ActivateLiveReconnect(context.Background(), providerTurnID); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}
}

func awaitEventsClosed(t *testing.T, events <-chan ports.ChatEvent, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("Events() did not close")
		}
	}
}

// newTestDriver wires a Driver to a scripted server over in-memory pipes, so no
// process is ever spawned. The env the driver computed is captured.
func newTestDriver(t *testing.T) (*Driver, *scriptedServer) {
	t.Helper()
	return newTestDriverWithPlugin(t, &fakePlugin{binary: "zcode", status: ports.AgentAuthStatusAuthorized})
}

func newTestDriverWithPlugin(t *testing.T, plugin zcodePlugin) (*Driver, *scriptedServer) {
	t.Helper()
	srv, proc := newScriptedServer(t)
	d := &Driver{
		plugin: plugin,
		log:    slog.New(slog.DiscardHandler),
		spawn: func(_ context.Context, _, _ string, env []string) (*process, error) {
			srv.envMu.Lock()
			srv.env = append([]string(nil), env...)
			srv.envMu.Unlock()
			return proc, nil
		},
		runtimeHeaders: declineResolver{},
	}
	return d, srv
}

// newHostTestDriver wires the persistent-host path with an injected connectHost
// so the live-reconnect branch runs over the same pipes.
func newHostTestDriver(t *testing.T, reconnected bool, nextRequestID int64) (*Driver, *scriptedServer, *[]persistenthost.Config) {
	t.Helper()
	srv, proc := newScriptedServer(t)
	var configs []persistenthost.Config
	d := &Driver{
		plugin:     &fakePlugin{binary: "zcode", status: ports.AgentAuthStatusAuthorized},
		log:        slog.New(slog.DiscardHandler),
		persistent: true,
		connectHost: func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
			configs = append(configs, cfg)
			return &persistenthost.Transport{
				Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: reconnected, NextRequestID: nextRequestID,
			}, nil
		},
		runtimeHeaders: declineResolver{},
	}
	return d, srv, &configs
}

func startConfig(t *testing.T) ports.ChatStartConfig {
	t.Helper()
	return ports.ChatStartConfig{
		SessionID:     "ao-1",
		DataDir:       t.TempDir(),
		WorkspacePath: t.TempDir(),
		Permissions:   ports.PermissionModeBypassPermissions,
	}
}

// startConversation opens a conversation on the default scripted exchange.
func startConversation(t *testing.T) (*conversation, *scriptedServer) {
	t.Helper()
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), startConfig(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	return conv.(*conversation), srv
}

// sendTurn prepares and starts one turn, returning the minted id.
func sendTurn(t *testing.T, conv *conversation, text string) string {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: text})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	return ref.ProviderTurnID
}

// nextEvent returns the next event of the wanted kind, skipping others.
func nextEvent(t *testing.T, events <-chan ports.ChatEvent, want ports.ChatEventKind) ports.ChatEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
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

// drainEvents collects whatever arrives within the window.
func drainEvents(events <-chan ports.ChatEvent, window time.Duration) []ports.ChatEvent {
	var out []ports.ChatEvent
	deadline := time.After(window)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
}

func countKind(events []ports.ChatEvent, kind ports.ChatEventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func paramsMap(t *testing.T, f frame) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(f.Params, &m); err != nil {
		t.Fatalf("params of %s not an object: %v", f.Method, err)
	}
	return m
}

// --- replay loader -----------------------------------------------------------

// replayLine is one direction-tagged line of a testdata/*.ndjson fixture:
// "out" is what the client wrote, "in" what the server wrote. Fixtures are
// extracted from the redacted Phase 1 captures (probes/captures/); Source
// names the capture file and line each frame came from, and a leading "meta"
// line describes the run and the trims applied.
type replayLine struct {
	Dir    string `json:"dir"`
	Frame  frame  `json:"frame"`
	Note   string `json:"note"`
	Source string `json:"source"`
}

func loadReplay(t *testing.T, name string) []replayLine {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var lines []replayLine
	for i, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var line replayLine
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("%s line %d: %v", name, i+1, err)
		}
		if line.Dir == "meta" {
			continue
		}
		if line.Dir != "in" && line.Dir != "out" {
			t.Fatalf("%s line %d: dir %q", name, i+1, line.Dir)
		}
		if line.Source == "" {
			t.Fatalf("%s line %d: no capture source", name, i+1)
		}
		lines = append(lines, line)
	}
	return lines
}

// fixtureNames lists every capture-derived fixture.
func fixtureNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ndjson") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// fixtureNotifications returns the server->client notifications of a fixture,
// in wire order, for the normalizer.
func fixtureNotifications(t *testing.T, name string) []notification {
	t.Helper()
	var out []notification
	for _, line := range loadReplay(t, name) {
		if line.Dir != "in" || line.Frame.ID != nil || line.Frame.Method == "" {
			continue
		}
		out = append(out, notification{Method: line.Frame.Method, Params: line.Frame.Params})
	}
	return out
}

// fixtureFrame returns the first fixture frame matching pred.
func fixtureFrame(t *testing.T, name string, pred func(replayLine) bool) frame {
	t.Helper()
	for _, line := range loadReplay(t, name) {
		if pred(line) {
			return line.Frame
		}
	}
	t.Fatalf("%s: no frame matches", name)
	return frame{}
}

// fixtureResult returns the result body of the fixture's response to the
// client request with the given numeric id.
func fixtureResult(t *testing.T, name string, id int64) string {
	t.Helper()
	f := fixtureFrame(t, name, func(line replayLine) bool {
		return line.Dir == "in" && line.Frame.Method == "" && line.Frame.ID != nil && rawID(*line.Frame.ID) == strconv.FormatInt(id, 10) && len(line.Frame.Result) > 0
	})
	return string(f.Result)
}

func isRequest(f frame) bool { return f.ID != nil && f.Method != "" }

// replay drives the onRequest/awaitReply hooks from a fixture: every "out"
// request becomes one group of the "in" lines that followed it. The groups of
// one method are played in fixture order, one per client request (the p1
// fixture has three session/send groups: the proven turn, then the two
// back-to-back sends of the -32010 probe). A group holds the response until
// each "out" reply to a server request has arrived and checks that reply
// against the capture: results byte-for-byte, refusals by code.
func (s *scriptedServer) replay(lines []replayLine) {
	s.t.Helper()
	groups := map[string][][]replayLine{}
	var order []string
	for i := 0; i < len(lines); i++ {
		if lines[i].Dir != "out" || !isRequest(lines[i].Frame) {
			continue
		}
		start := i + 1
		end := start
		for end < len(lines) && (lines[end].Dir != "out" || !isRequest(lines[end].Frame)) {
			end++
		}
		method := lines[i].Frame.Method
		if _, seen := groups[method]; !seen {
			order = append(order, method)
		}
		groups[method] = append(groups[method], lines[start:end])
		i = end - 1
	}
	for _, method := range order {
		queue := groups[method]
		var mu sync.Mutex
		next := 0
		s.onRequest(method, func(id, _ json.RawMessage) {
			mu.Lock()
			index := next
			if index >= len(queue) {
				index = len(queue) - 1
			}
			next++
			mu.Unlock()
			for _, line := range queue[index] {
				switch {
				case line.Dir == "in" && isRequest(line.Frame):
					s.push(marshalFrame(s.t, line.Frame))
				case line.Dir == "in" && line.Frame.ID != nil:
					// The capture's numeric id is replaced with the driver's own.
					f := line.Frame
					f.ID = &id
					s.push(marshalFrame(s.t, f))
				case line.Dir == "in":
					s.push(marshalFrame(s.t, line.Frame))
				case line.Dir == "out" && line.Frame.ID != nil && line.Frame.Method == "":
					got := s.awaitReply(rawID(*line.Frame.ID))
					switch {
					case line.Frame.Error != nil:
						if got.Error == nil || got.Error.Code != line.Frame.Error.Code {
							s.t.Errorf("reply to %s = %+v, capture refused with %d", rawID(*line.Frame.ID), got, line.Frame.Error.Code)
						}
					case !jsonEqual(got.Result, line.Frame.Result):
						s.t.Errorf("reply to %s = %s, capture has %s", rawID(*line.Frame.ID), got.Result, line.Frame.Result)
					}
				}
			}
		})
	}
}

func marshalFrame(t *testing.T, f frame) string {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return string(b)
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// --- driver / conversation tests ---------------------------------------------

func TestStartCreatesSessionAnswersRuntimePreferencesAndSubscribes(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replay(loadReplay(t, "p1-create-send-complete.ndjson"))

	cfg := startConfig(t)
	cfg.Permissions = ports.PermissionModeDefault
	conv, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	create := srv.awaitRequest(zcodeproto.MethodSessionCreate)
	params := paramsMap(t, create)
	if _, ok := params["workspace"]; !ok {
		t.Fatalf("session/create params lack workspace: %s", create.Params)
	}
	if _, ok := params["model"]; ok {
		t.Fatalf("session/create must omit model (HANDOFF.md:57-60): %s", create.Params)
	}
	// The PROVEN param set of the capture, no more: mode edit for the default
	// permission mode, no title generation, no thoughtLevel (STATIC).
	if params["mode"] != "edit" || params["titleGenerationEnabled"] != false {
		t.Fatalf("session/create params = %s, want mode edit and titleGenerationEnabled false", create.Params)
	}
	if _, ok := params["thoughtLevel"]; ok {
		t.Fatalf("session/create carries the STATIC thoughtLevel: %s", create.Params)
	}
	reply := srv.awaitReply("server-1")
	if want := `{"nativeSearchEnhancementsEnabled":true,"askUserQuestionAutoResolutionEnabled":false}`; string(reply.Result) != want {
		t.Fatalf("runtime preferences reply = %s, want %s", reply.Result, want)
	}
	// The reply must precede the create result; the replay holds the result
	// until it sees the reply, so Start returning proves the order. Pin it
	// on the frame log too.
	frames := srv.frames()
	replyIndex, createIndex := -1, -1
	for i, f := range frames {
		if f.Method == "" && f.ID != nil && rawID(*f.ID) == "server-1" {
			replyIndex = i
		}
		if f.Method == zcodeproto.MethodSessionCreate {
			createIndex = i
		}
	}
	if replyIndex < 0 || createIndex < 0 || replyIndex < createIndex {
		t.Fatalf("expected create request before preferences reply, got frames %+v", frames)
	}
	if got := conv.ProviderConversationID(); got != "sess_29fed201-ab0a-4113-a705-691ede0a4560" {
		t.Fatalf("ProviderConversationID = %q, want the params.sessionId of the preferences request", got)
	}
	// The official-MCP auth storm is declined, never left pending.
	if decline := srv.awaitReply("server-2"); decline.Error == nil || decline.Error.Code != zcodeproto.CodeClientDeclined {
		t.Fatalf("requestOfficialMcpAuthHeaders reply = %+v, want -32001", decline)
	}
	subscribe := srv.awaitRequest(zcodeproto.MethodSessionSubscribe)
	if got := paramsMap(t, subscribe)["deliveryKind"]; got != deliveryKind {
		t.Fatalf("subscribe deliveryKind = %v, want %q", got, deliveryKind)
	}
	if srv.sentMethod(zcodeproto.MethodSessionResume) {
		t.Fatal("Start must not send session/resume")
	}

	// The proven turn: prompt_started precedes the ack and maps to the minted
	// id; the native id, the reasoning row, the streamed text, the committed
	// response, the usage and the settlement follow in capture order.
	c := conv.(*conversation)
	models, err := c.ListModels(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "zai-api-ao/GLM-5.3" || !models[0].Default {
		t.Fatalf("ListModels from the create snapshot = %+v, %v", models, err)
	}
	id := sendTurn(t, c, "Reply with exactly one word: OK")
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	if got := paramsMap(t, send)["content"]; got != "Reply with exactly one word: OK" {
		t.Fatalf("send content = %v", got)
	}
	started := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if started.ProviderTurnID != id {
		t.Fatalf("turn.started id = %q, want minted %q", started.ProviderTurnID, id)
	}
	reasoning := nextEvent(t, conv.Events(), ports.ChatEventActivityStarted)
	if reasoning.ActivityKind != domain.ActivityKindReasoning || reasoning.ProviderTurnID != id {
		t.Fatalf("reasoning row = %+v", reasoning)
	}
	if delta := nextEvent(t, conv.Events(), ports.ChatEventReasoningDelta); delta.ProviderItemID != reasoning.ProviderItemID {
		t.Fatalf("reasoning.delta = %+v", delta)
	}
	var text string
	for ev := nextEvent(t, conv.Events(), ports.ChatEventMessageDelta); ; ev = nextEvent(t, conv.Events(), ports.ChatEventMessageDelta) {
		text += ev.Delta
		if text == "OK" {
			break
		}
	}
	message := nextEvent(t, conv.Events(), ports.ChatEventMessageCompleted)
	if message.Text != "OK" || message.ProviderTurnID != id {
		t.Fatalf("message.completed = %+v", message)
	}
	if usage := nextEvent(t, conv.Events(), ports.ChatEventUsage); usage.Usage == nil || usage.Usage.InputTokens != 270058 || usage.Usage.CachedTokens != 320 {
		t.Fatalf("usage = %+v", usage.Usage)
	}
	completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	if completed.ProviderTurnID != id || completed.TurnState != domain.TurnStateCompleted || completed.NativeTurnID != "turn_be5eae50-2260-43ca-bf5d-1daa514d20e1" {
		t.Fatalf("turn.completed = %+v", completed)
	}
	if got := drainEvents(conv.Events(), 150*time.Millisecond); countKind(got, ports.ChatEventTurnCompleted) != 0 {
		t.Fatalf("prompt_completed settled the turn a second time: %+v", got)
	}
}

// promptConflict is the captured -32010 refusal of a session/send written
// while a prompt was running (p1-create-send-complete.ndjson). AO's own
// SendTurn guard means the driver never writes that second send itself; the
// frame is replayed as the scripted answer to the one send it does write.
func promptConflict(t *testing.T) *rpcError {
	t.Helper()
	f := fixtureFrame(t, "p1-create-send-complete.ndjson", func(line replayLine) bool {
		return line.Dir == "in" && line.Frame.Error != nil && line.Frame.Error.Code == zcodeproto.CodePromptConflict
	})
	return f.Error
}

func TestCreateCarriesMCPServersInTheProvenShape(t *testing.T) {
	d, srv := newTestDriver(t)
	cfg := startConfig(t)
	cfg.MCPServers = []ports.ChatMCPServerConfig{
		{Name: "ao", Type: "stdio", Command: "/tmp/ao-zcode-dev", Args: []string{"mcp"}, Env: map[string]string{"AO_DATA_DIR": "/tmp/ao-v3-data"}},
		{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"},
	}
	conv, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()
	create := srv.awaitRequest(zcodeproto.MethodSessionCreate)
	var params struct {
		MCPServers []zcodeproto.MCPServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(create.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	// The captured create (p9-usage-read-mcp.ndjson) is the reference shape.
	want := fixtureFrame(t, "p9-usage-read-mcp.ndjson", func(line replayLine) bool {
		return line.Dir == "out" && line.Frame.Method == zcodeproto.MethodSessionCreate
	})
	var captured struct {
		MCPServers []zcodeproto.MCPServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(want.Params, &captured); err != nil {
		t.Fatalf("captured params: %v", err)
	}
	if !reflect.DeepEqual(params.MCPServers, captured.MCPServers) {
		t.Fatalf("mcpServers = %+v, want the captured %+v (http servers skipped)", params.MCPServers, captured.MCPServers)
	}

	// A server with no args and no env still sends both keys as empty arrays:
	// omitting them is refused live with -32602 "mcpServers.0: Invalid input".
	d2, srv2 := newTestDriver(t)
	cfg2 := startConfig(t)
	cfg2.MCPServers = []ports.ChatMCPServerConfig{{Name: "bare", Type: "stdio", Command: "/usr/bin/true"}}
	conv2, err := d2.Start(context.Background(), cfg2)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv2.Close() }()
	bare := srv2.awaitRequest(zcodeproto.MethodSessionCreate)
	if !strings.Contains(string(bare.Params), `"mcpServers":[{"name":"bare","command":"/usr/bin/true","args":[],"env":[],"isolation":"session"}]`) {
		t.Fatalf("bare server params = %s", bare.Params)
	}
}

func TestTurnFailedCarriesTheProviderAttribution(t *testing.T) {
	conv, srv := startConversation(t)
	srv.replay(loadReplay(t, "p1-turn-failed-provider.ndjson"))
	id := sendTurn(t, conv, "Reply with exactly one word: OK")
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	if completed.ProviderTurnID != id || completed.TurnState != domain.TurnStateFailed || completed.Err == nil {
		t.Fatalf("turn.completed = %+v", completed)
	}
	text := completed.Err.Error()
	for _, want := range []string{"[1113]", "Insufficient balance", "provider=zai-api-ao", "status=429", "retryable=false"} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure %q lacks %q", text, want)
		}
	}
	if errors.Is(completed.Err, ports.ErrChatAuthRequired) {
		t.Fatal("a provider quota failure read as an auth failure")
	}
	if got := drainEvents(conv.Events(), 150*time.Millisecond); countKind(got, ports.ChatEventTurnCompleted) != 0 {
		t.Fatalf("turn-failed/prompt_failed settled the turn again: %+v", got)
	}
}

// TestZcodeHibernateStopsAppServerAndNativeResumesSession is the zcode analog
// of the codex hibernate test and runs on Windows CI (go.yml), so it is pure
// pipes: nothing execs, no POSIX path assumptions.
func TestZcodeHibernateStopsAppServerAndNativeResumesSession(t *testing.T) {
	d, firstServer := newTestDriver(t)
	cfg := startConfig(t)
	first, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := first.ProviderConversationID()
	provider := first.(*conversation)
	sendTurn(t, provider, "first turn")
	completeTurn(t, provider, firstServer)

	hibernator := first.(ports.ChatProviderHibernator)
	if can, err := hibernator.CanHibernate(context.Background()); err != nil || !can {
		t.Fatalf("CanHibernate = %v, %v; want true on an idle projection", can, err)
	}
	stopped := false
	provider.proc.terminate = func() error {
		stopped = true
		return provider.proc.stop()
	}
	if err := hibernator.Hibernate(); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("hibernate detached the controller but left app-server alive")
	}

	replacement, server := newTestDriver(t)
	resumed, err := replacement.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: cfg.SessionID, DataDir: t.TempDir(), WorkspacePath: cfg.WorkspacePath,
		ProviderConversationID: sessionID, Permissions: cfg.Permissions, HasPriorTurns: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if resumed.ProviderConversationID() != sessionID || !server.sentMethod(zcodeproto.MethodSessionResume) || server.sentMethod(zcodeproto.MethodSessionCreate) {
		t.Fatal("replacement app-server did not resume the same zcode session")
	}
	sendTurn(t, resumed.(*conversation), "second turn")
	send := server.awaitRequest(zcodeproto.MethodSessionSend)
	if got := paramsMap(t, send)["sessionId"]; got != sessionID {
		t.Fatalf("second turn targeted %v, want %q", got, sessionID)
	}
}

func TestCanHibernateRefusesWhileTheProviderIsBusy(t *testing.T) {
	cases := []struct {
		name     string
		snapshot string
	}{
		{"running projection", strings.Replace(testSnapshot, `"status":"idle"`, `"status":"running"`, 1)},
		{"active tool call", strings.Replace(testSnapshot, `"activeToolCalls":[]`, `"activeToolCalls":[{"toolCallId":"tc1"}]`, 1)},
		{"background job", strings.Replace(testSnapshot, `"backgroundJobs":[]`, `"backgroundJobs":[{"id":"j1"}]`, 1)},
		{"pending permission", strings.Replace(testSnapshot, `"pendingPermissions":[]`, `"pendingPermissions":[{"requestId":"r1"}]`, 1)},
		{"pending request", strings.Replace(testSnapshot, `"pendingRequestIds":[]`, `"pendingRequestIds":["r1"]`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conv, srv := startConversation(t)
			srv.respondTo(zcodeproto.MethodSessionRead, tc.snapshot)
			if can, err := conv.CanHibernate(context.Background()); err != nil || can {
				t.Fatalf("CanHibernate = %v, %v; want false", can, err)
			}
		})
	}
	t.Run("read failure leaves the provider running", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.replyError(zcodeproto.MethodSessionRead, zcodeproto.CodeInternal, "boom")
		if can, err := conv.CanHibernate(context.Background()); err == nil || can {
			t.Fatalf("CanHibernate = %v, %v; want an error", can, err)
		}
	})
	t.Run("open turn", func(t *testing.T) {
		conv, srv := startConversation(t)
		sendTurn(t, conv, "hello")
		srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
		nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
		if can, err := conv.CanHibernate(context.Background()); err != nil || can {
			t.Fatalf("CanHibernate = %v, %v; want false with a turn open", can, err)
		}
	})
}

// storePinningPlugin is the real zcode plugin for AugmentRuntimeLaunchEnv with
// the binary and auth answers fixed, so the test neither execs nor reads a
// credential store.
type storePinningPlugin struct{ *zcode.Plugin }

func (storePinningPlugin) ResolveBinary(context.Context) (string, error) { return "zcode", nil }
func (storePinningPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}

func TestStartInjectsCredentialStoreEnv(t *testing.T) {
	t.Setenv("ZCODE_DATA_BASE_DIR", "")

	t.Run("pins the AO-owned store", func(t *testing.T) {
		d, srv := newTestDriverWithPlugin(t, storePinningPlugin{zcode.New()})
		cfg := startConfig(t)
		conv, err := d.Start(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = conv.Close() }()
		want := "ZCODE_DATA_BASE_DIR=" + zcode.CredentialStoreBase(cfg.DataDir)
		if !containsEnv(srv.capturedEnv(), want) {
			t.Fatalf("spawn env lacks %q", want)
		}
	})

	t.Run("overlay value wins", func(t *testing.T) {
		d, srv := newTestDriverWithPlugin(t, storePinningPlugin{zcode.New()})
		cfg := startConfig(t)
		override := filepath.Join(t.TempDir(), "override-store")
		cfg.Env = map[string]string{"ZCODE_DATA_BASE_DIR": override}
		conv, err := d.Start(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = conv.Close() }()
		if !containsEnv(srv.capturedEnv(), "ZCODE_DATA_BASE_DIR="+override) {
			t.Fatalf("spawn env lacks the overlay value")
		}
		if containsEnv(srv.capturedEnv(), "ZCODE_DATA_BASE_DIR="+zcode.CredentialStoreBase(cfg.DataDir)) {
			t.Fatalf("spawn env carries the default store despite an overlay")
		}
	})
}

func containsEnv(env []string, entry string) bool {
	for _, e := range env {
		if e == entry {
			return true
		}
	}
	return false
}

func TestStartPassesDataDirAndSessionToThePlugin(t *testing.T) {
	plugin := &fakePlugin{binary: "zcode", status: ports.AgentAuthStatusAuthorized}
	d, _ := newTestDriverWithPlugin(t, plugin)
	cfg := startConfig(t)
	conv, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	if len(plugin.augments) != 1 || plugin.augments[0].dataDir != cfg.DataDir || plugin.augments[0].sessionID != cfg.SessionID {
		t.Fatalf("AugmentRuntimeLaunchEnv calls = %+v", plugin.augments)
	}
}

func TestStartRejectsRelativeWorkspacePath(t *testing.T) {
	d, srv := newTestDriver(t)
	cfg := startConfig(t)
	cfg.WorkspacePath = "relative/ws"
	if _, err := d.Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Start err = %v, want an absolute-path refusal", err)
	}
	if len(srv.capturedEnv()) != 0 {
		t.Fatal("a rejected start must not spawn")
	}
}

func TestProbeReportsAuthRequired(t *testing.T) {
	d, _ := newTestDriverWithPlugin(t, &fakePlugin{binary: "zcode", status: ports.AgentAuthStatusUnauthorized})
	if _, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("Probe err = %v, want ErrChatAuthRequired", err)
	}
	// Layer 2: Start re-checks because a cached Probe outlives the token.
	if _, err := d.Start(context.Background(), startConfig(t)); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("Start err = %v, want ErrChatAuthRequired", err)
	}
}

func TestProbeReportsBinaryMissing(t *testing.T) {
	d, _ := newTestDriverWithPlugin(t, &fakePlugin{binErr: errors.New("no zcode wrapper"), status: ports.AgentAuthStatusAuthorized})
	if _, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverUnavailable) {
		t.Fatalf("Probe err = %v, want ErrChatDriverUnavailable", err)
	}
}

func TestProbeReportsCapabilitiesAndHarness(t *testing.T) {
	d, _ := newTestDriver(t)
	if d.Harness() != domain.HarnessZcode {
		t.Fatalf("Harness = %q", d.Harness())
	}
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	// Exactly the Phase 1-proven set (DESIGN 8, "2b"): nothing STATIC, and
	// nothing proven left out.
	want := ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true, ports.ChatCapabilityApprovals: true, ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume: true, ports.ChatCapabilityTools: true, ports.ChatCapabilityInteractive: true,
		ports.ChatCapabilityUsage: true, ports.ChatCapabilityModels: true, ports.ChatCapabilityHistory: true,
	}
	if !reflect.DeepEqual(caps, want) {
		t.Fatalf("capabilities = %v, want %v", caps, want)
	}
	if missing := ports.MissingProductionCapabilities(caps); len(missing) != 0 {
		t.Fatalf("missing production capabilities: %v", missing)
	}
	conv, _ := startConversation(t)
	if !reflect.DeepEqual(conv.Capabilities(), want) {
		t.Fatalf("conversation capabilities = %v", conv.Capabilities())
	}
}

func TestSendTurnIsDeferredAndCarriesInputID(t *testing.T) {
	conv, srv := startConversation(t)
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if ref.ProviderTurnID == "" {
		t.Fatal("SendTurn minted no id")
	}
	time.Sleep(50 * time.Millisecond)
	if srv.sentMethod(zcodeproto.MethodSessionSend) {
		t.Fatal("SendTurn wrote session/send before StartDeferredTurn")
	}
	if err := conv.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	params := paramsMap(t, send)
	if params["inputId"] != ref.ProviderTurnID || params["content"] != "hello" || params["sessionId"] != "sess-1" {
		t.Fatalf("session/send params = %s", send.Params)
	}
	if _, ok := params["expectedRevision"]; ok {
		t.Fatal("expectedRevision must be omitted (DESIGN 3.6)")
	}
}

func TestSendTurnRejectsEmptyTextAndBusyConversation(t *testing.T) {
	conv, _ := startConversation(t)
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "  "}); err == nil {
		t.Fatal("empty text accepted")
	}
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "a"}); err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "b"}); err == nil {
		t.Fatal("second SendTurn accepted while one is prepared")
	}
}

func TestPromptConflictFailsTheDeferredStart(t *testing.T) {
	conv, srv := startConversation(t)
	conflict := promptConflict(t)
	srv.replyError(zcodeproto.MethodSessionSend, conflict.Code, conflict.Message)

	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	err = conv.StartDeferredTurn(ref.ProviderTurnID)
	if err == nil || !strings.Contains(err.Error(), "A prompt is already running") {
		t.Fatalf("StartDeferredTurn err = %v, want the provider's message verbatim", err)
	}
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr.Code != zcodeproto.CodePromptConflict {
		t.Fatalf("error does not carry the -32010 code: %v", err)
	}
	if errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatal("a prompt conflict must never read as an auth failure")
	}
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventTurnStarted) != 0 {
		t.Fatalf("turn.started emitted for a rejected send: %+v", got)
	}

	// The tracker was disarmed: a second turn is accepted and maps correctly.
	srv.respondTo(zcodeproto.MethodSessionSend, `{"accepted":true,"sessionId":"sess-1","stateRevision":2}`)
	second := sendTurn(t, conv, "again")
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started","sessionId":"sess-1"}}`)
	started := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if started.ProviderTurnID != second {
		t.Fatalf("turn.started id = %q, want %q", started.ProviderTurnID, second)
	}
}

func TestPromptStartedBeforeAckStillMapsToMintedID(t *testing.T) {
	conv, srv := startConversation(t)
	srv.onRequest(zcodeproto.MethodSessionSend, func(id, _ json.RawMessage) {
		// The real wire order (DESIGN 3.6): the notification precedes the ack.
		srv.push(`{"method":"state.updated","params":{"type":"state.updated","scope":"session","sessionId":"sess-1","revision":2,"reason":"prompt_started","patch":{"status":"running"}}}`)
		srv.respond(id, `{"accepted":true,"sessionId":"sess-1","stateRevision":2}`)
	})
	id := sendTurn(t, conv, "hello")
	got := drainEvents(conv.Events(), 150*time.Millisecond)
	if n := countKind(got, ports.ChatEventTurnStarted); n != 1 {
		t.Fatalf("turn.started count = %d, events %+v", n, got)
	}
	for _, ev := range got {
		if ev.Kind == ports.ChatEventTurnStarted && ev.ProviderTurnID != id {
			t.Fatalf("turn.started id = %q, want minted %q", ev.ProviderTurnID, id)
		}
		if strings.HasPrefix(ev.ProviderTurnID, providerTurnPrefix) {
			t.Fatalf("provider-initiated id minted for AO's own turn: %+v", ev)
		}
	}
}

func TestDiscardDeferredTurnDisarmsTracker(t *testing.T) {
	conv, srv := startConversation(t)
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "first"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	conv.DiscardDeferredTurn(ref.ProviderTurnID)
	if err := conv.StartDeferredTurn(ref.ProviderTurnID); err == nil {
		t.Fatal("a discarded turn started")
	}
	second := sendTurn(t, conv, "second")
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
	started := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if started.ProviderTurnID != second {
		t.Fatalf("turn.started id = %q, want %q (not the discarded %q)", started.ProviderTurnID, second, ref.ProviderTurnID)
	}
}

func completeTurn(t *testing.T, conv *conversation, srv *scriptedServer) {
	t.Helper()
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_completed"}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
}

func TestStandingInstructionsLeadTheFirstTurnOnly(t *testing.T) {
	d, srv := newTestDriver(t)
	cfg := startConfig(t)
	cfg.SystemPrompt = "You are the worker."
	started, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = started.Close() }()
	conv := started.(*conversation)

	sendTurn(t, conv, "task one")
	first := srv.awaitRequest(zcodeproto.MethodSessionSend)
	if got := paramsMap(t, first)["content"]; got != "You are the worker.\n\ntask one" {
		t.Fatalf("first content = %q", got)
	}
	completeTurn(t, conv, srv)

	sendTurn(t, conv, "task two")
	srv.awaitFrame(func(f frame) bool {
		return f.Method == zcodeproto.MethodSessionSend && strings.Contains(string(f.Params), "task two")
	})
	if n := srv.countFrames(func(f frame) bool {
		return f.Method == zcodeproto.MethodSessionSend && strings.Contains(string(f.Params), "You are the worker.")
	}); n != 1 {
		t.Fatalf("preamble sent %d times, want once", n)
	}

	// A resume with prior turns never re-sends the role.
	d2, srv2 := newTestDriver(t)
	resumed, err := d2.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions, SystemPrompt: "You are the worker.", HasPriorTurns: true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = resumed.Close() }()
	sendTurn(t, resumed.(*conversation), "task three")
	send := srv2.awaitRequest(zcodeproto.MethodSessionSend)
	if got := paramsMap(t, send)["content"]; got != "task three" {
		t.Fatalf("resumed content = %q, want bare", got)
	}

	// A live reconnect before the opening prompt was dispatched still leads
	// with the role: the preamble is armed on that branch as well.
	d3, srv3, _ := newHostTestDriver(t, true, 0)
	live, err := d3.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions, SystemPrompt: "You are the worker.", HasPriorTurns: false,
	})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	defer func() { _ = live.Close() }()
	activateLive(t, live, "")
	sendTurn(t, live.(*conversation), "task four")
	if got := paramsMap(t, srv3.awaitRequest(zcodeproto.MethodSessionSend))["content"]; got != "You are the worker.\n\ntask four" {
		t.Fatalf("live-reconnect first content = %q, want the role to lead", got)
	}
}

func TestTurnLifecycleFromStateUpdatedOnly(t *testing.T) {
	conv, srv := startConversation(t)
	id := sendTurn(t, conv, "hello")
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started","patch":{"status":"running"}}}`)
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_completed","patch":{}}}`)
	got := drainEvents(conv.Events(), 150*time.Millisecond)
	if countKind(got, ports.ChatEventTurnStarted) != 1 || countKind(got, ports.ChatEventTurnCompleted) != 1 {
		t.Fatalf("events = %+v", got)
	}
	if countKind(got, ports.ChatEventMessageCompleted) != 0 {
		t.Fatal("message.completed emitted with no subscription text (the no-subscribe floor has none)")
	}
	for _, ev := range got {
		if ev.Kind == ports.ChatEventTurnCompleted && (ev.ProviderTurnID != id || ev.TurnState != domain.TurnStateCompleted) {
			t.Fatalf("turn.completed = %+v", ev)
		}
	}
}

func TestReplyTextFromTurnCompletedResponse(t *testing.T) {
	conv, srv := startConversation(t)
	id := sendTurn(t, conv, "hello")
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	srv.push(`{"method":"session/event","params":{"type":"turn.completed","turnId":"t-native","sessionId":"sess-1","payload":{"response":"OK","resultType":"success"}}}`)
	message := nextEvent(t, conv.Events(), ports.ChatEventMessageCompleted)
	if message.Text != "OK" || message.ProviderTurnID != id {
		t.Fatalf("message.completed = %+v", message)
	}
	completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	if completed.TurnState != domain.TurnStateCompleted || completed.ProviderTurnID != id {
		t.Fatalf("turn.completed = %+v", completed)
	}
	// The prompt_* fallback must not settle the turn a second time.
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_completed"}}`)
	if got := drainEvents(conv.Events(), 100*time.Millisecond); countKind(got, ports.ChatEventTurnCompleted) != 0 {
		t.Fatalf("turn settled twice: %+v", got)
	}
}

func TestInterruptSendsSessionStop(t *testing.T) {
	conv, srv := startConversation(t)
	id := sendTurn(t, conv, "hello")
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if err := conv.Interrupt(context.Background(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	stop := srv.awaitRequest(zcodeproto.MethodSessionStop)
	if got := paramsMap(t, stop)["sessionId"]; got != "sess-1" {
		t.Fatalf("session/stop params = %s", stop.Params)
	}
}

func TestInterruptWithoutActiveTurnReturnsNoActiveTurn(t *testing.T) {
	conv, srv := startConversation(t)
	if err := conv.Interrupt(context.Background(), ""); !errors.Is(err, ports.ErrChatNoActiveTurn) {
		t.Fatalf("Interrupt err = %v, want ErrChatNoActiveTurn", err)
	}
	if srv.sentMethod(zcodeproto.MethodSessionStop) {
		t.Fatal("session/stop written with no active turn")
	}
}

func TestInterruptedTurnSettlesInterrupted(t *testing.T) {
	conv, srv := startConversation(t)
	id := sendTurn(t, conv, "count slowly")
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if err := conv.Interrupt(context.Background(), ""); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	srv.push(`{"method":"state.updated","params":{"reason":"prompt_failed"}}`)
	completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	if completed.TurnState != domain.TurnStateInterrupted || completed.ProviderTurnID != id {
		t.Fatalf("turn.completed = %+v, want interrupted", completed)
	}
}

func TestResumeSendsSessionResumeAndNeverCreates(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = conv.Close() }()
	resume := srv.awaitRequest(zcodeproto.MethodSessionResume)
	params := paramsMap(t, resume)
	if params["sessionId"] != "sess-1" {
		t.Fatalf("session/resume params = %s", resume.Params)
	}
	if _, ok := params["workspace"]; !ok {
		t.Fatalf("session/resume lacks workspace: %s", resume.Params)
	}
	srv.awaitRequest(zcodeproto.MethodSessionSubscribe)
	if srv.sentMethod(zcodeproto.MethodSessionCreate) {
		t.Fatal("Resume sent session/create")
	}
	if conv.ProviderConversationID() != "sess-1" {
		t.Fatalf("ProviderConversationID = %q", conv.ProviderConversationID())
	}
	if conv.(*conversation).ReconnectedLive() {
		t.Fatal("a replacement-process resume reported as live reconnect")
	}
}

func TestResumeFailureDoesNotFallBackToCreate(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError(zcodeproto.MethodSessionResume, zcodeproto.CodeSessionUnavailable, "Session not found: sess-gone")
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-gone", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("Resume err = %v, want ErrChatResumeFailed", err)
	}
	if srv.sentMethod(zcodeproto.MethodSessionCreate) {
		t.Fatal("Resume fell back to session/create")
	}
}

func TestResumeFreshIfMissingCreatesOnlyOnSessionNotFound(t *testing.T) {
	t.Run("not found creates", func(t *testing.T) {
		d, srv := newTestDriver(t)
		srv.replyError(zcodeproto.MethodSessionResume, zcodeproto.CodeSessionUnavailable, "Session not found: sess-gone")
		conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
			SessionID: "ao-1", ProviderConversationID: "sess-gone", FreshIfMissing: true,
			DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeBypassPermissions,
		})
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		defer func() { _ = conv.Close() }()
		srv.awaitRequest(zcodeproto.MethodSessionCreate)
		if conv.ProviderConversationID() != "sess-1" {
			t.Fatalf("fresh session id = %q", conv.ProviderConversationID())
		}
	})
	t.Run("not active is not missing", func(t *testing.T) {
		d, srv := newTestDriver(t)
		srv.replyError(zcodeproto.MethodSessionResume, zcodeproto.CodeSessionUnavailable, "Session is not active: sess-1")
		_, err := d.Resume(context.Background(), ports.ChatResumeConfig{
			SessionID: "ao-1", ProviderConversationID: "sess-1", FreshIfMissing: true,
			DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeBypassPermissions,
		})
		if !errors.Is(err, ports.ErrChatResumeFailed) {
			t.Fatalf("Resume err = %v, want ErrChatResumeFailed", err)
		}
		if srv.sentMethod(zcodeproto.MethodSessionCreate) {
			t.Fatal("eviction treated as a missing session")
		}
	})
}

func TestReconnectSkipsCreateResumeAndSubscribe(t *testing.T) {
	d, srv, configs := newHostTestDriver(t, true, 41)
	conv, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	defer func() { _ = conv.Close() }()
	if len(*configs) != 1 || !(*configs)[0].ReconnectOnly || (*configs)[0].Protocol != persistenthost.ProtocolRaw {
		t.Fatalf("host config = %+v", *configs)
	}
	if !conv.(*conversation).ReconnectedLive() || !conv.(*conversation).PreservesProviderOnClose() {
		t.Fatal("live reconnect not reported")
	}
	read := srv.awaitRequest(zcodeproto.MethodSessionRead)
	if rawID(*read.ID) != "42" {
		t.Fatalf("first request id after reconnect = %s, want 42 (NextRequestID+1)", *read.ID)
	}
	// The reconcile read must succeed, so it carries the proven positive
	// limit: a zero messageLimit is refused by the server (-32602) and would
	// keep every host-replayed request, resolved ones included.
	if limit := messageLimitOf(t, read); limit < 1 {
		t.Fatalf("reconcile session/read messageLimit = %d, want >= 1: %s", limit, read.Params)
	}
	for _, method := range []string{zcodeproto.MethodSessionCreate, zcodeproto.MethodSessionResume, zcodeproto.MethodSessionSubscribe} {
		if srv.sentMethod(method) {
			t.Fatalf("live reconnect sent %s", method)
		}
	}
	activateLive(t, conv, "")
	sendTurn(t, conv.(*conversation), "hello")
	send := srv.awaitRequest(zcodeproto.MethodSessionSend)
	if rawID(*send.ID) != "43" {
		t.Fatalf("send id = %s, want 43", *send.ID)
	}
}

// busySnapshot is testSnapshot with the app-server reporting a running turn.
var busySnapshot = strings.Replace(testSnapshot,
	`"pendingRequestIds":[]`, `"activeTurnId":"t-native","pendingRequestIds":[]`, 1)

func reconnectBusy(t *testing.T) (*conversation, *scriptedServer) {
	t.Helper()
	d, srv, _ := newHostTestDriver(t, true, 10)
	srv.respondTo(zcodeproto.MethodSessionRead, busySnapshot)
	conv, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions, HasPriorTurns: true,
	})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	return conv.(*conversation), srv
}

func TestLiveReconnectReplayedCompletionSettlesTheRestoredTurn(t *testing.T) {
	completion := `{"method":"session/event","params":{"type":"turn.completed","turnId":"t-native","sessionId":"sess-1","payload":{"response":"OK","resultType":"success"}}}`

	t.Run("replay is held until the durable turn is restored", func(t *testing.T) {
		conv, srv := reconnectBusy(t)
		// The host replays the detach window the moment the daemon attaches,
		// before the service has handed the driver the durable turn.
		srv.push(completion)
		if got := drainEvents(conv.Events(), 100*time.Millisecond); len(got) != 0 {
			t.Fatalf("events released before activation: %+v", got)
		}
		activateLive(t, conv, "durable-uuid")
		message := nextEvent(t, conv.Events(), ports.ChatEventMessageCompleted)
		if message.ProviderTurnID != "durable-uuid" || message.Text != "OK" {
			t.Fatalf("message.completed = %+v, want the restored durable turn", message)
		}
		completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
		if completed.ProviderTurnID != "durable-uuid" || completed.NativeTurnID != "t-native" || completed.TurnState != domain.TurnStateCompleted {
			t.Fatalf("turn.completed = %+v", completed)
		}
	})

	t.Run("interrupt targets the restored turn", func(t *testing.T) {
		conv, srv := reconnectBusy(t)
		activateLive(t, conv, "durable-uuid")
		if err := conv.Interrupt(context.Background(), "durable-uuid"); err != nil {
			t.Fatalf("Interrupt: %v", err)
		}
		srv.awaitRequest(zcodeproto.MethodSessionStop)
		srv.push(`{"method":"state.updated","params":{"reason":"prompt_failed"}}`)
		completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
		if completed.ProviderTurnID != "durable-uuid" || completed.TurnState != domain.TurnStateInterrupted {
			t.Fatalf("turn.completed = %+v, want interrupted durable-uuid", completed)
		}
	})
}

func TestLiveReconnectWithUnownedActiveTurnIsInconclusive(t *testing.T) {
	conv, _ := reconnectBusy(t)
	err := conv.ActivateLiveReconnect(context.Background(), "")
	if !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("ActivateLiveReconnect with an unowned active turn = %v, want ErrChatRecoveryInconclusive", err)
	}
	// The service discards the conversation. No pump ever ran, so Close must
	// drain the relay itself rather than leave it parked on the replay.
	if err := conv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	awaitEventsClosed(t, conv.Events(), 5*time.Second)
}

func TestConversationClosedBeforeItsPumpDrainsTheRelay(t *testing.T) {
	srv, proc := newScriptedServer(t)
	conv := newConversation(proc, slog.New(slog.DiscardHandler), nil)
	// The server writes startup notifications before it answers the first
	// request (fixture line 2); with no pump they sit in the relay's queue.
	srv.push(`{"method":"startup/storageState","params":{}}`)
	srv.push(`{"method":"process/resourceSample","params":{}}`)
	// A failed create terminates the conversation without ever starting it.
	if err := conv.Terminate(); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	select {
	case <-conv.conn.wait():
	case <-time.After(time.Second):
		t.Fatal("read loop did not end")
	}
	// The stream closes only once the relay has been drained and exited.
	awaitEventsClosed(t, conv.Events(), time.Second)
}

func TestSubscribeRefusalFailsStartAndResume(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		d, srv := newTestDriver(t)
		srv.replyError(zcodeproto.MethodSessionSubscribe, zcodeproto.CodeMethodNotFound, "Method not found")
		conv, err := d.Start(context.Background(), startConfig(t))
		if err == nil || !strings.Contains(err.Error(), zcodeproto.MethodSessionSubscribe) {
			t.Fatalf("Start err = %v, want a session/subscribe failure", err)
		}
		if conv != nil {
			t.Fatal("a conversation was returned without session/event delivery")
		}
		if srv.sentMethod(zcodeproto.MethodSessionSend) {
			t.Fatal("session/send written on a half-open session")
		}
	})
	t.Run("resume", func(t *testing.T) {
		d, srv := newTestDriver(t)
		srv.replyError(zcodeproto.MethodSessionSubscribe, zcodeproto.CodeMethodNotFound, "Method not found")
		conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
			SessionID: "ao-1", ProviderConversationID: "sess-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
			Permissions: ports.PermissionModeBypassPermissions,
		})
		if !errors.Is(err, ports.ErrChatResumeFailed) || !strings.Contains(err.Error(), zcodeproto.MethodSessionSubscribe) {
			t.Fatalf("Resume err = %v, want ErrChatResumeFailed from session/subscribe", err)
		}
		if conv != nil {
			t.Fatal("a conversation was returned without session/event delivery")
		}
	})
}

func TestEvictedSendResumesSubscribesAndRetries(t *testing.T) {
	conv, srv := startConversation(t)
	var sends atomic.Int32
	srv.onRequest(zcodeproto.MethodSessionSend, func(id, _ json.RawMessage) {
		if sends.Add(1) == 1 {
			srv.push(`{"id":` + string(id) + `,"error":{"code":-32004,"message":"Session is not active: sess-1"}}`)
			return
		}
		srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
		srv.respond(id, `{"accepted":true,"sessionId":"sess-1","stateRevision":2}`)
	})
	id := sendTurn(t, conv, "hello")

	var order []string
	var inputIDs []any
	for _, f := range srv.frames() {
		if f.ID == nil || f.Method == "" {
			continue
		}
		if len(order) == 0 && f.Method != zcodeproto.MethodSessionSend {
			continue // the create exchange
		}
		order = append(order, f.Method)
		switch f.Method {
		case zcodeproto.MethodSessionSend:
			inputIDs = append(inputIDs, paramsMap(t, f)["inputId"])
		case zcodeproto.MethodSessionResume:
			params := paramsMap(t, f)
			if params["sessionId"] != "sess-1" || params["workspace"] == nil {
				t.Fatalf("session/resume after eviction = %s", f.Params)
			}
		}
	}
	want := []string{zcodeproto.MethodSessionSend, zcodeproto.MethodSessionResume, zcodeproto.MethodSessionSubscribe, zcodeproto.MethodSessionSend}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("frame order = %v, want %v", order, want)
	}
	if len(inputIDs) != 2 || inputIDs[0] != id || inputIDs[1] != id {
		t.Fatalf("retry changed the inputId: %v", inputIDs)
	}
	got := drainEvents(conv.Events(), 150*time.Millisecond)
	if countKind(got, ports.ChatEventTurnStarted) != 1 || countKind(got, ports.ChatEventTurnCompleted) != 0 {
		t.Fatalf("events after the retried send = %+v", got)
	}
}

func TestDeferredStartFailureAfterPromptStartedFollowsTurnStarted(t *testing.T) {
	conv, srv := startConversation(t)
	release := make(chan struct{})
	srv.onRequest(zcodeproto.MethodSessionSend, func(id, _ json.RawMessage) {
		srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
		<-release
		srv.push(`{"id":` + string(id) + `,"error":{"code":-32031,"message":"runtime rejected the prompt"}}`)
	})
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- conv.StartDeferredTurn(ref.ProviderTurnID) }()
	if started := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted); started.ProviderTurnID != ref.ProviderTurnID {
		t.Fatalf("turn.started = %+v", started)
	}
	close(release)
	if err := <-startErr; err == nil || !strings.Contains(err.Error(), "runtime rejected") {
		t.Fatalf("StartDeferredTurn err = %v", err)
	}
	completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	if completed.ProviderTurnID != ref.ProviderTurnID || completed.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn.completed = %+v, want failed %q after turn.started", completed, ref.ProviderTurnID)
	}
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "again"}); err != nil {
		t.Fatalf("SendTurn after the failure: %v", err)
	}
}

func TestResolveSessionIDPrefersTheRuntimePreferencesRequest(t *testing.T) {
	createWith := func(srv *scriptedServer, preferences bool, result string) {
		srv.onRequest(zcodeproto.MethodSessionCreate, func(id, _ json.RawMessage) {
			if preferences {
				srv.push(`{"id":"server-1","method":"session/requestRuntimePreferences","params":{"sessionId":"sess-1"}}`)
				srv.awaitReply("server-1")
			}
			srv.respond(id, result)
		})
	}

	t.Run("session.id is not a source", func(t *testing.T) {
		d, srv := newTestDriver(t)
		createWith(srv, true, `{"session":{"id":"sess-wrong-key"},"settings":{"model":{}}}`)
		conv, err := d.Start(context.Background(), startConfig(t))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = conv.Close() }()
		if got := conv.ProviderConversationID(); got != "sess-1" {
			t.Fatalf("ProviderConversationID = %q, want the request's sess-1 (session.id is ignored)", got)
		}
	})

	t.Run("mismatching snapshot loses and is logged", func(t *testing.T) {
		var buf strings.Builder
		d, srv := newTestDriver(t)
		d.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		createWith(srv, true, strings.Replace(testSnapshot, `"sessionId":"sess-1"`, `"sessionId":"sess-other"`, 1))
		conv, err := d.Start(context.Background(), startConfig(t))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = conv.Close() }()
		if got := conv.ProviderConversationID(); got != "sess-1" {
			t.Fatalf("ProviderConversationID = %q, want the request's sess-1", got)
		}
		if !strings.Contains(buf.String(), "session id mismatch") {
			t.Fatalf("mismatch not logged: %s", buf.String())
		}
	})

	t.Run("snapshot alone is accepted", func(t *testing.T) {
		d, srv := newTestDriver(t)
		createWith(srv, false, testSnapshot)
		conv, err := d.Start(context.Background(), startConfig(t))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = conv.Close() }()
		if got := conv.ProviderConversationID(); got != "sess-1" {
			t.Fatalf("ProviderConversationID = %q", got)
		}
	})

	t.Run("no source fails the start", func(t *testing.T) {
		d, srv := newTestDriver(t)
		createWith(srv, false, `{"session":{"id":"sess-wrong-key"},"settings":{"model":{}}}`)
		if _, err := d.Start(context.Background(), startConfig(t)); err == nil || !strings.Contains(err.Error(), "returned no session id") {
			t.Fatalf("Start err = %v, want a missing-id failure", err)
		}
	})
}

func TestUsagePollReadsAFreshSnapshotForContext(t *testing.T) {
	t.Run("fresh projection", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.respondTo(zcodeproto.MethodSessionUsage, `{"inputTokens":10,"outputTokens":5,"cacheReadTokens":2,"totalTokens":15}`)
		// The creation-time snapshot said 1200; the post-turn read says otherwise.
		srv.respondTo(zcodeproto.MethodSessionRead, strings.Replace(testSnapshot, `"contextUsed":1200`, `"contextUsed":4321`, 1))
		sendTurn(t, conv, "hello")
		completeTurn(t, conv, srv)
		usage := nextEvent(t, conv.Events(), ports.ChatEventUsage)
		if usage.Usage == nil || !usage.Usage.TotalsKnown || usage.Usage.InputTokens != 10 || usage.Usage.CachedTokens != 2 {
			t.Fatalf("usage = %+v", usage.Usage)
		}
		if !usage.Usage.ContextKnown || usage.Usage.ContextUsed != 4321 || usage.Usage.ContextWindow != 200000 {
			t.Fatalf("context = %+v, want the post-turn session/read projection", usage.Usage)
		}
		read := srv.awaitRequest(zcodeproto.MethodSessionRead)
		if limit := messageLimitOf(t, read); limit < 1 {
			t.Fatalf("post-turn session/read messageLimit = %d, want >= 1 (0 is refused): %s", limit, read.Params)
		}
	})
	t.Run("inline usage still reads the context off a fresh snapshot", func(t *testing.T) {
		// Every proven completed turn carries turn.completed.usage; the context
		// meter must fill on that path too (DESIGN 4.2), without a session/usage
		// call and without repeating the totals the inline event reported.
		conv, srv := startConversation(t)
		srv.respondTo(zcodeproto.MethodSessionRead, strings.Replace(testSnapshot, `"contextUsed":1200`, `"contextUsed":4321`, 1))
		sendTurn(t, conv, "hello")
		srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
		nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
		srv.push(`{"method":"session/event","params":{"type":"turn.completed","turnId":"t-native","sessionId":"sess-1","payload":{"response":"OK","resultType":"success","usage":{"source":"provider","inputTokens":10,"outputTokens":2,"cacheReadTokens":1,"totalTokens":12}}}}`)
		inline := nextEvent(t, conv.Events(), ports.ChatEventUsage)
		if inline.Usage == nil || !inline.Usage.TotalsKnown || inline.Usage.InputTokens != 10 || inline.Usage.ContextKnown {
			t.Fatalf("inline usage = %+v", inline.Usage)
		}
		nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
		occupancy := nextEvent(t, conv.Events(), ports.ChatEventUsage)
		if occupancy.Usage == nil || !occupancy.Usage.ContextKnown || occupancy.Usage.ContextUsed != 4321 || occupancy.Usage.ContextWindow != 200000 {
			t.Fatalf("context usage = %+v, want the post-turn projection", occupancy.Usage)
		}
		if occupancy.Usage.TotalsKnown || occupancy.Usage.InputTokens != 0 {
			t.Fatalf("context usage repeated totals: %+v", occupancy.Usage)
		}
		if srv.sentMethod(zcodeproto.MethodSessionUsage) {
			t.Fatal("session/usage was read although turn.completed carried usage")
		}
	})
	t.Run("unreadable projection is omitted, not stale", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.respondTo(zcodeproto.MethodSessionUsage, `{"inputTokens":10,"outputTokens":5,"totalTokens":15}`)
		srv.replyError(zcodeproto.MethodSessionRead, zcodeproto.CodeInternal, "boom")
		sendTurn(t, conv, "hello")
		completeTurn(t, conv, srv)
		usage := nextEvent(t, conv.Events(), ports.ChatEventUsage)
		if usage.Usage == nil || !usage.Usage.TotalsKnown || usage.Usage.ContextKnown || usage.Usage.ContextUsed != 0 {
			t.Fatalf("usage = %+v, want totals only", usage.Usage)
		}
	})
}

func TestPersistentStartUsesTheHostWithProvenArgv(t *testing.T) {
	d, _, configs := newHostTestDriver(t, false, 0)
	cfg := startConfig(t)
	conv, err := d.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()
	got := (*configs)[0]
	want := []string{"zcode", "app-server", "--stdio", "--cwd", cfg.WorkspacePath}
	if !reflect.DeepEqual(got.Argv, want) {
		t.Fatalf("argv = %v, want %v", got.Argv, want)
	}
	if got.Protocol != persistenthost.ProtocolRaw || got.SessionID != "ao-1" || got.Workdir != cfg.WorkspacePath {
		t.Fatalf("host config = %+v", got)
	}
}

func TestControllerStopIsAnnounced(t *testing.T) {
	conv, srv := startConversation(t)
	srv.push(`{"id":"server-9","method":"interaction/requestPermission","params":{"requestId":"r1","toolCallId":"tc1","toolName":"Bash","options":[{"optionId":"allow","name":"Allow","response":{"decision":"allow"}}]}}`)
	nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)

	_ = srv.toClient.Close()

	stopped := nextEvent(t, conv.Events(), ports.ChatEventControllerState)
	for stopped.ControllerState != ports.ChatControllerStopped {
		stopped = nextEvent(t, conv.Events(), ports.ChatEventControllerState)
	}
	if _, ok := <-conv.Events(); ok {
		t.Fatal("Events() still open after stop")
	}
	if err := conv.ResolveRequest(context.Background(), "r1", ports.ChatDecision{ID: "allow"}); !errors.Is(err, errConversationClosed) {
		t.Fatalf("ResolveRequest after stop = %v, want closed", err)
	}
}

func TestCloseClosesEventsExactlyOnce(t *testing.T) {
	conv, _ := startConversation(t)
	if err := conv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := conv.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-conv.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("Events() did not close after Close")
		}
	}
}
