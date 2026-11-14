package terminal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type EventSource interface {
	Subscribe(fn func(cdc.Event)) (unsubscribe func())
}

type wsConn interface {
	ReadJSON(ctx context.Context, v any) error
	WriteJSON(ctx context.Context, v any) error
	Ping(ctx context.Context) error
	Close(reason string) error
}

const (
	defaultHeartbeat   = 15 * time.Second
	dataWatermark      = 1 << 20
	burstFlushInterval = 4 * time.Millisecond
)

type Manager struct {
	src       Source
	events    EventSource
	log       *slog.Logger
	heartbeat time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	attachments map[*attachment]struct{}
	closed      bool

	sharedMu sync.Mutex
	shared   map[string]*sharedTerm

	inputLeaseMu sync.RWMutex
	inputLease   sessionguard.InputLease

	shellAgentsMu sync.Mutex
	shellAgents   ShellAgentSource
	panesMu       sync.Mutex
	panes         PaneSource

	inputMu      sync.Mutex
	inputBlocked map[string]int
	lastInputAt  map[string]time.Time

	fencesMu sync.Mutex
	fences   map[string]*inputFence
}

type inputFence struct {
	sem  chan struct{}
	refs int
}

type sharedTerm struct {
	members            map[*connState]*termMember
	authCols, authRows uint16
}

type termMember struct {
	att     *attachment
	cols    uint16
	rows    uint16
	primary bool
}

type Option func(*Manager)

type ShellAgentSource interface {
	SubscribeShellAgents(fn func(snapshot json.RawMessage)) (unsubscribe func())
}

type PaneSource interface {
	ConnectPanes(send func(command json.RawMessage)) PaneConn
}

type PaneConn interface {
	Report(sessionID string, layout json.RawMessage)
	Resolve(result json.RawMessage)
	Close()
}

func (m *Manager) SetPanes(src PaneSource) {
	m.panesMu.Lock()
	m.panes = src
	m.panesMu.Unlock()
}

func (m *Manager) SetShellAgents(src ShellAgentSource) {
	m.shellAgentsMu.Lock()
	m.shellAgents = src
	m.shellAgentsMu.Unlock()
}

func WithHeartbeat(d time.Duration) Option { return func(m *Manager) { m.heartbeat = d } }

func (m *Manager) SetSessionInputLease(lease sessionguard.InputLease) {
	m.inputLeaseMu.Lock()
	m.inputLease = lease
	m.inputLeaseMu.Unlock()
}

func (m *Manager) acquireSessionInput(id string) (func(), bool) {
	m.inputLeaseMu.RLock()
	lease := m.inputLease
	m.inputLeaseMu.RUnlock()
	if lease == nil {
		return func() {}, true
	}
	return lease.AcquireSessionInput(domain.SessionID(id))
}

func NewManager(src Source, events EventSource, log *slog.Logger, opts ...Option) *Manager {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		src:          src,
		events:       events,
		log:          log,
		heartbeat:    defaultHeartbeat,
		ctx:          ctx,
		cancel:       cancel,
		attachments:  map[*attachment]struct{}{},
		shared:       map[string]*sharedTerm{},
		inputBlocked: map[string]int{},
		lastInputAt:  map[string]time.Time{},
		fences:       map[string]*inputFence{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Manager) lockInputFence(terminalID string) (unlock func()) {
	unlock, _ = m.lockInputFenceContext(context.Background(), terminalID)
	return unlock
}

func (m *Manager) lockInputFenceContext(ctx context.Context, terminalID string) (unlock func(), err error) {
	m.fencesMu.Lock()
	f := m.fences[terminalID]
	if f == nil {
		f = &inputFence{sem: make(chan struct{}, 1)}
		m.fences[terminalID] = f
	}
	f.refs++
	m.fencesMu.Unlock()
	select {
	case f.sem <- struct{}{}:
	case <-ctx.Done():
		m.unrefInputFence(terminalID, f)
		return nil, ctx.Err()
	}
	return func() {
		<-f.sem
		m.unrefInputFence(terminalID, f)
	}, nil
}

func (m *Manager) unrefInputFence(terminalID string, f *inputFence) {
	m.fencesMu.Lock()
	f.refs--
	if f.refs == 0 {
		delete(m.fences, terminalID)
	}
	m.fencesMu.Unlock()
}

func (m *Manager) BeginInputDrain(terminalID string) (lastInputAt time.Time, release func()) {
	unlock := m.lockInputFence(terminalID)
	m.inputMu.Lock()
	m.inputBlocked[terminalID]++
	lastInputAt = m.lastInputAt[terminalID]
	m.inputMu.Unlock()
	unlock()

	var once sync.Once
	return lastInputAt, func() {
		once.Do(func() {
			m.inputMu.Lock()
			defer m.inputMu.Unlock()
			if m.inputBlocked[terminalID] <= 1 {
				delete(m.inputBlocked, terminalID)
				return
			}
			m.inputBlocked[terminalID]--
		})
	}
}

func (m *Manager) LastInputAt(terminalID string) time.Time {
	m.inputMu.Lock()
	defer m.inputMu.Unlock()
	return m.lastInputAt[terminalID]
}

func (m *Manager) HoldInput(terminalID string) (lastInputAt time.Time, release func()) {
	unlock := m.lockInputFence(terminalID)
	m.inputMu.Lock()
	lastInputAt = m.lastInputAt[terminalID]
	m.inputMu.Unlock()
	var once sync.Once
	return lastInputAt, func() { once.Do(unlock) }
}

func (m *Manager) ForgetTerminalInput(ctx context.Context, terminalID string) error {
	unlock, err := m.lockInputFenceContext(ctx, terminalID)
	if err != nil {
		return err
	}
	defer unlock()
	m.mu.Lock()
	var views []*attachment
	for a := range m.attachments {
		if a.id == terminalID {
			views = append(views, a)
		}
	}
	m.mu.Unlock()
	for _, a := range views {
		a.forgetInput()
	}
	m.inputMu.Lock()
	delete(m.lastInputAt, terminalID)
	m.inputMu.Unlock()
	return nil
}

func (m *Manager) TerminalOnScreen(terminalID string) bool {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[terminalID]
	if s == nil {
		return false
	}
	cols, rows := largestGrid(s.members)
	return cols > 0 && rows > 0
}

func (m *Manager) inputRefused(terminalID string) bool {
	m.inputMu.Lock()
	defer m.inputMu.Unlock()
	return m.inputBlocked[terminalID] > 0
}

func (m *Manager) writeInput(terminalID string, a *attachment, raw []byte, release func()) {
	unlock := m.lockInputFence(terminalID)
	defer unlock()
	m.inputMu.Lock()
	blocked := m.inputBlocked[terminalID] > 0
	m.inputMu.Unlock()
	if blocked {
		release()
		return
	}
	if err := a.writeLeased(raw, release); errors.Is(err, errInputRefused) {
		return
	}
	m.recordInputAt(terminalID, raw)
}

func (m *Manager) recordInputAt(terminalID string, raw []byte) {
	if terminalReports.Match(raw) {
		return
	}
	m.inputMu.Lock()
	m.lastInputAt[terminalID] = time.Now()
	m.inputMu.Unlock()
}

var terminalReports = regexp.MustCompile(`^(?:\x1b\[[IO]|\x1b\[[?>]?[0-9;]*[cRn]|\x1b\[\??[0-9;]*\$y|\x1b\[\?[0-9]*u|\x1b\[[0-9;]+t|\x1b\][0-9]+;[^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[^\x1b]*\x1b\\)+$`)

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	attachments := make([]*attachment, 0, len(m.attachments))
	for a := range m.attachments {
		attachments = append(attachments, a)
	}
	m.attachments = map[*attachment]struct{}{}
	m.mu.Unlock()

	m.cancel()
	for _, a := range attachments {
		a.close()
	}
}

func (m *Manager) track(a *attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return context.Canceled
	}
	m.attachments[a] = struct{}{}
	return nil
}

func (m *Manager) forget(a *attachment) {
	m.mu.Lock()
	delete(m.attachments, a)
	m.mu.Unlock()
}

func (m *Manager) joinTerminal(id string, c *connState, att *attachment, cols, rows uint16, primary bool) {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[id]
	if s == nil {
		s = &sharedTerm{members: map[*connState]*termMember{}}
		m.shared[id] = s
	}
	previousCols, previousRows := s.authCols, s.authRows
	s.members[c] = &termMember{att: att, cols: cols, rows: rows, primary: primary}
	m.reconcileLocked(id, s, false)
	if s.authCols > 0 && s.authRows > 0 &&
		previousCols == s.authCols && previousRows == s.authRows {
		_ = att.resize(s.authRows, s.authCols)
		c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgResize, Cols: s.authCols, Rows: s.authRows})
	}
}

func (m *Manager) updateTerminalSize(id string, c *connState, cols, rows uint16, force bool) {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[id]
	if s == nil {
		return
	}
	mem := s.members[c]
	if mem == nil {
		return
	}
	if !force && mem.cols == cols && mem.rows == rows {
		return
	}
	mem.cols, mem.rows = cols, rows
	m.reconcileLocked(id, s, force)
}

func (m *Manager) leaveTerminal(id string, c *connState) {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[id]
	if s == nil {
		return
	}
	if _, ok := s.members[c]; !ok {
		return
	}
	delete(s.members, c)
	if len(s.members) == 0 {
		delete(m.shared, id)
		return
	}
	m.reconcileLocked(id, s, false)
}

func (m *Manager) reconcileLocked(id string, s *sharedTerm, force bool) {
	cols, rows := largestGrid(s.members)
	if cols == 0 || rows == 0 {
		return
	}
	changed := cols != s.authCols || rows != s.authRows
	if !changed && !force {
		return
	}
	s.authCols, s.authRows = cols, rows
	for conn, mem := range s.members {
		_ = mem.att.resize(rows, cols)
		if changed {
			conn.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgResize, Cols: cols, Rows: rows})
		}
	}
}

func largestGrid(members map[*connState]*termMember) (cols, rows uint16) {
	anyPrimary := false
	for _, mem := range members {
		if mem.primary && mem.cols > 0 && mem.rows > 0 {
			anyPrimary = true
			break
		}
	}
	bestArea := 0
	for _, mem := range members {
		if anyPrimary && !mem.primary {
			continue
		}
		if mem.cols == 0 || mem.rows == 0 {
			continue
		}
		if a := int(mem.cols) * int(mem.rows); a > bestArea {
			bestArea, cols, rows = a, mem.cols, mem.rows
		}
	}
	return cols, rows
}

func (m *Manager) Serve(ctx context.Context, conn wsConn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := &connState{
		mgr:     m,
		conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		out:     newOutQueue(),
		terms:   map[string]*attachment{},
		writers: map[string]*inputWriter{},
	}
	defer c.cleanup()

	go c.writeLoop(ctx, burstFlushInterval)
	go c.heartbeatLoop(ctx, m.heartbeat)

	for {
		var msg clientMsg
		if err := conn.ReadJSON(ctx, &msg); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		c.handle(msg)
	}
}

type connState struct {
	mgr    *Manager
	conn   wsConn
	ctx    context.Context
	cancel context.CancelFunc
	out    *outQueue

	mu          sync.Mutex
	terms       map[string]*attachment
	unsubEvts   func()
	unsubAgents func()
	panes       PaneConn
	closed      bool

	writers map[string]*inputWriter
}

const inputQueueDepth = 256

type inputWriter struct {
	queue   chan queuedInput
	done    chan struct{}
	retired bool
}

type queuedInput struct {
	a          *attachment
	raw        []byte
	release    func()
	closeAfter bool
}

func (w *inputWriter) run(m *Manager, terminalID string, prev *inputWriter) {
	defer close(w.done)
	if prev != nil {
		<-prev.done
	}
	for in := range w.queue {
		if in.closeAfter {
			in.a.close()
			continue
		}
		m.writeInput(terminalID, in.a, in.raw, in.release)
	}
}

func (c *connState) queueInput(terminalID string, in queuedInput) {
	w := c.writers[terminalID]
	if w == nil || w.retired {
		next := &inputWriter{queue: make(chan queuedInput, inputQueueDepth), done: make(chan struct{})}
		go next.run(c.mgr, terminalID, w)
		c.writers[terminalID] = next
		w = next
	}
	w.queue <- in
}

func (c *connState) closeAfterInput(terminalID string, a *attachment) {
	if w := c.writers[terminalID]; w == nil || w.retired {
		a.close()
		return
	}
	c.queueInput(terminalID, queuedInput{a: a, closeAfter: true})
}

func (c *connState) retireWriter(terminalID string) {
	if w := c.writers[terminalID]; w != nil && !w.retired {
		close(w.queue)
		w.retired = true
	}
	for id, w := range c.writers {
		if !w.retired {
			continue
		}
		select {
		case <-w.done:
			delete(c.writers, id)
		default:
		}
	}
}

func (c *connState) handle(msg clientMsg) {
	switch msg.Ch {
	case chTerminal:
		c.handleTerminal(msg)
	case chSubscribe:
		c.handleSubscribe(msg)
	case chShellAgents:
		c.handleShellAgents(msg)
	case chPanes:
		c.handlePanes(msg)
	case chSystem:
		if msg.Type == msgPing {
			c.enqueue(serverMsg{Ch: chSystem, Type: msgPong})
		}
	}
}

func (c *connState) handleTerminal(msg clientMsg) {
	switch msg.Type {
	case msgOpen:
		c.openTerminal(msg.ID, msg.Rows, msg.Cols, msg.Role)
	case msgData:
		raw, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			return
		}
		release, ok := c.mgr.acquireSessionInput(msg.ID)
		if !ok {
			c.enqueue(serverMsg{Ch: chTerminal, ID: msg.ID, Type: msgError, Error: "session input is disabled while an exclusive session operation is in progress"})
			return
		}
		a := c.lookup(msg.ID)
		if a == nil || c.mgr.inputRefused(msg.ID) {
			release()
			return
		}
		c.queueInput(msg.ID, queuedInput{a: a, raw: raw, release: release})
	case msgResize:
		c.mgr.updateTerminalSize(msg.ID, c, msg.Cols, msg.Rows, msg.Force)
	case msgClose:
		c.closeTerminal(msg.ID)
	}
}

func (c *connState) openTerminal(id string, rows, cols uint16, role string) {
	if id == "" {
		c.enqueue(serverMsg{Ch: chTerminal, Type: msgError, Error: "missing terminal id"})
		return
	}
	c.mu.Lock()
	if _, ok := c.terms[id]; ok {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	var a *attachment
	a = newAttachment(id, ports.RuntimeHandle{ID: id}, c.mgr.src,
		func() {
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgOpened})
		},
		func(data []byte) {
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgData, raw: data})
		},
		func() {
			c.mu.Lock()
			if c.terms[id] == a {
				delete(c.terms, id)
			}
			c.mu.Unlock()
			c.mgr.leaveTerminal(id, c)
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgExited})
		},
		c.mgr.log)
	a.lockInput = func() func() { return c.mgr.lockInputFence(id) }
	a.onInputReplay = func(raw []byte) { c.mgr.recordInputAt(id, raw) }
	if err := c.mgr.track(a); err != nil {
		c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgError, Error: err.Error()})
		return
	}
	c.mu.Lock()
	c.terms[id] = a
	c.mu.Unlock()

	c.mgr.joinTerminal(id, c, a, cols, rows, role != roleSecondary)

	go func() {
		a.run(c.mgr.ctx)
		c.mgr.forget(a)
	}()
}

func (c *connState) closeTerminal(id string) {
	c.mu.Lock()
	a := c.terms[id]
	delete(c.terms, id)
	c.mu.Unlock()
	c.mgr.leaveTerminal(id, c)
	if a != nil {
		c.closeAfterInput(id, a)
	}
	c.retireWriter(id)
}

func (c *connState) lookup(id string) *attachment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terms[id]
}

func (c *connState) handleSubscribe(msg clientMsg) {
	if msg.Type != msgSubscribe || c.mgr.events == nil {
		return
	}
	c.mu.Lock()
	if c.unsubEvts != nil {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	unsub := c.mgr.events.Subscribe(func(e cdc.Event) {
		c.enqueue(serverMsg{
			Ch:   chSessions,
			Type: msgSnapshot,
			Session: &sessionUpdate{
				Seq:       e.Seq,
				ProjectID: e.ProjectID,
				SessionID: e.SessionID,
				EventType: string(e.Type),
			},
		})
	})
	c.mu.Lock()
	c.unsubEvts = unsub
	c.mu.Unlock()
}

func (c *connState) handleShellAgents(msg clientMsg) {
	c.mgr.shellAgentsMu.Lock()
	src := c.mgr.shellAgents
	c.mgr.shellAgentsMu.Unlock()
	if msg.Type != msgSubscribe || src == nil {
		return
	}
	c.mu.Lock()
	subscribed := c.unsubAgents != nil || c.closed
	c.mu.Unlock()
	if subscribed {
		return
	}
	unsub := src.SubscribeShellAgents(func(snapshot json.RawMessage) {
		c.enqueue(serverMsg{Ch: chShellAgents, Type: msgSnapshot, Agents: snapshot})
	})
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		unsub()
		return
	}
	c.unsubAgents = unsub
	c.mu.Unlock()
}

func (c *connState) handlePanes(msg clientMsg) {
	if msg.Type == msgSubscribe {
		if !paneClientAllowed(c.conn) {
			return
		}
		c.mgr.panesMu.Lock()
		src := c.mgr.panes
		c.mgr.panesMu.Unlock()
		if src == nil {
			return
		}
		c.mu.Lock()
		subscribed := c.panes != nil || c.closed
		c.mu.Unlock()
		if subscribed {
			return
		}
		conn := src.ConnectPanes(func(command json.RawMessage) {
			c.enqueue(serverMsg{Ch: chPanes, Type: msgCommand, Command: command})
		})
		c.mu.Lock()
		if c.closed || c.panes != nil {
			c.mu.Unlock()
			conn.Close()
			return
		}
		c.panes = conn
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	conn := c.panes
	c.mu.Unlock()
	if conn == nil {
		return
	}
	switch msg.Type {
	case msgLayout:
		conn.Report(msg.SessionID, msg.Layout)
	case msgResult:
		conn.Resolve(msg.Result)
	}
}

func paneClientAllowed(conn wsConn) bool {
	p, ok := conn.(interface{ AllowsPanes() bool })
	return ok && p.AllowsPanes()
}

type outQueue struct {
	mu     sync.Mutex
	frames []serverMsg
	bytes  int
	wake   chan struct{}
	room   chan struct{}
}

func newOutQueue() *outQueue {
	return &outQueue{wake: make(chan struct{}, 1), room: make(chan struct{}, 1)}
}

const maxMergedData = 256 << 10

func (q *outQueue) push(msg serverMsg) {
	q.mu.Lock()
	if n := len(q.frames); n > 0 && msg.raw != nil {
		if last := &q.frames[n-1]; last.raw != nil && last.Ch == msg.Ch && last.ID == msg.ID &&
			last.Type == msg.Type && len(last.raw)+len(msg.raw) <= maxMergedData {
			last.raw = append(last.raw, msg.raw...)
			q.bytes += len(msg.raw)
			q.mu.Unlock()
			return
		}
	}
	if msg.raw != nil {
		msg.raw = append([]byte(nil), msg.raw...)
	}
	q.frames = append(q.frames, msg)
	q.bytes += len(msg.Data) + len(msg.raw)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *outQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.frames) == 0
}

func (q *outQueue) full() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes >= dataWatermark
}

func (q *outQueue) drain() []serverMsg {
	q.mu.Lock()
	frames := q.frames
	q.frames, q.bytes = nil, 0
	q.mu.Unlock()
	for i := range frames {
		if frames[i].raw != nil {
			frames[i].Data = base64.StdEncoding.EncodeToString(frames[i].raw)
			frames[i].raw = nil
		}
	}
	select {
	case q.room <- struct{}{}:
	default:
	}
	return frames
}

func (c *connState) enqueue(msg serverMsg) {
	for msg.Type == msgData && c.out.full() {
		select {
		case <-c.out.room:
		case <-c.ctx.Done():
			return
		}
	}
	c.out.push(msg)
}

func (c *connState) writeLoop(ctx context.Context, flushInterval time.Duration) {
	var lastWrite time.Time
	var pace *time.Timer
	defer func() {
		if pace != nil {
			pace.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.out.wake:
		}
		if c.out.empty() {
			continue
		}
		if wait := flushInterval - time.Since(lastWrite); wait > 0 {
			if pace == nil {
				pace = time.NewTimer(wait)
			} else {
				pace.Reset(wait)
			}
			select {
			case <-ctx.Done():
				return
			case <-pace.C:
			}
		}
		msgs := c.out.drain()
		if len(msgs) == 0 {
			continue
		}
		for _, msg := range msgs {
			if err := c.conn.WriteJSON(ctx, msg); err != nil {
				c.cancel()
				return
			}
		}
		lastWrite = time.Now()
	}
}

func (c *connState) heartbeatLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, interval)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *connState) cleanup() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	attachments := make([]*attachment, 0, len(c.terms))
	ids := make([]string, 0, len(c.terms))
	for id, a := range c.terms {
		attachments = append(attachments, a)
		ids = append(ids, id)
	}
	c.terms = map[string]*attachment{}
	unsubEvts := c.unsubEvts
	c.unsubEvts = nil
	unsubAgents := c.unsubAgents
	c.unsubAgents = nil
	panes := c.panes
	c.panes = nil
	c.mu.Unlock()

	for _, id := range ids {
		c.mgr.leaveTerminal(id, c)
	}
	for i, a := range attachments {
		c.closeAfterInput(ids[i], a)
	}
	for _, w := range c.writers {
		if !w.retired {
			close(w.queue)
		}
	}
	if unsubEvts != nil {
		unsubEvts()
	}
	if unsubAgents != nil {
		unsubAgents()
	}
	if panes != nil {
		panes.Close()
	}
	_ = c.conn.Close("server: connection closed")
}
