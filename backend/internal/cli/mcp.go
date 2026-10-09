package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// `ao mcp` is the stdio MCP server AO passes to every chat session, so the
// agent sees html_preview and html_render in its tool list. The agent's
// harness spawns it outside the shell sandbox, so it reaches the loopback
// daemon even when the agent's own shell commands cannot. Hand-written, and
// only what these two tools need: newline-delimited JSON-RPC 2.0, one request
// at a time, nothing on stdout but replies.

const (
	mcpDefaultProtocolVersion = "2025-06-18"
	// maxMCPHTMLChars is T3's cap on a tool's html argument.
	maxMCPHTMLChars = 512_000
)

var mcpProtocolVersions = []string{mcpDefaultProtocolVersion, "2025-03-26", "2024-11-05"}

func newMCPCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Serve AO's chat tools over stdio MCP (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.serveMCP(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
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

func (c *commandContext) serveMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)
	for {
		line, readErr := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if resp := c.handleMCP(ctx, line); resp != nil {
				if err := encoder.Encode(resp); err != nil {
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

// handleMCP answers one message; a notification gets no reply.
func (c *commandContext) handleMCP(ctx context.Context, line []byte) *mcpResponse {
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
			"serverInfo":      map[string]any{"name": "ao", "version": Version},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch p.Name {
		case "html_preview":
			resp.Result = c.callHTMLPreview(ctx, p.Arguments)
		case "html_render":
			resp.Result = c.callHTMLRender(ctx, p.Arguments)
		default:
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
	if utf8.RuneCountInString(args.HTML) > maxMCPHTMLChars || len(args.HTML) > maxRenderFileBytes {
		return args, "", fmt.Errorf("html is too long; the limit is %d characters and %d bytes", maxMCPHTMLChars, maxRenderFileBytes)
	}
	sessionID, err := renderSessionID()
	return args, sessionID, err
}

// Every failure is a tool result the agent can read and act on, never a
// protocol error.
func mcpToolError(err error) map[string]any {
	return map[string]any{"content": []any{mcpText(err.Error())}, "isError": true}
}

func mcpText(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func (c *commandContext) callHTMLPreview(ctx context.Context, raw json.RawMessage) map[string]any {
	args, sessionID, err := readMCPToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if args.Width == 0 {
		args.Width = defaultRenderWidth
	}
	resp, missing, err := c.postRenderCheck(ctx, sessionID, args.HTML, args.Width)
	if err != nil {
		return mcpToolError(err)
	}
	note := ""
	if resp.Network == "none" {
		note = renderCheckOfflineNote
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

func (c *commandContext) callHTMLRender(ctx context.Context, raw json.RawMessage) map[string]any {
	args, sessionID, err := readMCPToolArgs(raw)
	if err != nil {
		return mcpToolError(err)
	}
	if args.Height == 0 {
		args.Height = defaultRenderHeight
	}
	resp, err := c.postRender(ctx, sessionID, args.HTML, args.Title, args.Height, args.Artifact)
	if err != nil {
		return mcpToolError(err)
	}
	text := renderShownText(resp.RenderID)
	if line := renderArtifactLine(resp); line != "" {
		text += "\n" + line
	}
	return map[string]any{"content": []any{mcpText(text)}}
}

// The tool text follows T3's html tools, with AO's own rules from
// skillassets/using-ao/commands/render.md.
const (
	mcpPageRules = `Write one self-contained HTML document with inline <style> and <script>. ` +
		`Remote https:// resources, such as a CDN chart library, load as they are; relative URLs do not resolve. ` +
		`A local image written as an absolute path (src="/abs/shot.png", CSS url(/abs/bg.webp), or a JS string) ` +
		`is put into the page: PNG, JPEG, GIF, WebP, AVIF, SVG, BMP, or ICO, up to 10 MiB each.`
	mcpLayoutGuide = `The frame is borderless on the thread background, as wide as the reply column, and its left edge lines up with your text. ` +
		`Use a fluid width with no outer padding, card, border, or banner title: the page is part of your reply. ` +
		`An expanded page opens in a dialog up to 1024 px wide that fits its height, and AO centers a top-level block that has a maximum width. ` +
		`Give charts fixed pixel heights. Do not size html or body with 100vh or height: 100%; the frame grows to fit the page, and viewport heights make it grow again. ` +
		`Scripts run in a sandbox with no access to AO, cookies, or storage. Links open in the user's browser.`
	mcpThemeGuide = `AO injects its active theme as CSS custom properties on :root, and they follow light/dark mode live: ` +
		`--background (identical to the thread), --foreground, --muted, --muted-foreground, --card, --card-foreground, --popover, ` +
		`--border, --border-strong, --primary, --primary-foreground, --accent, --accent-foreground, --success, --warning, ` +
		`--destructive, --code, --link, --chart-1 ... --chart-6 (categorical series), --radius, --font-sans, --font-mono. ` +
		`The base stylesheet sets the page background, text color, and font from these, sets body margin to 0, and hides the page scrollbar. ` +
		`Use the variables, not hard-coded colors, so the page reads correctly in both themes.`
)

var mcpHTMLSchema = map[string]any{
	"type": "string", "minLength": 1, "maxLength": maxMCPHTMLChars,
	"description": "A complete, self-contained HTML document.",
}

var mcpTools = []map[string]any{
	{
		"name":  "html_preview",
		"title": "Preview HTML",
		"description": "Load an HTML page in the AO desktop app the way readers see it. Returns a PNG screenshot, " +
			"contentHeight (the height the page needs at this width), and the page's console messages. " +
			"Call it before html_render. Read the screenshot, and fix every console error. " +
			"The check needs the AO desktop app; without it, publish without a check. " +
			mcpPageRules + " The page gets the theme variables and layout described in html_render.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"html": mcpHTMLSchema,
				"width": map[string]any{
					"type": "integer", "minimum": 240, "maximum": 1600,
					"description": "Viewport width in CSS pixels, 240-1600. Defaults to 720, the reply column; use 390 to check phones.",
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
		"description": "Show a finished HTML page (chart, table, diagram, image collage, mockup) inline in this chat thread, " +
			"above your final reply. Preview it with html_preview first, then call this before you write the reply. " +
			"The reader already sees the page, so the reply must not announce it, say where it is, or restate it: " +
			"add only what the page does not say. Use this tool, not a built-in visualize skill: AO does not show those. " +
			mcpPageRules + " " + mcpLayoutGuide + " " + mcpThemeGuide,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"html": mcpHTMLSchema,
				"title": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200, "description": "Short name for the page.",
				},
				"height": map[string]any{
					"type": "integer", "minimum": 80, "maximum": 2000,
					"description": "Frame height in CSS pixels, 80-2000; defaults to 400. AO measures the page when " +
						"you publish it, so this is used only when the desktop app is not running. The frame then fits the page.",
				},
				"artifact": map[string]any{
					"type": "boolean",
					"description": "Also keep the page as a session artifact, a deliverable the user keeps. " +
						"Set it only when the user asks to keep the page.",
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
