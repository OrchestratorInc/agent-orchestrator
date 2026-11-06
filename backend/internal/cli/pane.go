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

	var beside, side string
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
			return ctx.paneCommand(cmd.Context(), cmd.OutOrStdout(), sessionFlag, req, asJSON)
		},
	}
	split.Flags().StringVar(&beside, "beside", "", "key of the tab to split beside (defaults to the tab on screen)")
	split.Flags().StringVar(&side, "side", "", "left, right, top or bottom (defaults to right)")
	cmd.AddCommand(split)

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
	path, err := sessionPanesPath(sessionFlag)
	if err != nil {
		return err
	}
	var out paneLayoutResponseDTO
	if err := c.postJSON(ctx, path, req, &out); err != nil {
		return err
	}
	return writePaneLayout(w, out, asJSON)
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
	fmt.Fprintln(w, "Tabs:")
	for _, tab := range layout.Tabs {
		title := tab.Title
		if tab.Agent != nil && *tab.Agent != "" {
			title += " (" + *tab.Agent + ")"
		}
		fmt.Fprintf(w, "  %-28s %-9s %s\n", tab.Key, tab.Kind, title)
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
			fmt.Fprintf(w, "    %-28s %s%s\n", pane.Key, paneRegion(pane), focus)
		}
	}
	return nil
}

// paneRegion describes where a pane sits, e.g. "x 50%, y 0%, 50% wide, 100% tall".
func paneRegion(p paneBoxDTO) string {
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
	return fmt.Sprintf("x %s, y %s, %s wide, %s tall", pct(p.X), pct(p.Y), pct(p.Width), pct(p.Height))
}
