package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// paneCommandAPIRequest mirrors the daemon's body for
// POST /api/v1/sessions/{id}/panes. The CLI keeps its own copy so it need not
// import httpd.
type paneCommandAPIRequest struct {
	Action string  `json:"action"`
	Tab    *string `json:"tab,omitempty"`
	Beside *string `json:"beside,omitempty"`
	Side   *string `json:"side,omitempty"`
	Group  *string `json:"group,omitempty"`
}

type paneInputAPIRequest struct {
	Tab       string  `json:"tab"`
	Text      *string `json:"text,omitempty"`
	Enter     *bool   `json:"enter,omitempty"`
	Interrupt bool    `json:"interrupt,omitempty"`
}

type paneOutputDTO struct {
	SessionID string `json:"sessionId"`
	Tab       string `json:"tab"`
	Output    string `json:"output"`
}

type paneTabDTO struct {
	Key   string  `json:"key"`
	Kind  string  `json:"kind"`
	Title string  `json:"title"`
	Agent *string `json:"agent,omitempty"`
}

type paneBoxDTO struct {
	Key    string  `json:"key"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type paneGroupDTO struct {
	ID    string       `json:"id"`
	Focus string       `json:"focus"`
	Panes []paneBoxDTO `json:"panes"`
}

type paneLayoutDTO struct {
	Enabled  bool           `json:"enabled"`
	Selected string         `json:"selected"`
	Shown    *string        `json:"shown,omitempty"`
	Tabs     []paneTabDTO   `json:"tabs"`
	Groups   []paneGroupDTO `json:"groups"`
}

type paneLayoutResponseDTO struct {
	SessionID string        `json:"sessionId"`
	Layout    paneLayoutDTO `json:"layout"`
}

func newPaneCommand(ctx *commandContext) *cobra.Command {
	var sessionFlag string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "pane",
		Short: "Show or split the tabs of this session in the AO desktop window",
		Long: "Show or change how this session's tabs are split in the AO desktop window.\n\n" +
			"The window shows tabs side by side or stacked: the agent's own chat or\n" +
			"terminal (key \"session\"), shell terminals, reviewers and open files.\n" +
			"Run `ao pane` to list the tabs, their keys and the current splits.\n" +
			"Split panes are a Developer Mode preview; the window refuses changes\n" +
			"while they are off, and only the session on screen can be changed.",
		Example: `  ao pane
  ao pane split terminal
  ao pane split terminal --side bottom
  ao pane split file:src/app.ts --beside session --side right
  ao pane split terminal --run "npm test"
  ao pane run <terminal-key> "ls -la"
  ao pane read <terminal-key>
  ao pane interrupt <terminal-key>
  ao pane focus session
  ao pane remove <tab-key>
  ao pane ungroup`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.showPanes(cmd.Context(), cmd.OutOrStdout(), sessionFlag, asJSON)
		},
	}
	cmd.PersistentFlags().StringVar(&sessionFlag, "session", "", "session id (defaults to $AO_SESSION_ID)")
	cmd.PersistentFlags().BoolVar(&asJSON, "json", false, "print the layout as JSON")

	cmd.AddCommand(&cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List this session's tabs and splits",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.showPanes(cmd.Context(), cmd.OutOrStdout(), sessionFlag, asJSON)
		},
	})

	var beside, side, runAfterSplit string
	var runWait runWaitFlags
	split := &cobra.Command{
		Use:   "split <tab>",
		Short: "Show a tab beside another: a tab key, terminal (a new shell) or file:<path>",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := paneCommandAPIRequest{Action: "split", Tab: &args[0]}
			if beside != "" {
				req.Beside = &beside
			}
			if side != "" {
				switch side {
				case "left", "right", "top", "bottom":
				default:
					return usageError{fmt.Errorf("--side must be left, right, top or bottom")}
				}
				req.Side = &side
			}
			if runAfterSplit == "" {
				return ctx.paneCommand(cmd.Context(), cmd.OutOrStdout(), sessionFlag, req, asJSON)
			}
			if args[0] == "session" || strings.HasPrefix(args[0], "file:") || strings.HasPrefix(args[0], "reviewer") {
				return usageError{fmt.Errorf("--run needs a terminal: split terminal, or an existing terminal tab")}
			}
			layout, err := ctx.paneCommandLayout(cmd.Context(), sessionFlag, req)
			if err != nil {
				return err
			}
			// The split focuses the tab it placed, so the selection is the terminal.
			return ctx.runInPane(cmd.Context(), cmd.OutOrStdout(), sessionFlag, layout.Layout.Selected, runAfterSplit, runWait)
		},
	}
	split.Flags().StringVar(&beside, "beside", "", "key of the tab to split beside (defaults to the tab on screen)")
	split.Flags().StringVar(&side, "side", "", "left, right, top or bottom (defaults to right)")
	split.Flags().StringVar(&runAfterSplit, "run", "", "a command to run in the split terminal, then print its output")
	runWait.register(split)
	cmd.AddCommand(split)

	var runFlags runWaitFlags
	run := &cobra.Command{
		Use:   "run <terminal-tab> <command>",
		Short: "Run a command in one of this session's terminal tabs and print what it shows",
		Long: "Types the command into the terminal tab and presses Enter, then waits until\n" +
			"the screen stops changing (or --timeout passes) and prints its last lines.\n" +
			"A long-running command keeps running; read it later with `ao pane read`.",
		Args: usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runInPane(cmd.Context(), cmd.OutOrStdout(), sessionFlag, args[0], strings.Join(args[1:], " "), runFlags)
		},
	}
	runFlags.register(run)
	cmd.AddCommand(run)

	var readLines int
	read := &cobra.Command{
		Use:   "read <terminal-tab>",
		Short: "Print what one of this session's terminal tabs shows",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := ctx.readPane(cmd.Context(), sessionFlag, args[0], readLines)
			if err != nil {
				return err
			}
			return writePaneOutput(cmd.OutOrStdout(), out, asJSON)
		},
	}
	read.Flags().IntVar(&readLines, "lines", 200, "how many of the last lines to print (at most 2000)")
	cmd.AddCommand(read)

	var noEnter bool
	send := &cobra.Command{
		Use:   "send <terminal-tab> <text>",
		Short: "Type text into a terminal tab, pressing Enter unless --no-enter",
		Args:  usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args[1:], " ")
			enter := !noEnter
			return ctx.sendPaneInput(cmd.Context(), sessionFlag, paneInputAPIRequest{Tab: args[0], Text: &text, Enter: &enter})
		},
	}
	send.Flags().BoolVar(&noEnter, "no-enter", false, "type the text without pressing Enter (answers to prompts, TUI keys)")
	cmd.AddCommand(send)

	cmd.AddCommand(&cobra.Command{
		Use:   "interrupt <terminal-tab>",
		Short: "Send Ctrl-C to a terminal tab",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.sendPaneInput(cmd.Context(), sessionFlag, paneInputAPIRequest{Tab: args[0], Interrupt: true})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "remove <tab>",
		Short: "Take a tab out of its split; the rest of the split stays on screen",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.paneCommand(cmd.Context(), cmd.OutOrStdout(), sessionFlag, paneCommandAPIRequest{Action: "remove", Tab: &args[0]}, asJSON)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "ungroup [group-id]",
		Short: "Turn a split back into separate tabs (defaults to the split on screen)",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := paneCommandAPIRequest{Action: "ungroup"}
			if len(args) == 1 {
				req.Group = &args[0]
			}
			return ctx.paneCommand(cmd.Context(), cmd.OutOrStdout(), sessionFlag, req, asJSON)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "focus <tab>",
		Short: "Bring a tab on screen (with its split, if it is in one)",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.paneCommand(cmd.Context(), cmd.OutOrStdout(), sessionFlag, paneCommandAPIRequest{Action: "focus", Tab: &args[0]}, asJSON)
		},
	})
	return cmd
}

func sessionPanesPath(sessionFlag string) (string, error) {
	sessionID := strings.TrimSpace(sessionFlag)
	if sessionID == "" {
		sessionID = strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	}
	if sessionID == "" {
		return "", usageError{errors.New("ao pane needs a session: run it inside an AO session or pass --session")}
	}
	return "sessions/" + url.PathEscape(sessionID) + "/panes", nil
}

func (c *commandContext) showPanes(ctx context.Context, w io.Writer, sessionFlag string, asJSON bool) error {
	path, err := sessionPanesPath(sessionFlag)
	if err != nil {
		return err
	}
	var out paneLayoutResponseDTO
	if err := c.getJSON(ctx, path, &out); err != nil {
		return err
	}
	return writePaneLayout(w, out, asJSON)
}

func (c *commandContext) paneCommand(ctx context.Context, w io.Writer, sessionFlag string, req paneCommandAPIRequest, asJSON bool) error {
	out, err := c.paneCommandLayout(ctx, sessionFlag, req)
	if err != nil {
		return err
	}
	return writePaneLayout(w, out, asJSON)
}

func (c *commandContext) paneCommandLayout(ctx context.Context, sessionFlag string, req paneCommandAPIRequest) (paneLayoutResponseDTO, error) {
	path, err := sessionPanesPath(sessionFlag)
	if err != nil {
		return paneLayoutResponseDTO{}, err
	}
	var out paneLayoutResponseDTO
	err = c.postJSON(ctx, path, req, &out)
	return out, err
}

func (c *commandContext) sendPaneInput(ctx context.Context, sessionFlag string, req paneInputAPIRequest) error {
	path, err := sessionPanesPath(sessionFlag)
	if err != nil {
		return err
	}
	return c.postJSON(ctx, path+"/input", req, nil)
}

func (c *commandContext) readPane(ctx context.Context, sessionFlag, tab string, lines int) (paneOutputDTO, error) {
	path, err := sessionPanesPath(sessionFlag)
	if err != nil {
		return paneOutputDTO{}, err
	}
	query := url.Values{"tab": {tab}}
	if lines > 0 {
		query.Set("lines", fmt.Sprint(lines))
	}
	var out paneOutputDTO
	err = c.getJSON(ctx, path+"/output?"+query.Encode(), &out)
	return out, err
}

// runWaitFlags controls how long `run` waits for a command's output.
type runWaitFlags struct {
	timeout time.Duration
	lines   int
	noWait  bool
}

func (f *runWaitFlags) register(cmd *cobra.Command) {
	cmd.Flags().DurationVar(&f.timeout, "timeout", 15*time.Second, "longest to wait for the screen to settle")
	cmd.Flags().IntVar(&f.lines, "lines", 60, "how many of the last lines to print")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "return right after typing the command")
}

// paneSettleInterval is how long the screen must stay unchanged before `run`
// treats the command's output as settled.
var paneSettleInterval = 700 * time.Millisecond

// runInPane types a command into a terminal tab, waits for its screen to
// settle and prints its last lines.
func (c *commandContext) runInPane(ctx context.Context, w io.Writer, sessionFlag, tab, command string, wait runWaitFlags) error {
	if strings.TrimSpace(command) == "" {
		return usageError{errors.New("nothing to run")}
	}
	enter := true
	if err := c.sendPaneInput(ctx, sessionFlag, paneInputAPIRequest{Tab: tab, Text: &command, Enter: &enter}); err != nil {
		return err
	}
	if wait.noWait {
		fmt.Fprintf(w, "Ran in %s: %s\n", tab, command)
		return nil
	}
	deadline := time.Now().Add(wait.timeout)
	previous, settledSince := "", time.Time{}
	var out paneOutputDTO
	for {
		timer := time.NewTimer(paneSettleInterval / 2)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		next, err := c.readPane(ctx, sessionFlag, tab, wait.lines)
		if err != nil {
			return err
		}
		out = next
		now := time.Now()
		if out.Output != previous || settledSince.IsZero() {
			previous, settledSince = out.Output, now
		} else if now.Sub(settledSince) >= paneSettleInterval {
			break
		}
		if now.After(deadline) {
			fmt.Fprintf(w, "(still changing after %s; it may still be running. Check again with `ao pane read %s`.)\n", wait.timeout, tab)
			break
		}
	}
	return writePaneOutput(w, out, false)
}

func writePaneOutput(w io.Writer, out paneOutputDTO, asJSON bool) error {
	if asJSON {
		return writeJSON(w, out)
	}
	_, err := fmt.Fprintln(w, out.Output)
	return err
}

func writePaneLayout(w io.Writer, out paneLayoutResponseDTO, asJSON bool) error {
	if asJSON {
		return writeJSON(w, out)
	}
	layout := out.Layout
	state := "on"
	if !layout.Enabled {
		state = "off (turn on Split panes under Developer Mode in Settings → General)"
	}
	fmt.Fprintf(w, "Split panes: %s\n", state)
	fmt.Fprintf(w, "On screen: %s\n", layout.Selected)
	// Keys line up in one column; a terminal's key is long.
	width := 0
	for _, tab := range layout.Tabs {
		width = max(width, len(tab.Key))
	}
	fmt.Fprintln(w, "Tabs:")
	for _, tab := range layout.Tabs {
		title := tab.Title
		if tab.Agent != nil && *tab.Agent != "" {
			title += " (" + *tab.Agent + ")"
		}
		fmt.Fprintf(w, "  %-*s  %-8s  %s\n", width, tab.Key, tab.Kind, title)
	}
	if len(layout.Groups) == 0 {
		fmt.Fprintln(w, "Splits: none")
		return nil
	}
	fmt.Fprintln(w, "Splits:")
	for _, group := range layout.Groups {
		marker := ""
		if layout.Shown != nil && *layout.Shown == group.ID {
			marker = " (on screen)"
		}
		fmt.Fprintf(w, "  %s%s\n", group.ID, marker)
		for _, pane := range group.Panes {
			focus := ""
			if pane.Key == group.Focus {
				focus = "  focused"
			}
			fmt.Fprintf(w, "    %-*s  %s%s\n", width, pane.Key, paneRegion(pane), focus)
		}
	}
	return nil
}

// paneRegion describes where a pane sits, e.g. "x 50%, y 0%, 50% wide, 100% tall".
func paneRegion(p paneBoxDTO) string {
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
	return fmt.Sprintf("x %s, y %s, %s wide, %s tall", pct(p.X), pct(p.Y), pct(p.Width), pct(p.Height))
}
