package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// selectShim reports native <select> clicks, because headless Chromium never
// paints the popup into the screencast. The viewer draws the menu itself.
const selectShim = `(() => {
  if (window.__rbSelectInstalled) return; window.__rbSelectInstalled = true;
  let n = 0;
  document.addEventListener('mousedown', (e) => {
    const el = e.target && e.target.closest && e.target.closest('select');
    if (!el || el.multiple || el.size > 1 || el.disabled || !window.__rbSelect) return;
    e.preventDefault(); el.focus();
    const id = el.dataset.rbSel || (el.dataset.rbSel = String(++n));
    const r = el.getBoundingClientRect();
    window.__rbSelect(JSON.stringify({id, x: r.left, y: r.bottom, w: r.width,
      options: [...el.options].map(o => ({label: o.label || o.text, disabled: o.disabled})), selected: el.selectedIndex}));
  }, true);
})();`

type frame struct {
	still    bool // a sharp still at rest: sent as JPEG even in tab-video mode
	data     []byte
	meta     json.RawMessage
	inputSeq int64
	quality  int
	crop     [2]int // fullscreen: only this top-left part of the picture is the page
}

type viewer struct {
	h  *host
	id string
	// attachMu runs attaches one at a time: overlapping ones (a new tab while
	// the person picks another) left the shown tab and the "active" one apart.
	attachMu sync.Mutex
	claim    *time.Timer // takes the page size back after this view's input pauses
	conn     *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	signal   chan struct{}
	out      chan []byte
	inputs   chan func() // page input, run in order off the read loop

	// History reads run beside each other and retry: histAsked numbers each
	// read as it is sent, and histSent is the newest one sent to the view, so
	// an older read that answers late is dropped.
	histAsked atomic.Int64
	histMu    sync.Mutex
	histSent  int64

	mu          sync.Mutex
	targetID    string
	session     string
	unsub       func()
	w, hgt      int
	dpr         float64
	quality     int
	scrollQ     int
	scrollScale float64
	curQ        int
	maxFlight   int64
	latest      *frame
	seq, ackSeq int64
	inputSeq    int64
	scrolling   bool
	// bgTabUntil: a Ctrl- or middle-click just asked for a background tab.
	bgTabUntil  time.Time
	lastInput   time.Time // the person's last click or key in this view
	lastGesture time.Time // the same, scrolling aside: the page may write their clipboard just after
	lastFix     time.Time // last time a wrong-size frame made the page size come back
	wrongSince  time.Time // first wrong-size frame since the last right one
	scrollTimer *time.Timer
	fileNode    int64
	fullscreen  bool // the page is in fullscreen: frames cover Chrome's whole window (fullscreenCrop)
	// embed: this view is one tab of a host app (AO's Browser panel), pinned to
	// one agent browser tab. It never switches tabs itself: it tells the host
	// about new and closed tabs, and the host opens or closes its own tabs.
	embed       bool
	waiting     bool        // embedded with no page yet: the first page that appears becomes its page
	iconSeq     int64       // favicon reports: only the newest one's icon is sent
	hidden      bool        // a host app tab behind another: no stream, page left where it is
	fileSession string      // the session whose DOM holds fileNode: the page's or an iframe's
	shimQ       chan func() // shim reports, in order: a frame's offset may need CDP calls
	iconQ       chan func() // the newest favicon load waiting for the one running
	// Cross-site iframes run in their own renderer and need their own session
	// for the shims: child session -> parent session, and -> its frame id.
	children   map[string]string
	childFrame map[string]string
	ctxFrame   map[string]string // ctxKey -> frame id, for the frames' main worlds
	framesIn   int64             // Page.screencastFrame events from Chrome (debug)
	// Adaptive window (hello maxInFlight 0): enough frames in flight for 30 fps
	// at the path's round trip, shrinking when the round trip grows past its
	// minimum because frames are queueing on a slow link.
	adaptive   bool
	pace       *pacer                // hello maxInFlight -1: bandwidth-delay pacing with quality adaptation
	auth       map[string]*authEntry // origin -> the person's answer to its basic-auth prompt
	authTried  map[string]bool       // Fetch requests already sent the person's credentials
	authFailed map[string]bool       // origins that refused the credentials: say so when asking again
	baseQ      int                   // quality the viewer asked for; pacing lowers v.quality below it
	sentAt     map[int64]time.Time
	minRTT     time.Duration
	minRTTAt   time.Time
	srtt       time.Duration
}

type clientMsg struct {
	T          string   `json:"t"`
	Seq        int64    `json:"seq"`
	Type       string   `json:"type"`
	X          float64  `json:"x"`
	Y          float64  `json:"y"`
	Button     string   `json:"button"`
	Buttons    int      `json:"buttons"`
	ClickCount int      `json:"clickCount"`
	Mods       int      `json:"mods"`
	DX         float64  `json:"dx"`
	DY         float64  `json:"dy"`
	Key        string   `json:"key"`
	Code       string   `json:"code"`
	KeyCode    int      `json:"keyCode"`
	Text       string   `json:"text"`
	URL        string   `json:"url"`
	ID         string   `json:"id"`
	W          int      `json:"w"`
	H          int      `json:"h"`
	DPR        float64  `json:"dpr"`
	Quality    int      `json:"quality"`
	ScrollQ    int      `json:"scrollQuality"`
	ScrollS    float64  `json:"scrollScale"`
	MaxFlight  int64    `json:"maxInFlight"`
	Accept     bool     `json:"accept"`
	PromptText string   `json:"promptText"`
	Index      int      `json:"index"`
	Name       string   `json:"name"`
	Data       string   `json:"data"`
	C          float64  `json:"c"`
	Repeat     bool     `json:"repeat"`
	HTML       string   `json:"html"`
	Files      []file   `json:"files"`
	Ctx        string   `json:"ctx"`   // the frame a shim answer goes back to
	Dir        string   `json:"dir"`   // uploaded files: their folder under uploadRoot...
	Names      []string `json:"names"` // ...and their names, folder-relative
	Folder     bool     `json:"folder"`
	Embed      bool     `json:"embed"`  // hello: shown as one of a host app's tabs (AO's Browser panel)
	Tab        string   `json:"tab"`    // hello: the agent browser tab that host tab shows
	Hidden     bool     `json:"hidden"` // hello: that host tab is not the one in front
	On         bool     `json:"on"`
}

// file is one desktop file (pasted image, dropped file), base64 in Data.
type file struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
}

func (h *host) handleViewer(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 20) // file uploads arrive inline
	ctx, cancel := context.WithCancel(context.Background())
	v := &viewer{h: h, id: fmt.Sprintf("cdp-%p", c), conn: c, ctx: ctx, cancel: cancel,
		signal: make(chan struct{}, 1), out: make(chan []byte, 1024), inputs: make(chan func(), 256), shimQ: make(chan func(), 256), iconQ: make(chan func(), 1),
		children: map[string]string{}, childFrame: map[string]string{}, ctxFrame: map[string]string{}}
	for _, q := range []chan func(){v.inputs, v.shimQ, v.iconQ} {
		go func() {
			for {
				select {
				case f := <-q:
					f()
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	defer func() {
		cancel()
		v.detach()
		h.viewMu.Lock()
		delete(h.viewers, v)
		h.viewMu.Unlock()
		h.ctl.releaseIfHolder(v.id) // a human who leaves cannot keep the agent locked out
		h.broadcastControl()
		c.CloseNow()
	}()
	_, data, err := c.Read(ctx)
	if err != nil {
		return
	}
	var hello clientMsg
	_ = json.Unmarshal(data, &hello)
	v.w, v.hgt, v.dpr = clamp(hello.W, 200, 3840, 1280), clamp(hello.H, 150, 2160, 800), hello.DPR
	if v.dpr < 1 || v.dpr > 3 {
		v.dpr = 1
	}
	v.quality, v.scrollQ, v.maxFlight = clamp(hello.Quality, 10, 100, 70), clamp(hello.ScrollQ, 10, 100, 45), int64(clamp(int(hello.MaxFlight), 1, 12, 4))
	v.adaptive, v.sentAt, v.baseQ = hello.MaxFlight == 0, map[int64]time.Time{}, v.quality
	if hello.MaxFlight < 0 {
		v.pace, v.maxFlight = newPacer(), 12
		go v.adaptQuality()
	}
	v.scrollScale = hello.ScrollS
	if v.scrollScale < 0.25 || v.scrollScale > 1 {
		v.scrollScale = 0.5 // half size while scrolling
	}
	// Set before the view is published: the CDP reader reads them (onTabs).
	v.embed, v.hidden = hello.Embed, hello.Hidden
	// Embedded with no page of its own (the agent had none): the first page
	// that appears becomes its page.
	v.waiting = v.embed && hello.Tab == ""
	h.viewMu.Lock()
	h.viewers[v] = struct{}{}
	h.viewMu.Unlock()
	go v.writeLoop()
	v.sendJSON(map[string]any{"t": "tabs", "tabs": h.tabList()})
	v.sendControl(h.ctl.owner())
	// Attach off the read loop: a CDP call that stalls while the agent navigates
	// must not stop acks and input (seen: 15 s with no acks, frames stuck at 4 in flight).
	first := h.agentTab()
	if hello.Tab != "" && (v.embed || h.hasTab(hello.Tab)) {
		first = hello.Tab
	}
	go func() {
		// Embedded, a view shows only its own page: one that closed while the
		// view was away closes the view too. A restarted host learns its tabs first.
		if v.embed && hello.Tab != "" && !h.waitTab(hello.Tab, 3*time.Second) {
			v.sendJSON(map[string]any{"t": "closed", "next": h.openerOf(hello.Tab)})
			return
		}
		if v.embed && hello.Tab == "" { // no page yet, or onTabs gave it one already
			if first != "" && v.take() {
				v.adopt(first)
			}
			return
		}
		if err := v.attach(first); err != nil {
			log.Printf("viewer attach: %v", err)
		}
	}()
	v.readLoop()
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return max(lo, min(hi, v))
}

func (v *viewer) call(method string, params any) (json.RawMessage, error) {
	v.mu.Lock()
	sid := v.session
	v.mu.Unlock()
	ctx, cancel := context.WithTimeout(v.ctx, 15*time.Second)
	defer cancel()
	return v.h.cdp.Call(ctx, sid, method, params)
}

func (v *viewer) attach(targetID string) error {
	v.attachMu.Lock()
	defer v.attachMu.Unlock()
	v.detach()
	if targetID == "" {
		return fmt.Errorf("no page tab")
	}
	ctx, cancel := context.WithTimeout(v.ctx, 15*time.Second)
	defer cancel()
	res, err := v.h.cdp.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return err
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(res, &a)
	sid := a.SessionID
	unsub := v.h.cdp.On(func(m cdpMsg) {
		if m.SessionID == sid || v.isChild(m.SessionID) {
			v.onEvent(m.SessionID, m)
		}
	})
	v.mu.Lock()
	v.targetID, v.session, v.unsub = targetID, sid, unsub
	v.mu.Unlock()
	v.runSteps(ctx, sid, append(append([]cdpStep{{"Inspector.enable", nil}}, frameSteps...),
		cdpStep{"Fetch.enable", v.fetchPatterns()})) // a 401 document with a Basic challenge opens the viewer's login prompt
	// Headless Chrome stops painting a background tab: the one shown comes to
	// the front, before its size is set (a size set while the tab is behind
	// another leaves Chrome capturing its whole window, every frame the wrong
	// size). A hidden view (a host app tab behind another) must not take it.
	if !v.isHidden() {
		_, _ = v.h.cdp.Call(ctx, sid, "Page.bringToFront", nil)
	}
	// fitView sends a still: a static page paints nothing, so screencast
	// alone may never send a first frame (seen: 20 s of black).
	if err := v.fitView(true); err != nil {
		return err
	}
	v.sendJSON(map[string]any{"t": "active", "id": targetID})
	for _, t := range v.h.tabList() {
		if t.ID == targetID {
			v.sendJSON(map[string]any{"t": "url", "url": t.URL})
		}
	}
	return nil
}

type cdpStep struct {
	method string
	params any
}

// frameSteps set up the shims in a session: the page's, and each cross-site
// iframe's (those run in another renderer, where the page's scripts never go).
var frameSteps = []cdpStep{
	{"Page.enable", nil},
	// Bindings first: a shim put in a loaded page reports at once (its icon).
	{"Runtime.addBinding", map[string]any{"name": "__rbSelect"}},
	{"Runtime.addBinding", map[string]any{"name": "__rbUI"}},
	{"Runtime.enable", nil}, // replays the frames' contexts: onEvent puts the shims in each, so frames loaded before attach get them too
	{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": selectShim}},
	{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": uiShim}},
	{"Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}},
	// Not waitForDebuggerOnStart: a setup that stalls must never freeze a payment form.
	{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true, "filter": []map[string]any{{"type": "iframe"}}}},
}

func (v *viewer) runSteps(ctx context.Context, sid string, steps []cdpStep) {
	for _, step := range steps {
		t0 := time.Now()
		if _, err := v.h.cdp.Call(ctx, sid, step.method, step.params); err != nil {
			log.Printf("%s: %v", step.method, err)
		}
		if d := time.Since(t0); d > time.Second {
			log.Printf("slow attach step %s: %v", step.method, d)
		}
	}
}

func (v *viewer) isChild(sid string) bool {
	if sid == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.children[sid]
	return ok
}

func ctxKey(sid string, id int64) string { return fmt.Sprintf("%s/%d", sid, id) }

// evalIn runs expr in the frame a shim report came from (key from ctxKey);
// with no key, in the page's main frame. gesture: as if the person clicked.
func (v *viewer) evalIn(key, expr string, gesture bool) {
	p := map[string]any{"expression": expr, "userGesture": gesture}
	sid, id, ok := strings.Cut(key, "/")
	ctxID, err := strconv.ParseInt(id, 10, 64)
	if !ok || err != nil {
		_, _ = v.call("Runtime.evaluate", p)
		return
	}
	p["contextId"] = ctxID
	ctx, cancel := context.WithTimeout(v.ctx, 15*time.Second)
	defer cancel()
	_, _ = v.h.cdp.Call(ctx, sid, "Runtime.evaluate", p)
}

// frameOffset is where a frame's viewport sits in the page, in page CSS px:
// the shims report positions inside their own frame.
// ponytail: ignores CSS transforms on iframes (a scaled iframe gets its menus misplaced).
func (v *viewer) frameOffset(sid, frameID string) (x, y float64) {
	v.mu.Lock()
	parent, child := v.children[sid]
	root := v.targetID
	if child {
		root = v.childFrame[sid]
	}
	v.mu.Unlock()
	if frameID != "" && frameID != root {
		// A frame in this session's renderer: its owner's box is in the coordinates of the session's root frame.
		x, y = v.ownerBox(sid, frameID)
	}
	if child { // the session's root frame is an iframe in its parent's
		bx, by := v.ownerBox(parent, root)
		px, py := v.frameOffset(parent, "")
		x, y = x+bx+px, y+by+py
	}
	return x, y
}

// ownerBox is the top-left of the content box of the <iframe> holding frameID.
func (v *viewer) ownerBox(sid, frameID string) (x, y float64) {
	ctx, cancel := context.WithTimeout(v.ctx, 3*time.Second)
	defer cancel()
	res, err := v.h.cdp.Call(ctx, sid, "DOM.getFrameOwner", map[string]any{"frameId": frameID})
	var o struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	if err != nil || json.Unmarshal(res, &o) != nil {
		return 0, 0
	}
	res, err = v.h.cdp.Call(ctx, sid, "DOM.getBoxModel", map[string]any{"backendNodeId": o.BackendNodeID})
	var b struct {
		Model struct {
			Content []float64 `json:"content"`
		} `json:"model"`
	}
	if err != nil || json.Unmarshal(res, &b) != nil || len(b.Model.Content) < 2 {
		return 0, 0
	}
	return b.Model.Content[0], b.Model.Content[1]
}

// relayShim passes a shim's report to the viewer, with where its frame sits
// in the page (dx, dy) and the frame to answer (ctx).
func (v *viewer) relayShim(sid, kind, payload string, ctxID int64) {
	key := ctxKey(sid, ctxID)
	v.mu.Lock()
	frameID := v.ctxFrame[key]
	v.mu.Unlock()
	dx, dy := v.frameOffset(sid, frameID)
	v.sendRaw(fmt.Appendf(nil, `{"t":%q,"data":%s,"ctx":%q,"dx":%g,"dy":%g}`, kind, payload, key, dx, dy))
}

// onChildAttached sets the shims up in a cross-site iframe's session.
func (v *viewer) onChildAttached(parent string, params json.RawMessage) {
	var p struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfo"`
	}
	if json.Unmarshal(params, &p) != nil || p.TargetInfo.Type != "iframe" {
		return
	}
	v.mu.Lock()
	v.children[p.SessionID], v.childFrame[p.SessionID] = parent, p.TargetInfo.TargetID
	v.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(v.ctx, 10*time.Second)
		defer cancel()
		v.runSteps(ctx, p.SessionID, frameSteps)
	}()
}

func (v *viewer) detach() {
	v.mu.Lock()
	sid, unsub, target := v.session, v.unsub, v.targetID
	v.session, v.unsub, v.latest = "", nil, nil
	clear(v.children)
	clear(v.childFrame)
	clear(v.ctxFrame)
	v.mu.Unlock()
	if sid == "" {
		return
	}
	unsub()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = v.h.cdp.Call(ctx, sid, "Page.stopScreencast", nil)
	_, _ = v.h.cdp.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": sid})
	// Chrome keeps one size per page, and a detaching session's emulation
	// takes it away for every view: put it back, from a view still here.
	v.h.viewMu.Lock()
	s := v.h.sizer[target]
	if s == v {
		delete(v.h.sizer, target)
		s = nil
	}
	v.h.viewMu.Unlock()
	if s == nil {
		for _, o := range v.h.viewersOf(target) {
			if o != v {
				s = o
				break
			}
		}
	}
	if s != nil {
		go func() { _ = s.applyViewport(true) }()
	}
}

// fullscreenCrop: in fullscreen, headless Chrome captures its whole window,
// the page's emulated viewport in the top-left corner and black around it
// (the size check dropped every frame, and the view froze). The viewer draws
// only the page part. full reports fullscreen.
// ponytail: the cropped page is drawn from fewer pixels (about 0.8x); raise the screencast's max size in fullscreen if that blur matters.
func (v *viewer) fullscreenCrop(meta json.RawMessage, img []byte) (crop [2]int, full bool) {
	v.mu.Lock()
	full = v.fullscreen
	v.mu.Unlock()
	if !full {
		return crop, false
	}
	var m struct{ DeviceWidth, DeviceHeight float64 }
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	v.h.viewMu.Lock()
	s := v.h.sizer[v.target()]
	v.h.viewMu.Unlock()
	if json.Unmarshal(meta, &m) != nil || m.DeviceWidth == 0 || err != nil || s == nil {
		return crop, true
	}
	s.mu.Lock()
	w, h := float64(s.w), float64(s.hgt)
	s.mu.Unlock()
	return [2]int{int(float64(cfg.Width) * min(1, w/m.DeviceWidth)), int(float64(cfg.Height) * min(1, h/m.DeviceHeight))}, true
}

// wrongSize reports a frame of a page that isn't at the size its views set:
// the agent's screenshot or device emulation mid-command, or an emulation
// that another client cleared on its way out. The person keeps seeing the
// last right frame; outside an agent command the size is put back.
// meta may be nil (a still): then only the picture's shape is checked.
func (v *viewer) wrongSize(meta json.RawMessage, img []byte) bool {
	target := v.target()
	v.h.viewMu.Lock()
	s := v.h.sizer[target]
	v.h.viewMu.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	w, h := float64(s.w), float64(s.hgt)
	s.mu.Unlock()
	var m struct{ DeviceWidth, DeviceHeight float64 }
	sized := json.Unmarshal(meta, &m) != nil || m.DeviceWidth == 0 || math.Abs(m.DeviceWidth-w) <= 2 && math.Abs(m.DeviceHeight-h) <= 2
	// The metadata carries this session's emulation, not the agent's: an
	// emulated phone came through as a 648x1404 frame with desktop metadata.
	// The picture's shape tells (frames scale, they keep the page's aspect).
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(img)); sized && err == nil && cfg.Height > 0 {
		sized = math.Abs(float64(cfg.Width)/float64(cfg.Height)-w/h) <= 0.02*w/h
	}
	if sized {
		v.mu.Lock()
		v.wrongSince = time.Time{}
		v.mu.Unlock()
		return false
	}
	agent := v.h.ctl.agentRunning()
	v.mu.Lock()
	now := time.Now()
	if agent || v.wrongSince.IsZero() {
		v.wrongSince = now // the agent's own size while it works is not stuck
	}
	stuck := now.Sub(v.wrongSince) > time.Second
	fix := !agent && now.Sub(v.lastFix) > time.Second
	if fix {
		v.lastFix = now
	}
	v.mu.Unlock()
	switch {
	case fix && stuck:
		// The page is at its size and the frames still are not (a size set
		// while the tab was behind another): set it again on this session.
		go func() { _ = v.fitView(true) }()
	case fix:
		go s.reassert()
	}
	return true
}

// reload reloads the page. A page stuck in a script loop answers nothing,
// Page.reload included, so its script is stopped first.
func (v *viewer) reload() {
	ctx, cancel := context.WithTimeout(v.ctx, 500*time.Millisecond)
	defer cancel()
	v.mu.Lock()
	sid := v.session
	v.mu.Unlock()
	if _, err := v.h.cdp.Call(ctx, sid, "Runtime.evaluate", map[string]any{"expression": "1"}); err != nil {
		_, _ = v.call("Runtime.terminateExecution", nil)
	}
	_, _ = v.call("Page.reload", nil)
}

func (v *viewer) reassert() {
	v.mu.Lock()
	w, h, dpr := v.w, v.hgt, v.dpr
	v.mu.Unlock()
	res, err := v.call("Runtime.evaluate", map[string]any{"returnByValue": true,
		"expression": "[innerWidth * devicePixelRatio, innerHeight * devicePixelRatio]"})
	if err != nil {
		return
	}
	var r struct {
		Result struct {
			Value []float64 `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(res, &r) != nil || len(r.Result.Value) != 2 ||
		math.Abs(r.Result.Value[0]-float64(w)*dpr) <= 8 && math.Abs(r.Result.Value[1]-float64(h)*dpr) <= 8 {
		return
	}
	_ = v.applyViewport(true)
}

// fitView sizes the page to this view if the tab has no size yet or this view
// set it. Otherwise the page keeps the size of the view in use and this one
// letterboxes it: AO parks hidden tabs offscreen at 1280 px, and that resize
// took the page from the view on screen. force: as in applyViewport.
func (v *viewer) fitView(force bool) error {
	target := v.target()
	v.h.viewMu.Lock()
	s := v.h.sizer[target]
	v.h.viewMu.Unlock()
	if s == nil || s == v {
		return v.applyViewport(force)
	}
	s.mu.Lock()
	w, h, dpr := s.w, s.hgt, s.dpr
	s.mu.Unlock()
	// Same size as the page on this session too: Chrome sizes a session's
	// screencast frames from that session's own emulation.
	if force {
		_, _ = v.call("Emulation.clearDeviceMetricsOverride", nil)
	}
	_, _ = v.call("Emulation.setDeviceMetricsOverride", pageSize(w, h, dpr))
	v.sendJSON(map[string]any{"t": "viewport", "w": w, "h": h, "mine": false})
	q := v.currentBaseQuality()
	if err := v.restartScreencast(q); err != nil {
		return err
	}
	v.captureStill(q)
	return nil
}

func (v *viewer) target() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.targetID
}

// applyViewport gives the page this view's size, like resizing a local
// browser window, then restarts the stream. Every view of the tab sets the
// same size on its own session: Chrome sizes a session's screencast frames
// from that session's own emulation (a 2x page streamed 1x frames otherwise).
// force re-sends a size Chrome would skip as unchanged, after someone else's
// emulation (the agent's, a detached view's) replaced or wiped it, or after
// the tab came to the front (a size set behind another tab does not hold).
func (v *viewer) applyViewport(force bool) error {
	v.mu.Lock()
	w, h, dpr, target := v.w, v.hgt, v.dpr, v.targetID
	v.mu.Unlock()
	size := pageSize(w, h, dpr)
	views := v.h.viewersOf(target)
	for _, o := range views {
		if o != v {
			_, _ = o.call("Emulation.setDeviceMetricsOverride", size)
		}
	}
	if force {
		_, _ = v.call("Emulation.clearDeviceMetricsOverride", nil)
	}
	if _, err := v.call("Emulation.setDeviceMetricsOverride", size); err != nil { // last, so it wins
		return err
	}
	// One page, one size: this view's. The others letterbox it.
	v.h.viewMu.Lock()
	v.h.sizer[target] = v
	v.h.viewMu.Unlock()
	for _, o := range views {
		o.sendJSON(map[string]any{"t": "viewport", "w": w, "h": h, "mine": o == v})
	}
	q := v.currentBaseQuality()
	if err := v.restartScreencast(q); err != nil {
		return err
	}
	// The repaint for the new size lands on the old screencast, capped at the
	// old size (1x to 2x stayed blurry until the page changed), and a static
	// page paints nothing new for the restarted one.
	v.captureStill(q)
	return nil
}

// pageSize is the emulation that gives the page a view's size. The screen is
// the view too: fullscreen video filled Chrome's default 800x600 screen,
// letterboxed inside a bigger view.
func pageSize(w, h int, dpr float64) map[string]any {
	return map[string]any{"width": w, "height": h, "deviceScaleFactor": dpr, "mobile": false, "screenWidth": w, "screenHeight": h}
}

func (v *viewer) currentBaseQuality() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.scrolling {
		return v.scrollQ
	}
	return v.quality
}

func (v *viewer) restartScreencast(q int) error {
	v.mu.Lock()
	w, h, dpr, hidden := v.w, v.hgt, v.dpr, v.hidden
	if v.scrolling {
		dpr *= v.scrollScale
	}
	v.curQ = q
	v.mu.Unlock()
	_, _ = v.call("Page.stopScreencast", nil)
	if hidden { // no one sees it: no stream until shown
		return nil
	}
	_, err := v.call("Page.startScreencast", map[string]any{"format": "jpeg", "quality": q,
		"maxWidth": int(float64(w) * dpr), "maxHeight": int(float64(h) * dpr), "everyNthFrame": 1})
	return err
}

// onEvent runs on the CDP read goroutine and must not block on CDP calls.
// sid is the page's session or one of its cross-site iframes' (child) sessions.
func (v *viewer) onEvent(sid string, m cdpMsg) {
	v.mu.Lock()
	top := sid == v.session
	v.mu.Unlock()
	switch m.Method {
	case "Runtime.executionContextCreated":
		var p struct {
			Context struct {
				ID      int64 `json:"id"`
				AuxData struct {
					FrameID   string `json:"frameId"`
					IsDefault bool   `json:"isDefault"`
				} `json:"auxData"`
			} `json:"context"`
		}
		if json.Unmarshal(m.Params, &p) != nil || !p.Context.AuxData.IsDefault {
			return
		}
		v.mu.Lock()
		v.ctxFrame[ctxKey(sid, p.Context.ID)] = p.Context.AuxData.FrameID
		v.mu.Unlock()
		// Every frame's page world gets the shims, including frames that loaded
		// before this view attached (new documents get them on their own).
		for _, s := range []string{selectShim, uiShim} {
			v.h.cdp.Notify(sid, "Runtime.evaluate", map[string]any{"expression": s, "contextId": p.Context.ID})
		}
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID int64 `json:"executionContextId"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			v.mu.Lock()
			delete(v.ctxFrame, ctxKey(sid, p.ID))
			v.mu.Unlock()
		}
	case "Runtime.executionContextsCleared": // a navigation: this session's contexts are all gone
		v.mu.Lock()
		for k := range v.ctxFrame {
			if strings.HasPrefix(k, sid+"/") {
				delete(v.ctxFrame, k)
			}
		}
		v.mu.Unlock()
	case "Target.attachedToTarget":
		v.onChildAttached(sid, m.Params)
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			v.mu.Lock()
			delete(v.children, p.SessionID)
			delete(v.childFrame, p.SessionID)
			v.mu.Unlock()
		}
	case "Page.screencastFrame":
		var p struct {
			Data      string          `json:"data"`
			Metadata  json.RawMessage `json:"metadata"`
			SessionID int64           `json:"sessionId"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		// Ack Chrome here, not after the viewer draws, so the capture rate is
		// not capped by the network round trip. Only the newest frame is kept.
		v.h.cdp.Notify(sid, "Page.screencastFrameAck", map[string]any{"sessionId": p.SessionID})
		img, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			return
		}
		crop, full := v.fullscreenCrop(p.Metadata, img)
		if !full && v.wrongSize(p.Metadata, img) {
			return
		}
		v.mu.Lock()
		v.latest = &frame{data: img, meta: p.Metadata, inputSeq: v.inputSeq, quality: v.curQ, crop: crop}
		v.framesIn++
		if v.pace != nil {
			v.pace.produced += int64(len(img))
		}
		v.mu.Unlock()
		v.kick()
	case "Page.javascriptDialogOpening":
		if !top { // an iframe's dialog also reaches the page's session: one card
			return
		}
		var p struct {
			URL           string `json:"url"`
			Message       string `json:"message"`
			Type          string `json:"type"`
			DefaultPrompt string `json:"defaultPrompt"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v.h.setDialog(v.target(), p.Type+": "+p.Message)
		v.sendJSON(map[string]any{"t": "dialog", "type": p.Type, "message": p.Message, "defaultPrompt": p.DefaultPrompt, "url": p.URL})
	case "Page.javascriptDialogClosed": // answered here, in another view, or by the agent
		if !top {
			return
		}
		v.h.setDialog(v.target(), "")
		v.sendJSON(map[string]any{"t": "dialogClosed"})
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ParentID       string `json:"parentId"`
				URL            string `json:"url"`
				UnreachableURL string `json:"unreachableUrl"`
			} `json:"frame"`
		}
		if json.Unmarshal(m.Params, &p) == nil && top && p.Frame.ParentID == "" {
			// A page that failed to load keeps its address in the bar, as in
			// Chrome, not chrome-error://chromewebdata/: Reload retries it.
			v.sendJSON(map[string]any{"t": "url", "url": firstNonEmpty(p.Frame.UnreachableURL, p.Frame.URL)})
			go v.sendHistory()
		}
	case "Page.navigatedWithinDocument": // pushState, replaceState, #hash: single-page apps
		var p struct {
			FrameID string `json:"frameId"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(m.Params, &p) == nil && top && p.FrameID == v.target() {
			v.sendJSON(map[string]any{"t": "url", "url": p.URL})
			go v.sendHistory()
		}
	case "Page.frameStartedLoading", "Page.frameStoppedLoading": // the reload button turns into Stop
		var p struct {
			FrameID string `json:"frameId"`
		}
		if json.Unmarshal(m.Params, &p) == nil && top && p.FrameID == v.target() {
			v.sendJSON(map[string]any{"t": "loading", "on": m.Method == "Page.frameStartedLoading"})
			if m.Method == "Page.frameStoppedLoading" {
				go v.sendHistory()
			}
		}
	case "Runtime.bindingCalled":
		var p struct {
			Name      string `json:"name"`
			Payload   string `json:"payload"`
			ContextID int64  `json:"executionContextId"`
		}
		if json.Unmarshal(m.Params, &p) != nil || !json.Valid([]byte(p.Payload)) {
			return
		}
		kind := map[string]string{"__rbSelect": "select", "__rbUI": "ui"}[p.Name]
		if kind == "" {
			return
		}
		var fs struct {
			Kind string `json:"kind"`
			On   bool   `json:"on"`
			Href string `json:"href"`
		}
		err := json.Unmarshal([]byte(p.Payload), &fs)
		v.mu.Lock()
		main := top && v.targetID != "" && v.ctxFrame[ctxKey(sid, p.ContextID)] == v.targetID
		gesture := time.Since(v.lastGesture) < activation
		v.mu.Unlock()
		if kind == "ui" && fs.Kind == "favicon" {
			// Never relayed: the desktop would fetch any URL a page names. Only
			// the page's own main frame names its icon, which travels as bytes.
			if main && err == nil {
				// One load at a time, the newest report's waiting: never a
				// goroutine per report, as a page can report as often as it likes.
				select {
				case <-v.iconQ:
				default:
				}
				select {
				case v.iconQ <- func() { v.sendFavicon(fs.Href) }:
				default:
				}
			}
			return
		}
		// The page can call the binding itself, from any frame and at any time:
		// it writes the person's clipboard only as a tab may, from its main
		// frame and just after the person's click or key in this view.
		if kind == "ui" && clipboardReport(p.Payload) && !(main && gesture) {
			return
		}
		if top && err == nil && fs.Kind == "fullscreen" {
			v.mu.Lock()
			v.fullscreen = fs.On
			v.mu.Unlock()
			select { // a still page sends no frame of its new layout (queued, like the icon)
			case v.shimQ <- func() { v.captureStill(v.currentBaseQuality()) }:
			default:
			}
		}
		select { // drop rather than stall the CDP reader
		case v.shimQ <- func() { v.relayShim(sid, kind, p.Payload, p.ContextID) }:
		default:
		}
	case "Fetch.requestPaused", "Fetch.authRequired":
		v.onBridgeEvent(sid, m)
	case "Inspector.targetCrashed":
		if top {
			v.sendJSON(map[string]any{"t": "crashed"})
		}
	case "Page.fileChooserOpened":
		var p struct {
			Mode          string `json:"mode"`
			BackendNodeID int64  `json:"backendNodeId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v.mu.Lock()
		v.fileNode, v.fileSession = p.BackendNodeID, sid
		v.mu.Unlock()
		go v.showFileChooser(sid, p.Mode, p.BackendNodeID)
	}
}

// activation is how long after the person's click or key in a view its page
// may write their clipboard: Chromium's user activation lasts as long.
const activation = 5 * time.Second

// clipboardReport reports a shim report the viewer takes as a clipboard write.
// It reads the kind as the viewer's JSON.parse does: Go's decoding into a
// struct matches keys in any case, so {"kind":"clipboard","Kind":"x"} gave "x".
func clipboardReport(payload string) bool {
	var m map[string]json.RawMessage
	var kind string
	return json.Unmarshal([]byte(payload), &m) == nil && json.Unmarshal(m["kind"], &kind) == nil && kind == "clipboard"
}

// sendHistory tells the view whether Back and Forward have somewhere to go,
// for a host app whose own buttons drive this tab. While a cross-site
// navigation moves the page to a new renderer, Chromium answers "Not attached
// to an active page" for a moment: it asks again, and the load stopping asks
// once more, so Back is not left disabled.
func (v *viewer) sendHistory() {
	for try := 1; ; try++ {
		// CDP answers one session's calls in order: a read sent later saw a
		// later page.
		asked := v.histAsked.Add(1)
		res, err := v.call("Page.getNavigationHistory", nil)
		var h struct {
			CurrentIndex int               `json:"currentIndex"`
			Entries      []json.RawMessage `json:"entries"`
		}
		if err == nil && json.Unmarshal(res, &h) == nil {
			v.histMu.Lock()
			if asked > v.histSent {
				v.histSent = asked
				v.sendJSON(map[string]any{"t": "history", "back": h.CurrentIndex > 0, "fwd": h.CurrentIndex < len(h.Entries)-1})
			}
			v.histMu.Unlock()
			return
		}
		if try == historyTries {
			return
		}
		select {
		case <-v.ctx.Done():
			return
		case <-time.After(historyRetry):
		}
	}
}

const (
	historyTries = 3
	historyRetry = 150 * time.Millisecond
)

// setVisible pauses the stream of a view the host app hid (one per agent
// tab, most of them in the background) and resumes it, in front, when shown.
func (v *viewer) isHidden() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.hidden
}

func (v *viewer) setVisible(on bool) {
	if !on {
		_, _ = v.call("Page.stopScreencast", nil)
		return
	}
	_, _ = v.call("Page.bringToFront", nil)
	// The size set while the tab was behind another leaves Chrome capturing
	// its whole window now that it is in front: set it again. fitView also
	// restarts the stream and sends a still.
	_ = v.fitView(true)
}

// sendFavicon gives the view its page's icon as a data: URL, or "" when there
// is none. Only the newest report's icon is sent (a slow icon of the previous
// page must not replace the new page's).
func (v *viewer) sendFavicon(href string) {
	v.mu.Lock()
	v.iconSeq++
	seq, sid, target := v.iconSeq, v.session, v.targetID
	v.mu.Unlock()
	icon := v.h.favicon(v.ctx, sid, target, href)
	v.mu.Lock()
	current := seq == v.iconSeq && target == v.targetID
	v.mu.Unlock()
	if current {
		v.sendJSON(map[string]any{"t": "favicon", "href": icon})
	}
}

// showFileChooser asks the viewer for files the way the page's input wants
// them: several, a folder, or of the accepted types.
func (v *viewer) showFileChooser(sid, mode string, node int64) {
	ctx, cancel := context.WithTimeout(v.ctx, 3*time.Second)
	defer cancel()
	msg := map[string]any{"t": "filechooser", "mode": mode, "accept": "", "folder": false}
	if res, err := v.h.cdp.Call(ctx, sid, "DOM.describeNode", map[string]any{"backendNodeId": node}); err == nil {
		var d struct {
			Node struct {
				Attributes []string `json:"attributes"` // name, value, name, value...
			} `json:"node"`
		}
		_ = json.Unmarshal(res, &d)
		for i := 0; i+1 < len(d.Node.Attributes); i += 2 {
			switch strings.ToLower(d.Node.Attributes[i]) {
			case "accept":
				msg["accept"] = d.Node.Attributes[i+1]
			case "webkitdirectory":
				msg["folder"] = true
			}
		}
	}
	v.sendJSON(msg)
}

// adaptQuality lowers JPEG quality while Chrome produces more than the path
// delivers (an animating page on a slow link), and restores it with headroom.
func (v *viewer) adaptQuality() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	calm := 0
	for {
		select {
		case <-v.ctx.Done():
			return
		case <-tick.C:
		}
		v.mu.Lock()
		demand, bw, q := float64(v.pace.produced), v.pace.bandwidth(), v.quality
		v.pace.produced = 0
		next := q
		switch {
		case bw > 0 && demand > 1.3*bw && q > 30:
			next, calm = max(30, q-15), 0
		case bw > 0 && demand < 0.6*bw && q < v.baseQ:
			if calm++; calm >= 3 {
				next, calm = min(v.baseQ, q+10), 0
			}
		default:
			calm = 0
		}
		v.quality = next
		scrolling := v.scrolling
		v.mu.Unlock()
		if next != q && !scrolling {
			_ = v.restartScreencast(next)
		}
	}
}

// observeRTT adapts the window to one frame's send-to-ack time. Caller holds v.mu.
func (v *viewer) observeRTT(rtt time.Duration) {
	if !v.adaptive {
		return
	}
	now := time.Now()
	if v.minRTT == 0 || rtt < v.minRTT || now.Sub(v.minRTTAt) > 10*time.Second {
		v.minRTT, v.minRTTAt = rtt, now
	}
	if v.srtt == 0 {
		v.srtt = rtt
	}
	v.srtt = (7*v.srtt + rtt) / 8
	// Enough frames in flight for 30 fps at the path's base round trip. Not
	// shrunk on delay: at 5 Mbit a frame's own serialization time inflates the
	// round trip as much as queueing does, and shrinking starved the stream.
	v.maxFlight = max(4, min(int64(v.minRTT/(33*time.Millisecond))+2, 12))
}

func (v *viewer) kick() {
	select {
	case v.signal <- struct{}{}:
	default:
	}
}

// sendRaw queues b for the view and never waits: the CDP reader and /agent,
// which every view and the agent share, call it. A view whose queue is full
// has stopped reading: it is dropped, and viewer.html reconnects.
func (v *viewer) sendRaw(b []byte) {
	select {
	case v.out <- b:
	case <-v.ctx.Done():
	default:
		log.Printf("viewer %s: not reading, dropped", v.id)
		v.cancel()
	}
}

func (v *viewer) sendJSON(m map[string]any) {
	b, _ := json.Marshal(m)
	v.sendRaw(b)
}

func (v *viewer) sendControl(owner string) {
	v.sendJSON(map[string]any{"t": "control", "owner": owner})
}

func (v *viewer) onTabs(tabs []tab, newTab string) {
	v.sendJSON(map[string]any{"t": "tabs", "tabs": tabs})
	agent := v.h.ctl.agentRunning()
	v.mu.Lock()
	cur, background := v.targetID, time.Now().Before(v.bgTabUntil)
	// A tab the agent opens while the person types here stays in the
	// background: following it would send their next keys to the new page.
	busy := agent && time.Since(v.lastInput) < 3*time.Second
	v.mu.Unlock()
	alive := false
	for _, t := range tabs {
		alive = alive || t.ID == cur
	}
	if v.embed {
		switch {
		case cur == "" && len(tabs) > 0 && v.take(): // no page yet: this is its first
			next := newTab
			if next == "" {
				next = tabs[len(tabs)-1].ID
			}
			go v.adopt(next)
		case cur != "" && !alive: // its tab closed: the host closes this one, back to the opener
			v.sendJSON(map[string]any{"t": "closed", "next": v.h.openerOf(cur)})
		case newTab != "" && newTab != cur:
			v.sendJSON(map[string]any{"t": "opened", "id": newTab, "follow": !background && !busy})
		}
		return
	}
	switch {
	case newTab != "" && newTab != cur && busy:
		v.sendJSON(map[string]any{"t": "agentTab", "id": newTab})
	case newTab != "" && newTab != cur && !background: // follow new tabs and popups (not Cmd+click ones), like a local browser
		go func() { _ = v.attach(newTab) }()
	case !alive && len(tabs) > 0:
		// A closed popup returns to the tab that opened it, as in Chrome (an
		// OAuth window hands back to the app), else to the newest tab.
		next := tabs[len(tabs)-1].ID
		if o := v.h.openerOf(cur); o != "" {
			next = o
		}
		go func() { _ = v.attach(next) }()
	case len(tabs) == 0 && cur != "":
		go func() { // off the CDP reader: detach waits on Chrome
			v.attachMu.Lock()
			defer v.attachMu.Unlock()
			if v.target() != cur {
				return
			}
			v.detach()
			v.mu.Lock()
			v.targetID = ""
			v.mu.Unlock()
			v.sendJSON(map[string]any{"t": "active", "id": ""})
		}()
	}
}

// take claims the first page for a view that has none yet: true once.
func (v *viewer) take() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	t := v.waiting
	v.waiting = false
	return t
}

// adopt shows target in a view that had no page; it waits again if that fails.
func (v *viewer) adopt(target string) {
	if err := v.attach(target); err != nil {
		log.Printf("viewer attach: %v", err)
		v.mu.Lock()
		v.waiting = true
		v.mu.Unlock()
	}
}

// writeLoop owns the socket: control messages in order, frames newest-first
// with at most maxFlight unacknowledged frames on the wire.
func (v *viewer) writeLoop() {
	for {
		select {
		case <-v.ctx.Done():
			return
		case b := <-v.out:
			if v.conn.Write(v.ctx, websocket.MessageText, b) != nil {
				v.cancel()
				return
			}
		case <-v.signal:
			for {
				v.mu.Lock()
				f := v.latest
				if f == nil || v.seq-v.ackSeq >= v.maxFlight {
					v.mu.Unlock()
					break
				}
				if v.pace != nil {
					if ok, wait := v.pace.canSend(int64(len(f.data))); !ok {
						if wait > 0 {
							time.AfterFunc(wait, v.kick) // paced: try again when the gap has passed
						}
						v.mu.Unlock()
						break
					}
				}
				v.latest = nil
				v.seq++
				seq := v.seq
				v.sentAt[seq] = time.Now()
				if v.pace != nil {
					v.pace.onSend(seq, int64(len(f.data)))
				}
				v.mu.Unlock()
				h := map[string]any{"seq": seq, "ts": time.Now().UnixMilli(), "inputSeq": f.inputSeq, "q": f.quality, "meta": f.meta}
				if f.crop[0] > 0 {
					h["crop"] = f.crop
				}
				hdr, _ := json.Marshal(h)
				msg := make([]byte, 4+len(hdr)+len(f.data))
				binary.BigEndian.PutUint32(msg, uint32(len(hdr)))
				copy(msg[4:], hdr)
				copy(msg[4+len(hdr):], f.data)
				if v.conn.Write(v.ctx, websocket.MessageBinary, msg) != nil {
					v.cancel()
					return
				}
				v.h.bytesOut.Add(int64(len(msg)))
				v.h.frames.Add(1)
			}
		}
	}
}

func (v *viewer) human() {
	v.h.ctl.mu.Lock()
	v.h.ctl.lastEdit = time.Now()
	v.h.ctl.mu.Unlock()
	v.mu.Lock()
	v.lastGesture = time.Now()
	v.mu.Unlock()
	v.scrolled()
}

// scrolled is input that moves the page but changes nothing on it.
func (v *viewer) scrolled() {
	v.mu.Lock()
	v.lastInput = time.Now()
	v.mu.Unlock()
	if v.h.ctl.humanInput(v.id) {
		v.h.broadcastControl()
	}
	v.claimSoon()
}

// claimSoon gives the page this view's size once its input pauses: resizing
// mid-drag would move what the person is dragging.
func (v *viewer) claimSoon() {
	v.mu.Lock()
	if v.claim == nil {
		v.claim = time.AfterFunc(time.Second, func() {
			if t := v.target(); t != "" && !v.sizes(t) {
				_ = v.applyViewport(false)
			}
		})
	} else {
		v.claim.Reset(time.Second)
	}
	v.mu.Unlock()
}

func (v *viewer) sizes(target string) bool {
	v.h.viewMu.Lock()
	defer v.h.viewMu.Unlock()
	return v.h.sizer[target] == v
}

func (v *viewer) markInput(seq int64) {
	if seq > 0 {
		v.mu.Lock()
		v.inputSeq = seq
		v.mu.Unlock()
	}
}

func (v *viewer) readLoop() {
	for {
		typ, data, err := v.conn.Read(v.ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m clientMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		v.mu.Lock()
		sid := v.session
		v.mu.Unlock()
		switch m.T {
		case "ack":
			v.mu.Lock()
			if sent, ok := v.sentAt[m.Seq]; ok {
				v.observeRTT(time.Since(sent))
			}
			if v.pace != nil {
				v.pace.onAck(m.Seq)
			}
			for s := range v.sentAt {
				if s <= m.Seq {
					delete(v.sentAt, s)
				}
			}
			v.ackSeq = max(v.ackSeq, m.Seq)
			v.mu.Unlock()
			v.kick()
		case "ping":
			v.sendJSON(map[string]any{"t": "pong", "c": m.C, "s": time.Now().UnixMilli()})
		case "mouse":
			if m.Type == "mouseWheel" {
				v.scrolled()
			} else if m.Type != "mouseMoved" {
				v.human()
			} else {
				v.claimSoon() // the pointer over a view means someone is looking at it
			}
			if m.Type == "mousePressed" {
				v.h.ctl.mu.Lock()
				v.h.ctl.lastPress = time.Now()
				v.h.ctl.mu.Unlock()
			}
			if m.Type == "mousePressed" && (m.Mods&2 != 0 || m.Button == "middle") {
				v.mu.Lock()
				v.bgTabUntil = time.Now().Add(1500 * time.Millisecond)
				v.mu.Unlock()
			}
			if m.Type == "mouseWheel" {
				v.onWheel()
			}
			v.markInput(m.Seq)
			p := map[string]any{"type": m.Type, "x": m.X, "y": m.Y, "modifiers": m.Mods,
				"button": firstNonEmpty(m.Button, "none"), "buttons": m.Buttons, "clickCount": m.ClickCount}
			if m.Type == "mouseWheel" {
				// Wait for each wheel: sent back to back, Chrome loses track of
				// the gesture and scroll-snap rows stop between items.
				p["deltaX"], p["deltaY"] = m.DX, m.DY
				v.input(func() { v.dispatch(sid, "Input.dispatchMouseEvent", p) })
				break
			}
			v.input(func() { v.h.cdp.Notify(sid, "Input.dispatchMouseEvent", p) })
		case "key":
			v.human()
			v.markInput(m.Seq)
			p := map[string]any{"type": m.Type, "key": m.Key, "code": m.Code, "modifiers": m.Mods,
				"windowsVirtualKeyCode": m.KeyCode, "nativeVirtualKeyCode": m.KeyCode, "autoRepeat": m.Repeat}
			if m.Text != "" {
				p["text"], p["unmodifiedText"] = m.Text, m.Text
			}
			v.input(func() { v.h.cdp.Notify(sid, "Input.dispatchKeyEvent", p) })
		case "text": // IME commit, emoji panel, dictation
			v.human()
			v.markInput(m.Seq)
			v.input(func() { v.h.cdp.Notify(sid, "Input.insertText", map[string]any{"text": m.Text}) })
		case "paste":
			v.human()
			v.markInput(m.Seq)
			v.input(func() { v.paste(sid, m.Text, m.HTML, m.Files) })
		case "drop":
			v.human()
			v.markInput(m.Seq)
			v.input(func() { v.drop(sid, m.X, m.Y, m.Dir, m.Names) })
		case "nav":
			v.human()
			if sid == "" { // every tab is closed
				go func() {
					_, _ = v.h.cdp.Call(v.ctx, "", "Target.createTarget", map[string]any{"url": normalizeURL(m.URL)})
				}()
				break
			}
			go func() { _, _ = v.call("Page.navigate", map[string]any{"url": normalizeURL(m.URL)}) }()
		case "back", "forward":
			v.human()
			expr := "history.back()"
			if m.T == "forward" {
				expr = "history.forward()"
			}
			go func() { _, _ = v.call("Runtime.evaluate", map[string]any{"expression": expr}) }()
		case "reload":
			v.human()
			go v.reload()
		case "stop":
			go func() { _, _ = v.call("Page.stopLoading", nil) }()
		case "visible": // a host app hid or showed this view's tab
			v.mu.Lock()
			v.hidden = !m.On
			v.mu.Unlock()
			go v.setVisible(m.On)
		case "tab":
			go func() { _ = v.attach(m.ID) }()
		case "newtab":
			go func() { _, _ = v.h.cdp.Call(v.ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}) }()
		case "closetab":
			go func() { _, _ = v.h.cdp.Call(v.ctx, "", "Target.closeTarget", map[string]any{"targetId": m.ID}) }()
		case "resize":
			v.mu.Lock()
			v.w, v.hgt = clamp(m.W, 200, 3840, v.w), clamp(m.H, 150, 2160, v.hgt)
			picked := m.DPR >= 1 && m.DPR <= 3 && m.DPR != v.dpr
			if picked {
				v.dpr = m.DPR
			}
			v.mu.Unlock()
			if picked { // chosen in this view's toolbar, so it is in use
				go func() { _ = v.applyViewport(false) }()
			} else {
				go func() { _ = v.fitView(false) }()
			}
		case "quality":
			v.mu.Lock()
			v.quality = clamp(m.Quality, 10, 100, v.quality)
			v.mu.Unlock()
			go func() { _ = v.restartScreencast(v.currentBaseQuality()) }()
		case "dialog":
			v.human()
			go func() {
				_, _ = v.call("Page.handleJavaScriptDialog", map[string]any{"accept": m.Accept, "promptText": m.PromptText})
			}()
		case "selectResp":
			v.human()
			// The id goes in as a JSON string and is compared as data: Go's %q
			// inside a JS string let a crafted id run script.
			id, _ := json.Marshal(m.ID)
			expr := fmt.Sprintf(`(() => { const el = [...document.querySelectorAll('select[data-rb-sel]')].find((e) => e.dataset.rbSel === %s); if (!el) return;
				el.selectedIndex = %d; el.dispatchEvent(new Event('input', {bubbles: true})); el.dispatchEvent(new Event('change', {bubbles: true})); })()`, id, m.Index)
			go v.evalIn(m.Ctx, expr, false)
		case "upload":
			v.human()
			go v.upload(m.Dir, m.Names, m.Folder)
		case "release":
			v.h.ctl.release()
			v.h.broadcastControl()
		default:
			v.onBridgeMessage(data, m.T)
		}
	}
}

// onWheel drops quality while the page scrolls and restores it 200 ms after
// the last wheel event, so motion stays fluid and the resting frame is sharp.
func (v *viewer) onWheel() {
	v.mu.Lock()
	start := !v.scrolling
	v.scrolling = true
	if v.scrollTimer != nil {
		v.scrollTimer.Stop()
	}
	v.scrollTimer = time.AfterFunc(200*time.Millisecond, func() {
		v.mu.Lock()
		v.scrolling = false
		q := v.quality
		v.mu.Unlock()
		_ = v.restartScreencast(q)
		v.captureStill(q) // a static page paints no new frame, so send a sharp one
	})
	scrollQ := v.scrollQ
	v.mu.Unlock()
	if start {
		go func() { _ = v.restartScreencast(scrollQ) }()
	}
}

// captureStill pushes one full-resolution frame through the normal frame path.
func (v *viewer) captureStill(q int) {
	v.mu.Lock()
	sid, hidden := v.session, v.hidden
	v.mu.Unlock()
	if hidden {
		return
	}
	// A screenshot requested while the page navigates can wait for a frame that
	// never comes; a still is only an optimisation, so give up quickly.
	ctx, cancel := context.WithTimeout(v.ctx, 3*time.Second)
	defer cancel()
	t0 := time.Now()
	res, err := v.h.cdp.Call(ctx, sid, "Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": q, "optimizeForSpeed": true})
	if d := time.Since(t0); d > time.Second {
		log.Printf("slow still: %v (err %v)", d, err)
	}
	if err != nil {
		return
	}
	var r struct {
		Data string `json:"data"`
	}
	if json.Unmarshal(res, &r) != nil {
		return
	}
	img, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil || v.wrongSize(nil, img) {
		return
	}
	v.mu.Lock()
	if v.latest == nil { // a newer screencast frame wins
		v.latest = &frame{data: img, inputSeq: v.inputSeq, quality: q, still: true}
	}
	v.mu.Unlock()
	v.kick()
}

// input queues page input. One goroutine sends it in order, so a wheel or a
// paste that waits on Chrome never holds up frame acks, and nothing overtakes it.
func (v *viewer) input(f func()) {
	select {
	case v.inputs <- f:
	default: // the page has stopped taking input (hung): drop rather than stall
	}
}

// dispatch sends input and waits for Chrome to take it, at most a second:
// a click that opens alert() is not answered until the dialog closes.
func (v *viewer) dispatch(sid, method string, params any) {
	ctx, cancel := context.WithTimeout(v.ctx, time.Second)
	defer cancel()
	_, _ = v.h.cdp.Call(ctx, sid, method, params)
}

// paste puts the desktop clipboard on the remote browser's and presses
// Ctrl+V, so the page gets a real paste event: HTML, images and its own paste
// handler all work. A page that can't take a clipboard write still gets the text.
func (v *viewer) paste(sid, text, html string, files []file) {
	png := ""
	for _, f := range files {
		if f.Type == "image/png" {
			png = f.Data
			break
		}
	}
	payload, _ := json.Marshal(map[string]string{"text": text, "html": html, "png": png})
	ctx, cancel := context.WithTimeout(v.ctx, 2*time.Second)
	defer cancel()
	res, err := v.h.cdp.Call(ctx, sid, "Runtime.evaluate", map[string]any{"awaitPromise": true, "userGesture": true, "returnByValue": true,
		// Text and HTML go through a copy event of our own, which works on any
		// page; the async clipboard API (needed for an image) only exists on
		// https and localhost pages.
		"expression": `(async (d) => {
			if (d.png && window.isSecureContext) {
				const item = { 'image/png': new Blob([Uint8Array.from(atob(d.png), (c) => c.charCodeAt(0))], { type: 'image/png' }) };
				if (d.text) item['text/plain'] = new Blob([d.text], { type: 'text/plain' });
				await navigator.clipboard.write([new ClipboardItem(item)]);
				return true;
			}
			let ok = false;
			const fill = (e) => {
				e.stopImmediatePropagation(); e.preventDefault();
				e.clipboardData.setData('text/plain', d.text);
				if (d.html) e.clipboardData.setData('text/html', d.html);
				ok = true;
			};
			window.addEventListener('copy', fill, true);
			try { document.execCommand('copy'); } finally { window.removeEventListener('copy', fill, true); }
			return ok;
		})(` + string(payload) + `)`})
	var r struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err != nil || json.Unmarshal(res, &r) != nil || !r.Result.Value {
		if text != "" {
			v.h.cdp.Notify(sid, "Input.insertText", map[string]any{"text": text})
		}
		return
	}
	for _, t := range []string{"rawKeyDown", "keyUp"} {
		v.h.cdp.Notify(sid, "Input.dispatchKeyEvent", map[string]any{"type": t, "key": "v", "code": "KeyV",
			"windowsVirtualKeyCode": 86, "nativeVirtualKeyCode": 86, "modifiers": 2})
	}
}

// drop drops desktop files (already uploaded to dir) onto the page at x,y, as
// a drag from Finder would.
func (v *viewer) drop(sid string, x, y float64, dir string, names []string) {
	paths := uploadPaths(dir, names)
	if len(paths) == 0 {
		return
	}
	data := map[string]any{"items": []any{}, "files": paths, "dragOperationsMask": 1}
	for _, t := range []string{"dragEnter", "dragOver", "drop"} {
		v.dispatch(sid, "Input.dispatchDragEvent", map[string]any{"type": t, "x": x, "y": y, "data": data})
	}
}

func uploadPaths(dir string, names []string) []string {
	var paths []string
	for _, n := range names {
		if p, ok := uploadPath(dir, n); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// upload gives the page's file input the files the person picked (already
// uploaded to dir), in the session of the frame whose input asked.
func (v *viewer) upload(dir string, names []string, folder bool) {
	paths := uploadPaths(dir, names)
	if folder && len(paths) > 0 {
		// A folder input takes the folder itself: Chrome lists it, and each
		// file keeps its path inside it (webkitRelativePath).
		top, _, _ := strings.Cut(filepath.ToSlash(names[0]), "/")
		paths = []string{filepath.Join(uploadRoot, dir, top)}
	}
	v.mu.Lock()
	node, sid := v.fileNode, v.fileSession
	v.mu.Unlock()
	if len(paths) == 0 || node == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(v.ctx, 15*time.Second)
	defer cancel()
	if _, err := v.h.cdp.Call(ctx, sid, "DOM.setFileInputFiles", map[string]any{"files": paths, "backendNodeId": node}); err != nil {
		log.Printf("upload: %v", err)
	}
}

func normalizeURL(u string) string {
	if u == "" {
		return "about:blank"
	}
	for _, p := range []string{"http://", "https://", "about:", "data:", "file:"} {
		if len(u) >= len(p) && u[:len(p)] == p {
			return u
		}
	}
	return "http://" + u
}
