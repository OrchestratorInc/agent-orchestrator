package httpd

// Chat traffic benchmark harness. It replays committed Codex and Claude
// fixtures through the daemon's own chat projector, SQLite triggers, CDC
// stream, HTTP router, and remote LAN middleware, all in process on loopback,
// and measures what a simulated renderer reads on its sockets. See
// docs/performance/chat-traffic-benchmark.md and backend/internal/chattraffic.
//
// Without AO_CHATTRAFFIC_OUT it runs as an ordinary test: the replay must
// reproduce the committed expected states.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/chattraffic"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const (
	benchEnvOut       = "AO_CHATTRAFFIC_OUT"
	benchEnvSide      = "AO_CHATTRAFFIC_SIDE"
	benchEnvSourceRef = "AO_CHATTRAFFIC_SOURCE_REF"
	benchEnvDirtyDiff = "AO_CHATTRAFFIC_DIRTY_DIFF_SHA256"
	benchEnvCeilings  = "AO_CHATTRAFFIC_CEILINGS_SHA256"
	benchEnvUpdate    = "AO_CHATTRAFFIC_UPDATE_EXPECTED"
	// benchEnvDump names a directory that receives every request, decoded
	// response body, event frame and live-turn step as JSON lines, one file per
	// scenario, for analysis. It never changes a measurement.
	benchEnvDump = "AO_CHATTRAFFIC_DUMP_DIR"

	benchProject  = domain.ProjectID("bench-project")
	benchSession  = domain.SessionID("bench-project-1")
	benchPassword = "chattraffic-dummy-password"
	// Chromium's Accept-Encoding; the daemon negotiates gzip from it.
	benchAcceptEncoding = "gzip, deflate, br, zstd"
	benchStepTimeout    = 15 * time.Second
)

// simulatorClientRevisions lists every renderer revision this simulator was
// checked against (chattraffic.ListDigest of ClientSourceFiles), with what it
// models there. A revision not listed is reported as unmodeled, never silently
// compared. In neither does the read that brings a closed local chat up to date
// as it reopens take part in a scenario.
var simulatorClientRevisions = map[string]string{
	// A local chat opens with a 30-message page and reads older history 200 at a
	// time; each conversation event refetches every loaded page.
	"4158ecbf3e028c51401b03701270b63ea9dc13926c32b47abf2fbd3b9adece6a": "main c709ef2a73",
	// The same, except that an open local chat reads only the rows above its
	// floor and merges them, refetching every loaded page when it cannot.
	"e02e80959e5b57cdb63b66e51d6f6d3d3ceddbbbecf0c7f0e963ce4bc7cdec1f": "main c709ef2a73 with the local tail refresh (#8502)",
}

// simulatorClientFor is the reviewed revision a run's renderer matches, or a
// digest no renderer has when it matches none.
func simulatorClientFor(clientDigest string) string {
	if _, ok := simulatorClientRevisions[clientDigest]; ok {
		return clientDigest
	}
	return chattraffic.SHA256Hex([]byte("unreviewed renderer revision"))
}

func TestChatTrafficBenchmark(t *testing.T) {
	if os.Getenv(benchEnvOut) == "" && os.Getenv("AO_CHATTRAFFIC") != "1" && os.Getenv(benchEnvUpdate) != "1" {
		t.Skip("set AO_CHATTRAFFIC=1, or run scripts/chat-traffic-benchmark.sh, to replay the chat traffic benchmark")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := loadBenchFixtures(t, repoRoot)
	policy, clientDigest := readBenchClientPolicy(t, repoRoot)

	// Heartbeats are wall-clock frames; a deterministic replay excludes them.
	savedHeartbeat := eventsHeartbeatInterval
	eventsHeartbeatInterval = time.Hour
	t.Cleanup(func() { eventsHeartbeatInterval = savedHeartbeat })

	var results []benchResult
	for _, fixture := range fixtures {
		for _, transport := range chattraffic.Transports {
			results = append(results, runBenchWorld(t, fixture, transport, policy)...)
		}
	}

	update := os.Getenv(benchEnvUpdate) == "1"
	expectedDir := filepath.Join(repoRoot, filepath.FromSlash(chattraffic.ExpectedDir))
	if update {
		if err := os.MkdirAll(expectedDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// With an output path, the artifact's state digests carry every semantic
	// difference to the comparator, so they are logged here rather than failing
	// the test. Any other failure means the replay broke an invariant, and the
	// run writes no artifact (see below).
	out := os.Getenv(benchEnvOut)
	reportState := t.Errorf
	if out != "" {
		reportState = t.Logf
	}
	scenarios := make([]chattraffic.Scenario, 0, len(results))
	for _, result := range results {
		scenario := result.scenario
		name := scenario.Provider + "-" + scenario.Phase + "-" + scenario.Transport + ".txt"
		path := filepath.Join(expectedDir, name)
		if update {
			if err := os.WriteFile(path, []byte(result.serverText), 0o644); err != nil { //nolint:gosec // committed test data.
				t.Fatal(err)
			}
		}
		expected, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read expected state %s (set %s=1 to create it): %v", name, benchEnvUpdate, err)
		}
		if problem := checkOracle(expectedDir, result); problem != "" && result.fixtureProblem == "" {
			result.fixtureProblem = problem
		}
		clientText := result.clientText
		if result.fixtureProblem != "" {
			reportState("%s: fixture content check failed: %s", scenario.Key(), result.fixtureProblem)
			clientText = "fixture check failed: " + result.fixtureProblem + "\n" + clientText
		}
		scenario.State.ExpectedDigest = chattraffic.SHA256Hex(expected)
		scenario.State.ClientDigest = chattraffic.SHA256Hex([]byte(clientText))
		scenario.State.ServerDigest = chattraffic.SHA256Hex([]byte(result.serverText))
		if string(expected) != result.serverText {
			reportState("%s: server state differs from %s:\n%s", scenario.Key(), name, firstDifference(string(expected), result.serverText))
		}
		if clientText != result.serverText {
			reportState("%s: simulated client state differs from the server:\n%s", scenario.Key(), firstDifference(result.serverText, clientText))
		}
		scenarios = append(scenarios, scenario)
	}

	if out == "" {
		return
	}
	if t.Failed() {
		t.Fatal("the replay broke an invariant; no run artifact was written")
	}
	fixtureFiles, err := chattraffic.DirectoryDigests(repoRoot, chattraffic.FixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	expectedFiles, err := chattraffic.DirectoryDigests(repoRoot, chattraffic.ExpectedDir)
	if err != nil {
		t.Fatal(err)
	}
	harnessFiles, err := chattraffic.HarnessFileDigests(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	dirtyDiff := os.Getenv(benchEnvDirtyDiff)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	run := chattraffic.Run{
		SchemaVersion:         chattraffic.RunSchema,
		Side:                  os.Getenv(benchEnvSide),
		SourceRef:             os.Getenv(benchEnvSourceRef),
		Dirty:                 dirtyDiff != "",
		DirtyDiffSha256:       dirtyDiff,
		GoVersion:             runtime.Version(),
		GOOS:                  runtime.GOOS,
		GOARCH:                runtime.GOARCH,
		HarnessSha256:         chattraffic.ListDigest(harnessFiles),
		FixturesSha256:        chattraffic.ListDigest(append(fixtureFiles, expectedFiles...)),
		CeilingsSha256:        os.Getenv(benchEnvCeilings),
		ClientSourceSha256:    clientDigest,
		SimulatorClientSha256: simulatorClientFor(clientDigest),
		ClientPolicy:          policy,
		Nonce:                 hex.EncodeToString(nonce),
		Scenarios:             scenarios,
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	// The harness holds itself to the same reader a comparison uses.
	if _, err := chattraffic.DecodeRun(data); err != nil {
		t.Fatalf("run artifact is invalid: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // benchmark output.
		t.Fatal(err)
	}
}

type benchResult struct {
	scenario   chattraffic.Scenario
	clientText string
	serverText string
	// oracleText is the server state with whole outputs, which the
	// fixture-only oracle derives without any transport's output limit.
	oracleText     string
	fixtureProblem string
}

func loadBenchFixtures(t *testing.T, repoRoot string) []chattraffic.Fixture {
	t.Helper()
	var fixtures []chattraffic.Fixture
	for _, provider := range chattraffic.Providers {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(chattraffic.FixturesDir), provider+".json"))
		if err != nil {
			t.Fatal(err)
		}
		fixture, err := chattraffic.DecodeFixture(data)
		if err != nil {
			t.Fatalf("fixture %s: %v", provider, err)
		}
		if fixture.Provider != provider {
			t.Fatalf("fixture %s.json names provider %q", provider, fixture.Provider)
		}
		fixtures = append(fixtures, fixture)
	}
	return fixtures
}

// readBenchClientPolicy reads the renderer's paging policy from this
// revision's source, so a page-size change shows up as a policy change and is
// applied, rather than appearing as an unexplained byte difference.
func readBenchClientPolicy(t *testing.T, repoRoot string) (chattraffic.ClientPolicy, string) {
	t.Helper()
	var files []chattraffic.FileDigest
	sources := map[string]string{}
	for _, rel := range chattraffic.ClientSourceFiles {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read renderer source %s: %v", rel, err)
		}
		sources[rel] = string(data)
		files = append(files, chattraffic.FileDigest{Path: rel, Sha256: chattraffic.SHA256Hex(data)})
	}
	constant := func(rel, name string) int64 {
		matches := regexp.MustCompile(`(?m)^const `+name+` = ([0-9_]+);`).FindAllStringSubmatch(sources[rel], -1)
		if len(matches) != 1 {
			t.Fatalf("%s must declare %s exactly once; found %d", rel, name, len(matches))
		}
		value, err := strconv.ParseInt(strings.ReplaceAll(matches[0][1], "_", ""), 10, 64)
		if err != nil || value <= 0 {
			t.Fatalf("%s has an unusable %s", rel, name)
		}
		return value
	}
	hook, transport := chattraffic.ClientSourceFiles[0], chattraffic.ClientSourceFiles[1]
	return chattraffic.ClientPolicy{
		LocalPageSize:       constant(hook, "LOCAL_INITIAL_PAGE_SIZE"),
		LocalOlderPageSize:  constant(hook, "LOCAL_OLDER_PAGE_SIZE"),
		LocalTailRefresh:    strings.Contains(sources[transport], "refreshLocalConversationTail("),
		RemoteLivePageSize:  constant(hook, "REMOTE_LIVE_PAGE_SIZE"),
		RemoteOlderPageSize: constant(hook, "REMOTE_OLDER_PAGE_SIZE"),
		RemoteOutputLimit:   constant(hook, "REMOTE_OUTPUT_LIMIT"),
		InvalidateWindowMs:  constant(transport, "INVALIDATE_WINDOW_MS"),
	}, chattraffic.ListDigest(files)
}

/* ---- the daemon side ---------------------------------------------------- */

// benchWorld is one isolated daemon slice: scratch SQLite with production
// migrations and triggers, the production chat service and lifecycle reducer,
// the CDC poller and broadcaster, and the production router served on a
// loopback httptest listener. Nothing binds beyond 127.0.0.1.
type benchWorld struct {
	t              *testing.T
	ctx            context.Context
	fixture        chattraffic.Fixture
	transport      string
	store          *sqlite.Store
	svc            *chatsvc.Service
	conv           *benchConversation
	clock          *benchClock
	poller         *cdc.Poller
	server         *httptest.Server
	inspect        http.Handler
	conversationID string
	events         int
}

func runBenchWorld(t *testing.T, fixture chattraffic.Fixture, transport string, policy chattraffic.ClientPolicy) []benchResult {
	t.Helper()
	w := newBenchWorld(t, fixture, transport)
	for _, turn := range fixture.HistoryTurnList() {
		w.runTurn(turn, w.sendDirect, func() {})
	}
	if err := w.poller.SeekToHead(w.ctx); err != nil {
		t.Fatal(err)
	}
	results := []benchResult{w.measureHistoryOpen(policy)}
	full, client := w.measureHistoryFull(policy)
	results = append(results, full)
	if transport == "remote-lan" {
		results = append(results, w.measureExpandOutput(client))
	}
	results = append(results, w.measureLiveTurn(policy))
	return results
}

func newBenchWorld(t *testing.T, fixture chattraffic.Fixture, transport string) *benchWorld {
	t.Helper()
	dir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	clock := &benchClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	harness := domain.AgentHarness(fixture.Harness)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: string(benchProject), Path: dir, RegisteredAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, domain.SessionRecord{
		ID: benchSession, ProjectID: benchProject, Kind: domain.KindWorker, Harness: harness,
		Mode: domain.SessionModeChat, CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	conv := &benchConversation{
		id: fixture.ProviderConversationID, events: make(chan ports.ChatEvent, 1), acks: make(chan string, 64),
	}
	var idMu sync.Mutex
	ids := 0
	svc := chatsvc.New(chatsvc.Options{
		Store:      store,
		Sessions:   store,
		Reader:     benchSnapshotReader(store),
		PageReader: benchSnapshotPageReader(store),
		Drivers:    benchRegistry{driver: benchDriver{harness: harness, conv: conv}},
		// The production lifecycle reducer, so session activity writes and their
		// CDC rows happen as they do in the daemon.
		Activity: lifecycle.New(benchLifecycleStore{store}, nil),
		Log:      slog.New(slog.DiscardHandler),
		// UUID-shaped like production's uuid.NewString, so ids weigh the same.
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			ids++
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", ids)
		},
		Now:              clock.Now,
		StopProviderHost: func(context.Context, domain.SessionID) error { return nil },
	})
	ctrl, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: benchSession, ProjectID: benchProject, Kind: domain.KindWorker, Harness: harness,
		DataDir: dir, WorkspacePath: dir,
	})
	if err != nil {
		t.Fatalf("start chat controller: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), benchSession) })

	broadcaster := cdc.NewBroadcaster()
	router := NewRouterWithControl(config.Config{DataDir: dir}, discardLogger(), nil,
		APIDeps{Conversations: svc, CDC: store, Events: broadcaster}, ControlDeps{})
	handler := http.Handler(router)
	if transport == "remote-lan" {
		// The daemon's own LAN chain: control block, bearer auth, and remote
		// JSON compression. Served by httptest on loopback, never Start's 0.0.0.0.
		remote := NewMobileLAN(router, "bench-host", 0, discardLogger(), nil)
		remote.SetPasswordHash(mobilebridge.HashPassword(benchPassword))
		handler = remote.handler
	}
	server := httptest.NewServer(handler)
	// An open event stream would keep Close waiting forever after a failure.
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	if !server.Listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatal("benchmark listener must stay on loopback")
	}
	return &benchWorld{
		t: t, ctx: ctx, fixture: fixture, transport: transport, store: store, svc: svc, conv: conv,
		clock: clock, poller: cdc.NewPoller(store, broadcaster, cdc.PollerConfig{Logger: discardLogger()}),
		server: server, inspect: router, conversationID: ctrl.ConversationID(),
	}
}

// runTurn sends the user message, then replays each provider event and waits
// for the projector to commit it. after runs once per committed step.
func (w *benchWorld) runTurn(turn chattraffic.Turn, send func(text, clientMessageID string), after func()) {
	w.t.Helper()
	providerTurn := fmt.Sprintf("%s-turn-%03d", w.fixture.Provider, turn.Number)
	w.conv.setNextTurn(providerTurn)
	w.clock.advance()
	send(turn.User, fmt.Sprintf("00000000-0000-4000-9000-%012d", turn.Number))
	after()
	for _, step := range turn.Steps {
		w.clock.advance()
		w.emit(benchEvent(step, providerTurn))
		after()
	}
}

func (w *benchWorld) sendDirect(text, clientMessageID string) {
	w.t.Helper()
	turn, err := w.svc.Send(w.ctx, benchSession, ports.ChatUserMessage{
		Text: text, ClientMessageID: clientMessageID, Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		w.t.Fatalf("send: %v", err)
	}
	if turn.ProviderTurnID != w.conv.currentTurn() {
		w.t.Fatalf("send dispatched provider turn %q, want %q", turn.ProviderTurnID, w.conv.currentTurn())
	}
}

// emit hands one event to the projector and returns once every write the
// event causes has committed. The controller acknowledges a unit's events
// before its after-commit work (activity reporting, turn lifecycle), which can
// still write rows, so the acknowledgement alone is not a barrier. The event is
// then delivered a second time: the archive recognizes its provider event id
// and projects nothing, as it does for a persistent host's replay, and the
// controller reads it only after the previous unit has fully returned. Its
// acknowledgement therefore marks the end of the first delivery's writes. One
// event per unit also makes the replay independent of the 50 ms
// delta-coalescing timer: every fixture step is exactly one committed unit.
func (w *benchWorld) emit(event ports.ChatEvent) {
	w.t.Helper()
	w.events++
	event.ProviderEventID = fmt.Sprintf("%s:%06d", w.fixture.ProviderConversationID, w.events)
	w.deliver(event)
	w.deliver(event)
}

func (w *benchWorld) deliver(event ports.ChatEvent) {
	w.t.Helper()
	select {
	case w.conv.events <- event:
	case <-time.After(benchStepTimeout):
		w.t.Fatalf("projector did not accept %s", event.Kind)
	}
	select {
	case id := <-w.conv.acks:
		if id != event.ProviderEventID {
			w.t.Fatalf("projector acknowledged %s, want %s", id, event.ProviderEventID)
		}
	case <-time.After(benchStepTimeout):
		w.t.Fatalf("projector did not commit %s", event.Kind)
	}
}

func benchEvent(step chattraffic.Step, providerTurn string) ports.ChatEvent {
	event := ports.ChatEvent{
		Kind: ports.ChatEventKind(step.Kind), ProviderTurnID: providerTurn, ProviderItemID: step.Item,
		Delta: step.Delta, Text: step.Text, Summary: step.Summary,
		ActivityKind: domain.ActivityKind(step.ActivityKind), ActivityStatus: domain.ActivityStatus(step.Status),
		TurnState: domain.TurnState(step.TurnState),
	}
	if len(step.Detail) > 0 {
		// Adapters build details as maps and json.Marshal them: sorted keys,
		// compact, HTML-escaped. Re-encode the fixture's object the same way.
		var detail map[string]any
		dec := json.NewDecoder(bytes.NewReader(step.Detail))
		dec.UseNumber()
		if err := dec.Decode(&detail); err == nil {
			event.Detail, _ = json.Marshal(detail)
		}
	}
	return event
}

// inspectAll reads the whole conversation through the production handler
// without a socket. It is the server-side truth a client view is checked
// against and is never counted.
func (w *benchWorld) inspectAll() benchPage {
	w.t.Helper()
	var pages []benchPage
	before := int64(0)
	for {
		query := url.Values{"limit": {"500"}}
		if before > 0 {
			query.Set("beforeSequence", strconv.FormatInt(before, 10))
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+string(benchSession)+"/conversation?"+query.Encode(), nil)
		rec := httptest.NewRecorder()
		w.inspect.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			w.t.Fatalf("inspect conversation: %d %s", rec.Code, rec.Body.String())
		}
		page := decodeBenchPage(w.t, rec.Body.Bytes())
		pages = append(pages, page)
		if !page.HasMoreBefore {
			break
		}
		if page.OldestSequence <= 0 || (before > 0 && page.OldestSequence >= before) {
			w.t.Fatalf("inspect conversation: the page before %d reports more history from %d; the cursor did not move back", before, page.OldestSequence)
		}
		before = page.OldestSequence
	}
	return mergeBenchPages(pages)
}

/* ---- phases ------------------------------------------------------------- */

func (w *benchWorld) result(phase string, client *benchClient, view benchPage, metrics chattraffic.Metrics, fixtureTurns []chattraffic.Turn) benchResult {
	w.t.Helper()
	server := w.inspectAll()
	start := int64(0)
	if view.HasMoreBefore {
		start = view.OldestSequence
	}
	// The client view is reduced exactly as received. The server read holds
	// whole outputs, so it is reduced to the preview this transport's pages
	// must carry; a missing, empty or wrong preview then differs.
	clientRows, clientTurns := canonicalBenchRows(view, start, 0)
	serverRows, serverTurns := canonicalBenchRows(server, start, w.outputLimit(client.policy))
	oracleRows, _ := canonicalBenchRows(server, start, 0)
	state := chattraffic.State{Turns: int64(len(clientTurns))}
	for _, row := range clientRows {
		if row.Kind == "message" {
			state.Messages++
		} else {
			state.Activities++
		}
	}
	result := benchResult{
		scenario: chattraffic.Scenario{
			Provider: w.fixture.Provider, Phase: phase, Transport: w.transport, Metrics: metrics, State: state,
		},
		clientText: chattraffic.CanonicalState(clientRows, clientTurns),
		serverText: chattraffic.CanonicalState(serverRows, serverTurns),
		oracleText: chattraffic.CanonicalState(oracleRows, serverTurns),
	}
	if fixtureTurns != nil {
		result.fixtureProblem = checkFixtureContent(fixtureTurns, view, client.policy, w.transport)
	}
	return result
}

func (w *benchWorld) measureHistoryOpen(policy chattraffic.ClientPolicy) benchResult {
	c := w.newClient(policy)
	c.startDump("history-open")
	c.openChat()
	if c.remote && !c.pages[0].HasMoreBefore {
		// A page limit counts messages; activities ride along under a byte
		// limit. A history that fits one remote page would never exercise
		// paging, so the fixtures are sized to overflow it.
		w.t.Errorf("%s history fits one remote page; the fixture must overflow it", w.fixture.Provider)
	}
	return w.result("history-open", c, c.view(), c.metrics, nil)
}

func (w *benchWorld) measureHistoryFull(policy chattraffic.ClientPolicy) (benchResult, *benchClient) {
	c := w.newClient(policy)
	c.startDump("history-full")
	c.openChat()
	for c.pages[len(c.pages)-1].HasMoreBefore {
		c.loadOlder()
	}
	return w.result("history-full", c, c.view(), c.metrics, w.fixture.HistoryTurnList()), c
}

// measureExpandOutput opens every tool output the remote page cut to its head
// and tail, as fetchActivityOutput does when a reader expands it. This is an
// upper bound: a reader expands only what they open.
func (w *benchWorld) measureExpandOutput(history *benchClient) benchResult {
	w.t.Helper()
	c := w.newClient(history.policy)
	c.startDump("expand-output")
	view := history.view()
	server := w.inspectAll()
	full := map[string]benchItem{}
	for _, item := range server.Items {
		full[item.Kind+":"+item.ID] = item
	}
	var clientRows, serverRows []chattraffic.CanonicalRow
	var turnStates []string
	seenTurns := map[string]bool{}
	turnIndex := map[string]int{}
	turnState := map[string]string{}
	for _, turn := range view.Turns {
		turnState[turn.ID] = turn.State
	}
	for _, item := range view.Items {
		cutSize, cut := item.Detail["outputBytes"].(float64)
		if item.Kind != "activity" || !cut {
			continue
		}
		output := c.expandOutput(item)
		if int64(len(output)) != int64(cutSize) {
			w.t.Errorf("expanded output of %s is %d bytes; the page said %d", item.ID, len(output), int64(cutSize))
		}
		if item.TurnID != "" && !seenTurns[item.TurnID] {
			seenTurns[item.TurnID] = true
			turnIndex[item.TurnID] = len(turnStates)
			turnStates = append(turnStates, turnState[item.TurnID])
		}
		turn := -1
		if item.TurnID != "" {
			turn = turnIndex[item.TurnID]
		}
		clientRow := expandedRow(item, output)
		clientRow.Turn = turn
		clientRows = append(clientRows, clientRow)
		serverOutput, _ := full["activity:"+item.ID].Detail["output"].(string)
		serverRow := expandedRow(full["activity:"+item.ID], serverOutput)
		serverRow.Turn = turn
		serverRows = append(serverRows, serverRow)
	}
	if len(clientRows) == 0 {
		w.t.Fatal("no remote output was cut; the fixture must include outputs longer than the remote output limit")
	}
	result := benchResult{
		scenario: chattraffic.Scenario{
			Provider: w.fixture.Provider, Phase: "expand-output", Transport: w.transport, Metrics: c.metrics,
			State: chattraffic.State{Activities: int64(len(clientRows)), Turns: int64(len(turnStates))},
		},
		clientText: chattraffic.CanonicalState(clientRows, turnStates),
		serverText: chattraffic.CanonicalState(serverRows, turnStates),
		oracleText: chattraffic.CanonicalState(serverRows, turnStates),
	}
	outputs := fixtureOutputs(w.fixture.HistoryTurnList())
	for _, row := range clientRows {
		if !outputs[chattraffic.SHA256Hex([]byte(row.Text))] {
			result.fixtureProblem = "an expanded output is not any fixture output"
		}
	}
	return result
}

func expandedRow(item benchItem, output string) chattraffic.CanonicalRow {
	return chattraffic.CanonicalRow{
		Kind: "activity", ActivityKind: item.ActivityKind, Status: item.Status, Text: output, OutputBytes: int64(len(output)),
	}
}

func (w *benchWorld) measureLiveTurn(policy chattraffic.ClientPolicy) benchResult {
	w.t.Helper()
	c := w.newClient(policy)
	c.openChat()
	c.metrics = chattraffic.Metrics{}
	if err := w.poller.Poll(w.ctx); err != nil {
		w.t.Fatal(err)
	}
	startSeq := w.latestSeq()
	c.startDump("live-turn")
	c.openStream()
	turn := w.fixture.Live()
	step := 0
	w.runTurn(turn, c.postMessage, func() {
		kind := "send"
		if step > 0 {
			kind = turn.Steps[step-1].Kind
		}
		c.record(map[string]any{"type": "step", "index": step, "kind": kind})
		step++
		w.syncStep(c)
	})
	c.closeStream()
	c.metrics.CDCRows = w.latestSeq() - startSeq
	// Every change row reaches the client as one frame, after the cursor frame.
	if c.metrics.SSEFrames != c.metrics.CDCRows+1 {
		w.t.Errorf("%d change rows produced %d frames; want rows + 1 cursor frame", c.metrics.CDCRows, c.metrics.SSEFrames)
	}
	history := w.fixture.HistoryTurnList()
	return w.result("live-turn", c, c.view(), c.metrics, append(history, turn))
}

// syncStep publishes the step's change rows, lets the client read exactly
// those frames, and applies the renderer's routing. Each committed step is
// one refresh cycle: the scheduled-replay model in the docs. A row written
// after the step's barrier would make the replay timing-dependent, so it is
// reported rather than silently attributed to whichever step saw it.
func (w *benchWorld) syncStep(c *benchClient) {
	w.t.Helper()
	settled := w.latestSeq()
	if err := w.poller.Poll(w.ctx); err != nil {
		w.t.Fatal(err)
	}
	if published := w.poller.LastSeq(); published != settled {
		w.t.Errorf("change rows %d..%d were written after the step barrier; the replay is not deterministic", settled+1, published)
	}
	frames := c.readFramesThrough(w.poller.LastSeq())
	if c.routesToConversation(frames) {
		c.refresh()
	}
}

func (w *benchWorld) latestSeq() int64 {
	w.t.Helper()
	seq, err := w.store.LatestSeq(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return seq
}

/* ---- production wiring mirrored from daemon.go -------------------------- */

// benchSnapshotReader and benchSnapshotPageReader mirror the daemon's
// adapters (internal/daemon/daemon.go chatsvc.New Reader and PageReader) by
// copying every service field from the store snapshot field of the same name.
// A field the store does not provide fails the benchmark rather than being
// silently left empty, so the harness cannot serve smaller pages than the
// daemon does.
func benchSnapshotReader(store *sqlite.Store) chatsvc.SnapshotReader {
	return chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
		rows, err := store.LoadConversationSnapshot(ctx, conversationID)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return benchRows(rows)
	})
}

func benchSnapshotPageReader(store *sqlite.Store) chatsvc.SnapshotPageReader {
	return chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, conversationID string, before, after, limit int64) (chatsvc.ConversationRows, error) {
		rows, err := store.LoadConversationSnapshotRange(ctx, conversationID, before, after, limit)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return benchRows(rows)
	})
}

func benchRows(source any) (chatsvc.ConversationRows, error) {
	var rows chatsvc.ConversationRows
	src := reflect.ValueOf(source)
	dst := reflect.ValueOf(&rows).Elem()
	for i := range dst.NumField() {
		field := dst.Type().Field(i)
		if !field.IsExported() {
			return rows, fmt.Errorf("service rows field %s is unexported", field.Name)
		}
		value := src.FieldByName(field.Name)
		if !value.IsValid() || !value.Type().AssignableTo(field.Type) {
			return rows, fmt.Errorf("store snapshot has no %s for the service rows", field.Name)
		}
		dst.Field(i).Set(value)
	}
	return rows, nil
}

// benchLifecycleStore is the store the production lifecycle reducer writes
// through. The reducer has no exported clock and stamps sessions with wall
// time; RFC 3339 trims trailing zeros, so a nanosecond stamp would change the
// length of each session event frame from run to run. Session times are
// truncated to the second, which keeps every byte count repeatable and
// changes no decision the reducer has already made.
type benchLifecycleStore struct{ *sqlite.Store }

func (s benchLifecycleStore) UpdateSession(ctx context.Context, rec domain.SessionRecord) error {
	return s.Store.UpdateSession(ctx, truncateSessionTimes(rec))
}

func (s benchLifecycleStore) UpdateSessionFromActivitySignal(ctx context.Context, rec domain.SessionRecord, expectedRevision int64) (bool, error) {
	return s.Store.UpdateSessionFromActivitySignal(ctx, truncateSessionTimes(rec), expectedRevision)
}

func truncateSessionTimes(rec domain.SessionRecord) domain.SessionRecord {
	value := reflect.ValueOf(&rec).Elem()
	timeType := reflect.TypeOf(time.Time{})
	for i := range value.NumField() {
		field := value.Field(i)
		if !field.CanSet() {
			continue
		}
		switch {
		case field.Type() == timeType:
			field.Set(reflect.ValueOf(field.Interface().(time.Time).Truncate(time.Second)))
		case field.Type() == reflect.PointerTo(timeType) && !field.IsNil():
			truncated := field.Elem().Interface().(time.Time).Truncate(time.Second)
			field.Set(reflect.ValueOf(&truncated))
		}
	}
	return rec
}

type benchClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *benchClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// advance moves time by whole seconds only between steps, so every timestamp
// is deterministic and has a fixed RFC 3339 length.
func (c *benchClock) advance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
}

type benchRegistry struct{ driver ports.ChatDriver }

func (r benchRegistry) Driver(domain.AgentHarness) (ports.ChatDriver, error) { return r.driver, nil }
func (r benchRegistry) SupportsChat(domain.AgentHarness) bool                { return true }

type benchDriver struct {
	harness domain.AgentHarness
	conv    *benchConversation
}

func (d benchDriver) Harness() domain.AgentHarness { return d.harness }
func (d benchDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return benchCapabilities(), nil
}

func (d benchDriver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil {
		if _, err := cfg.PrepareEnv(ctx); err != nil {
			return nil, err
		}
	}
	return d.conv, nil
}

func (d benchDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil {
		if _, err := cfg.PrepareEnv(ctx); err != nil {
			return nil, err
		}
	}
	return d.conv, nil
}

func benchCapabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
}

// benchConversation stands where a persistent Codex or ACP provider host
// stands: it streams the fixture's normalized events and receives the
// controller's acknowledgement of each committed event.
type benchConversation struct {
	id        string
	events    chan ports.ChatEvent
	acks      chan string
	mu        sync.Mutex
	nextTurn  string
	closeOnce sync.Once
}

func (c *benchConversation) setNextTurn(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextTurn = id
}

func (c *benchConversation) currentTurn() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextTurn
}

func (c *benchConversation) ProviderConversationID() string          { return c.id }
func (c *benchConversation) Capabilities() ports.ChatCapabilities    { return benchCapabilities() }
func (c *benchConversation) Events() <-chan ports.ChatEvent          { return c.events }
func (c *benchConversation) Interrupt(context.Context, string) error { return nil }
func (c *benchConversation) ResolveRequest(context.Context, string, ports.ChatDecision) error {
	return nil
}

func (c *benchConversation) SendTurn(context.Context, ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	return ports.ChatTurnRef{ProviderTurnID: c.currentTurn()}, nil
}

func (c *benchConversation) Close() error {
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}

func (c *benchConversation) AcknowledgeProviderEvent(_ context.Context, id string) error {
	c.acks <- id
	return nil
}

/* ---- the simulated renderer -------------------------------------------- */

// benchClient mirrors the renderer's conversation paging and live refresh
// (frontend/src/renderer/hooks/useConversation.ts) and its event routing
// (frontend/src/renderer/lib/event-transport.ts) at the revisions listed in
// simulatorClientRevisions. It is a simulator: it decodes real responses and
// applies the same rules, but it is not the TypeScript code.
type benchClient struct {
	w       *benchWorld
	remote  bool
	policy  chattraffic.ClientPolicy
	api     *meteredTransport
	pages   []benchPage
	metrics chattraffic.Metrics
	stream  *benchStream
	dump    *json.Encoder
}

func (w *benchWorld) newClient(policy chattraffic.ClientPolicy) *benchClient {
	return &benchClient{w: w, remote: w.transport == "remote-lan", policy: policy, api: newMeteredTransport()}
}

// startDump begins recording this client's counted traffic when
// AO_CHATTRAFFIC_DUMP_DIR is set.
func (c *benchClient) startDump(phase string) {
	dir := os.Getenv(benchEnvDump)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.w.t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, c.w.fixture.Provider+"-"+phase+"-"+c.w.transport+".jsonl"))
	if err != nil {
		c.w.t.Fatal(err)
	}
	c.w.t.Cleanup(func() { _ = file.Close() })
	c.dump = json.NewEncoder(file)
}

func (c *benchClient) record(entry map[string]any) {
	if c.dump != nil {
		if err := c.dump.Encode(entry); err != nil {
			c.w.t.Fatal(err)
		}
	}
}

func (c *benchClient) decorate(req *http.Request, accept string) {
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Encoding", benchAcceptEncoding)
	if c.remote {
		req.Header.Set("Authorization", "Bearer "+benchPassword)
	}
}

// pageQuery is conversationPageQuery.
func (c *benchClient) pageQuery(before int64) url.Values {
	query := url.Values{}
	if before > 0 {
		query.Set("beforeSequence", strconv.FormatInt(before, 10))
	}
	if !c.remote {
		size := c.policy.LocalPageSize
		if before > 0 {
			size = c.policy.LocalOlderPageSize
		}
		query.Set("limit", strconv.FormatInt(size, 10))
		return query
	}
	size := c.policy.RemoteLivePageSize
	if before > 0 {
		size = c.policy.RemoteOlderPageSize
	}
	query.Set("limit", strconv.FormatInt(size, 10))
	query.Set("outputLimit", strconv.FormatInt(c.policy.RemoteOutputLimit, 10))
	return query
}

func (c *benchClient) fetch(query url.Values) benchPage {
	c.w.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.w.server.URL+"/api/v1/sessions/"+string(benchSession)+"/conversation?"+query.Encode(), nil)
	if err != nil {
		c.w.t.Fatal(err)
	}
	c.decorate(req, "application/json")
	return decodeBenchPage(c.w.t, c.do(req))
}

func (c *benchClient) openChat() { c.pages = []benchPage{c.fetch(c.pageQuery(0))} }

// loadOlder is fetchNextPage with getNextPageParam = oldestSequence.
// loadOlder reads the page before the oldest one.
func (c *benchClient) loadOlder() {
	c.pages = append(c.pages, c.fetchOlder(c.pages[len(c.pages)-1].OldestSequence))
}

// fetchOlder reads the page before a cursor. A page that still reports more
// history must move the cursor back, or paging would repeat or never end.
func (c *benchClient) fetchOlder(before int64) benchPage {
	c.w.t.Helper()
	page := c.fetch(c.pageQuery(before))
	if page.HasMoreBefore && (page.OldestSequence <= 0 || page.OldestSequence >= before) {
		c.w.t.Fatalf("the page before %d reports more history from %d; the cursor did not move back", before, page.OldestSequence)
	}
	return page
}

func (c *benchClient) view() benchPage { return mergeBenchPages(c.pages) }

func (c *benchClient) postMessage(text, clientMessageID string) {
	c.w.t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text, "clientMessageId": clientMessageID})
	req, err := http.NewRequest(http.MethodPost, c.w.server.URL+"/api/v1/sessions/"+string(benchSession)+"/conversation/messages", bytes.NewReader(body))
	if err != nil {
		c.w.t.Fatal(err)
	}
	c.decorate(req, "application/json")
	req.Header.Set("Content-Type", "application/json")
	c.do(req)
}

// expandOutput is fetchActivityOutput: a one-row page without outputLimit.
func (c *benchClient) expandOutput(item benchItem) string {
	query := url.Values{"beforeSequence": {strconv.FormatInt(item.Sequence+1, 10)}, "limit": {"1"}}
	if item.Sequence > 1 {
		query.Set("afterSequence", strconv.FormatInt(item.Sequence-1, 10))
	}
	page := c.fetch(query)
	for _, activity := range page.Items {
		if activity.Kind == "activity" && activity.ID == item.ID {
			output, _ := activity.Detail["output"].(string)
			return output
		}
	}
	c.w.t.Fatalf("expanding %s returned no such activity", item.ID)
	return ""
}

// do sends one request, reads the whole response, and attributes every byte
// the socket carried to the metrics.
func (c *benchClient) do(req *http.Request) []byte {
	c.w.t.Helper()
	// A finite request gets a step's deadline, headers and body included, so a
	// stalled handler fails the replay instead of waiting for the test timeout.
	ctx, cancel := context.WithTimeout(req.Context(), benchStepTimeout)
	defer cancel()
	req = req.WithContext(ctx)
	mark := c.api.mark()
	resp, err := c.api.client.Do(req)
	if err != nil {
		c.w.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	encoded, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		c.w.t.Fatalf("%s %s: reading the body: %v", req.Method, req.URL.Path, err)
	}
	if resp.StatusCode/100 != 2 {
		c.w.t.Fatalf("%s %s: %d %s", req.Method, req.URL.Path, resp.StatusCode, encoded)
	}
	decoded := encoded
	switch encoding := resp.Header.Get("Content-Encoding"); encoding {
	case "":
	case "gzip":
		if !c.remote {
			c.w.t.Fatal("the loopback listener compressed a response")
		}
		reader, err := gzip.NewReader(bytes.NewReader(encoded))
		if err != nil {
			c.w.t.Fatal(err)
		}
		if decoded, err = io.ReadAll(reader); err != nil {
			c.w.t.Fatal(err)
		}
	default:
		c.w.t.Fatalf("unexpected Content-Encoding %q", encoding)
	}
	raw, written := c.api.since(mark)
	headerEnd := bytes.Index(raw, []byte("\r\n\r\n"))
	if headerEnd < 0 || len(raw) < headerEnd+4+len(encoded) {
		c.w.t.Fatal("socket bytes do not hold the response")
	}
	header := int64(headerEnd + 4)
	c.metrics.Requests++
	c.metrics.HeaderBytesIn += header
	c.metrics.BodyEncodedBytes += int64(len(encoded))
	c.metrics.BodyDecodedBytes += int64(len(decoded))
	c.metrics.FramingBytesIn += int64(len(raw)) - header - int64(len(encoded))
	c.metrics.WireBytesIn += int64(len(raw))
	c.metrics.WireBytesOut += written
	if c.dump != nil {
		body := json.RawMessage(decoded)
		if !json.Valid(decoded) {
			body, _ = json.Marshal(string(decoded))
		}
		c.record(map[string]any{
			"type": "request", "method": req.Method, "url": req.URL.RequestURI(), "status": resp.StatusCode,
			"contentEncoding": resp.Header.Get("Content-Encoding"), "headerBytes": header,
			"framingBytes": int64(len(raw)) - header - int64(len(encoded)), "encodedBytes": len(encoded),
			"decodedBytes": len(decoded), "wireOut": written, "body": body,
		})
	}
	return decoded
}

// routesToConversation applies the renderer's event routing to one step's
// frames and reports whether the open chat must be refreshed. Both streams
// route a frame with a conversationId to that chat; a session update without
// one refreshes it unless it carries only ids and the chat is settled.
func (c *benchClient) routesToConversation(frames []benchFrame) bool {
	refresh := false
	for _, frame := range frames {
		if frame.event == "cursor" || frame.event == "heartbeat" {
			continue
		}
		var decoded struct {
			Type      string         `json:"type"`
			SessionID string         `json:"sessionId"`
			Payload   map[string]any `json:"payload"`
		}
		if err := json.Unmarshal([]byte(frame.data), &decoded); err != nil {
			c.w.t.Fatalf("event frame is not JSON: %v", err)
		}
		conversationID, hasConversation := decoded.Payload["conversationId"].(string)
		_, transition := decoded.Payload["interfaceTransitionId"].(string)
		_, shell := decoded.Payload["shellTerminalHandleId"].(string)
		sessionTarget := decoded.SessionID != "" && conversationID != ""
		sessionUpdate := decoded.Type == "session_updated" && decoded.SessionID != "" && !transition
		if c.remote {
			// The remote stream's else-if chain (event-transport.ts connectRemote).
			_, hasReview := decoded.Payload["reviewId"].(string)
			switch {
			case hasReview:
			case sessionTarget:
				refresh = true
			case shell:
			case sessionUpdate && c.refreshesForSessionUpdate(decoded.Payload):
				refresh = true
			}
			continue
		}
		// The local stream's independent checks (event-transport.ts handleEvent).
		if sessionTarget {
			refresh = true
		}
		if sessionUpdate && !hasConversation && !shell && c.refreshesForSessionUpdate(decoded.Payload) {
			refresh = true
		}
	}
	return refresh
}

// refreshesForSessionUpdate: an update carrying only ids refreshes a chat
// only while it is not yet settled.
func (c *benchClient) refreshesForSessionUpdate(payload map[string]any) bool {
	idOnly := payload != nil
	for key := range payload {
		if key != "id" && key != "sessionId" {
			idOnly = false
		}
	}
	return !idOnly || !c.settled()
}

func (c *benchClient) settled() bool {
	state := c.pages[0].Controller
	return state == "ready" || state == "busy"
}

// refresh is the local invalidation (refetch every loaded page in order) or
// the remote refreshRemoteConversation.
func (c *benchClient) refresh() {
	if !c.remote {
		if c.policy.LocalTailRefresh && c.localTail() {
			return
		}
		c.record(map[string]any{"type": "refresh", "mode": "local-refetch", "pages": len(c.pages)})
		next := []benchPage{c.fetch(c.pageQuery(0))}
		for len(next) < len(c.pages) && next[len(next)-1].HasMoreBefore {
			next = append(next, c.fetchOlder(next[len(next)-1].OldestSequence))
		}
		c.pages = next
		return
	}
	prior := c.pages[0]
	floor := remoteConversationFloor(prior)
	var latest benchPage
	if floor >= 1 {
		query := url.Values{
			"afterSequence": {strconv.FormatInt(floor, 10)},
			"limit":         {strconv.FormatInt(c.policy.RemoteLivePageSize, 10)},
			"outputLimit":   {strconv.FormatInt(c.policy.RemoteOutputLimit, 10)},
		}
		tail := c.fetch(query)
		if tail.AfterSequence == floor {
			if merged, ok := mergeRemoteConversationTail(c.pages[0], tail, floor); ok {
				c.record(map[string]any{"type": "refresh", "mode": "tail-merged", "floor": floor})
				c.pages[0] = merged
				return
			}
		}
		if tail.AfterSequence != 0 {
			c.record(map[string]any{"type": "refresh", "mode": "tail-then-full", "floor": floor})
			latest = c.fetch(c.pageQuery(0))
		} else {
			latest = tail
		}
	} else {
		c.record(map[string]any{"type": "refresh", "mode": "full", "floor": floor})
		latest = c.fetch(c.pageQuery(0))
	}
	previous := c.pages
	priorLive := previous[0]
	switch {
	case priorLive.ConversationID == latest.ConversationID && priorLive.ActiveBranchID == latest.ActiveBranchID &&
		latest.LatestSequence < priorLive.LatestSequence:
		return
	case !latest.HasMoreBefore || priorLive.ConversationID != latest.ConversationID || priorLive.ActiveBranchID != latest.ActiveBranchID,
		latest.OldestSequence > priorLive.LatestSequence+1:
		c.pages = []benchPage{latest}
		return
	}
	live := latest
	if latest.OldestSequence > priorLive.OldestSequence {
		var slid []benchItem
		for _, item := range priorLive.Items {
			if item.Sequence < latest.OldestSequence {
				slid = append(slid, item)
			}
		}
		if len(slid) > 0 {
			live = mergeBenchPages([]benchPage{latest, withItems(priorLive, slid)})
		}
	}
	c.pages = append([]benchPage{live}, previous[1:]...)
}

// remoteConversationFloor: rows above it can still change.
func remoteConversationFloor(page benchPage) int64 {
	live := map[string]bool{}
	for _, turn := range page.Turns {
		if turn.State == "queued" || turn.State == "running" {
			live[turn.ID] = true
		}
	}
	for i := len(page.Items) - 1; i >= 0; i-- {
		if page.Items[i].TurnID != "" {
			live[page.Items[i].TurnID] = true
			break
		}
	}
	floor := page.LatestSequence
	for _, item := range page.Items {
		unsettled := item.Kind == "activity" && (item.Status == "running" || item.Status == "pending")
		if unsettled || (item.TurnID != "" && live[item.TurnID]) {
			floor = min(floor, item.Sequence-1)
		}
	}
	return floor
}

// mergeRemoteConversationTail replaces the rows above the floor, or refuses
// when the tail could have changed rows below it.
func mergeRemoteConversationTail(prior, tail benchPage, floor int64) (benchPage, bool) {
	if tail.ConversationID != prior.ConversationID || tail.ActiveBranchID != prior.ActiveBranchID || tail.HasMoreBefore {
		return benchPage{}, false
	}
	if !equalOptional(tail.CompactedAt, prior.CompactedAt) || tail.NativeForkAfter != prior.NativeForkAfter {
		return benchPage{}, false
	}
	priorTurns := map[string]benchTurn{}
	for _, turn := range prior.Turns {
		priorTurns[turn.ID] = turn
	}
	for _, turn := range tail.Turns {
		before, ok := priorTurns[turn.ID]
		if !ok || before.State != turn.State || before.RolledBack != turn.RolledBack {
			return benchPage{}, false
		}
	}
	priorIDs := map[string]bool{}
	for _, item := range prior.Items {
		priorIDs[item.ID] = true
	}
	for _, item := range tail.Items {
		if spans, _ := item.Detail["spansTurns"].(bool); item.Kind == "activity" && item.ActivityKind == "plan" && spans && !priorIDs[item.ID] {
			return benchPage{}, false
		}
	}
	var kept []benchItem
	for _, item := range prior.Items {
		if item.Sequence <= floor {
			kept = append(kept, item)
		}
	}
	return mergeBenchPages([]benchPage{tail, withItems(prior, kept)}), true
}

// withItems is `{ ...page, items, turns: turns those items name }`.
func withItems(page benchPage, items []benchItem) benchPage {
	named := map[string]bool{}
	for _, item := range items {
		named[item.TurnID] = true
	}
	out := page
	out.Items = items
	out.Turns = nil
	for _, turn := range page.Turns {
		if named[turn.ID] {
			out.Turns = append(out.Turns, turn)
		}
	}
	return out
}

func equalOptional(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

/* ---- wire decoding (toSnapshot) and merging (mergeConversationPages) ---- */

type benchPage struct {
	ConversationID  string
	ActiveBranchID  string
	Controller      string
	LatestSequence  int64
	OldestSequence  int64
	HasMoreBefore   bool
	AfterSequence   int64
	NativeForkAfter int64
	CompactedAt     *string
	Items           []benchItem
	Turns           []benchTurn
}

type benchItem struct {
	Kind         string
	ID           string
	TurnID       string
	Sequence     int64
	Role         string
	Text         string
	ActivityKind string
	Status       string
	Summary      string
	Detail       map[string]any
}

type benchTurn struct {
	ID          string
	State       string
	RequestedAt string
	RolledBack  bool
}

func decodeBenchPage(t *testing.T, data []byte) benchPage {
	t.Helper()
	var wire struct {
		ConversationID                   string  `json:"conversationId"`
		ActiveBranchID                   string  `json:"activeBranchId"`
		Controller                       string  `json:"controller"`
		LatestSequence                   int64   `json:"latestSequence"`
		OldestSequence                   *int64  `json:"oldestSequence"`
		HasMoreBefore                    bool    `json:"hasMoreBefore"`
		AfterSequence                    int64   `json:"afterSequence"`
		NativeForkAvailableAfterSequence int64   `json:"nativeForkAvailableAfterSequence"`
		CompactedAt                      *string `json:"compactedAt"`
		Turns                            []struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			RequestedAt string `json:"requestedAt"`
			RolledBack  bool   `json:"rolledBack"`
		} `json:"turns"`
		Messages []struct {
			ID       string `json:"id"`
			TurnID   string `json:"turnId"`
			Sequence int64  `json:"sequence"`
			Role     string `json:"role"`
			Text     string `json:"text"`
		} `json:"messages"`
		Activities []struct {
			ID           string         `json:"id"`
			TurnID       string         `json:"turnId"`
			Sequence     int64          `json:"sequence"`
			ActivityKind string         `json:"activityKind"`
			Status       string         `json:"status"`
			Summary      string         `json:"summary"`
			Detail       map[string]any `json:"detail"`
		} `json:"activities"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("conversation page is not the expected JSON: %v", err)
	}
	page := benchPage{
		ConversationID: wire.ConversationID, ActiveBranchID: wire.ActiveBranchID, Controller: wire.Controller,
		LatestSequence: wire.LatestSequence, OldestSequence: wire.LatestSequence + 1, HasMoreBefore: wire.HasMoreBefore,
		AfterSequence: wire.AfterSequence, NativeForkAfter: wire.NativeForkAvailableAfterSequence, CompactedAt: wire.CompactedAt,
	}
	if wire.OldestSequence != nil {
		page.OldestSequence = *wire.OldestSequence
	}
	for _, turn := range wire.Turns {
		page.Turns = append(page.Turns, benchTurn{ID: turn.ID, State: turn.State, RequestedAt: turn.RequestedAt, RolledBack: turn.RolledBack})
	}
	for _, message := range wire.Messages {
		page.Items = append(page.Items, benchItem{
			Kind: "message", ID: message.ID, TurnID: message.TurnID, Sequence: message.Sequence, Role: message.Role, Text: message.Text,
		})
	}
	for _, activity := range wire.Activities {
		page.Items = append(page.Items, benchItem{
			Kind: "activity", ID: activity.ID, TurnID: activity.TurnID, Sequence: activity.Sequence,
			ActivityKind: activity.ActivityKind, Status: activity.Status, Summary: activity.Summary, Detail: activity.Detail,
		})
	}
	slices.SortStableFunc(page.Items, func(a, b benchItem) int { return compareInt64(a.Sequence, b.Sequence) })
	return page
}

// mergeBenchPages is mergeConversationPages: pages[0] is the live page and
// wins when a row appears on several pages.
func mergeBenchPages(pages []benchPage) benchPage {
	if len(pages) == 1 {
		return pages[0]
	}
	items := map[string]benchItem{}
	var itemOrder []string
	turns := map[string]benchTurn{}
	var turnOrder []string
	for i := len(pages) - 1; i >= 0; i-- {
		for _, item := range pages[i].Items {
			key := item.Kind + ":" + item.ID
			if _, seen := items[key]; !seen {
				itemOrder = append(itemOrder, key)
			}
			items[key] = item
		}
		for _, turn := range pages[i].Turns {
			if _, seen := turns[turn.ID]; !seen {
				turnOrder = append(turnOrder, turn.ID)
			}
			turns[turn.ID] = turn
		}
	}
	merged := pages[0]
	oldest := pages[len(pages)-1]
	merged.OldestSequence, merged.HasMoreBefore = oldest.OldestSequence, oldest.HasMoreBefore
	merged.Items = make([]benchItem, 0, len(items))
	for _, key := range itemOrder {
		merged.Items = append(merged.Items, items[key])
	}
	slices.SortStableFunc(merged.Items, func(a, b benchItem) int { return compareInt64(a.Sequence, b.Sequence) })
	merged.Turns = make([]benchTurn, 0, len(turns))
	for _, id := range turnOrder {
		merged.Turns = append(merged.Turns, turns[id])
	}
	slices.SortStableFunc(merged.Turns, func(a, b benchTurn) int { return strings.Compare(a.RequestedAt, b.RequestedAt) })
	return merged
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

/* ---- semantic state ------------------------------------------------------ */

// canonicalBenchRows reduces a view to its rows at or after start, and the
// states of the turns those rows belong to in first-reference order; each row
// names its turn by its index in that order. An
// activity's text is its summary plus any settled text in its detail; its
// output size is outputBytes when a page cut it, else the output length; its
// payload is the rest of its detail. A whole output longer than previewLimit
// is reduced to the preview a page with that limit keeps (0 keeps it whole).
func canonicalBenchRows(view benchPage, start, previewLimit int64) ([]chattraffic.CanonicalRow, []string) {
	turnState := map[string]string{}
	for _, turn := range view.Turns {
		turnState[turn.ID] = turn.State
	}
	var rows []chattraffic.CanonicalRow
	var turns []string
	index := map[string]int{}
	for _, item := range view.Items {
		if item.Sequence < start {
			continue
		}
		if _, seen := index[item.TurnID]; item.TurnID != "" && !seen {
			state, ok := turnState[item.TurnID]
			if !ok {
				state = "missing"
			}
			index[item.TurnID] = len(turns)
			turns = append(turns, state)
		}
		row := canonicalItem(item, previewLimit)
		row.Turn = -1
		if item.TurnID != "" {
			row.Turn = index[item.TurnID]
		}
		rows = append(rows, row)
	}
	return rows, turns
}

func canonicalItem(item benchItem, previewLimit int64) chattraffic.CanonicalRow {
	if item.Kind == "message" {
		return chattraffic.CanonicalRow{Kind: "message", Role: item.Role, Text: item.Text}
	}
	text := item.Summary
	if settled, ok := item.Detail["text"].(string); ok {
		text += "\x00" + settled
	}
	output, _ := item.Detail["output"].(string)
	if _, cut := item.Detail["outputBytes"]; !cut {
		output = chattraffic.OutputPreview(output, previewLimit)
	}
	return chattraffic.CanonicalRow{
		Kind: "activity", ActivityKind: item.ActivityKind, Status: item.Status, Text: text, OutputBytes: outputSize(item.Detail),
		Payload: chattraffic.CanonicalPayload(item.Detail), Output: output,
	}
}

// outputLimit is the output limit this world's transport reads pages with.
func (w *benchWorld) outputLimit(policy chattraffic.ClientPolicy) int64 {
	if w.transport == "remote-lan" {
		return policy.RemoteOutputLimit
	}
	return 0
}

func outputSize(detail map[string]any) int64 {
	if size, ok := detail["outputBytes"].(float64); ok {
		return int64(size)
	}
	output, _ := detail["output"].(string)
	return int64(len(output))
}

// checkFixtureContent checks a client view against the fixture itself, so a
// wrong committed expectation cannot hide lost content: every user and
// assistant message the turns produced appears with its exact text, every
// activity appears with its final kind, status, summary, output size, tool
// payload and the output (or remote preview) a reader holds, and any full
// output the view holds is one the fixture produced.
func checkFixtureContent(turns []chattraffic.Turn, view benchPage, policy chattraffic.ClientPolicy, transport string) string {
	start := int64(0)
	if view.HasMoreBefore {
		start = view.OldestSequence
	}
	have := map[string]int{}
	rows, viewTurns := canonicalBenchRows(view, start, 0)
	for _, row := range rows {
		have[canonicalLine(row)]++
	}
	previewLimit := int64(0)
	if transport == "remote-lan" {
		previewLimit = policy.RemoteOutputLimit
	}
	want := fixtureRows(turns, previewLimit)
	if view.HasMoreBefore {
		// A bounded page holds only the newest turns; require the live one, which
		// is the view's last turn.
		want = fixtureRows(turns[len(turns)-1:], previewLimit)
		for i := range want {
			want[i].Turn += len(viewTurns) - 1
		}
	}
	for _, row := range want {
		line := canonicalLine(row)
		if have[line] == 0 {
			return "missing " + row.Kind + " " + row.Role + row.ActivityKind + " " + chattraffic.SHA256Hex([]byte(row.Text))[:12]
		}
		have[line]--
	}
	outputs := fixtureOutputs(turns)
	for _, item := range view.Items {
		if output, ok := item.Detail["output"].(string); ok && output != "" && item.Detail["outputBytes"] == nil {
			if !outputs[chattraffic.SHA256Hex([]byte(output))] {
				return "an activity output is not any fixture output"
			}
		}
		if transport == "remote-lan" && item.Detail["outputBytes"] != nil {
			if output, _ := item.Detail["output"].(string); int64(len(output)) > policy.RemoteOutputLimit+int64(len("\n…\n"))+4 {
				return "a remote output exceeds the output limit"
			}
		}
	}
	return ""
}

// checkOracle holds the server state to the fixture-only oracle that
// scripts/chat-traffic-oracle.py derives without running the product, so
// an expected state can never be blessed from product output alone: the whole
// history and every cut output must match it exactly, and the live turn's rows
// must be the view's final rows.
func checkOracle(expectedDir string, result benchResult) string {
	scenario := result.scenario
	var name string
	switch scenario.Phase {
	case "history-full":
		name = "oracle-" + scenario.Provider + "-history.txt"
	case "expand-output":
		name = "oracle-" + scenario.Provider + "-expand.txt"
	case "live-turn":
		name = "oracle-" + scenario.Provider + "-live.txt"
	default:
		return ""
	}
	data, err := os.ReadFile(filepath.Join(expectedDir, name))
	if err != nil {
		return "oracle " + name + " is missing; run scripts/chat-traffic-oracle.py"
	}
	oracle := string(data)
	if scenario.Phase != "live-turn" {
		if oracle != result.oracleText {
			return "server state differs from " + name + ": " + firstDifference(oracle, result.oracleText)
		}
		return ""
	}
	// The live oracle holds one turn, numbered 0; in the view it follows the
	// history. Its ownership is checked against the fixture instead, so the
	// turn column is dropped here.
	rows := func(text string) []string {
		var lines []string
		for _, line := range strings.SplitAfter(text, "\n") {
			if line != "" && !strings.HasPrefix(line, "turn\t") {
				fields := strings.Split(line, "\t")
				lines = append(lines, strings.Join(append(fields[:1], fields[2:]...), "\t"))
			}
		}
		return lines
	}
	want, got := rows(oracle), rows(result.oracleText)
	if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		return "the live turn's rows differ from " + name
	}
	return ""
}

func canonicalLine(row chattraffic.CanonicalRow) string {
	return chattraffic.CanonicalState([]chattraffic.CanonicalRow{row}, nil)
}

// fixtureRows derives the rows the turns must produce, from the fixture alone,
// holding outputs as a page with previewLimit keeps them (0 keeps them whole).
// Each row names its turn by its index in turns.
func fixtureRows(turns []chattraffic.Turn, previewLimit int64) []chattraffic.CanonicalRow {
	var rows []chattraffic.CanonicalRow
	for turnIndex, turn := range turns {
		rows = append(rows, chattraffic.CanonicalRow{Turn: turnIndex, Kind: "message", Role: "user", Text: turn.User})
		type itemState struct {
			message  bool
			text     string
			settled  string
			kind     string
			status   string
			summary  string
			detail   map[string]any
			streamed string
		}
		items := map[string]*itemState{}
		var order []string
		for _, step := range turn.Steps {
			if step.Item == "" {
				continue
			}
			state := items[step.Item]
			if state == nil {
				state = &itemState{}
				items[step.Item] = state
				order = append(order, step.Item)
			}
			switch step.Kind {
			case "message.delta":
				state.message = true
				state.text += step.Delta
			case "message.completed":
				state.message = true
				if step.Text != "" {
					state.text = step.Text
				}
			case "reasoning.delta":
				state.settled += step.Delta
			case "command.output.delta":
				state.streamed += step.Delta
			case "activity.started", "activity.completed":
				state.kind, state.status, state.summary = step.ActivityKind, step.Status, step.Summary
				if step.Text != "" {
					state.settled = step.Text
				}
				if len(step.Detail) > 0 {
					state.detail = map[string]any{}
					_ = json.Unmarshal(step.Detail, &state.detail)
				}
			}
		}
		for _, id := range order {
			state := items[id]
			if state.message {
				rows = append(rows, chattraffic.CanonicalRow{Turn: turnIndex, Kind: "message", Role: "assistant", Text: state.text})
				continue
			}
			if state.streamed != "" {
				// Documented display rule (controllers.activityDetailPayload):
				// streamed command output wins over the provider's aggregate,
				// and says so.
				if state.detail == nil {
					state.detail = map[string]any{}
				}
				state.detail["output"] = state.streamed
				state.detail["outputSource"] = "stream"
				state.detail["outputMayBePartial"] = true
				delete(state.detail, "outputTruncated")
			}
			text := state.summary
			if settled, ok := state.detail["text"].(string); ok {
				text += "\x00" + settled
			} else if state.settled != "" && state.kind == "reasoning" {
				text += "\x00" + state.settled
			}
			output, _ := state.detail["output"].(string)
			rows = append(rows, chattraffic.CanonicalRow{
				Turn: turnIndex, Kind: "activity", ActivityKind: state.kind, Status: state.status, Text: text, OutputBytes: int64(len(output)),
				Payload: chattraffic.CanonicalPayload(state.detail), Output: chattraffic.OutputPreview(output, previewLimit),
			})
		}
	}
	return rows
}

func fixtureOutputs(turns []chattraffic.Turn) map[string]bool {
	outputs := map[string]bool{}
	for _, turn := range turns {
		streamed := map[string]string{}
		for _, step := range turn.Steps {
			if step.Kind == "command.output.delta" {
				streamed[step.Item] += step.Delta
				outputs[chattraffic.SHA256Hex([]byte(streamed[step.Item]))] = true
			}
			var detail map[string]any
			if len(step.Detail) > 0 && json.Unmarshal(step.Detail, &detail) == nil {
				if output, ok := detail["output"].(string); ok {
					outputs[chattraffic.SHA256Hex([]byte(output))] = true
				}
			}
		}
	}
	return outputs
}

func firstDifference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(wantLines), len(gotLines)) {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return fmt.Sprintf("line %d: want %q, got %q (%d vs %d lines)", i+1, w, g, len(wantLines), len(gotLines))
		}
	}
	return "identical"
}

/* ---- sockets --------------------------------------------------------------- */

// meteredTransport is an HTTP/1.1 client whose connections record every byte
// read and count every byte written. Compression is never requested
// implicitly, so the encoded body is visible.
type meteredTransport struct {
	client *http.Client
	mu     sync.Mutex
	conns  []*meteredConn
}

func newMeteredTransport() *meteredTransport {
	m := &meteredTransport{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			metered := &meteredConn{Conn: conn}
			m.mu.Lock()
			m.conns = append(m.conns, metered)
			m.mu.Unlock()
			return metered, nil
		},
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxConnsPerHost:     1,
		MaxIdleConnsPerHost: 1,
	}
	m.client = &http.Client{Transport: transport}
	return m
}

type meteredMark struct {
	read    []int
	written int64
}

func (m *meteredTransport) mark() meteredMark {
	m.mu.Lock()
	defer m.mu.Unlock()
	mark := meteredMark{}
	for _, conn := range m.conns {
		read, written := conn.counts()
		mark.read = append(mark.read, read)
		mark.written += written
	}
	return mark
}

// since returns the bytes read on every connection after mark, and the
// number of bytes written.
func (m *meteredTransport) since(mark meteredMark) ([]byte, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var raw []byte
	var written int64
	for i, conn := range m.conns {
		from := 0
		if i < len(mark.read) {
			from = mark.read[i]
		}
		raw = append(raw, conn.readSince(from)...)
		_, w := conn.counts()
		written += w
	}
	return raw, written - mark.written
}

type meteredConn struct {
	net.Conn
	mu      sync.Mutex
	read    []byte
	written int64
}

func (c *meteredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.read = append(c.read, p[:n]...)
	c.mu.Unlock()
	return n, err
}

func (c *meteredConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	c.written += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *meteredConn) counts() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.read), c.written
}

func (c *meteredConn) readSince(from int) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.read[from:]...)
}

/* ---- the event stream ---------------------------------------------------- */

type benchFrame struct {
	id    int64
	event string
	data  string
}

type benchStream struct {
	transport *meteredTransport
	body      *countingReader
	frames    chan benchFrame
	errs      chan error
	cancel    context.CancelFunc
	resp      *http.Response
	// last is the newest change sequence the client has read.
	last int64
}

type countingReader struct {
	r  io.Reader
	mu sync.Mutex
	n  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.mu.Lock()
	c.n += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *countingReader) count() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// openStream connects the CDC stream at the head, as the renderer does, and
// reads the cursor frame. Its response headers and frames count toward the
// live turn.
func (c *benchClient) openStream() {
	c.w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	transport := newMeteredTransport()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.w.server.URL+"/api/v1/events?after=latest", nil)
	if err != nil {
		c.w.t.Fatal(err)
	}
	c.decorate(req, "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	// Only opening the stream is bounded; frames are read under their own
	// step deadline for as long as the scenario runs.
	opening := time.AfterFunc(benchStepTimeout, cancel)
	resp, err := transport.client.Do(req)
	if !opening.Stop() {
		c.w.t.Fatalf("event stream did not open within %s", benchStepTimeout)
	}
	if err != nil {
		c.w.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		c.w.t.Fatalf("event stream answered %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"))
	}
	stream := &benchStream{
		transport: transport, body: &countingReader{r: resp.Body}, frames: make(chan benchFrame, 4096),
		errs: make(chan error, 1), cancel: cancel, resp: resp,
	}
	go stream.read()
	c.stream = stream
	c.w.t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})
	cursor := c.nextFrame()
	if cursor.event != "cursor" {
		c.w.t.Fatalf("event stream opened with %q, want the cursor frame", cursor.event)
	}
	c.metrics.SSEFrames++
	stream.last = cursor.id
}

func (s *benchStream) read() {
	reader := bufio.NewReader(s.body)
	var frame benchFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			s.errs <- err
			return
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			if frame.event != "" || frame.data != "" {
				s.frames <- frame
			}
			frame = benchFrame{}
		case strings.HasPrefix(line, "id: "):
			frame.id, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
		case strings.HasPrefix(line, "event: "):
			frame.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			frame.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (c *benchClient) nextFrame() benchFrame {
	c.w.t.Helper()
	select {
	case frame := <-c.stream.frames:
		return frame
	case err := <-c.stream.errs:
		c.w.t.Fatalf("event stream ended: %v", err)
	case <-time.After(benchStepTimeout):
		c.w.t.Fatal("event stream stalled")
	}
	return benchFrame{}
}

// readFramesThrough reads frames until the one carrying seq.
func (c *benchClient) readFramesThrough(seq int64) []benchFrame {
	c.w.t.Helper()
	var frames []benchFrame
	for c.stream.last < seq {
		frame := c.nextFrame()
		frames = append(frames, frame)
		c.metrics.SSEFrames++
		c.stream.last = frame.id
		c.record(map[string]any{
			"type": "frame", "id": frame.id, "event": frame.event,
			"bytes": len(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", frame.id, frame.event, frame.data)),
			"data":  json.RawMessage(frame.data),
		})
	}
	return frames
}

// closeStream attributes the stream's socket bytes to the metrics and closes
// it. Only frames the server sent before this point exist: heartbeats are off.
func (c *benchClient) closeStream() {
	c.w.t.Helper()
	raw, written := c.stream.transport.since(meteredMark{})
	headerEnd := bytes.Index(raw, []byte("\r\n\r\n"))
	body := c.stream.body.count()
	if headerEnd < 0 || int64(len(raw)) < int64(headerEnd+4)+body {
		c.w.t.Fatal("event stream socket bytes do not hold its frames")
	}
	header := int64(headerEnd + 4)
	c.metrics.HeaderBytesIn += header
	c.metrics.SSEBytes += body
	c.metrics.FramingBytesIn += int64(len(raw)) - header - body
	c.metrics.WireBytesIn += int64(len(raw))
	c.metrics.WireBytesOut += written
	c.stream.cancel()
	_ = c.stream.resp.Body.Close()
}

// localTail is refreshLocalConversationTail: read the rows above the live
// page's floor, at most a live page of them, and merge them into the live page.
// False when the tail cannot replace those rows, or older loaded pages hold
// rows below the floor that can still change; the caller then refetches every
// loaded page. A tail that falls back still costs its request.
func (c *benchClient) localTail() bool {
	prior := c.pages[0]
	floor := remoteConversationFloor(prior)
	if floor < 1 {
		return false
	}
	if len(c.pages) > 1 && remoteConversationFloor(mergeBenchPages(c.pages)) < prior.OldestSequence-1 {
		return false
	}
	tail := c.fetch(url.Values{"afterSequence": {strconv.FormatInt(floor, 10)}, "limit": {strconv.FormatInt(c.policy.LocalPageSize, 10)}})
	if tail.AfterSequence != floor {
		return false
	}
	merged, ok := mergeRemoteConversationTail(prior, tail, floor)
	if !ok {
		return false
	}
	c.record(map[string]any{"type": "refresh", "mode": "local-tail-merged", "floor": floor})
	c.pages[0] = merged
	return true
}
