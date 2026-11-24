package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This is the stdio MCP server AO passes to every chat session (ao-mcp, or
// `ao mcp` without it), so the agent sees html_preview and html_render in its
// tool list, and the computer use tools when the session has computer use
// (cua.go). The agent's harness spawns it outside the shell sandbox, so it
// reaches the loopback daemon even when the agent's own shell commands cannot.
// The protocol (framing, version negotiation, lifecycle, errors) is the
// official MCP Go SDK's; this package only defines the tools.

const (
	// maxMCPHTMLChars caps a tool's html argument.
	maxMCPHTMLChars = 512_000
)

// Version is the ao-mcp build's version, set at link time.
var Version = "dev"

// Daemon is the daemon API the tools call. Paths are relative to /api/v1/.
type Daemon interface {
	GetJSON(ctx context.Context, path string, out any) error
	PostJSON(ctx context.Context, path string, body, out any) error
}

type server struct {
	daemon Daemon
	mcp    *mcp.Server

	// cuaMu guards the computer-use tools, which are added to mcp the first
	// time the daemon can list them.
	cuaMu     sync.Mutex
	cuaLoaded bool
}

// Serve answers MCP messages from in on out until in ends.
func Serve(ctx context.Context, in io.Reader, out io.Writer, daemon Daemon, version string) error {
	s := newServer(daemon, version)
	transport := &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}}
	return s.mcp.Run(ctx, transport)
}

func newServer(daemon Daemon, version string) *server {
	s := &server{daemon: daemon}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "ao", Version: version}, &mcp.ServerOptions{
		// Tools only; the SDK's default would also advertise logging.
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	s.mcp.AddTool(htmlPreviewTool, s.callHTMLPreview)
	s.mcp.AddTool(htmlRenderTool, s.callHTMLRender)
	// Computer use tools come from the daemon, so they are listed lazily: each
	// tools/list or tools/call tries again until the daemon can list them.
	s.mcp.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" || method == "tools/call" {
				s.loadCuaTools(ctx)
			}
			return next(ctx, method, req)
		}
	})
	return s
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type mcpToolArgs struct {
	HTML     string `json:"html"`
	Title    string `json:"title"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Artifact bool   `json:"artifact"`
}

// readMCPToolArgs decodes a call's arguments and returns the session it acts in.
func readMCPToolArgs(req *mcp.CallToolRequest) (mcpToolArgs, string, error) {
	raw := req.Params.Arguments
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
func mcpToolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{mcpText(err.Error())}, IsError: true}
}

func mcpText(text string) *mcp.TextContent {
	return &mcp.TextContent{Text: text}
}

func (s *server) callHTMLPreview(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, sessionID, err := readMCPToolArgs(req)
	if err != nil {
		return mcpToolError(err), nil
	}
	if args.Width == 0 {
		args.Width = DefaultRenderWidth
	}
	resp, missing, err := s.postRenderCheck(ctx, sessionID, args.HTML, args.Width)
	if err != nil {
		return mcpToolError(err), nil
	}
	screenshot, err := base64.StdEncoding.DecodeString(resp.Screenshot.Data)
	if err != nil {
		return mcpToolError(fmt.Errorf("decode screenshot: %w", err)), nil
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
		return mcpToolError(err), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ImageContent{MIMEType: "image/png", Data: screenshot},
		mcpText(string(report)),
	}}, nil
}

func (s *server) callHTMLRender(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, sessionID, err := readMCPToolArgs(req)
	if err != nil {
		return mcpToolError(err), nil
	}
	if args.Height == 0 {
		args.Height = DefaultRenderHeight
	}
	resp, err := s.postRender(ctx, sessionID, args.HTML, args.Title, args.Height, args.Artifact)
	if err != nil {
		return mcpToolError(err), nil
	}
	text := ShownText(resp.RenderID)
	if line := ArtifactLine(resp); line != "" {
		text += "\n" + line
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcpText(text)}}, nil
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

func boolPtr(v bool) *bool { return &v }

var htmlPreviewTool = &mcp.Tool{
	Name:  "html_preview",
	Title: "Preview HTML",
	Description: "Opens the page in the Orchestrator desktop app exactly as readers will get it and returns a PNG screenshot, " +
		"the page's contentHeight at that width in CSS pixels, and everything it wrote to the console. " +
		"Use it before html_render: look at the screenshot and clear every console error. " +
		"It only works while the desktop app is running; if it is not, skip the check and publish. " +
		mcpPageRules + " Theme variables and layout are the ones html_render describes.",
	InputSchema: map[string]any{
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
	Annotations: &mcp.ToolAnnotations{
		Title: "Preview HTML", ReadOnlyHint: true, DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(true),
	},
}

var htmlRenderTool = &mcp.Tool{
	Name:  "html_render",
	Title: "Render HTML",
	Description: "Puts a finished HTML page, such as a chart, table, diagram, image collage, or mockup, into this chat thread " +
		"directly above your final reply. Check it with html_preview, then call this before writing the reply. " +
		"Your reply sits under a page the reader can already see: do not point to it, introduce it, or repeat it; " +
		"say only what it leaves out. Orchestrator shows pages from this tool only, not from built-in visualization skills. " +
		mcpPageRules + " " + mcpLayoutGuide + " " + mcpThemeGuide,
	InputSchema: map[string]any{
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
	// Not read-only: with artifact it writes a file to the artifact directory.
	Annotations: &mcp.ToolAnnotations{
		Title: "Render HTML", ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false, OpenWorldHint: boolPtr(true),
	},
}
