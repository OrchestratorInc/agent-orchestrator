package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// This is the stdio MCP server AO passes to every chat session (ao-mcp, or
// `ao mcp` without it), so the agent sees html_preview and html_render in its
// tool list, and the computer use tools when the session has computer use
// (cua.go). The agent's
// harness spawns it outside the shell sandbox, so it reaches the loopback
// daemon even when the agent's own shell commands cannot. Hand-written, and
// only what these tools need: newline-delimited JSON-RPC 2.0, one request at
// a time, nothing on stdout but replies.

const (
	mcpDefaultProtocolVersion = "2025-06-18"
	// maxMCPHTMLChars caps a tool's html argument.
	maxMCPHTMLChars = 512_000
)

var mcpProtocolVersions = []string{mcpDefaultProtocolVersion, "2025-03-26", "2024-11-05"}

// Version is the ao-mcp build's version, set at link time.
var Version = "dev"

// Daemon is the daemon API the tools call. Paths are relative to /api/v1/.
type Daemon interface {
	GetJSON(ctx context.Context, path string, out any) error
	PostJSON(ctx context.Context, path string, body, out any) error
	DoJSON(ctx context.Context, method, path string, body, out any) error
}

type server struct {
	daemon  Daemon
	version string
}

// Serve answers MCP messages from in on out until in ends.
func Serve(ctx context.Context, in io.Reader, out io.Writer, daemon Daemon, version string) error {
	return (&server{daemon: daemon, version: version}).serveMCP(ctx, in, out)
}

type mcpRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

func (s *server) serveMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)
	state := &mcpState{}
	var write, waitsMu sync.Mutex
	var waits sync.WaitGroup
	cancelWait := map[string]context.CancelFunc{}
	defer waits.Wait()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	reply := func(resp *mcpResponse) error {
		if resp == nil {
			return nil
		}
		write.Lock()
		defer write.Unlock()
		return encoder.Encode(resp)
	}
	for {
		line, readErr := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			switch id, kind := classifyMCPLine(line); kind {
			case mcpLineCancel:
				waitsMu.Lock()
				if cancel := cancelWait[id]; cancel != nil {
					cancel()
				}
				waitsMu.Unlock()
			case mcpLineWait:
				waitCtx, cancel := context.WithCancel(ctx)
				waitsMu.Lock()
				cancelWait[id] = cancel
				waitsMu.Unlock()
				waits.Go(func() {
					defer cancel()
					_ = reply(s.handleMCP(waitCtx, state, line))
					waitsMu.Lock()
					delete(cancelWait, id)
					waitsMu.Unlock()
				})
			default:
				if err := reply(s.handleMCP(ctx, state, line)); err != nil {
					return err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

type mcpLineKind int

const (
	mcpLineOther mcpLineKind = iota
	mcpLineWait
	mcpLineCancel
)

func classifyMCPLine(line []byte) (string, mcpLineKind) {
	var peek struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			RequestID json.RawMessage `json:"requestId"`
		} `json:"params"`
	}
	if json.Unmarshal(line, &peek) != nil {
		return "", mcpLineOther
	}
	switch {
	case peek.Method == "notifications/cancelled":
		return string(peek.Params.RequestID), mcpLineCancel
	case peek.Method == "tools/call" && peek.Params.Name == "session_wait" && peek.ID != nil:
		return string(peek.ID), mcpLineWait
	}
	return "", mcpLineOther
}

// handleMCP answers one message; a notification gets no reply.
func (s *server) handleMCP(ctx context.Context, state *mcpState, line []byte) *mcpResponse {
	var req mcpRequest
	if err := json.Unmarshal(line, &req); err != nil {
		if !json.Valid(line) {
			return &mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "parse error: " + err.Error()}}
		}
		// Valid JSON of the wrong shape. Unmarshal still fills a readable id.
		id := req.ID
		if id == nil {
			id = json.RawMessage("null")
		}
		return &mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: -32600, Message: "invalid request: " + err.Error()}}
	}
	if req.ID == nil {
		return nil
	}
	resp := &mcpResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpDefaultProtocolVersion
		if slices.Contains(mcpProtocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ao", "version": s.version},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := append(append(append([]map[string]any{}, mcpTools...), catalogDefinitions()...), s.cuaTools(ctx, state)...)
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch p.Name {
		case "html_preview":
			resp.Result = s.callHTMLPreview(ctx, p.Arguments)
		case "html_render":
			resp.Result = s.callHTMLRender(ctx, p.Arguments)
		default:
			if tool, ok := findCatalogTool(p.Name); ok {
				resp.Result = s.callCatalog(ctx, tool, p.Arguments)
				break
			}
			if s.cuaTools(ctx, state) != nil && state.cuaNames[p.Name] {
				resp.Result = s.callCua(ctx, p.Name, p.Arguments)
				break
			}
			resp.Error = &mcpError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", p.Name)}
		}
	default:
		resp.Error = &mcpError{Code: -32601, Message: fmt.Sprintf("method %q not found", req.Method)}
	}
	return resp
}

type mcpToolArgs struct {
	HTML     string `json:"html"`
	Title    string `json:"title"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Artifact bool   `json:"artifact"`
}

// readMCPToolArgs decodes a call's arguments and returns the session it acts in.
func readMCPToolArgs(raw json.RawMessage) (mcpToolArgs, string, error) {
	var args mcpToolArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return args, "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if utf8.RuneCountInString(args.HTML) > maxMCPHTMLChars || len(args.HTML) > MaxRenderFileBytes {
		return args, "", fmt.Errorf("html is too long; the limit is %d characters and %d bytes", maxMCPHTMLChars, MaxRenderFileBytes)
	}
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return args, "", errors.New("ao mcp must run inside an Orchestrator chat session (AO_SESSION_ID is not set)")
	}
	return args, sessionID, nil
}

// postRender inlines a page's local images and shows it in the session's
// thread.
func (s *server) postRender(ctx context.Context, sessionID, html, title string, height int, artifact bool) (RenderResponse, error) {
	var resp RenderResponse
	html, missing, err := InlineLocalImages(html)
	if err != nil {
		return resp, err
	}
	if len(missing) > 0 {
		return resp, MissingImagesError(missing)
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/renders"
	err = s.daemon.PostJSON(ctx, path, RenderRequest{HTML: html, Title: title, Height: height, Artifact: artifact}, &resp)
	return resp, err
}

// postRenderCheck inlines a page's local images and has the desktop app
// screenshot it; missing lists the images it could not read.
func (s *server) postRenderCheck(ctx context.Context, sessionID, html string, width int) (resp RenderCheckResponse, missing []string, err error) {
	html, missing, err = InlineLocalImages(html)
	if err != nil {
		return resp, nil, err
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/renders/check"
	err = s.daemon.PostJSON(ctx, path, RenderCheckRequest{HTML: html, Width: width}, &resp)
	return resp, missing, err
}

// Every failure is a tool result the agent can read and act on, never a
// protocol error.
func mcpToolError(err error) map[string]any {
	return map[string]any{"content": []any{mcpText(err.Error())}, "isError": true}
}

func mcpText(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func (s *server) callHTMLPreview(ctx context.Context, raw json.RawMessage) map[string]any {
	args, sessionID, err := readMCPToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if args.Width == 0 {
		args.Width = DefaultRenderWidth
	}
	resp, missing, err := s.postRenderCheck(ctx, sessionID, args.HTML, args.Width)
	if err != nil {
		return mcpToolError(err)
	}
	note := ""
	if resp.Network == "none" {
		note = CheckOfflineNote
	}
	report, err := json.Marshal(struct {
		Width           int      `json:"width"`
		ContentHeight   int      `json:"contentHeight"`
		ConsoleMessages any      `json:"consoleMessages"`
		MissingImages   []string `json:"missingImages,omitempty"`
		Note            string   `json:"note,omitempty"`
	}{args.Width, resp.ContentHeight, resp.ConsoleMessages, missing, note})
	if err != nil {
		return mcpToolError(err)
	}
	return map[string]any{"content": []any{
		map[string]any{"type": "image", "mimeType": "image/png", "data": resp.Screenshot.Data},
		mcpText(string(report)),
	}}
}

func (s *server) callHTMLRender(ctx context.Context, raw json.RawMessage) map[string]any {
	args, sessionID, err := readMCPToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if args.Height == 0 {
		args.Height = DefaultRenderHeight
	}
	resp, err := s.postRender(ctx, sessionID, args.HTML, args.Title, args.Height, args.Artifact)
	if err != nil {
		return mcpToolError(err)
	}
	text := ShownText(resp.RenderID)
	if line := ArtifactLine(resp); line != "" {
		text += "\n" + line
	}
	return map[string]any{"content": []any{mcpText(text)}}
}

// The tool text states the page rules in skillassets/using-ao/commands/render.md.
const (
	mcpPageRules = `The page must be a single HTML file that carries its own CSS and JavaScript inline. ` +
		`It may load https:// resources such as a charting library from a CDN, but relative URLs do not resolve. ` +
		`To include a picture from this computer, give its absolute path in an img src, a CSS url(), or a JavaScript string, ` +
		`for example /tmp/plot.png; Orchestrator embeds PNG, JPEG, GIF, WebP, AVIF, SVG, BMP, and ICO files of up to 10 MiB each.`
	mcpLayoutGuide = `Layout: the page sits in the reply column with no frame border, over the same background as the thread, starting at the left edge of your text. ` +
		`Treat it as part of your answer: let it fill the width, and leave out outer padding, a wrapping card or border, and a heading banner. ` +
		`When the reader expands it, it opens in a dialog of up to 1024 px wide sized to its height, and Orchestrator centers a top-level block that sets a max-width. ` +
		`Charts need a fixed height in pixels. The frame takes the page's height, so html and body must not be sized from the viewport (100vh, height: 100%), which would make the frame keep growing. ` +
		`Scripts are sandboxed away from Orchestrator, cookies, and storage, and links open in the user's browser.`
	mcpThemeGuide = `Theme: these CSS variables on :root hold Orchestrator's current theme and change when the user switches between light and dark: ` +
		`--background (the thread's own color), --foreground, --muted, --muted-foreground, --card, --card-foreground, --popover, ` +
		`--border, --border-strong, --primary, --primary-foreground, --accent, --accent-foreground, --success, --warning, ` +
		`--destructive, --code, --link, --chart-1 to --chart-6 for data series, --radius, --font-sans, and --font-mono. ` +
		`A base stylesheet already applies the background, text color, and font, removes the body margin, and hides the scrollbar. ` +
		`Color the page with these variables instead of fixed values so it works in both themes.`
)

var mcpHTMLSchema = map[string]any{
	"type": "string", "minLength": 1, "maxLength": maxMCPHTMLChars,
	"description": "The page's full HTML source.",
}

var mcpTools = []map[string]any{
	{
		"name":  "html_preview",
		"title": "Preview HTML",
		"description": "Opens the page in the Orchestrator desktop app exactly as readers will get it and returns a PNG screenshot, " +
			"the page's contentHeight at that width in CSS pixels, and everything it wrote to the console. " +
			"Use it before html_render: look at the screenshot and clear every console error. " +
			"It only works while the desktop app is running; if it is not, skip the check and publish. " +
			mcpPageRules + " Theme variables and layout are the ones html_render describes.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"html": mcpHTMLSchema,
				"width": map[string]any{
					"type": "integer", "minimum": 240, "maximum": 1600,
					"description": "Width of the viewport in CSS pixels, from 240 to 1600. The default, 720, is the reply column; 390 shows a phone.",
				},
			},
			"required": []string{"html"},
		},
		"annotations": map[string]any{
			"title": "Preview HTML", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
	},
	{
		"name":  "html_render",
		"title": "Render HTML",
		"description": "Puts a finished HTML page, such as a chart, table, diagram, image collage, or mockup, into this chat thread " +
			"directly above your final reply. Check it with html_preview, then call this before writing the reply. " +
			"Your reply sits under a page the reader can already see: do not point to it, introduce it, or repeat it; " +
			"say only what it leaves out. Orchestrator shows pages from this tool only, not from built-in visualization skills. " +
			mcpPageRules + " " + mcpLayoutGuide + " " + mcpThemeGuide,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"html": mcpHTMLSchema,
				"title": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200, "description": "Title for the page, a few words.",
				},
				"height": map[string]any{
					"type": "integer", "minimum": 80, "maximum": 2000,
					"description": "Frame height in CSS pixels, 80-2000; defaults to 400. Orchestrator measures the page when " +
						"you publish it, so this is used only when the desktop app is not running. The frame then fits the page.",
				},
				"artifact": map[string]any{
					"type": "boolean",
					"description": "Also keep the page as a session artifact, a deliverable the user keeps. " +
						"Set it only when the user asks to keep the page, then run ao artifact add on the path this " +
						"returns, or the page may not be listed as an artifact.",
				},
			},
			"required": []string{"html", "title"},
		},
		"annotations": map[string]any{
			// Not read-only: with artifact it writes a file to the artifact directory.
			"title": "Render HTML", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true,
		},
	},
}
