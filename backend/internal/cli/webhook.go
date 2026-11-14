package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

type webhookStatus struct {
	Sent        int        `json:"sent"`
	Queued      int        `json:"queued"`
	Folded      int        `json:"folded"`
	LastSentAt  *time.Time `json:"lastSentAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	LastErrorAt *time.Time `json:"lastErrorAt,omitempty"`
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

type webhookView struct {
	Name        string        `json:"name"`
	Host        string        `json:"host"`
	HeaderNames []string      `json:"headerNames"`
	Projects    []string      `json:"projects"`
	Events      []string      `json:"events"`
	QuietAtDesk bool          `json:"quietAtDesk"`
	CreatedAt   time.Time     `json:"createdAt"`
	Status      webhookStatus `json:"status"`
}

type webhookListResponse struct {
	Webhooks []webhookView `json:"webhooks"`
	Kinds    []string      `json:"kinds"`
}

type webhookPutRequest struct {
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	Projects    []string          `json:"projects,omitempty"`
	Events      []string          `json:"events,omitempty"`
	QuietAtDesk bool              `json:"quietAtDesk,omitempty"`
}

type webhookPatchRequest struct {
	Projects    *[]string `json:"projects,omitempty"`
	Events      *[]string `json:"events,omitempty"`
	QuietAtDesk *bool     `json:"quietAtDesk,omitempty"`
}

type webhookTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func newWebhookCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Send questions, finished turns, exits and PR news to webhooks such as a chat bot",
		Long: "Orchestrator POSTs one JSON event per question, finished orchestrator turn, agent exit, PR and review change " +
			"to each webhook that wants it. Event kinds: needs_input (and input_resolved), finished, worker_finished, failed, exited, " +
			"ready_to_merge, pr_merged, pr_closed, review_completed, review_changes_requested. A webhook with no --events " +
			"gets all of them except worker_finished. Bursts of finished turns arrive as one summary event.",
	}
	cmd.AddCommand(newWebhookAddCommand(ctx), newWebhookListCommand(ctx), newWebhookSetCommand(ctx),
		newWebhookRemoveCommand(ctx), newWebhookTestCommand(ctx))
	return cmd
}

func newWebhookAddCommand(ctx *commandContext) *cobra.Command {
	var projects []string
	var events string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Add or replace a webhook; reads its URL, then its Authorization line, from stdin",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebhookProjects(ctx, cmd, projects); err != nil {
				return err
			}
			hookURL, auth, err := readWebhookSecrets(cmd.InOrStdin(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			body := webhookPutRequest{URL: hookURL, Projects: projects, Events: splitEvents(events), QuietAtDesk: quiet}
			if auth != "" {
				body.Headers = map[string]string{"Authorization": auth}
			}
			var view webhookView
			if err := ctx.doJSON(cmd.Context(), http.MethodPut, "webhooks/"+url.PathEscape(args[0]), body, &view); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "saved webhook %s (%s). Run `ao webhook test %s` to check it.\n", view.Name, view.Host, view.Name)
			return err
		},
	}
	cmd.Flags().StringArrayVar(&projects, "project", nil, "Only events from this project id (repeatable)")
	cmd.Flags().StringVar(&events, "events", "", "Comma-separated event kinds (default: all but worker_finished)")
	cmd.Flags().BoolVar(&quiet, "quiet-at-desk", false, "Skip events while the Orchestrator app is in front and in use (macOS)")
	return cmd
}

func newWebhookListCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List webhooks and their delivery state",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out webhookListResponse
			if err := ctx.doJSON(cmd.Context(), http.MethodGet, "webhooks", nil, &out); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			if len(out.Webhooks) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No webhooks. Add one with `ao webhook add NAME`.")
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "NAME\tHOST\tPROJECTS\tEVENTS\tSENT\tSTATE"); err != nil {
				return err
			}
			for _, h := range out.Webhooks {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", h.Name, h.Host, orAll(h.Projects), orDefault(h.Events, h.QuietAtDesk), h.Status.Sent, webhookState(h.Status)); err != nil {
					return err
				}
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newWebhookSetCommand(ctx *commandContext) *cobra.Command {
	var projects []string
	var events string
	var allProjects, allEvents, quiet bool
	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Change which projects and events a webhook gets; keeps its URL and key",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var body webhookPatchRequest
			flags := cmd.Flags()
			switch {
			case allProjects && flags.Changed("project"):
				return usageError{errors.New("use --project or --all-projects, not both")}
			case allProjects:
				body.Projects = &[]string{}
			case flags.Changed("project"):
				if err := checkWebhookProjects(ctx, cmd, projects); err != nil {
					return err
				}
				body.Projects = &projects
			}
			switch {
			case allEvents && flags.Changed("events"):
				return usageError{errors.New("use --events or --all-events, not both")}
			case allEvents:
				body.Events = &[]string{}
			case flags.Changed("events"):
				list := splitEvents(events)
				body.Events = &list
			}
			if flags.Changed("quiet-at-desk") {
				body.QuietAtDesk = &quiet
			}
			if body.Projects == nil && body.Events == nil && body.QuietAtDesk == nil {
				return usageError{errors.New("nothing to change: pass --project, --all-projects, --events, --all-events or --quiet-at-desk")}
			}
			var view webhookView
			if err := ctx.doJSON(cmd.Context(), http.MethodPatch, "webhooks/"+url.PathEscape(args[0]), body, &view); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: projects %s, events %s\n", view.Name, orAll(view.Projects), orDefault(view.Events, view.QuietAtDesk))
			return err
		},
	}
	cmd.Flags().StringArrayVar(&projects, "project", nil, "Only events from this project id (repeatable; replaces the list)")
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, "Events from every project")
	cmd.Flags().StringVar(&events, "events", "", "Comma-separated event kinds (replaces the list)")
	cmd.Flags().BoolVar(&allEvents, "all-events", false, "Every kind but worker_finished")
	cmd.Flags().BoolVar(&quiet, "quiet-at-desk", false, "Skip events while the Orchestrator app is in front and in use (macOS)")
	return cmd
}

func newWebhookRemoveCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use: "remove NAME", Aliases: []string{"rm"}, Short: "Remove a webhook",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.doJSON(cmd.Context(), http.MethodDelete, "webhooks/"+url.PathEscape(args[0]), nil, nil); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "removed webhook %s\n", args[0])
			return err
		},
	}
}

func newWebhookTestCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use: "test NAME", Short: "Send a test event and wait for the webhook's answer",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out webhookTestResponse
			if err := ctx.doJSON(cmd.Context(), http.MethodPost, "webhooks/"+url.PathEscape(args[0])+"/test", nil, &out); err != nil {
				return err
			}
			if !out.OK {
				return fmt.Errorf("webhook %s: %s", args[0], out.Error)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "OK")
			return err
		},
	}
}

func readWebhookSecrets(in io.Reader, prompt io.Writer) (string, string, error) {
	var lines [2]string
	labels := [2]string{"Line 1: webhook URL (starts with https://): ", "Line 2: Authorization header, or Return for none: "}
	file, tty := in.(*os.File)
	tty = tty && term.IsTerminal(file.Fd())
	reader := bufio.NewReader(in)
	for i := range lines {
		if tty {
			_, _ = fmt.Fprint(prompt, labels[i])
			b, err := term.ReadPassword(file.Fd())
			_, _ = fmt.Fprintln(prompt)
			if err != nil {
				return "", "", err
			}
			lines[i] = string(b)
		} else {
			s, err := reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return "", "", err
			}
			lines[i] = s
		}
		lines[i] = strings.TrimSpace(lines[i])
		if i == 0 && tty {
			if u, err := url.Parse(lines[0]); err == nil && u.Host != "" {
				_, _ = fmt.Fprintf(prompt, "Got the URL for %s.\n", u.Host)
			}
		}
	}
	if lines[0] == "" {
		return "", "", usageError{errors.New("no webhook URL on stdin")}
	}
	auth := lines[1]
	if name, value, ok := strings.Cut(auth, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "authorization") {
		auth = strings.TrimSpace(value)
	}
	return lines[0], auth, nil
}

func checkWebhookProjects(ctx *commandContext, cmd *cobra.Command, projects []string) error {
	for _, p := range projects {
		var resp apiResponseError
		err := ctx.doJSON(cmd.Context(), http.MethodGet, "projects/"+url.PathEscape(p), nil, nil)
		if errors.As(err, &resp) && resp.StatusCode == http.StatusNotFound {
			return usageError{fmt.Errorf("no project %q here; `ao project ls` lists the ids", p)}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func splitEvents(s string) []string {
	var out []string
	for _, e := range strings.Split(s, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func orAll(projects []string) string {
	if len(projects) == 0 {
		return "all"
	}
	return strings.Join(projects, ",")
}

func orDefault(events []string, quiet bool) string {
	s := "all but worker_finished"
	if len(events) > 0 {
		s = strings.Join(events, ",")
	}
	if quiet {
		s += " (quiet at desk)"
	}
	return s
}

func webhookState(s webhookStatus) string {
	switch {
	case s.PausedUntil != nil:
		return "rate limited until " + s.PausedUntil.Local().Format("15:04")
	case s.LastErrorAt != nil && (s.LastSentAt == nil || s.LastErrorAt.After(*s.LastSentAt)):
		return "failing: " + s.LastError
	case s.LastSentAt != nil:
		return "ok, last sent " + s.LastSentAt.Local().Format("Jan 2 15:04")
	}
	return "nothing sent yet"
}
