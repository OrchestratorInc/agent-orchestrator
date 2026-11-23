package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
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
