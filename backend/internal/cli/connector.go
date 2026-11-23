package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const (
	connectorSignInWait = 10 * time.Minute
	connectorSignInPoll = 2 * time.Second
)

type connectorDTO struct {
	ID           string   `json:"id"`
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Transport    string   `json:"transport"`
	URL          string   `json:"url,omitempty"`
	Command      string   `json:"command,omitempty"`
	Args         []string `json:"args"`
	EnvKeys      []string `json:"envKeys"`
	HeaderKeys   []string `json:"headerKeys"`
	Auth         string   `json:"auth"`
	SignedIn     bool     `json:"signedIn"`
	ProjectID    *string  `json:"projectId,omitempty"`
	Enabled      bool     `json:"enabled"`
	Status       string   `json:"status"`
	StatusDetail *struct {
		Step  string `json:"step"`
		Error string `json:"error"`
	} `json:"statusDetail,omitempty"`
	Tools []string `json:"tools"`
}

type connectorEnvelopeDTO struct {
	Connector connectorDTO `json:"connector"`
}

type connectorListDTO struct {
	Connectors []connectorDTO `json:"connectors"`
}

type connectorSignInDTO struct {
	AuthorizationURL string       `json:"authorizationUrl,omitempty"`
	CallbackURL      string       `json:"callbackUrl"`
	Connector        connectorDTO `json:"connector"`
}

type connectorImportCandidateDTO struct {
	ID         string   `json:"id"`
	Agent      string   `json:"agent"`
	Source     string   `json:"source"`
	Key        string   `json:"key"`
	Transport  string   `json:"transport"`
	URL        string   `json:"url,omitempty"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	NeedsValue []string `json:"needsValue,omitempty"`
	ProjectID  string   `json:"projectId,omitempty"`
	Exists     bool     `json:"exists"`
}

type connectorImportListDTO struct {
	Candidates []connectorImportCandidateDTO `json:"candidates"`
}

type connectorImportRequestDTO struct {
	IDs    []string                     `json:"ids"`
	Values map[string]map[string]string `json:"values,omitempty"`
}

type connectorRequestDTO struct {
	Key       string            `json:"key,omitempty"`
	Name      string            `json:"name,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Auth      string            `json:"auth,omitempty"`
	ProjectID *string           `json:"projectId,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
}

func newConnectorCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connector",
		Aliases: []string{"connectors"},
		Short:   "Add an MCP server once for every agent",
		Long: "Add, list and remove connectors. A connector is an MCP server that Orchestrator gives to every agent session.\n\n" +
			"Sessions that are already open keep the connectors they started with.",
	}
	cmd.AddCommand(
		newConnectorAddCommand(ctx),
		newConnectorListCommand(ctx),
		newConnectorRemoveCommand(ctx),
		newConnectorEnableCommand(ctx, "enable", "Give the connector to new sessions", true),
		newConnectorEnableCommand(ctx, "disable", "Keep the connector but leave it out of new sessions", false),
		newConnectorConnectCommand(ctx),
		newConnectorDisconnectCommand(ctx),
		newConnectorTestCommand(ctx),
		newConnectorImportCommand(ctx),
	)
	return cmd
}

func newConnectorAddCommand(ctx *commandContext) *cobra.Command {
	var serverURL, name, auth, project string
	var headers, env []string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "add <key> (--url <url> | -- <command> [args...])",
		Short: "Add a server URL or a local command",
		Example: "  ao connector add linear --url https://mcp.linear.app/mcp\n" +
			"  ao connector add docs --url https://docs.example.com/mcp --header \"Authorization=Bearer $TOKEN\"\n" +
			"  ao connector add paper --env PAPER_KEY=... -- npx -y paper-mcp",
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := connectorRequestDTO{Key: args[0], Name: name, Auth: auth}
			command := args[1:]
			switch {
			case strings.TrimSpace(serverURL) != "" && len(command) > 0:
				return usageError{errors.New("provide --url or a command after --, not both")}
			case strings.TrimSpace(serverURL) != "":
				body.Transport, body.URL = "http", serverURL
			case len(command) > 0:
				body.Transport, body.Command, body.Args = "stdio", command[0], command[1:]
			default:
				return usageError{errors.New("provide --url <url> or a command after --")}
			}
			var err error
			if body.Headers, err = keyValues("--header", headers); err != nil {
				return err
			}
			if body.Env, err = keyValues("--env", env); err != nil {
				return err
			}
			if body.Transport == "http" && body.Auth == "" {
				body.Auth = "oauth"
				if len(body.Headers) > 0 {
					body.Auth = "header"
				}
			}
			if strings.TrimSpace(project) != "" {
				resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
				if err != nil {
					return err
				}
				body.ProjectID = &resolved.ID
			}
			var response connectorEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), "connectors", body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "added connector %s (%s). New sessions get its tools.\n", response.Connector.Key, connectorTarget(&response.Connector))
			return err
		},
	}
	cmd.Flags().StringVar(&serverURL, "url", "", "Server URL (https, or http on this computer)")
	cmd.Flags().StringVar(&name, "name", "", "Display name (default: the key)")
	cmd.Flags().StringVar(&auth, "auth", "", "Sign-in of a server URL: oauth, header or none (default: header with --header, else oauth)")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "Request header as Name=value; repeat for more")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Environment value of the command as NAME=value; repeat for more")
	cmd.Flags().StringVar(&project, "project", "", "Only this project gets the connector (default: all projects)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newConnectorListCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List connectors and their status",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var response connectorListDTO
			if err := ctx.getJSON(cmd.Context(), "connectors", &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "KEY\tSTATUS\tPROJECT\tTARGET")
			for i := range response.Connectors {
				c := &response.Connectors[i]
				status, project := c.Status, "all"
				if !c.Enabled {
					status = "off"
				}
				if c.ProjectID != nil {
					project = *c.ProjectID
				}
				_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", c.Key, status, project, connectorTarget(c))
			}
			return writer.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newConnectorRemoveCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <key>",
		Aliases: []string{"rm"},
		Short:   "Remove a connector and its stored values",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.deleteJSON(cmd.Context(), "connectors/"+url.PathEscape(args[0]), nil); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "removed connector %s\n", args[0])
			return err
		},
	}
}

func newConnectorEnableCommand(ctx *commandContext, use, short string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <key>",
		Short: short,
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var response connectorEnvelopeDTO
			if err := ctx.doJSON(cmd.Context(), http.MethodPatch, "connectors/"+url.PathEscape(args[0]), connectorRequestDTO{Enabled: &enabled}, &response); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%sd connector %s\n", use, response.Connector.Key)
			return err
		},
	}
}

func newConnectorConnectCommand(ctx *commandContext) *cobra.Command {
	var finish string
	var noWait bool
	cmd := &cobra.Command{
		Use:   "connect <key>",
		Short: "Sign in to a connector",
		Long: "Sign in to a connector. The command prints a page to open and waits until you approve.\n\n" +
			"When the browser is on another device, the page that opens after approval does not load.\n" +
			"Copy its address and run the command again with --finish.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "connectors/" + url.PathEscape(args[0])
			out := cmd.OutOrStdout()
			if strings.TrimSpace(finish) != "" {
				var done connectorEnvelopeDTO
				if err := ctx.postJSON(cmd.Context(), path+"/connect/finish", map[string]string{"callbackUrl": finish}, &done); err != nil {
					return err
				}
				return printConnectorStatus(out, &done.Connector)
			}
			var started connectorSignInDTO
			if err := ctx.postJSON(cmd.Context(), path+"/connect", nil, &started); err != nil {
				return err
			}
			if started.AuthorizationURL == "" {
				return printConnectorStatus(out, &started.Connector)
			}
			if _, err := fmt.Fprintf(out, "Open this page and approve:\n\n  %s\n\nIf the page after approval does not load, copy its address and run:\n  ao connector connect %s --finish '<address>'\n", started.AuthorizationURL, args[0]); err != nil {
				return err
			}
			if noWait {
				return nil
			}
			waitCtx, cancel := context.WithTimeout(cmd.Context(), connectorSignInWait)
			defer cancel()
			for {
				select {
				case <-waitCtx.Done():
					return errors.New("the sign-in was not completed in time")
				case <-time.After(connectorSignInPoll):
				}
				var current connectorEnvelopeDTO
				if err := ctx.getJSON(waitCtx, path, &current); err != nil {
					return err
				}
				if current.Connector.SignedIn {
					return printConnectorStatus(out, &current.Connector)
				}
				if detail := current.Connector.StatusDetail; detail != nil && detail.Step == "sign-in" && detail.Error != "" {
					return fmt.Errorf("the sign-in did not complete: %s", detail.Error)
				}
			}
		},
	}
	cmd.Flags().StringVar(&finish, "finish", "", "Address of the page your browser opened after you approved")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Print the page and return")
	return cmd
}

func newConnectorDisconnectCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect <key>",
		Short: "Forget the sign-in of a connector",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var response connectorEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), "connectors/"+url.PathEscape(args[0])+"/disconnect", nil, &response); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "signed out of connector %s\n", response.Connector.Key)
			return err
		},
	}
}

func newConnectorTestCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "test <key>",
		Short: "Connect to the server and list its tools",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var response connectorEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), "connectors/"+url.PathEscape(args[0])+"/test", nil, &response); err != nil {
				return err
			}
			if err := printConnectorStatus(cmd.OutOrStdout(), &response.Connector); err != nil {
				return err
			}
			if response.Connector.Status != "connected" {
				return fmt.Errorf("connector %s is not connected", response.Connector.Key)
			}
			return nil
		},
	}
}

func printConnectorStatus(out io.Writer, c *connectorDTO) error {
	switch {
	case c.Status == "connected":
		_, err := fmt.Fprintf(out, "%s: connected, %d tools\n", c.Key, len(c.Tools))
		return err
	case c.Status == "needs_signin":
		_, err := fmt.Fprintf(out, "%s: needs sign-in. Run: ao connector connect %s\n", c.Key, c.Key)
		return err
	case c.StatusDetail != nil:
		_, err := fmt.Fprintf(out, "%s: %s at step %q: %s\n", c.Key, c.Status, c.StatusDetail.Step, c.StatusDetail.Error)
		return err
	default:
		_, err := fmt.Fprintf(out, "%s: %s\n", c.Key, c.Status)
		return err
	}
}

func newConnectorImportCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	var values []string
	cmd := &cobra.Command{
		Use:   "import [id...]",
		Short: "List the MCP servers of Claude Code, Codex and Cursor, or add the ones you name",
		Long: "Without an ID, lists the MCP servers found in the configuration of Claude Code, Codex and Cursor.\n" +
			"With IDs from that list, adds those servers as connectors. The files of the agents do not change.",
		RunE: func(cmd *cobra.Command, args []string) error {
			typed, err := keyValues("--value", values)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if typed != nil {
					return usageError{fmt.Errorf("--value needs the ID of one server")}
				}
				var response connectorImportListDTO
				if err := ctx.getJSON(cmd.Context(), "connectors/import", &response); err != nil {
					return err
				}
				if jsonOutput {
					return writeJSON(cmd.OutOrStdout(), response)
				}
				writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(writer, "ID\tAGENT\tKEY\tTARGET\tNOTE")
				for i := range response.Candidates {
					c := &response.Candidates[i]
					note := ""
					switch {
					case c.Exists:
						note = "a connector has this key"
					case len(c.NeedsValue) > 0:
						note = "needs --value for " + strings.Join(c.NeedsValue, ", ")
					}
					target := connectorTarget(&connectorDTO{Transport: c.Transport, URL: c.URL, Command: c.Command, Args: c.Args})
					_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", c.ID, c.Agent, c.Key, target, note)
				}
				return writer.Flush()
			}
			body := connectorImportRequestDTO{IDs: args}
			if typed != nil {
				if len(args) != 1 {
					return usageError{fmt.Errorf("--value needs the ID of one server")}
				}
				body.Values = map[string]map[string]string{args[0]: typed}
			}
			var response connectorListDTO
			if err := ctx.postJSON(cmd.Context(), "connectors/import", body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			for i := range response.Connectors {
				c := &response.Connectors[i]
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "added connector %s (%s). New sessions get its tools.\n", c.Key, connectorTarget(c)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&values, "value", nil, "Value the file does not hold, as NAME=value; repeat for more")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func connectorTarget(c *connectorDTO) string {
	if c.Transport == "http" {
		return c.URL
	}
	return strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
}

func keyValues(flag string, pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, found := strings.Cut(pair, "=")
		if name = strings.TrimSpace(name); !found || name == "" || value == "" {
			return nil, usageError{fmt.Errorf("%s needs Name=value, got %q", flag, pair)}
		}
		out[name] = value
	}
	return out, nil
}
