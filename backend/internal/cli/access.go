package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type accessGrantDTO struct {
	Preset    string     `json:"preset,omitempty"`
	Groups    []string   `json:"groups"`
	Projects  []string   `json:"projects,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Ended     bool       `json:"ended,omitempty"`
}

func (g accessGrantDTO) level() string {
	if g.Preset != "" {
		return g.Preset
	}
	return strings.Join(g.Groups, ",")
}

func (g accessGrantDTO) scope() string {
	if len(g.Projects) == 0 {
		return "every project"
	}
	return strings.Join(g.Projects, ", ")
}

func (g accessGrantDTO) expiry() string {
	switch {
	case g.Ended:
		return "ended"
	case g.ExpiresAt == nil:
		return "never"
	default:
		return g.ExpiresAt.Local().Format("2006-01-02 15:04")
	}
}

type whoamiDTO struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Grant *accessGrantDTO `json:"grant,omitempty"`
}

type accessTokenDTO struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Grant      accessGrantDTO `json:"grant"`
	CreatedAt  time.Time      `json:"createdAt"`
	LastUsedAt *time.Time     `json:"lastUsedAt,omitempty"`
}

type accessActionDTO struct {
	ID         string           `json:"id"`
	Group      string           `json:"group"`
	Method     string           `json:"method"`
	Path       string           `json:"path"`
	Summary    string           `json:"summary,omitempty"`
	Allowed    bool             `json:"allowed"`
	Parameters []map[string]any `json:"parameters,omitempty"`
	Body       map[string]any   `json:"body,omitempty"`
}

func newWhoamiCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who the daemon takes this caller to be, and what it may do",
		Long: `Show who the daemon takes this caller to be, and what it may do.

A caller with no agent token on this machine is the user and has no limit. A
caller that sets AO_TOKEN is that token, held to its grant. A session that a
token started carries the same grant. Inside a session the caller is always
the session, and AO_TOKEN is not used.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var who whoamiDTO
			if err := ctx.getJSON(cmd.Context(), "access/whoami", &who); err != nil {
				return err
			}
			if os.Getenv(agentTokenEnv) != "" && callerSession() != "" {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), agentTokenEnv+" is set but not used: inside a session, the session is the caller"); err != nil {
					return err
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), who)
			}
			out := cmd.OutOrStdout()
			line := who.Kind
			if label := strings.TrimSpace(who.Name + " " + who.ID); label != "" && who.Kind != "session" {
				line += " " + label
			} else if who.Kind == "session" {
				line += " " + who.ID
			}
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
			if who.Grant == nil {
				_, err := fmt.Fprintln(out, "  grant: none, no limit")
				return err
			}
			if who.Grant.Ended && len(who.Grant.Groups) == 0 {
				_, err := fmt.Fprintln(out, "  grant: ended; the token that started this session is gone")
				return err
			}
			if who.Kind == "session" && who.Name != "" {
				if _, err := fmt.Fprintf(out, "  started by token: %s\n", who.Name); err != nil {
					return err
				}
			}
			_, err := fmt.Fprintf(out, "  grant: %s\n  groups: %s\n  projects: %s\n  expires: %s\n",
				who.Grant.level(), strings.Join(who.Grant.Groups, ", "), who.Grant.scope(), who.Grant.expiry())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newTokenCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Make and revoke tokens for agents outside the app",
		Long: `Make and revoke tokens for agents outside the app.

An agent that sets AO_TOKEN to a token is held to the token's grant: a preset
(read, operate or manage) or named groups, limited to projects when you name
them. With remote access on, the same token works from another machine as a
bearer token. A grant limits calls to the daemon. It does not confine a process
that runs as your OS user, or what a session it starts does in its shell.`,
	}
	cmd.AddCommand(newTokenCreateCommand(ctx), newTokenListCommand(ctx), newTokenRevokeCommand(ctx))
	return cmd
}

const maxTokenExpiryDays = 36500

func parseTokenExpiry(value string, now time.Time) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "never" {
		return nil, nil
	}
	span, err := time.ParseDuration(value)
	if days, ok := strings.CutSuffix(value, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		if n > maxTokenExpiryDays {
			err = errors.New("too far ahead")
		}
		span = time.Duration(n) * 24 * time.Hour
	}
	if err != nil || span <= 0 {
		return nil, usageError{fmt.Errorf("invalid --expires %q; use a span such as 12h or 30d", value)}
	}
	at := now.Add(span).UTC()
	return &at, nil
}

func newTokenCreateCommand(ctx *commandContext) *cobra.Command {
	var (
		preset, expires  string
		groups, projects []string
		asJSON           bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Make a token and print its secret once",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if preset != "" && len(groups) > 0 {
				return usageError{errors.New("give --preset or --group, not both")}
			}
			if preset == "" && len(groups) == 0 {
				preset = "read"
			}
			if cmd.Flags().Changed("project") && (len(projects) == 0 || slices.ContainsFunc(projects, func(id string) bool { return strings.TrimSpace(id) == "" })) {
				return usageError{errors.New("--project needs a project id; an empty one would mean every project")}
			}
			expiresAt, err := parseTokenExpiry(expires, ctx.deps.Now())
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0], "preset": preset, "groups": groups, "projects": projects, "expiresAt": expiresAt}
			var res struct {
				Token  accessTokenDTO `json:"token"`
				Secret string         `json:"secret"`
			}
			if err := ctx.postJSON(cmd.Context(), "access/tokens", body, &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Made token %s (%s): %s on %s, expires %s.\n\n  %s=%s\n\nThis is the only time the secret is shown. Revoke it with `ao token revoke %s`.\n",
				res.Token.Name, res.Token.ID, res.Token.Grant.level(), res.Token.Grant.scope(), res.Token.Grant.expiry(),
				agentTokenEnv, res.Secret, res.Token.Name)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&preset, "preset", "", "Grant preset: read, operate or manage (default read)")
	f.StringSliceVar(&groups, "group", nil, "Grant exactly these groups instead of a preset; see `ao actions ls`")
	f.StringSliceVarP(&projects, "project", "p", nil, "Limit the grant to this project id; repeat for more")
	f.StringVar(&expires, "expires", "", "End the token after this span, such as 12h or 30d (default never)")
	f.BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newTokenListCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List this machine's agent tokens",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res struct {
				Tokens []accessTokenDTO `json:"tokens"`
			}
			if err := ctx.getJSON(cmd.Context(), "access/tokens", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			if len(res.Tokens) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No agent tokens. Make one with `ao token create <name>`.")
				return err
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "ID\tNAME\tGRANT\tPROJECTS\tEXPIRES\tLAST USED"); err != nil {
				return err
			}
			for _, token := range res.Tokens {
				used := "never"
				if token.LastUsedAt != nil {
					used = token.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", token.ID, token.Name, token.Grant.level(), token.Grant.scope(), token.Grant.expiry(), used); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newTokenRevokeCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id-or-name>",
		Short: "Revoke a token; sessions it started are refused from then on",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.deleteJSON(cmd.Context(), "access/tokens/"+url.PathEscape(args[0]), nil); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s.\n", args[0])
			return err
		},
	}
}

func newActionsCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List the actions a caller with a grant can run",
		Long: `List the actions a caller with a grant can run.

The catalog is an explicit list. Each action has an id, the group a grant needs
for it, and an existing daemon route. Run one with a typed command where one
exists, or with ` + "`ao call <id>`" + `.`,
	}
	var (
		asJSON bool
		group  string
	)
	list := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List catalog actions and whether this caller's grant allows each",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res struct {
				Actions []accessActionDTO `json:"actions"`
			}
			if err := ctx.getJSON(cmd.Context(), "access/actions", &res); err != nil {
				return err
			}
			if group != "" {
				kept := res.Actions[:0]
				for _, action := range res.Actions {
					if action.Group == group {
						kept = append(kept, action)
					}
				}
				res.Actions = kept
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "ACTION\tGROUP\tALLOWED\tWHAT IT DOES"); err != nil {
				return err
			}
			for _, action := range res.Actions {
				allowed := "yes"
				if !action.Allowed {
					allowed = "no"
				}
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", action.ID, action.Group, allowed, action.Summary); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	list.Flags().StringVar(&group, "group", "", "Only actions of this group")
	show := &cobra.Command{
		Use:   "show <action>",
		Short: "Show one action's input: path and query parameters and the JSON body schema",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			action, err := ctx.catalogAction(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), action)
		},
	}
	cmd.AddCommand(list, show)
	return cmd
}

func (c *commandContext) catalogAction(ctx context.Context, id string) (accessActionDTO, error) {
	var res struct {
		Action accessActionDTO `json:"action"`
	}
	err := c.getJSON(ctx, "access/actions/"+url.PathEscape(id), &res)
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "ACTION_NOT_FOUND" {
		return accessActionDTO{}, exitError{code: exitNotFound, err: fmt.Errorf("unknown action %q: the catalog has no such action; run `ao actions ls`", id)}
	}
	return res.Action, err
}

func scalarString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case json.Number:
		return v.String(), true
	}
	return "", false
}

func actionRequest(action accessActionDTO, input map[string]any) (path string, body any, err error) {
	path = action.Path
	query := url.Values{}
	for _, parameter := range action.Parameters {
		name, _ := parameter["name"].(string)
		value, given := input[name]
		delete(input, name)
		switch parameter["in"] {
		case "path":
			text, ok := scalarString(value)
			if !given || !ok || text == "" {
				return "", nil, usageError{fmt.Errorf("%s needs %q in --input", action.ID, name)}
			}
			path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(text))
		case "query":
			if !given {
				continue
			}
			values, isList := value.([]any)
			if !isList {
				values = []any{value}
			}
			for _, item := range values {
				text, ok := scalarString(item)
				if !ok {
					return "", nil, usageError{fmt.Errorf("%s: %q must be a string, a number or a boolean", action.ID, name)}
				}
				query.Add(name, text)
			}
		}
	}
	if strings.Contains(path, "{") {
		return "", nil, fmt.Errorf("%s: the catalog path %s has a parameter the daemon did not describe", action.ID, action.Path)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if action.Method == http.MethodGet || action.Method == http.MethodDelete || action.Body == nil {
		if len(input) > 0 {
			unknown := make([]string, 0, len(input))
			for name := range input {
				unknown = append(unknown, name)
			}
			sort.Strings(unknown)
			return "", nil, usageError{fmt.Errorf("%s takes no %s; see `ao actions show %s`", action.ID, strings.Join(unknown, ", "), action.ID)}
		}
		if action.Method == http.MethodGet || action.Method == http.MethodDelete {
			return path, nil, nil
		}
	}
	return path, input, nil
}

func callTimeout(action string) time.Duration {
	switch action {
	case "session.spawn", "orchestrator.spawn", "session.restore":
		return 0
	}
	return commandTimeout
}

func newCallCommand(ctx *commandContext) *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:   "call <action>",
		Short: "Run one catalog action and print its JSON result",
		Long: `Run one catalog action and print its JSON result.

The action must be in the catalog (` + "`ao actions ls`" + `). --input is one JSON object:
keys that name a path or query parameter go there, and the rest form the JSON
body. ` + "`ao actions show <action>`" + ` prints the shape. The daemon applies the same
grant check as for a typed command.

Exit codes: 0 done, 2 wrong usage, 3 unknown action, 4 refused by the grant or
the token, 1 any other failure.`,
		Example: `  ao call session.list --input '{"project":"api"}'
  ao call session.spawn --input '{"projectId":"api","prompt":"Review PR 512"}'
  echo '{"sessionId":"api-3","message":"Run the tests"}' | ao call session.send --input -`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := []byte(input)
			if input == "-" {
				var err error
				if raw, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 32<<20)); err != nil {
					return err
				}
			}
			fields := map[string]any{}
			if text := strings.TrimSpace(string(raw)); text != "" {
				decoder := json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if err := decoder.Decode(&fields); err != nil {
					return usageError{fmt.Errorf("--input must be one JSON object: %w", err)}
				}
				if _, err := decoder.Token(); fields == nil || !errors.Is(err, io.EOF) {
					return usageError{errors.New("--input must be one JSON object, with nothing after it")}
				}
			}
			action, err := ctx.catalogAction(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			path, body, err := actionRequest(action, fields)
			if err != nil {
				return err
			}
			var res rawResponse
			if err := ctx.doJSONPathWithHeadersAndTimeout(cmd.Context(), action.Method, path, body, &res, nil, callTimeout(args[0])); err != nil {
				return err
			}
			if len(res.body) == 0 {
				return nil
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(res.body), "\n"))
			return err
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "The action's input as one JSON object, or - to read it from stdin")
	return cmd
}
