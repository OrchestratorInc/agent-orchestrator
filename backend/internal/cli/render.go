package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/mcpserver"
)

func newRenderCommand(ctx *commandContext) *cobra.Command {
	var title string
	var height int
	var check bool
	var artifact bool
	var width int
	var out string
	cmd := &cobra.Command{
		Use:   "render <file.html>",
		Short: "Show a self-contained HTML page inline in this chat session's thread",
		Long: "Show a chart, table, diagram, or mockup inline in the current chat session's\n" +
			"thread, above your final reply. Run it before writing that reply, and let the\n" +
			"reply add to the page rather than introduce or repeat it.\n\n" +
			"The file is a single HTML page of at most 1 MiB with its CSS and JavaScript\n" +
			"inline. It may load https:// resources such as a charting library from a CDN;\n" +
			"relative URLs do not resolve. A picture from this computer, named by absolute\n" +
			"path in an img src, a CSS url(), or a JavaScript string (/tmp/plot.png), is\n" +
			"embedded in the page, up to 10 MiB each. Orchestrator keeps its own copy, so\n" +
			"write the file outside the repository.\n\n" +
			"Color the page with the theme variables on :root, which switch with light and\n" +
			"dark mode: --background --foreground --muted --muted-foreground --card\n" +
			"--card-foreground --popover --border --border-strong --primary\n" +
			"--primary-foreground --accent --accent-foreground --success --warning\n" +
			"--destructive --code --link --chart-1..--chart-6 --radius --font-sans --font-mono.\n" +
			"Let the page fill the width without outer padding, a card, or a border; give\n" +
			"charts a fixed pixel height; do not size html or body with 100vh. Chat sessions\n" +
			"only: in a terminal session use `ao preview` instead.\n\n" +
			"Run `ao render --check <file>` first: the Orchestrator desktop app returns a\n" +
			"screenshot, the page's content height, and its console messages.",
		Example: `  ao render --check "$TMPDIR/turns-by-day.html" --width 390
  ao render "$TMPDIR/turns-by-day.html" --title "Turns by day" --height 420`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return ctx.checkRender(cmd, args[0], width, out)
			}
			if strings.TrimSpace(title) == "" {
				return usageError{errors.New("--title is required unless --check is set")}
			}
			return ctx.publishRender(cmd, args[0], title, height, artifact)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "short name for the page (required unless --check)")
	cmd.Flags().IntVar(&height, "height", mcpserver.DefaultRenderHeight, "first-paint frame height in CSS pixels, 80-2000; the frame then fits the page")
	cmd.Flags().BoolVar(&check, "check", false, "screenshot the page in the Orchestrator desktop app instead of publishing it")
	cmd.Flags().BoolVar(&artifact, "artifact", false, "also keep the page as a session artifact; only when the user asks to keep it")
	cmd.Flags().IntVar(&width, "width", mcpserver.DefaultRenderWidth, "with --check: viewport width in CSS pixels, 240-1600; use 390 for phones")
	cmd.Flags().StringVar(&out, "out", "", "with --check: PNG path to write (default: a new file in the temp directory)")
	return cmd
}

func renderSessionID() (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{errors.New("ao render must run inside an Orchestrator chat session (AO_SESSION_ID is not set)")}
	}
	return sessionID, nil
}

func readRenderFile(file string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", usageError{fmt.Errorf("read %s: %w", file, err)}
	}
	if info.Size() > mcpserver.MaxRenderFileBytes {
		return "", usageError{fmt.Errorf("%s is %d bytes; ao render accepts at most %d", file, info.Size(), mcpserver.MaxRenderFileBytes)}
	}
	html, err := os.ReadFile(file)
	if err != nil {
		return "", usageError{fmt.Errorf("read %s: %w", file, err)}
	}
	return string(html), nil
}

func (c *commandContext) publishRender(cmd *cobra.Command, file, title string, height int, artifact bool) error {
	sessionID, err := renderSessionID()
	if err != nil {
		return err
	}
	html, err := readRenderFile(file)
	if err != nil {
		return err
	}
	resp, err := c.postRender(cmd.Context(), sessionID, html, title, height, artifact)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), mcpserver.ShownText(resp.RenderID)); err != nil {
		return err
	}
	line := mcpserver.ArtifactLine(resp)
	if line == "" {
		return nil
	}
	out := cmd.OutOrStdout()
	if resp.ArtifactError != "" {
		out = cmd.ErrOrStderr()
	}
	_, err = fmt.Fprintln(out, line)
	return err
}

// postRender inlines a page's local images and shows it in the session's
// thread. ao render and the html_render MCP tool both publish through it.
func (c *commandContext) postRender(ctx context.Context, sessionID, html, title string, height int, artifact bool) (mcpserver.RenderResponse, error) {
	var resp mcpserver.RenderResponse
	html, missing, err := mcpserver.InlineLocalImages(html)
	if err != nil {
		return resp, usageError{err}
	}
	if len(missing) > 0 {
		return resp, usageError{mcpserver.MissingImagesError(missing)}
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/renders"
	err = c.postJSON(ctx, path, mcpserver.RenderRequest{HTML: html, Title: title, Height: height, Artifact: artifact}, &resp)
	return resp, err
}

// postRenderCheck inlines a page's local images and has the desktop app
// screenshot it. A check shows what an unreadable image does to the page
// instead of refusing it, so missing lists those images.
func (c *commandContext) postRenderCheck(ctx context.Context, sessionID, html string, width int) (resp mcpserver.RenderCheckResponse, missing []string, err error) {
	html, missing, err = mcpserver.InlineLocalImages(html)
	if err != nil {
		return resp, nil, usageError{err}
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/renders/check"
	err = c.postJSON(ctx, path, mcpserver.RenderCheckRequest{HTML: html, Width: width}, &resp)
	return resp, missing, err
}

func (c *commandContext) checkRender(cmd *cobra.Command, file string, width int, out string) error {
	sessionID, err := renderSessionID()
	if err != nil {
		return err
	}
	html, err := readRenderFile(file)
	if err != nil {
		return err
	}
	resp, missing, err := c.postRenderCheck(cmd.Context(), sessionID, html, width)
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("ao-render-check-%d.png", time.Now().UnixNano()))
	}
	saved, err := writeRenderCheckScreenshot(resp.Screenshot.Data, out)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "Saved %s (%dx%d)\n", saved, resp.Screenshot.Width, resp.Screenshot.Height); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content height: %d px at width %d.\n", resp.ContentHeight, width); err != nil {
		return err
	}
	if resp.Network == "none" {
		if _, err := fmt.Fprintln(w, mcpserver.CheckOfflineNote); err != nil {
			return err
		}
	}
	// Image paths and console text come from the page and from any script it
	// loads, so they are marked as untrusted the way `ao browser console` marks them.
	lines := make([]string, 0, len(resp.ConsoleMessages)+1)
	if len(missing) > 0 {
		lines = append(lines, "missing images: "+strings.Join(missing, ", "))
	}
	for _, m := range resp.ConsoleMessages {
		lines = append(lines, fmt.Sprintf("console.%s: %s", m.Level, m.Text))
	}
	if len(lines) == 0 {
		return nil
	}
	_, err = fmt.Fprintln(w, browserUntrustedText(strings.Join(lines, "\n")))
	return err
}

// writeRenderCheckScreenshot writes the check's PNG to target and returns its
// absolute path. Unlike `ao browser screenshot`, it replaces an earlier file at
// target, so the check, fix, check-again loop can reuse one --out path. Only a
// regular file is replaced, never a symlink or a directory, and the new file
// lands with one rename.
func writeRenderCheckScreenshot(encoded, target string) (string, error) {
	if encoded == "" {
		return "", errors.New("render check returned an empty screenshot")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode render check screenshot: %w", err)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing to replace %s: it is not a regular file", abs)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".ao-render-check-*.png")
	if err != nil {
		return "", err
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), abs); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return abs, nil
}
