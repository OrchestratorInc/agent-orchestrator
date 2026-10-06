package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const maxRenderFileBytes = 1 << 20

type renderAPIRequest struct {
	HTML   string `json:"html"`
	Title  string `json:"title"`
	Height int    `json:"height"`
}

type renderAPIResponse struct {
	RenderID   string `json:"renderId"`
	ActivityID string `json:"activityId"`
	Path       string `json:"path"`
}

type renderCheckAPIRequest struct {
	HTML  string `json:"html"`
	Width int    `json:"width"`
}

type renderCheckAPIResponse struct {
	Screenshot struct {
		Data   string `json:"data"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"screenshot"`
	ContentHeight   int `json:"contentHeight"`
	ConsoleMessages []struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	} `json:"consoleMessages"`
}

func newRenderCommand(ctx *commandContext) *cobra.Command {
	var title string
	var height int
	var check bool
	var width int
	var out string
	cmd := &cobra.Command{
		Use:   "render <file.html>",
		Short: "Show a self-contained HTML page inline in this chat session's thread",
		Long: "Show a chart, table, diagram, or mockup inline in the current chat session's\n" +
			"thread, above your final reply. Call it before that reply, and do not announce\n" +
			"or restate the page in it.\n\n" +
			"The file must be one self-contained HTML document (inline <style> and <script>,\n" +
			"at most 1 MiB). Remote https:// resources such as a CDN chart library load\n" +
			"as-is; local paths and relative URLs do not resolve, so embed images as data:\n" +
			"URIs. AO stores its own copy, so write the file outside the repository.\n\n" +
			"Style with the theme variables AO injects on :root, which follow light/dark\n" +
			"live: --background --foreground --muted --muted-foreground --card\n" +
			"--card-foreground --popover --border --border-strong --primary\n" +
			"--primary-foreground --accent --accent-foreground --success --warning\n" +
			"--destructive --code --link --chart-1..--chart-6 --radius --font-sans --font-mono.\n" +
			"Use a fluid width with no outer padding, card, or border; give charts fixed\n" +
			"pixel heights; never size html/body with 100vh. Chat sessions only: in a\n" +
			"terminal session use `ao preview` instead.\n\n" +
			"Run `ao render --check <file>` first: the AO desktop app returns a\n" +
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
			return ctx.publishRender(cmd.Context(), cmd.OutOrStdout(), args[0], title, height)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "short name for the page (required unless --check)")
	cmd.Flags().IntVar(&height, "height", 400, "first-paint frame height in CSS pixels, 80-2000; the frame then fits the page")
	cmd.Flags().BoolVar(&check, "check", false, "screenshot the page in the AO desktop app instead of publishing it")
	cmd.Flags().IntVar(&width, "width", 720, "with --check: viewport width in CSS pixels, 240-1600; use 390 for phones")
	cmd.Flags().StringVar(&out, "out", "", "with --check: PNG path to write (default: a new file in the temp directory)")
	return cmd
}

func renderSessionID() (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{errors.New("ao render must run inside an AO chat session (AO_SESSION_ID is not set)")}
	}
	return sessionID, nil
}

func readRenderFile(file string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", usageError{fmt.Errorf("read %s: %w", file, err)}
	}
	if info.Size() > maxRenderFileBytes {
		return "", usageError{fmt.Errorf("%s is %d bytes; ao render accepts at most %d", file, info.Size(), maxRenderFileBytes)}
	}
	html, err := os.ReadFile(file)
	if err != nil {
		return "", usageError{fmt.Errorf("read %s: %w", file, err)}
	}
	return string(html), nil
}

func (c *commandContext) publishRender(ctx context.Context, out io.Writer, file, title string, height int) error {
	sessionID, err := renderSessionID()
	if err != nil {
		return err
	}
	html, err := readRenderFile(file)
	if err != nil {
		return err
	}
	var resp renderAPIResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/renders"
	if err := c.postJSON(ctx, path, renderAPIRequest{HTML: html, Title: title, Height: height}, &resp); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Shown above your reply (render %s). Do not describe the page; add only what it does not say.\n", resp.RenderID)
	return err
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
	var resp renderCheckAPIResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/renders/check"
	if err := c.postJSON(cmd.Context(), path, renderCheckAPIRequest{HTML: html, Width: width}, &resp); err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("ao-render-check-%d.png", time.Now().UnixNano()))
	}
	shot := map[string]any{"data": resp.Screenshot.Data, "width": resp.Screenshot.Width, "height": resp.Screenshot.Height}
	if err := writeBrowserScreenshot(cmd, shot, out, false, false); err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "Content height: %d px at width %d.\n", resp.ContentHeight, width); err != nil {
		return err
	}
	if len(resp.ConsoleMessages) == 0 {
		return nil
	}
	// Console text comes from the page and from any script it loads, so it is
	// marked as untrusted the way `ao browser console` marks it.
	lines := make([]string, 0, len(resp.ConsoleMessages))
	for _, m := range resp.ConsoleMessages {
		lines = append(lines, fmt.Sprintf("console.%s: %s", m.Level, m.Text))
	}
	_, err = fmt.Fprintln(w, browserUntrustedText(strings.Join(lines, "\n")))
	return err
}
