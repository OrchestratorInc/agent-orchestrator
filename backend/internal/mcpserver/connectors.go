package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const (
	proposalWait = 100 * time.Second
	proposalPoll = time.Second
)

var connectorTools = []catalogTool{
	{
		name: "connector_list", title: "List connectors",
		desc: "Lists the connectors: the MCP servers that Orchestrator gives to every new session. Each has a key that starts its tool names, " +
			"a status, and inThisSession, which says whether this session started with it. A session keeps the connectors it started with.",
		call: callConnectorList,
	},
	{
		name: "connector_propose", title: "Ask the user to add a connector", act: true, ungated: true,
		desc: "Asks the user to add an MCP server as a connector. The user sees the request in this chat and approves or declines it; " +
			"nothing is added before they approve. Call it only when the task needs a server that connector_list does not have, and say why in reason. " +
			"Give a server URL, or a command with its arguments. Never put a token, a key or a password in any field: " +
			"list the names of the values in envKeys or headerKeys, and the user types them. " +
			"The call waits for the answer for up to 100 seconds. When the state is still pending, do not ask again: continue without the connector. " +
			"An approved connector reaches sessions that start later. This session gets its tools after its agent restarts: the user restarts it from the chat, and a message then says that the connector is ready.",
		props: map[string]any{
			"key":        stringProp("Short name that starts each tool name: 1 to 32 lowercase letters, digits, hyphens or underscores. Not the name of a tool server that you already have: a connector with that name does not reach you."),
			"url":        stringProp("Server URL, https only. Give this or command."),
			"command":    stringProp("Program that runs the server on this computer, such as npx. Give this or url."),
			"args":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Arguments of the command."},
			"envKeys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Names of the environment values the command needs. Names only."},
			"headerKeys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Names of the request headers the server needs, such as Authorization. Names only. Leave it out for a server that signs in with OAuth."},
			"reason":     stringProp("One sentence for the user: what the task needs this server for."),
		},
		required: []string{"key", "reason"},
		call:     callConnectorPropose,
	},
}

func callConnectorList(ctx context.Context, s *server, _ map[string]any) (any, error) {
	var all struct {
		Connectors []map[string]any `json:"connectors"`
	}
	if err := s.daemon.DoJSON(ctx, http.MethodGet, "connectors", nil, &all); err != nil {
		return nil, err
	}
	here := map[string]bool{}
	if caller := callerSession(); caller != "" {
		var mine struct {
			Connectors []map[string]any `json:"connectors"`
		}
		if err := s.daemon.DoJSON(ctx, http.MethodGet, sessionPath(caller, "/connectors"), nil, &mine); err == nil {
			for _, one := range mine.Connectors {
				if key, ok := one["key"].(string); ok {
					here[key] = true
				}
			}
		}
	}
	out := make([]any, 0, len(all.Connectors))
	for _, one := range all.Connectors {
		row := keep(one, "key", "name", "transport", "status", "enabled", "projectId")
		key, _ := one["key"].(string)
		row["inThisSession"] = here[key]
		out = append(out, row)
	}
	return map[string]any{"connectors": out}, nil
}

func callConnectorPropose(ctx context.Context, s *server, args map[string]any) (any, error) {
	caller := callerSession()
	if caller == "" {
		return nil, errors.New("connector_propose works only inside an Orchestrator session")
	}
	body := map[string]any{"sessionId": caller, "key": stringArg(args, "key"), "reason": stringArg(args, "reason"), "transport": "http"}
	for _, name := range []string{"url", "command"} {
		if value := stringArg(args, name); value != "" {
			body[name] = value
		}
	}
	for _, name := range []string{"args", "envKeys", "headerKeys"} {
		if value, ok := args[name]; ok {
			body[name] = value
		}
	}
	if body["command"] != nil {
		body["transport"] = "stdio"
	}
	var created struct {
		Proposal map[string]any `json:"proposal"`
	}
	if err := s.daemon.DoJSON(ctx, http.MethodPost, "connectors/proposals", body, &created); err != nil {
		return nil, err
	}
	id, _ := created.Proposal["id"].(string)
	answer := created.Proposal
	deadline := time.NewTimer(proposalWait)
	defer deadline.Stop()
	tick := time.NewTicker(proposalPoll)
	defer tick.Stop()
	for answer["state"] == "pending" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return proposalResult(answer), nil
		case <-tick.C:
		}
		var current struct {
			Proposal map[string]any `json:"proposal"`
		}
		if err := s.daemon.DoJSON(ctx, http.MethodGet, "connectors/proposals/"+url.PathEscape(id), nil, &current); err != nil {
			return nil, err
		}
		answer = current.Proposal
	}
	return proposalResult(answer), nil
}

func proposalResult(proposal map[string]any) map[string]any {
	out := keep(proposal, "state", "key", "connectorKey")
	switch proposal["state"] {
	case "approved":
		out["next"] = "The user approved. Sessions that start from now get its tools. This session gets them after its agent restarts: the user restarts it from the chat, and a message then says that the connector is ready. Stop here and wait for that message."
	case "declined":
		out["next"] = "The user declined. Do not ask again for this connector. Continue without it."
	case "pending":
		out["next"] = "The user has not answered yet. Do not ask again. Continue without the connector; the user can still approve it in the chat."
	default:
		out["next"] = "The request is no longer open. Continue without the connector."
	}
	return out
}
