package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

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

func newRenderCommand(ctx *commandContext) *cobra.Command {
	var title string
	var height int
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
			"terminal session use `ao preview` instead.",
		Example: `  ao render "$TMPDIR/turns-by-day.html" --title "Turns by day" --height 420`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.publishRender(cmd.Context(), cmd.OutOrStdout(), args[0], title, height)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "short name for the page (required)")
	cmd.Flags().IntVar(&height, "height", 400, "first-paint frame height in CSS pixels, 80-2000; the frame then fits the page")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func (c *commandContext) publishRender(ctx context.Context, out io.Writer, file, title string, height int) error {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("ao render must run inside an AO chat session (AO_SESSION_ID is not set)")}
	}
	info, err := os.Stat(file)
	if err != nil {
		return usageError{fmt.Errorf("read %s: %w", file, err)}
	}
	if info.Size() > maxRenderFileBytes {
		return usageError{fmt.Errorf("%s is %d bytes; ao render accepts at most %d", file, info.Size(), maxRenderFileBytes)}
	}
	html, err := os.ReadFile(file)
	if err != nil {
		return usageError{fmt.Errorf("read %s: %w", file, err)}
	}
	var resp renderAPIResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/renders"
	if err := c.postJSON(ctx, path, renderAPIRequest{HTML: string(html), Title: title, Height: height}, &resp); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Shown above your reply (render %s). Do not describe the page; add only what it does not say.\n", resp.RenderID)
	return err
}
