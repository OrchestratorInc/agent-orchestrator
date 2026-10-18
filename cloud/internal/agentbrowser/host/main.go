// Package host is the agent's browser on a remote host. It runs next to the
// agent: it owns one headless Chromium, lets the agent drive it through
// agent-browser on loopback CDP (POST /agent), and streams it to the desktop
// (/v/cdp and /ws/cdp, a CDP screencast), which shows its pages as tabs.
// Main runs it as its own process.
package host

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed viewer.html
var viewerHTML []byte

type tab struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type host struct {
	dialogMu sync.Mutex
	dialogs  map[string]string // tab -> the JavaScript dialog open in it
	cdp      *cdpClient
	token    string
	abPath   string
	cdpPort  int
	ctl      control
	tabsMu   sync.Mutex
	tabs     map[string]*tab
	order    []string          // creation order, newest last
	openers  map[string]string // tab -> the tab that opened it (popups)
	viewMu   sync.Mutex
	sizer    map[string]*viewer // per tab: the view whose size the page has; other views letterbox it
	viewers  map[*viewer]struct{}
	bytesOut atomic.Int64
	frames   atomic.Int64
	agentMu  sync.Mutex
	dl       *downloads // the remote browser's downloads, served to viewers
	iconMu   sync.Mutex
	icons    map[string]string // favicon href -> its data: URL
}

// control reports who is acting. People and the agent share the page: human
// input never blocks the agent, the viewer just shows when the agent drives.
type control struct {
	mu        sync.Mutex
	human     string
	lastInput time.Time
	lastPress time.Time // the person's last click: they may have moved the caret
	lastEdit  time.Time // the person's last input that can change the page (not scrolling)
	agentBusy int       // agent commands running: they may overlap
	snapshot  time.Time // the agent's last snapshot: its element refs date from then
}

// releaseIfHolder frees control when the human holding it disconnects.
func (c *control) releaseIfHolder(id string) {
	c.mu.Lock()
	if c.human == id {
		c.human = ""
	}
	c.mu.Unlock()
}

func (c *control) humanInput(id string) (changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed = c.human != id
	c.human, c.lastInput = id, time.Now()
	return changed
}

// typing reports a person's input within d: their caret is in use.
func (c *control) typing(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastInput) < d
}

func (h *host) setDialog(target, d string) {
	h.dialogMu.Lock()
	defer h.dialogMu.Unlock()
	if h.dialogs == nil {
		h.dialogs = map[string]string{}
	}
	if d == "" {
		delete(h.dialogs, target)
	} else {
		h.dialogs[target] = d
	}
}

// openDialog is the JavaScript dialog still open in tab target, if any. Views
// clear the entry when its dialog closes; one closed with no view attached
// (the agent answered it while the person's window was closed) refused the
// agent for good, so the entry is checked: a page showing a dialog runs no script.
func (h *host) openDialog(ctx context.Context, target string) string {
	h.dialogMu.Lock()
	d := h.dialogs[target]
	h.dialogMu.Unlock()
	if d == "" || h.scriptBlocked(ctx, target) {
		return d
	}
	h.dialogMu.Lock()
	if h.dialogs[target] == d {
		delete(h.dialogs, target)
	}
	h.dialogMu.Unlock()
	return ""
}

// scriptBlocked reports whether target's page runs no script, as while it
// shows a dialog. A tab that is gone blocks nothing.
func (h *host) scriptBlocked(ctx context.Context, target string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := h.cdp.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	var a struct {
		SessionID string `json:"sessionId"`
	}
	if err != nil || json.Unmarshal(res, &a) != nil {
		return false
	}
	defer h.cdp.Notify("", "Target.detachFromTarget", map[string]any{"sessionId": a.SessionID})
	ectx, ecancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer ecancel()
	_, err = h.cdp.Call(ectx, a.SessionID, "Runtime.evaluate", map[string]any{"expression": "0"})
	return err != nil
}

// editSince reports a person's input after t that may have changed the page.
func (c *control) editSince(t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastEdit.After(t)
}

func (c *control) pressSince(t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPress.After(t)
}

func (c *control) snapshotAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot
}

// agentCommand is the agent-browser command in args: the first argument that
// is not a global flag or a flag's value (`--json snapshot -i`).
func agentCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case slices.Contains(abValueFlags, a):
			i++
		case strings.HasPrefix(a, "-"):
			if i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				i++ // a boolean flag may say which (--headed false)
			}
		default:
			return a
		}
	}
	return ""
}

// abValueFlags are agent-browser's global flags that take a value (its --help, v0.38.1).
var abValueFlags = strings.Fields(`--action-policy --allowed-domains --args --ca-cert --cdp --color-scheme
	--config --confirm-actions --device --download-path --enable --engine --executable-path --extension
	--headers --idle-timeout --init-script --input-mode --max-output --model --namespace -p --profile
	--provider --proxy --proxy-bypass --restore-check-fn --restore-check-text --restore-check-url
	--restore-save --screenshot-dir --screenshot-format --screenshot-quality --session --session-name
	--state --user-agent`)

// usesRef reports an agent command aimed at a snapshot ref (@e12).
func usesRef(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "@e") {
			return true
		}
	}
	return false
}

func agentRefused(w http.ResponseWriter, why string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"output": why, "error": "refused", "ms": 0, "files": map[string][]byte{}})
}

func (c *control) agentRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agentBusy > 0
}

// An agent command focuses its own targets; while the person is typing, their
// field and caret come back afterwards so their next keys land where they were.
const saveFocus = `(() => { const a = document.activeElement; window.__rbFocus = a && a !== document.body ? a : null; window.__rbCaret = a && typeof a.selectionStart === 'number' ? [a.selectionStart, a.selectionEnd] : null; })()`
const restoreFocus = `(() => { const a = window.__rbFocus, c = window.__rbCaret; if (!a || !document.contains(a) || document.activeElement === a) return; a.focus({ preventScroll: true }); if (c) try { a.setSelectionRange(c[0], c[1]); } catch {} })()`

func (c *control) release() {
	c.mu.Lock()
	c.human = ""
	c.mu.Unlock()
}

func (c *control) owner() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.agentBusy > 0 {
		return "agent"
	}
	return "none"
}

// dataDir (-data) holds the service's files; profileDir is Chromium's profile
// in it. Main sets both.
var dataDir, profileDir string

// Main runs the browser service with args, its command-line flags without the
// program name. It never returns: the service ends by exiting the process.
func Main(args []string) {
	fs := flag.NewFlagSet("rbhost", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address for viewers")
	token := fs.String("token", os.Getenv("RB_TOKEN"), "shared secret for every route")
	chrome := fs.String("chrome", "/usr/bin/chromium", "Chromium binary")
	ab := fs.String("ab", "/usr/local/bin/agent-browser", "agent-browser binary")
	cdpPort := fs.Int("cdp-port", 9222, "loopback CDP port")
	dsf := fs.Float64("dsf", 2, "Chromium device scale factor; headless screencast ignores emulated DPR, so 2 is needed for Retina-sharp frames (1x viewers get frames scaled down)")
	data := fs.String("data", "/tmp", "directory for the Chromium profile, downloads, uploads and agent-browser's files")
	_ = fs.Parse(args)
	if len(*token) < 16 {
		log.Fatal("-token (or RB_TOKEN) must be at least 16 characters")
	}
	dataDir = *data
	profileDir = filepath.Join(dataDir, "rb-profile")
	tabsFile = filepath.Join(profileDir, "rb-tabs.json")
	downloadDir = filepath.Join(dataDir, "rb-downloads")
	uploadRoot = filepath.Join(dataDir, "rb-upload")
	agentDir = filepath.Join(dataDir, "rb-agent")
	agentSocketDir = filepath.Join(dataDir, "rb-agent-socket")
	// A restarted container keeps -data: stale profile locks would stop Chromium.
	// The profile itself stays, so logins survive a restart.
	for _, p := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_ = os.RemoveAll(filepath.Join(profileDir, p))
	}
	ctx := context.Background()
	h := &host{token: *token, abPath: *ab, cdpPort: *cdpPort,
		tabs: map[string]*tab{}, openers: map[string]string{}, viewers: map[*viewer]struct{}{}, sizer: map[string]*viewer{}}
	chromeCtx, stopChrome := context.WithCancel(ctx)
	wsURL, err := launchChrome(chromeCtx, *chrome, *cdpPort, *dsf)
	if err != nil {
		log.Fatalf("chrome: %v", err)
	}
	cdp, err := dialCDP(ctx, wsURL)
	if err != nil {
		log.Fatalf("cdp: %v", err)
	}
	h.cdp = cdp
	// Chromium can close the CDP connection and live on (seen: a message over
	// its size limit), and nothing works without it: stop Chromium, and both
	// start over as when it dies.
	go func() {
		<-cdp.done
		stopChrome()
	}()
	saved := loadTabs() // before tracking: the blank start tab rewrites the file
	if err := h.trackTargets(ctx); err != nil {
		log.Fatalf("targets: %v", err)
	}
	// Copy buttons may write the clipboard, and the viewer's paste goes through it.
	// setPermission, not grantPermissions: that one denies every permission it
	// doesn't list, so pages could no longer ask for notifications or location.
	if _, err := h.cdp.Call(ctx, "", "Browser.setPermission", map[string]any{"permission": map[string]any{"name": "clipboard-write"}, "setting": "granted"}); err != nil {
		log.Printf("clipboard permission: %v", err)
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		// Close Chromium properly: a killed one loses its last cookie writes,
		// such as a login made seconds before.
		stopping.Store(true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.cdp.Call(ctx, "", "Browser.close", nil)
		<-ctx.Done()
		os.Exit(0)
	}()
	// Attach the agent-browser daemon to the same Chromium.
	h.restoreTabs(ctx, saved)
	warmUp := []string{"get", "url"}
	stopStaleAgent()
	go func() {
		if out, err := h.runAgent(ctx, warmUp); err != nil {
			log.Printf("agent-browser warm-up failed: %v\n%s", err, out)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v/cdp", h.auth(serveHTML(viewerHTML)))
	mux.HandleFunc("GET /ws/cdp", h.auth(h.handleViewer))
	h.startDownloads(ctx)
	mux.HandleFunc("GET /download/{guid}", h.auth(h.handleDownload))
	mux.HandleFunc("POST /upload", h.auth(h.handleUpload))
	mux.HandleFunc("POST /agent", h.auth(h.handleAgent))
	mux.HandleFunc("POST /control/release", h.auth(func(w http.ResponseWriter, r *http.Request) {
		h.ctl.release()
		h.broadcastControl()
	}))
	mux.HandleFunc("GET /stats", h.auth(func(w http.ResponseWriter, r *http.Request) {
		var vs []map[string]any
		h.eachViewer(func(v *viewer) {
			v.mu.Lock()
			vs = append(vs, map[string]any{"target": v.targetID, "framesIn": v.framesIn, "seq": v.seq, "ackSeq": v.ackSeq, "pending": v.latest != nil, "scrolling": v.scrolling, "window": v.maxFlight, "minRTTms": v.minRTT.Milliseconds(), "srttMs": v.srtt.Milliseconds(), "quality": v.quality, "pace": paceStats(v.pace)})
			v.mu.Unlock()
		})
		_ = json.NewEncoder(w).Encode(map[string]any{"bytesOut": h.bytesOut.Load(), "frames": h.frames.Load(), "owner": h.ctl.owner(), "viewers": vs, "tabs": h.tabList()})
	}))
	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func serveHTML(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

func (h *host) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("token")
		if got == "" {
			got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func launchChrome(ctx context.Context, bin string, port int, dsf float64) (string, error) {
	// Scrollbars stay visible for the person watching. Sites treat a
	// "HeadlessChrome" user agent as a bot (Google serves a degraded page), so
	// the browser reports the Chrome it is.
	args := []string{
		"--headless=new", "--user-data-dir=" + profileDir, "--no-sandbox", "--disable-dev-shm-usage", "--no-first-run",
		"--no-default-browser-check", "--mute-audio", "--window-size=1280,800", "--user-agent=" + chromeUserAgent(bin), "about:blank",
		// Fullscreen resizes the window to the screen, 800x600 by default in
		// headless: the stream then showed a shrunken page. A screen the
		// window's size keeps the window, and the page's emulated size, as is.
	}
	args = append(args, fmt.Sprintf("--screen-info={0,0 %dx%d}", int(1280*dsf), int(800*dsf))) // device pixels
	if dsf != 1 {
		args = append(args, fmt.Sprintf("--force-device-scale-factor=%g", dsf))
	}
	// The person's language and light or dark mode (RB_TZ is the time zone, in
	// chromeEnv): pages format dates and pick themes for them, not for a server.
	if l := os.Getenv("RB_LANG"); l != "" {
		args = append(args, "--lang="+l, "--accept-lang="+l)
	}
	if os.Getenv("RB_SCHEME") == "dark" {
		args = append(args, "--force-dark-mode")
	}
	return launchChromeArgs(ctx, bin, port, args)
}

// chromeEnv is Chromium's environment. Without a UTF-8 locale it cannot name
// files outside ASCII: uploads and downloads called "résumé.pdf" fail.
func chromeEnv() []string {
	env := os.Environ()
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=C.UTF-8")
	}
	if tz := os.Getenv("RB_TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	return env
}

// chromeUserAgent is Chrome's own reduced Linux user agent for this build's major version.
func chromeUserAgent(bin string) string {
	major := "141"
	if out, err := exec.Command(bin, "--version").Output(); err == nil {
		if m := regexp.MustCompile(`(\d+)\.\d+\.\d+`).FindSubmatch(out); m != nil {
			major = string(m[1])
		}
	}
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + major + ".0.0.0 Safari/537.36"
}

// launchChromeArgs starts Chromium with loopback-only CDP and waits for it.
// When ctx ends, Chromium gets SIGTERM, which lets it save its last cookie
// writes, and is killed if it is still there 5 s later.
func launchChromeArgs(ctx context.Context, bin string, port int, args []string) (string, error) {
	args = append([]string{"--remote-debugging-address=127.0.0.1", fmt.Sprintf("--remote-debugging-port=%d", port)}, args...)
	if os.Getenv("RB_CHROME_LOG") != "" {
		args = append([]string{"--enable-logging=stderr", "--v=0", "--vmodule=*desktop*=1,*screen*=1,*capture*=1,*x11*=1"}, args...)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	// Gone with the service, however it ends (Chromium that did not close in
	// time on SIGTERM, say): an orphan would hold the profile the next
	// service's Chromium opens.
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	DieWithParent(cmd.SysProcAttr, syscall.SIGKILL)
	cmd.Env = chromeEnv()
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if os.Getenv("RB_CHROME_LOG") != "" {
		if f, err := os.Create(filepath.Join(dataDir, fmt.Sprintf("chrome-%d.log", port))); err == nil {
			cmd.Stdout, cmd.Stderr = f, f
		}
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() {
		_ = cmd.Wait()
		if stopping.Load() {
			os.Exit(0)
		}
		// Chromium crashed, was killed (OOM) or lost its CDP connection. Start
		// over with the same profile; the new process reopens the saved tabs
		// and viewers reconnect.
		// ponytail: restarts the whole host; inside AO's daemon, restart only the browser.
		log.Print("chromium exited; restarting")
		time.Sleep(2 * time.Second) // no tight loop if it dies at launch
		self, err := os.Executable()
		if err == nil {
			err = syscall.Exec(self, os.Args, os.Environ())
		}
		log.Fatalf("restart: %v", err)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
		if err == nil {
			var v struct {
				URL string `json:"webSocketDebuggerUrl"`
			}
			err = json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if err == nil && v.URL != "" {
				return v.URL, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("no CDP endpoint on port %d", port)
}

// trackTargets keeps the page-tab list current for every viewer.
func (h *host) trackTargets(ctx context.Context) error {
	h.cdp.On(func(m cdpMsg) {
		if m.SessionID != "" {
			return
		}
		switch m.Method {
		case "Target.targetCreated", "Target.targetInfoChanged":
			var p struct {
				TargetInfo struct {
					TargetID, Type, URL, Title string
					OpenerID                   string `json:"openerId"`
				} `json:"targetInfo"`
			}
			if json.Unmarshal(m.Params, &p) != nil || p.TargetInfo.Type != "page" {
				return
			}
			ti := p.TargetInfo
			h.tabsMu.Lock()
			created := h.tabs[ti.TargetID] == nil
			if created {
				h.tabs[ti.TargetID] = &tab{ID: ti.TargetID}
				h.order = append(h.order, ti.TargetID)
				if ti.OpenerID != "" {
					h.openers[ti.TargetID] = ti.OpenerID
				}
				go h.holdLocale(ti.TargetID)
			}
			h.tabs[ti.TargetID].URL, h.tabs[ti.TargetID].Title = ti.URL, ti.Title
			h.tabsMu.Unlock()
			h.broadcastTabs(createdID(created, ti.TargetID))
		case "Target.targetDestroyed":
			var p struct{ TargetID string }
			if json.Unmarshal(m.Params, &p) != nil {
				return
			}
			h.tabsMu.Lock()
			delete(h.tabs, p.TargetID)
			for id := range h.openers { // keep the closed tab's: its viewers go back to its opener
				if id != p.TargetID && h.tabs[id] == nil {
					delete(h.openers, id)
				}
			}
			for i, id := range h.order {
				if id == p.TargetID {
					h.order = append(h.order[:i:i], h.order[i+1:]...)
					break
				}
			}
			h.tabsMu.Unlock()
			h.broadcastTabs("")
		}
	})
	_, err := h.cdp.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true})
	go h.pollTitles(ctx)
	return err
}

// pollTitles keeps tab titles current: Chrome sends no event when a page sets
// document.title, so the tab picker showed URLs.
// ponytail: a 2 s poll; a title shim in every page if picker lag matters.
func (h *host) pollTitles(ctx context.Context) {
	for range time.Tick(2 * time.Second) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := h.cdp.Call(cctx, "", "Target.getTargets", nil)
		cancel()
		var got struct {
			TargetInfos []struct{ TargetID, Type, Title string } `json:"targetInfos"`
		}
		if err != nil || json.Unmarshal(res, &got) != nil {
			continue
		}
		changed := false
		h.tabsMu.Lock()
		for _, ti := range got.TargetInfos {
			if t := h.tabs[ti.TargetID]; t != nil && ti.Type == "page" && t.Title != ti.Title {
				t.Title, changed = ti.Title, true
			}
		}
		h.tabsMu.Unlock()
		if changed {
			h.broadcastTabs("")
		}
	}
}

func createdID(created bool, id string) string {
	if created {
		return id
	}
	return ""
}

func (h *host) tabList() []tab {
	h.tabsMu.Lock()
	defer h.tabsMu.Unlock()
	out := make([]tab, 0, len(h.order))
	for _, id := range h.order {
		out = append(out, *h.tabs[id])
	}
	return out
}

func (h *host) newestTab() string {
	h.tabsMu.Lock()
	defer h.tabsMu.Unlock()
	if len(h.order) == 0 {
		return ""
	}
	return h.order[len(h.order)-1]
}

func (h *host) hasTab(id string) bool {
	h.tabsMu.Lock()
	defer h.tabsMu.Unlock()
	return h.tabs[id] != nil
}

// openerOf is the open tab that opened target (a popup's opener), if any.
func (h *host) openerOf(target string) string {
	h.tabsMu.Lock()
	defer h.tabsMu.Unlock()
	if o := h.openers[target]; h.tabs[o] != nil {
		return o
	}
	return ""
}

// holdLocale gives a tab the person's locale (RB_LANG): this Chromium ships
// only en-US, so pages formatted dates and numbers US-style (10/13/2026,
// 1,234,567). An override lasts while the session that set it is attached,
// so this one stays attached until the tab closes.
// ponytail: cross-site iframes keep en-US; hold their sessions too if it matters.
func (h *host) holdLocale(target string) {
	loc := os.Getenv("RB_LANG")
	if loc == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := h.cdp.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	var a struct {
		SessionID string `json:"sessionId"`
	}
	if err != nil || json.Unmarshal(res, &a) != nil {
		return
	}
	if _, err := h.cdp.Call(ctx, a.SessionID, "Emulation.setLocaleOverride", map[string]any{"locale": loc}); err != nil {
		log.Printf("locale %s: %v", loc, err)
	}
}

// agentTab is the tab the agent last worked in, else the newest: a person
// opening the view wants to see what the agent is doing.
// ponytail: reads agent-browser's own state file; rbhost could track the target per command instead.
// waitTab reports whether tab id exists, waiting up to d for it to appear.
func (h *host) waitTab(id string, d time.Duration) bool {
	for end := time.Now().Add(d); !h.hasTab(id); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			return false
		}
	}
	return true
}

func (h *host) agentTab() string {
	home, _ := os.UserHomeDir()
	var st struct {
		TargetID string `json:"targetId"`
	}
	if b, err := os.ReadFile(filepath.Join(home, ".agent-browser", "poc.target")); err == nil && json.Unmarshal(b, &st) == nil {
		h.tabsMu.Lock()
		alive := h.tabs[st.TargetID] != nil
		h.tabsMu.Unlock()
		if alive {
			return st.TargetID
		}
	}
	return h.newestTab()
}

var uploadRoot string // set by Main

var uploadDir = regexp.MustCompile(`^[a-z0-9]{8,40}$`)

// uploadPath is where an uploaded file lives: name may be a folder-relative
// path (a picked folder keeps its tree), never one that leaves the folder.
func uploadPath(dir, name string) (string, bool) {
	if !uploadDir.MatchString(dir) || !filepath.IsLocal(name) {
		return "", false
	}
	return filepath.Join(uploadRoot, dir, name), true
}

// handleUpload stores one file the person picked or dropped. Files stream
// over HTTP, not inline on the viewer socket: no base64, no size cap (a 50 MB
// file used to overrun the socket's read limit and drop the connection).
// ponytail: uploads stay in -data until the sandbox goes; delete after setFileInputFiles if disk matters.
func (h *host) handleUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := uploadPath(r.URL.Query().Get("dir"), r.URL.Query().Get("name"))
	if !ok {
		http.Error(w, "bad upload name", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Create(p)
	if err == nil {
		_, err = io.Copy(f, r.Body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		_ = os.Remove(p)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *host) viewersOf(target string) []*viewer {
	var vs []*viewer
	h.eachViewer(func(v *viewer) {
		if v.target() == target {
			vs = append(vs, v)
		}
	})
	return vs
}

func (h *host) eachViewer(fn func(*viewer)) {
	h.viewMu.Lock()
	vs := make([]*viewer, 0, len(h.viewers))
	for v := range h.viewers {
		vs = append(vs, v)
	}
	h.viewMu.Unlock()
	for _, v := range vs {
		fn(v)
	}
}

func (h *host) broadcastTabs(newTab string) {
	tabs := h.tabList()
	h.eachViewer(func(v *viewer) { v.onTabs(tabs, newTab) })
	saveTabs(tabs)
}

// tabsFile lists the open tabs' URLs, so a restarted Chromium can reopen them.
var tabsFile string // set by Main

// stopping is set on shutdown: Chromium exiting is then expected, and the
// tabs it closes on the way out stay saved.
var stopping atomic.Bool

func saveTabs(tabs []tab) {
	if stopping.Load() {
		return
	}
	urls := []string{}
	for _, t := range tabs {
		if strings.HasPrefix(t.URL, "http:") || strings.HasPrefix(t.URL, "https:") || strings.HasPrefix(t.URL, "file:") {
			urls = append(urls, t.URL)
		}
	}
	b, _ := json.Marshal(urls)
	if os.WriteFile(tabsFile+".tmp", b, 0o600) == nil {
		_ = os.Rename(tabsFile+".tmp", tabsFile)
	}
}

func loadTabs() []string {
	var urls []string
	if b, err := os.ReadFile(tabsFile); err == nil {
		_ = json.Unmarshal(b, &urls)
	}
	return urls
}

// restoreTabs reopens the tabs saved before Chromium last went away, in
// place of the blank tab it starts with. It reports whether there were any.
func (h *host) restoreTabs(ctx context.Context, urls []string) bool {
	if len(urls) == 0 {
		return false
	}
	res, err := h.cdp.Call(ctx, "", "Target.getTargets", nil)
	var got struct {
		TargetInfos []struct{ TargetID, Type, URL string } `json:"targetInfos"`
	}
	if err != nil || json.Unmarshal(res, &got) != nil {
		return false
	}
	for _, u := range urls {
		_, _ = h.cdp.Call(ctx, "", "Target.createTarget", map[string]any{"url": u})
	}
	for _, t := range got.TargetInfos {
		if t.Type == "page" && t.URL == "about:blank" {
			_, _ = h.cdp.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.TargetID})
		}
	}
	log.Printf("reopened %d tabs", len(urls))
	return true
}

func (h *host) broadcastControl() {
	owner := h.ctl.owner()
	h.eachViewer(func(v *viewer) { v.sendControl(owner) })
}

// runAgent runs one agent-browser command against the shared Chromium.
func (h *host) runAgent(ctx context.Context, args []string) ([]byte, error) {
	h.agentMu.Lock()
	defer h.agentMu.Unlock()
	full := append([]string{"--cdp", fmt.Sprint(h.cdpPort), "--session", agentSession, "--no-auto-dialog"}, args...)
	// Its daemon starts in the first command's directory and saves files at
	// relative paths there.
	_ = os.MkdirAll(agentDir, 0o755)
	cmd := exec.CommandContext(ctx, h.abPath, full...)
	cmd.Dir = agentDir
	// Its own socket directory: the agent may run agent-browser itself, and
	// that daemon is none of this service's.
	cmd.Env = append(os.Environ(), "AGENT_BROWSER_SOCKET_DIR="+agentSocketDir)
	return cmd.CombinedOutput()
}

// agentSession is the agent-browser session this service drives.
const agentSession = "poc"

// stopStaleAgent stops the agent-browser daemon a previous run of this
// service left: it holds a dead connection and waits that out for 15 s on its
// next command. Only that daemon, by the PID it recorded.
func stopStaleAgent() {
	data, err := os.ReadFile(filepath.Join(agentSocketDir, agentSession+".pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return
	}
	// The PID may since have gone to another process.
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil || strings.TrimSpace(string(comm)) != "agent-browser" {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

// agentDir is agent-browser's working directory, and agentSocketDir its
// daemon's (set by Main). The agent's relative paths (an upload, a screenshot
// to save) are to its own directory: files travel between the two.
var agentDir, agentSocketDir string

// savesFile are the agent-browser commands that save a file at a path they are given.
var savesFile = []string{"screenshot", "pdf", "download", "record", "trace", "profiler", "network", "state"}

// here is where the agent's path p is on this machine.
func here(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(agentDir, p)
}

func (h *host) handleAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Args  []string          `json:"args"`
		Files map[string][]byte `json:"files"` // the caller's files named in Args (upload)
		// Dir is the caller's working directory when it shares this machine's
		// files (a cloud sandbox's agent): then no file travels either way.
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Args) == 0 {
		http.Error(w, `{"error":"args required"}`, http.StatusBadRequest)
		return
	}
	if req.Dir != "" && !filepath.IsAbs(req.Dir) {
		http.Error(w, `{"error":"dir must be absolute"}`, http.StatusBadRequest)
		return
	}
	// Refuse at once what would only block or misfire: a dialog open in the
	// agent's tab stops its every other command for up to a minute (one in
	// another tab is the person's), and refs from before the person used the
	// page may now point at something else (a different page's button).
	cmd := agentCommand(req.Args)
	if cmd != "dialog" {
		if d := h.openDialog(r.Context(), h.agentTab()); d != "" {
			agentRefused(w, "A dialog is open in the page ("+d+"). Answer it with `dialog accept` or `dialog dismiss`, or wait for the person to answer it.")
			return
		}
	}
	if usesRef(req.Args) && h.ctl.editSince(h.ctl.snapshotAt()) {
		agentRefused(w, "The person used the page after your last snapshot, so its element refs may point at something else now. Run `snapshot` again and use the new refs.")
		return
	}
	start := time.Now()
	// The agent may run on another machine: put its files where agent-browser
	// looks for them, at the same paths. One already there is this machine's
	// own file, not to be rewritten under whoever is using it.
	if req.Dir == "" {
		for p, b := range req.Files {
			if have, err := os.ReadFile(here(p)); err == nil && bytes.Equal(have, b) {
				continue
			}
			err := os.MkdirAll(filepath.Dir(here(p)), 0o755)
			if err == nil {
				err = os.WriteFile(here(p), b, 0o644)
			}
			if err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"output": "", "error": "copy " + p + ": " + err.Error(), "ms": 0, "files": map[string][]byte{}})
				return
			}
		}
	}
	h.ctl.mu.Lock()
	h.ctl.agentBusy++
	h.ctl.mu.Unlock()
	h.broadcastControl()
	args := slices.Clone(req.Args)
	for i, a := range args {
		// An upload names its files in full: Chromium, which reads them, runs
		// in another directory.
		if cmd == "upload" {
			if _, ok := req.Files[a]; ok && req.Dir == "" {
				args[i] = here(a)
			} else if req.Dir != "" && filepath.IsLocal(a) {
				if st, err := os.Stat(filepath.Join(req.Dir, a)); err == nil && st.Mode().IsRegular() {
					args[i] = filepath.Join(req.Dir, a)
				}
			}
		}
		// A file saved at a relative path may go in a folder the agent has and
		// this side lacks (screenshot shots/a.png): agent-browser makes none.
		if slices.Contains(savesFile, cmd) && filepath.IsLocal(a) {
			_ = os.MkdirAll(here(filepath.Dir(a)), 0o755)
		}
	}
	ran := time.Now()
	keepFocus := h.ctl.typing(3 * time.Second)
	if keepFocus {
		h.eachViewer(func(v *viewer) { _, _ = v.call("Runtime.evaluate", map[string]any{"expression": saveFocus}) })
	}
	out, err := h.runAgent(r.Context(), args)
	if err == nil && cmd == "snapshot" {
		h.ctl.mu.Lock()
		h.ctl.snapshot = ran
		h.ctl.mu.Unlock()
	}
	if keepFocus && !h.ctl.pressSince(ran) { // a click meanwhile put the caret where the person wants it
		h.eachViewer(func(v *viewer) { _, _ = v.call("Runtime.evaluate", map[string]any{"expression": restoreFocus}) })
	}
	// agent-browser can emulate its own viewport on the shared page; the
	// stream owns it, so each tab gets back the size of the view that set it.
	h.viewMu.Lock()
	for _, v := range h.sizer {
		go v.reassert()
	}
	h.viewMu.Unlock()
	h.ctl.mu.Lock()
	h.ctl.agentBusy--
	h.ctl.mu.Unlock()
	h.broadcastControl()
	files := map[string][]byte{}
	if req.Dir == "" {
		paths := slices.Clone(req.Args)
		for _, m := range absPath.FindAllStringSubmatch(string(out), -1) {
			paths = append(paths, m[1])
		}
		sent := map[string][]byte{}
		for p, b := range req.Files {
			sent[here(p)] = b
		}
		files = written(paths, ran, sent)
	} else {
		// Saved at a relative path, a file is in agent-browser's directory: it
		// goes to the agent's. One saved at a full path is already in place.
		local := slices.DeleteFunc(slices.Clone(req.Args), func(p string) bool { return !filepath.IsLocal(p) })
		for p, b := range written(local, ran, nil) {
			to := filepath.Join(req.Dir, p)
			saveErr := os.MkdirAll(filepath.Dir(to), 0o755)
			if saveErr == nil {
				saveErr = os.WriteFile(to, b, 0o644)
			}
			if saveErr != nil && err == nil {
				err = fmt.Errorf("save %s: %w", p, saveErr)
			}
		}
	}
	resp := map[string]any{"output": string(out), "ms": time.Since(start).Milliseconds(), "files": files}
	if err != nil {
		resp["error"] = err.Error()
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// absPath finds the absolute paths in agent-browser's output (a file saved
// where it chose): a "/" that starts a word, not the one in "./shot.png".
var absPath = regexp.MustCompile(`(?:^|[\s'"])(/[^\s'"]+)`)

// fileClockSlack covers file times, which Linux takes from a coarse clock up
// to a tick behind time.Now: a file saved just after start can carry a time
// just before it.
const fileClockSlack = time.Second

// written returns the files among the agent's paths that changed since start
// (a screenshot, pdf or download). A file the agent sent (sent, by where it
// is here) comes back only if the command wrote over it: a screenshot saved
// where an older one was.
func written(paths []string, start time.Time, sent map[string][]byte) map[string][]byte {
	files := map[string][]byte{}
	for _, p := range paths {
		st, err := os.Stat(here(p))
		if err != nil || !st.Mode().IsRegular() || st.ModTime().Before(start.Add(-fileClockSlack)) || st.Size() > 64<<20 {
			continue
		}
		if b, err := os.ReadFile(here(p)); err == nil {
			if up, ok := sent[here(p)]; !ok || !bytes.Equal(b, up) {
				files[p] = b
			}
		}
	}
	return files
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
