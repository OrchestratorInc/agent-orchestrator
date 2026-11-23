# ao connector

A connector is an MCP server that Orchestrator gives to every agent session: a server URL, or a command that runs on this computer. The user adds it once. Orchestrator keeps its sign-in and its values, and passes each session's calls to the server. The configuration files of Claude Code, Codex and other agents do not change.

A session keeps the connectors it started with. A session that starts later gets the current list. A connector can belong to all projects or to one project.

```text
ao connector list [--json]
ao connector add <key> --url <url> [--auth oauth|header|none] [--header Name=value]... [--name <name>] [--project <id>] [--json]
ao connector add <key> [--env NAME=value]... [--name <name>] [--project <id>] [--json] -- <command> [args...]
ao connector connect <key> [--no-wait] [--finish <address>]
ao connector test <key>
ao connector enable <key>
ao connector disable <key>
ao connector disconnect <key>
ao connector remove <key>
ao connector import [id...] [--value NAME=value]... [--json]
```

The key starts each tool name. It has 1 to 32 lowercase letters, digits, hyphens or underscores. `ao` and `import` are not available. A server URL must be `https`, or `http` on this computer.

## Sign-in

`ao connector connect <key>` prints a page and waits. The user opens the page and approves. With `--no-wait` the command prints the page and returns.

On a machine without a browser, the user opens the page on another device. After approval, that browser opens an address on `127.0.0.1` and shows an error, because the address belongs to this machine. The user copies that address, and `ao connector connect <key> --finish "<address>"` completes the sign-in. Do not open the address yourself.

Do not ask the user to type a token, a key or a password into the conversation. For a server that needs a header, the user runs `ao connector add` with `--header`, or adds the connector in Settings.

## Status

`ao connector list` shows `connected`, `needs_signin`, `unavailable`, `unchecked` or `off`. `ao connector test <key>` connects, lists the tools and shows the step that fails: `reach`, `start`, `sign-in`, `initialize` or `list tools`.

When a tool call answers that the connector needs sign-in, stop and tell the user to sign in from Settings, Connectors, or with `ao connector connect <key>`. A retry does not help.

## Import

`ao connector import` lists the MCP servers found in the configuration of Claude Code, Codex and Cursor, without their values. `ao connector import <id>...` adds the ones you name. When the list shows `needs --value`, the file holds a variable and not a value: the user supplies it with `--value NAME=value` for one ID.

Add, remove or import a connector only when the user asks for it.
