package chat

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/renderpage"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

const (
	// The CLI inlines an agent's local images, so a page is far larger than
	// the 1 MiB HTML file the agent writes.
	maxRenderHTMLBytes  = 25 << 20
	maxRenderTitleRunes = 200
	minRenderHeight     = 80
	maxRenderHeight     = 2000
	// defaultRenderHeight is the frame height for a page published without
	// one, as ao render and html_render default to.
	defaultRenderHeight = 400

	defaultRenderCheckWidth = 720
	minRenderCheckWidth     = 240
	maxRenderCheckWidth     = 1600
)

// ErrRenderInvalid reports a page the agent must fix before publishing. The
// wrapped message says what to change.
var ErrRenderInvalid = errors.New("invalid render")

// ErrRenderNotFound reports a render id that names no stored page.
var ErrRenderNotFound = errors.New("render not found")

var (
	// renderMeasureWidths are the reader widths a published page is measured
	// at, T3 Code's set; each frame opens at the height for its own width.
	renderMeasureWidths = []int{320, 375, 430, 520, 640, 728, 860, 1000, 1144}
	// renderMeasureTimeout is how long publishing waits for heights before it
	// publishes without them. A variable so tests can shorten it.
	renderMeasureTimeout = 6 * time.Second
)

// RenderFiles stores render pages; *attachmentstore.Store satisfies it.
type RenderFiles interface {
	PutRender(ctx context.Context, id domain.SessionID, renderID string, data []byte) error
	OpenRender(ctx context.Context, id domain.SessionID, renderID string) (*os.File, fs.FileInfo, error)
	RemoveRender(ctx context.Context, id domain.SessionID, renderID string) error
}

// RenderInput is a self-contained HTML page an agent shows in its thread.
// BaseURL is the daemon origin the desktop app can load to measure the page,
// e.g. http://127.0.0.1:3001. Artifact also keeps the page as a session artifact.
type RenderInput struct {
	HTML     string
	Title    string
	Height   int
	BaseURL  string
	Artifact bool
}

// RenderResult names the stored page and the timeline row that shows it. With
// RenderInput.Artifact, ArtifactPath is the kept page's absolute path, or
// ArtifactError says why it was not kept.
type RenderResult struct {
	RenderID      string
	ActivityID    string
	Path          string
	ArtifactPath  string
	ArtifactError string
}

// PublishRender stores an agent's HTML page and shows it in the turn the agent
// is running, above its final reply.
func (s *Service) PublishRender(ctx context.Context, id domain.SessionID, in RenderInput) (RenderResult, error) {
	title := strings.TrimSpace(in.Title)
	if runes := []rune(title); len(runes) > maxRenderTitleRunes {
		title = string(runes[:maxRenderTitleRunes])
	}
	switch {
	case strings.TrimSpace(in.HTML) == "":
		return RenderResult{}, fmt.Errorf("%w: the page is empty", ErrRenderInvalid)
	case len(in.HTML) > maxRenderHTMLBytes:
		return RenderResult{}, fmt.Errorf("%w: the page is %d bytes; the limit is %d", ErrRenderInvalid, len(in.HTML), maxRenderHTMLBytes)
	case title == "":
		return RenderResult{}, fmt.Errorf("%w: a title is required", ErrRenderInvalid)
	case s.renders == nil:
		return RenderResult{}, errors.New("render storage is not configured")
	}
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return RenderResult{}, err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return RenderResult{}, err
	}
	renderID := renderIDFor(controller.renderNetwork(), s.newID())
	if err := s.renders.PutRender(ctx, id, renderID, []byte(in.HTML)); err != nil {
		return RenderResult{}, fmt.Errorf("store render: %w", err)
	}
	path := "/api/v1/sessions/" + url.PathEscape(string(id)) + "/renders/" + url.PathEscape(renderID)
	height := cmp.Or(in.Height, defaultRenderHeight)
	height = min(max(height, minRenderHeight), maxRenderHeight)
	activityID, turn, err := controller.recordRender(ctx, renderID, title, height, nil, path)
	if err != nil {
		// Only the timeline row lets anything find the page, so an unrecorded page goes.
		if removeErr := s.renders.RemoveRender(context.WithoutCancel(ctx), id, renderID); removeErr != nil {
			s.log.Warn("render cleanup failed", "session", id, "render", renderID, "error", removeErr)
		}
		return RenderResult{}, err
	}
	result := RenderResult{RenderID: renderID, ActivityID: activityID, Path: path}
	s.measureRenderLater(ctx, controller, id, turn, activityID, renderID, title, height, path, strings.TrimRight(in.BaseURL, "/")+path)
	// The page is already in the thread, so a failed save does not fail the publish.
	if in.Artifact {
		if artifact, err := s.SaveRenderAsArtifact(ctx, id, renderID, title); err != nil {
			result.ArtifactError = err.Error()
		} else {
			// Absolute, so the agent can pass it to ao report --artifact.
			result.ArtifactPath = filepath.Join(artifact.Dir, artifact.Path)
			// A report of the file in this turn would show the page twice.
			controller.markArtifactShown(turn, artifact.Path)
		}
	}
	return result, nil
}

// RenderArtifact is a render kept as a session artifact. Path is relative to
// Dir, the session's artifact directory.
type RenderArtifact struct {
	Path string
	Name string
	Dir  string
}

const (
	// maxRenderArtifactNames bounds the "Name (n).html" names a save tries.
	maxRenderArtifactNames = 100
	// maxRenderArtifactStemBytes keeps "Name (100).html" under the 255-byte
	// NAME_MAX of common file systems; 120 CJK runes alone are 360 bytes.
	maxRenderArtifactStemBytes = 200
)

// windowsDeviceName matches the names Windows reserves for devices. Windows
// ends the device name at the first dot, so "con.v2" is CON too.
var windowsDeviceName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)

// renderArtifactStem is the render's file name without ".html", cut to fit
// NAME_MAX on a rune boundary and never a Windows device name.
func renderArtifactStem(title string) string {
	stem := strings.TrimSuffix(renderpage.FileName(title), ".html")
	if len(stem) > maxRenderArtifactStemBytes {
		cut := maxRenderArtifactStemBytes
		for !utf8.RuneStart(stem[cut]) {
			cut--
		}
		stem = strings.TrimSpace(stem[:cut])
	}
	if windowsDeviceName.MatchString(stem) {
		stem = "_" + stem
	}
	return stem
}

// SaveRenderAsArtifact keeps a published render as a session artifact, a
// deliverable the user keeps. The file is the page as the render route serves
// it, bootstrap included, so it renders on its own. A file of that name with
// the same bytes is returned as it is; otherwise the next free "Name (n).html"
// is written, and the session's output type is updated at once.
func (s *Service) SaveRenderAsArtifact(ctx context.Context, id domain.SessionID, renderID, title string) (RenderArtifact, error) {
	title = strings.TrimSpace(title)
	if n := len([]rune(title)); n == 0 || n > maxRenderTitleRunes {
		return RenderArtifact{}, fmt.Errorf("%w: the title must be 1-%d characters", ErrRenderInvalid, maxRenderTitleRunes)
	}
	if s.renders == nil {
		return RenderArtifact{}, errors.New("render storage is not configured")
	}
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return RenderArtifact{}, err
	}
	file, _, err := s.renders.OpenRender(ctx, id, renderID)
	if errors.Is(err, fs.ErrNotExist) {
		return RenderArtifact{}, ErrRenderNotFound
	}
	if err != nil {
		return RenderArtifact{}, fmt.Errorf("open render: %w", err)
	}
	stored, err := io.ReadAll(io.LimitReader(file, maxRenderHTMLBytes))
	_ = file.Close()
	if err != nil {
		return RenderArtifact{}, fmt.Errorf("read render: %w", err)
	}
	dir := record.Metadata.ArtifactDir
	if dir == "" {
		dir = sessionartifacts.Dir(s.dataDir, id)
	}
	if dir == "" {
		return RenderArtifact{}, errors.New("the session has no artifact directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return RenderArtifact{}, fmt.Errorf("create artifact directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return RenderArtifact{}, fmt.Errorf("open artifact directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	doc := renderpage.Document(stored)
	stem := renderArtifactStem(title)
	for n := 1; n <= maxRenderArtifactNames; n++ {
		name := stem + ".html"
		if n > 1 {
			name = fmt.Sprintf("%s (%d).html", stem, n)
		}
		artifact := RenderArtifact{Path: name, Name: name, Dir: dir}
		if sameRenderArtifact(root, name, doc) {
			return artifact, nil
		}
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return RenderArtifact{}, fmt.Errorf("save render artifact: %w", err)
		}
		_, err = f.Write(doc)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = root.Remove(name)
			return RenderArtifact{}, fmt.Errorf("save render artifact: %w", err)
		}
		if s.reconcileOutput != nil {
			if err := s.reconcileOutput(ctx, id); err != nil {
				s.log.Warn("render artifact saved; output type not updated", "session", id, "file", name, "error", err)
			}
		}
		return artifact, nil
	}
	return RenderArtifact{}, fmt.Errorf("save render artifact: %s and the next %d names are taken", stem+".html", maxRenderArtifactNames-1)
}

// sameRenderArtifact reports whether name in root is a regular file holding
// doc. It opens without blocking and checks the open file, so a FIFO or device
// swapped in cannot hang or flood the read.
func sameRenderArtifact(root *os.Root, name string, doc []byte) bool {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(doc)) {
		return false
	}
	existing, err := io.ReadAll(io.LimitReader(f, info.Size()+1))
	return err == nil && bytes.Equal(existing, doc)
}

// measureRender asks the desktop app for the page's height at each reader
// width, sorted by width. Measuring is best effort: without the desktop app,
// past the timeout, or on any error, the page is published without heights and
// opens at the agent's height.
// measureRenderLater measures a recorded page in the background and settles
// the heights onto its row. Measuring loads the page in the desktop app, which
// takes up to renderMeasureTimeout (and a reconnect wait with no app at all), so
// the agent does not wait for it; a reader's frame fits the page's own reported
// height until the heights arrive.
func (s *Service) measureRenderLater(ctx context.Context, controller *Controller, id domain.SessionID, turn, activityID, renderID, title string, height int, path, pageURL string) {
	if s.renderMeasure == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	s.renderMeasures.Add(1)
	go func() {
		defer s.renderMeasures.Done()
		heights := s.measureRender(ctx, id, pageURL, renderIDNetwork(renderID))
		if len(heights) == 0 {
			return
		}
		if err := controller.upsertRender(ctx, turn, activityID, renderID, title, height, heights, path); err != nil {
			s.log.Debug("render heights not recorded", "session", id, "render", renderID, "error", err)
		}
	}()
}

func (s *Service) measureRender(ctx context.Context, id domain.SessionID, pageURL, network string) [][2]int {
	if s.renderMeasure == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, renderMeasureTimeout)
	defer cancel()
	value, err := s.renderMeasure(ctx, id, map[string]any{"url": pageURL, "widths": renderMeasureWidths, "network": network})
	var heights [][2]int
	if err == nil {
		heights, err = readRenderHeights(value)
	}
	if err != nil {
		s.log.Debug("render measure failed; publishing without heights", "session", id, "url", pageURL, "error", err)
		return nil
	}
	return heights
}

// readRenderHeights checks the desktop app's measure result: one height of
// 1-2000 px for each measured width.
func readRenderHeights(value any) ([][2]int, error) {
	// The broker hands back decoded JSON, so numbers arrive as float64; a round
	// trip through the typed result turns them into ints.
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode render measure result: %w", err)
	}
	var result struct {
		Heights [][2]int `json:"heights"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("unreadable render measure result: %w", err)
	}
	heights := result.Heights
	if len(heights) != len(renderMeasureWidths) {
		return nil, fmt.Errorf("render measure returned %d heights for %d widths", len(heights), len(renderMeasureWidths))
	}
	slices.SortFunc(heights, func(a, b [2]int) int { return cmp.Compare(a[0], b[0]) })
	for i, pair := range heights {
		if pair[0] != renderMeasureWidths[i] || pair[1] < 1 || pair[1] > maxRenderHeight {
			return nil, fmt.Errorf("render measure returned %v for the widths %v", heights, renderMeasureWidths)
		}
	}
	return heights, nil
}

// recordRender attaches a published page to the turn in flight, as a system
// activity identified by its "render" discriminator, the way a steer is. It
// returns the row's id and the provider turn it landed on.
func (c *Controller) recordRender(ctx context.Context, renderID, title string, height int, heights [][2]int, path string) (string, string, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	providerTurnID, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return "", "", ErrNoActiveTurn
	}
	activityID := c.newID()
	if err := c.upsertRender(ctx, providerTurnID, activityID, renderID, title, height, heights, path); err != nil {
		return "", "", fmt.Errorf("record render on turn %s: %w", providerTurnID, err)
	}
	return activityID, providerTurnID, nil
}

// upsertRender writes a render row. A second write with the same render id
// settles the existing row by its provider item id, so measured heights added
// later keep the row's place in the thread, even after the turn has ended.
func (c *Controller) upsertRender(ctx context.Context, providerTurnID, activityID, renderID, title string, height int, heights [][2]int, path string) error {
	render := map[string]any{"id": renderID, "title": title, "height": height, "path": path}
	if len(heights) > 0 {
		render["heights"] = heights
	}
	detail, err := json.Marshal(map[string]any{"event": "render", "render": render})
	if err != nil {
		return fmt.Errorf("encode render detail: %w", err)
	}
	return c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID, domain.ConversationActivity{
		ID:             activityID,
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        title,
		Detail:         detail,
		ProviderItemID: "render:" + renderID,
	}, c.now())
}

// ErrRenderCheckUnavailable reports that no desktop app is connected to load the page.
var ErrRenderCheckUnavailable = errors.New("render check needs the AO desktop app")

// RenderCheck asks the desktop app to load a render URL in a hidden view. The
// daemon wires it to the browser-runtime broker.
type RenderCheck func(ctx context.Context, id domain.SessionID, args map[string]any) (any, error)

// RenderMeasure asks the desktop app for a published page's height at each
// width. The daemon wires it to the browser-runtime broker.
type RenderMeasure func(ctx context.Context, id domain.SessionID, args map[string]any) (any, error)

// RenderCheckInput is a page an agent wants to see before it publishes it.
// BaseURL is the daemon origin the desktop app can load, e.g. http://127.0.0.1:3001.
type RenderCheckInput struct {
	HTML    string
	Width   int
	BaseURL string
}

// RenderConsoleMessage is one console line the page wrote while it loaded.
type RenderConsoleMessage struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// RenderCheckResult is what the page looked like at the requested width.
type RenderCheckResult struct {
	PNG    string `json:"data"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// ImageWidth and ImageHeight are the PNG's pixel size, which the desktop app
	// scales down when the full-size image would be too large to hand back.
	ImageWidth      int                    `json:"imageWidth,omitempty"`
	ImageHeight     int                    `json:"imageHeight,omitempty"`
	ContentHeight   int                    `json:"contentHeight"`
	ConsoleMessages []RenderConsoleMessage `json:"consoleMessages"`
	// Network is the network the page could use: RenderNetworkPublic, or
	// RenderNetworkNone when the agent's own sandbox has none.
	Network string `json:"network"`
}

// The network a page loaded by a render check or measure may use. It is never
// more than the agent's own sandbox allows, so the html tools are not a way
// around it.
const (
	RenderNetworkPublic = "public"
	RenderNetworkNone   = "none"
)

// offlineRenderPrefix starts the id of a page made by an agent whose sandbox
// has no network. The id never changes, so the page keeps no network for as
// long as it is shown: publishing a page is not a way around the sandbox.
const offlineRenderPrefix = "offline-"

// renderIDFor names a new page made under network.
func renderIDFor(network, id string) string {
	if network == RenderNetworkNone {
		return offlineRenderPrefix + id
	}
	return id
}

// renderIDNetwork is the network the page renderID may use.
func renderIDNetwork(renderID string) string {
	if RenderOffline(renderID) {
		return RenderNetworkNone
	}
	return RenderNetworkPublic
}

// RenderOffline reports whether the page renderID must be served with no network.
func RenderOffline(renderID string) bool {
	return strings.HasPrefix(renderID, offlineRenderPrefix)
}

// renderNetwork is the network this agent's pages may use: the network of the
// sandbox its turn in flight runs under. The mode picked for the next turn
// does not apply until that turn is sent.
func (c *Controller) renderNetwork() string {
	sandbox, ok := c.conv.(ports.ChatSandboxNetwork)
	if !ok {
		return RenderNetworkPublic
	}
	c.mu.Lock()
	mode, dispatched := c.dispatchedApproval, c.hasDispatchedApproval
	c.mu.Unlock()
	if !dispatched {
		mode = c.Settings().ApprovalMode
	}
	if !sandbox.SandboxAllowsNetwork(mode) {
		return RenderNetworkNone
	}
	return RenderNetworkPublic
}

// SetRenderCheck installs the desktop-app page loader after daemon wiring.
func (s *Service) SetRenderCheck(check RenderCheck) {
	s.renderCheck = check
}

// SetRenderMeasure installs the desktop-app page measurer after daemon wiring.
func (s *Service) SetRenderMeasure(measure RenderMeasure) {
	s.renderMeasure = measure
}

// CheckRender shows the agent its page as readers will see it: the page is
// stored, and the desktop app loads it in a hidden view through the render
// route, which adds the bootstrap as it does for readers.
func (s *Service) CheckRender(ctx context.Context, id domain.SessionID, in RenderCheckInput) (RenderCheckResult, error) {
	width := in.Width
	if width == 0 {
		width = defaultRenderCheckWidth
	}
	switch {
	case strings.TrimSpace(in.HTML) == "":
		return RenderCheckResult{}, fmt.Errorf("%w: the page is empty", ErrRenderInvalid)
	case len(in.HTML) > maxRenderHTMLBytes:
		return RenderCheckResult{}, fmt.Errorf("%w: the page is %d bytes; the limit is %d", ErrRenderInvalid, len(in.HTML), maxRenderHTMLBytes)
	case width < minRenderCheckWidth || width > maxRenderCheckWidth:
		return RenderCheckResult{}, fmt.Errorf("%w: width must be %d-%d", ErrRenderInvalid, minRenderCheckWidth, maxRenderCheckWidth)
	case s.renders == nil || s.renderCheck == nil:
		return RenderCheckResult{}, ErrRenderCheckUnavailable
	}
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return RenderCheckResult{}, err
	}
	// No live controller means no way to tell what the agent's sandbox allows.
	network := RenderNetworkNone
	if controller, err := s.Controller(id); err == nil {
		network = controller.renderNetwork()
	}
	renderID := renderIDFor(network, "check-"+s.newID())
	if err := s.renders.PutRender(ctx, id, renderID, []byte(in.HTML)); err != nil {
		return RenderCheckResult{}, fmt.Errorf("store render check: %w", err)
	}
	defer func() {
		if err := s.renders.RemoveRender(context.WithoutCancel(ctx), id, renderID); err != nil {
			s.log.Warn("render check cleanup failed", "session", id, "render", renderID, "error", err)
		}
	}()
	pageURL := strings.TrimRight(in.BaseURL, "/") +
		"/api/v1/sessions/" + url.PathEscape(string(id)) + "/renders/" + url.PathEscape(renderID)
	value, err := s.renderCheck(ctx, id, map[string]any{"url": pageURL, "width": width, "network": network})
	if err != nil {
		return RenderCheckResult{}, err
	}
	// The broker hands back decoded JSON, so numbers arrive as float64; a round
	// trip through the typed result turns them into ints.
	encoded, err := json.Marshal(value)
	if err != nil {
		return RenderCheckResult{}, fmt.Errorf("encode render check result: %w", err)
	}
	var result RenderCheckResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return RenderCheckResult{}, fmt.Errorf("desktop app returned an unreadable render check: %w", err)
	}
	if result.PNG == "" {
		return RenderCheckResult{}, errors.New("desktop app returned a render check with no screenshot")
	}
	result.Network = network
	return result, nil
}
