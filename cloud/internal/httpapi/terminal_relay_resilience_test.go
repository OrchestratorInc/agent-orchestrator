package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const relayTestTerminalID = "4b0fcd37-6c64-4d5c-9b55-6a3f3c7a1f10"

// mirrorStubStore serves the store surface the worker stream handler touches.
// appendErrors maps a frame ID to the error its durable mirror returns.
type mirrorStubStore struct {
	Store
	mu           sync.Mutex
	appendErrors map[int64]error
	appended     []int64
}

func (s *mirrorStubStore) ClaimTerminalInput(
	context.Context, string, string, string, int64, string, time.Duration,
) (domain.WorkerRequest, bool, error) {
	return domain.WorkerRequest{}, false, nil
}

func (s *mirrorStubStore) AppendTerminalOutputAt(
	_ context.Context,
	_, _, _, _ string,
	_, sequence int64,
	_ []byte,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendErrors[sequence]; err != nil {
		return 0, err
	}
	s.appended = append(s.appended, sequence)
	return sequence, nil
}

func startWorkerStream(t *testing.T, server *Server, query string) *websocket.Conn {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/worker/terminals/{terminalId}/stream", func(w http.ResponseWriter, r *http.Request) {
		claims := worker.Claims{
			OrgID: "org", SessionID: "session", WorkerID: "worker", Epoch: 1,
			Scopes: []string{"worker:transport"},
		}
		server.workerTerminalStream(w, r.WithContext(context.WithValue(r.Context(), workerContextKey{}, claims)))
	})
	listener := httptest.NewServer(router)
	t.Cleanup(listener.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + listener.URL[len("http"):] + "/worker/terminals/" + relayTestTerminalID + "/stream" + query
	connection, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial worker stream: %v", err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func writeWorkerFrame(t *testing.T, connection *websocket.Conn, frame worker.TerminalStreamFrame) {
	t.Helper()
	encoded, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Write(ctx, websocket.MessageText, encoded); err != nil {
		t.Fatalf("write worker frame: %v", err)
	}
}

func readWorkerFrame(t *testing.T, connection *websocket.Conn) worker.TerminalStreamFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, message, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read worker frame: %v", err)
	}
	var frame worker.TerminalStreamFrame
	if err := json.Unmarshal(message, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func relayServer(store Store) *Server {
	return &Server{
		store:                 store,
		logger:                slog.Default(),
		terminalStreamEnabled: true,
		terminalRelayEnabled:  true,
		terminalStreams:       newTerminalStreams(),
	}
}

func receiveLive(t *testing.T, live chan terminalRelayOutput) terminalRelayOutput {
	t.Helper()
	select {
	case frame := <-live:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("live relay output never reached the browser")
	}
	return terminalRelayOutput{}
}

// A durable-mirror failure used to cancel the whole worker stream, which also
// cut live output to the browser; the worker redialed into the same failure
// and the terminal looked frozen. The mirror is best effort: live frames keep
// flowing, later frames are still mirrored, and every processed frame is acked
// (only stored ones with a replay sequence).
func TestTerminalRelayMirrorFailureKeepsLiveStream(t *testing.T) {
	store := &mirrorStubStore{appendErrors: map[int64]error{
		1: errors.New("database unavailable"),
		2: postgres.ErrTerminalExpired,
	}}
	server := relayServer(store)
	live, unsubscribe := server.terminalStreams.subscribeRelayOutput(relayTestTerminalID)
	defer unsubscribe()
	connection := startWorkerStream(t, server, "")

	for id := int64(1); id <= 3; id++ {
		writeWorkerFrame(t, connection, worker.TerminalStreamFrame{Type: "output", ID: id, Data: []byte("frame")})
		if got := receiveLive(t, live); got.sequence != id {
			t.Fatalf("live frame sequence = %d, want %d", got.sequence, id)
		}
	}
	// Every processed frame is acknowledged, stored or not, so the worker's
	// flow-control window never stalls on a failing store; only frame 3 has a
	// replay sequence.
	for id := int64(1); id <= 3; id++ {
		ack := readWorkerFrame(t, connection)
		if ack.Type != "ack" || ack.ID != id {
			t.Fatalf("worker got %+v, want the ack for frame %d", ack, id)
		}
		if stored := ack.Sequence != 0; stored != (id == 3) {
			t.Fatalf("ack for frame %d has sequence %d; only frame 3 was stored", id, ack.Sequence)
		}
	}
	store.mu.Lock()
	appended := append([]int64(nil), store.appended...)
	store.mu.Unlock()
	if len(appended) != 1 || appended[0] != 3 {
		t.Fatalf("mirrored frames = %v, want [3]", appended)
	}
}

// A rejection that proves the terminal is gone still ends the stream, with the
// code an old worker already treats as permanent.
func TestTerminalRelayClosedTerminalEndsStream(t *testing.T) {
	store := &mirrorStubStore{appendErrors: map[int64]error{1: postgres.ErrTerminalClosed}}
	server := relayServer(store)
	connection := startWorkerStream(t, server, "")
	writeWorkerFrame(t, connection, worker.TerminalStreamFrame{Type: "output", ID: 1, Data: []byte("bye")})
	frame := readWorkerFrame(t, connection)
	if frame.Type != "error" || frame.Code != "TRANSPORT_EXPIRED" {
		t.Fatalf("worker got %+v, want a TRANSPORT_EXPIRED error", frame)
	}
}

// Resize and repaint frames only go down the stream of a worker that dialed
// with the control feature; an older worker would drop unknown frame types.
func TestTerminalStreamControlFramesNeedWorkerFeature(t *testing.T) {
	legacy := relayServer(&mirrorStubStore{})
	startWorkerStream(t, legacy, "")
	waitForWorkerStream(t, legacy.terminalStreams)
	if legacy.terminalStreams.pushControl(relayTestTerminalID, worker.TerminalStreamFrame{Type: "resize", Columns: 100, Rows: 30}) {
		t.Fatal("resize was pushed to a worker that did not advertise control frames")
	}

	current := relayServer(&mirrorStubStore{})
	connection := startWorkerStream(t, current, "?features="+worker.TerminalStreamControlFeature)
	waitForWorkerStream(t, current.terminalStreams)
	if !current.terminalStreams.pushControl(relayTestTerminalID, worker.TerminalStreamFrame{Type: "resize", Columns: 100, Rows: 30}) {
		t.Fatal("resize was not pushed to a worker that advertised control frames")
	}
	frame := readWorkerFrame(t, connection)
	if frame.Type != "resize" || frame.Columns != 100 || frame.Rows != 30 {
		t.Fatalf("worker got %+v, want resize 100x30", frame)
	}
}

func waitForWorkerStream(t *testing.T, registry *terminalStreams) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for registry.lookupWorker(relayTestTerminalID) == nil {
		if time.Now().After(deadline) {
			t.Fatal("worker stream never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// resizeStubStore records durable resizes and repaints.
type resizeStubStore struct {
	Store
	mu       sync.Mutex
	block    chan struct{}
	resizes  [][2]uint16
	repaints int
	frames   []domain.TerminalOutput
}

func (s *resizeStubStore) QueueTerminalResize(_ context.Context, _ domain.TerminalSession, columns, rows uint16) error {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizes = append(s.resizes, [2]uint16{columns, rows})
	return nil
}

func (s *resizeStubStore) QueueTerminalRepaint(context.Context, domain.TerminalSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repaints++
	return nil
}

func (s *resizeStubStore) ListTerminalOutput(
	_ context.Context,
	_ domain.TerminalSession,
	after int64,
	_ int,
) ([]domain.TerminalOutput, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var frames []domain.TerminalOutput
	for _, frame := range s.frames {
		if frame.Sequence > after {
			frames = append(frames, frame)
		}
	}
	return frames, "open", nil
}

func (s *resizeStubStore) snapshot() ([][2]uint16, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]uint16(nil), s.resizes...), s.repaints
}

// A burst of resizes while one is still being queued collapses to the newest
// size, delivered last, and never holds up the input loop.
func TestTerminalResizesCoalesceWhileDelivering(t *testing.T) {
	store := &resizeStubStore{block: make(chan struct{})}
	server := &Server{store: store, logger: slog.Default()}
	resizes := newTerminalResizes()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.deliverTerminalResizes(ctx, domain.TerminalSession{ID: "term"}, resizes)

	resizes.request(80, 24)
	// Let the first delivery start and block in the durable queue.
	time.Sleep(50 * time.Millisecond)
	for columns := uint16(81); columns <= 120; columns++ {
		resizes.request(columns, 40)
	}
	close(store.block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := store.snapshot()
		if len(got) == 2 {
			if got[0] != [2]uint16{80, 24} || got[1] != [2]uint16{120, 40} {
				t.Fatalf("delivered resizes = %v, want [80x24 120x40]", got)
			}
			return
		}
		if len(got) > 2 || time.Now().After(deadline) {
			t.Fatalf("delivered resizes = %v, want exactly the first and the newest", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// With the worker stream on this replica, a resize takes the in-memory path
// keystrokes take instead of the durable queue.
func TestTerminalResizeUsesWorkerStreamWhenConnected(t *testing.T) {
	store := &resizeStubStore{}
	server := &Server{store: store, logger: slog.Default(), terminalStreamEnabled: true, terminalStreams: newTerminalStreams()}
	stream := &workerTerminalStream{
		send:    make(chan []byte, 1),
		control: make(chan worker.TerminalStreamFrame, 4),
		done:    make(chan struct{}),
	}
	server.terminalStreams.registerWorker("term", stream)
	resizes := newTerminalResizes()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.deliverTerminalResizes(ctx, domain.TerminalSession{ID: "term"}, resizes)
	resizes.request(132, 43)
	select {
	case frame := <-stream.control:
		if frame.Type != "resize" || frame.Columns != 132 || frame.Rows != 43 {
			t.Fatalf("worker stream got %+v, want resize 132x43", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resize never reached the worker stream")
	}
	if got, _ := store.snapshot(); len(got) != 0 {
		t.Fatalf("durable resizes = %v, want none while the stream is connected", got)
	}
}

// A replay whose head was pruned (it starts above sequence 1) leaves the
// client's screen inconsistent; the program is asked to repaint.
func TestTerminalReplayAfterPruneRequestsRepaint(t *testing.T) {
	store := &resizeStubStore{frames: []domain.TerminalOutput{
		{Sequence: 40, Data: []byte("tail")},
		{Sequence: 41, Data: []byte("end")},
	}}
	server := &Server{store: store, logger: slog.Default()}
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var writeMu sync.Mutex
		_ = server.writeTerminalOutput(relayCtx, connection, domain.TerminalSession{
			ID: "term", OrgID: "org", SessionID: "session", WorkerEpoch: 1,
		}, 0, true, &writeMu)
	}))
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+listener.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for {
		_, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("read replay: %v", err)
		}
		var message terminalServerMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "replay_complete" {
			break
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, repaints := store.snapshot(); repaints == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("pruned replay did not request a repaint")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// liveOutputHarness runs writeTerminalOutput for terminal "term" against
// store, with a worker stream registered so the mirror's progress is visible,
// and returns that stream and a reader for the client's output frames.
func liveOutputHarness(t *testing.T, store *resizeStubStore) (*terminalStreams, *workerTerminalStream, func() terminalServerMessage) {
	t.Helper()
	registry := newTerminalStreams()
	stream := &workerTerminalStream{send: make(chan []byte, 1), done: make(chan struct{})}
	registry.registerWorker("term", stream)
	server := &Server{store: store, logger: slog.Default(), terminalRelayEnabled: true, terminalStreams: registry}
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	t.Cleanup(cancelRelay)
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var writeMu sync.Mutex
		_ = server.writeTerminalOutput(relayCtx, connection, domain.TerminalSession{
			ID: "term", OrgID: "org", SessionID: "session", WorkerEpoch: 1,
		}, 0, true, &writeMu)
	}))
	t.Cleanup(listener.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	connection, _, err := websocket.Dial(ctx, "ws"+listener.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.CloseNow() })
	// writeTerminalOutput subscribes to live output itself; wait for it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		registry.mu.Lock()
		subscribed := len(registry.clients["term"]) > 0
		registry.mu.Unlock()
		if subscribed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client never subscribed to live output")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return registry, stream, func() terminalServerMessage {
		t.Helper()
		for {
			_, payload, err := connection.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var message terminalServerMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatal(err)
			}
			if message.Type == "output" {
				return message
			}
		}
	}
}

// A frame that never reaches the replay log (lost in transit, or a failing
// mirror) used to stop a client's live output for good: every later live
// frame waited for the durable pass to fill the gap. Once the mirror has
// finished with the missing frame and the log still lacks it, the client
// skips the gap and the program repaints.
func TestTerminalLiveOutputSkipsLostGap(t *testing.T) {
	store := &resizeStubStore{}
	registry, stream, readOutput := liveOutputHarness(t, store)
	registry.relayOutput("term", terminalRelayOutput{sequence: 1, data: []byte("one")})
	if got := readOutput(); got.Sequence != 1 {
		t.Fatalf("first live frame = %+v", got)
	}
	// Frame 2 is lost: the mirror finished with it (and 3) without storing it.
	stream.markMirrored(3)
	registry.relayOutput("term", terminalRelayOutput{sequence: 3, data: []byte("three")})
	time.Sleep(200 * time.Millisecond)
	registry.relayOutput("term", terminalRelayOutput{sequence: 4, data: []byte("four")})
	if got := readOutput(); got.Sequence != 4 {
		t.Fatalf("live frame after the gap = %+v, want sequence 4", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, repaints := store.snapshot(); repaints == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("skipped gap did not request a repaint")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Under a flood the worker may run its whole unacknowledged window ahead of
// the durable mirror, seconds of output. Frames a saturated live buffer
// dropped are still on their way into the log, so the client must wait for
// them however long that takes, not skip them after a fixed delay.
func TestTerminalLiveOutputWaitsForALaggingMirror(t *testing.T) {
	store := &resizeStubStore{}
	registry, stream, readOutput := liveOutputHarness(t, store)
	registry.relayOutput("term", terminalRelayOutput{sequence: 1, data: []byte("one")})
	if got := readOutput(); got.Sequence != 1 {
		t.Fatalf("first live frame = %+v", got)
	}
	stream.markMirrored(1)
	// Frame 2 was dropped by the live buffer; the mirror has not reached it.
	registry.relayOutput("term", terminalRelayOutput{sequence: 3, data: []byte("three")})
	time.Sleep(2500 * time.Millisecond)
	registry.relayOutput("term", terminalRelayOutput{sequence: 4, data: []byte("four")})
	time.Sleep(200 * time.Millisecond)
	// The mirror catches up and stores 2 to 4.
	store.mu.Lock()
	for sequence, data := range map[int64]string{2: "two", 3: "three", 4: "four"} {
		store.frames = append(store.frames, domain.TerminalOutput{Sequence: sequence, Data: []byte(data)})
	}
	sort.Slice(store.frames, func(i, j int) bool { return store.frames[i].Sequence < store.frames[j].Sequence })
	store.mu.Unlock()
	stream.markMirrored(4)
	for _, want := range []int64{2, 3, 4} {
		if got := readOutput(); got.Sequence != want {
			t.Fatalf("output = %+v, want sequence %d", got, want)
		}
	}
	if _, repaints := store.snapshot(); repaints != 0 {
		t.Fatalf("repaints = %d, want 0: nothing was skipped", repaints)
	}
}

// A burst of panel toggles that ends at the size the program already has sends
// no size change, but the client's screen reflowed at the sizes in between. The
// client marks that final resize with repaint, and the program is asked to
// redraw once, however many such resizes arrive within the interval.
func TestTerminalResizeRepaintFlagRequestsOneRepaint(t *testing.T) {
	store := &resizeStubStore{}
	server := &Server{store: store, logger: slog.Default()}
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var writeMu sync.Mutex
		_ = server.readTerminalInput(serverCtx, connection, domain.TerminalSession{
			ID: "term", OrgID: "org", SessionID: "session", WorkerEpoch: 1, Scopes: []string{"terminal:operate"},
		}, &writeMu)
	}))
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+listener.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for _, message := range []string{
		`{"type":"resize","columns":148,"rows":43}`,
		`{"type":"resize","columns":148,"rows":43,"repaint":true}`,
		`{"type":"resize","columns":148,"rows":43,"repaint":true}`,
	} {
		if err := connection.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, repaints := store.snapshot(); repaints >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a resize marked repaint did not request a repaint")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if _, repaints := store.snapshot(); repaints != 1 {
		t.Fatalf("repaints = %d, want 1 (the second within the interval is dropped)", repaints)
	}
}

// A program flooding its terminal keeps the socket's write lock busy, so the
// keepalive's pings miss while the client is reading every frame. Misses only
// close the socket when no output write completed between them.
func TestTerminalLivenessCountsOnlyMissesWithoutProgress(t *testing.T) {
	progress := &terminalWriteProgress{}
	busy := terminalLiveness{progress: progress}
	for ping := 0; ping < 10; ping++ {
		progress.wrote()
		if busy.dead(false) {
			t.Fatalf("ping %d: a client accepting output was declared dead", ping)
		}
	}
	dead := terminalLiveness{progress: progress}
	dead.dead(false) // takes the current write count as its baseline
	for ping := 1; ping < terminalPingMaxFailures; ping++ {
		if dead.dead(false) {
			t.Fatalf("closed after %d misses, want %d", ping, terminalPingMaxFailures)
		}
	}
	if !dead.dead(false) {
		t.Fatalf("a client with no pongs and no progress stayed open after %d misses", terminalPingMaxFailures)
	}
	answered := terminalLiveness{}
	for ping := 0; ping < 10; ping++ {
		if answered.dead(ping%2 == 0) {
			t.Fatal("alternating answered pings closed the socket")
		}
	}
}
