# MCP Apps in Codex Chat

Codex tools that return an MCP Apps resource render an interactive view in the
conversation on desktop and mobile. Configure the MCP server in Codex as usual;
the server must expose a `ui://` resource with MIME type
`text/html;profile=mcp-app`, and the installed Codex app-server must support MCP
resource reads and tool calls. Ordinary tool output remains available below the
view. Apps remain visible when the turn's other work is folded.

This implements the public MCP Apps protocol (`2026-01-26`). It does not consume
ChatGPT's native Intelligent UI stream, and it does not depend on selecting a
particular GPT model. Other Chat harnesses can add the optional `ChatMCPApps`
port; they do not currently provide this transport.

## Interactions

- Apps receive tool arguments, the completed result, theme variables, dimensions,
  locale, and display-mode updates after the initialization handshake.
- App tool calls and resource reads use the originating server and provider
  thread. Model-only tools are unavailable to apps. Tools explicitly annotated
  read-only can run directly; other calls require an Allow once confirmation.
- Apps can request a text user message, with confirmation, or update text and
  structured context for the next message. Context replaces the app's previous
  note and is presented to the model as untrusted data.
- Links and file exports require confirmation. Resource-link exports are read
  through the originating MCP server, never interpreted as local file paths.
- Desktop expansion preserves the same iframe and its selection state. Mobile
  expansion opens a native modal and reloads the WebView. Mobile exports use
  the system share sheet.

## Saved views and authority

The daemon saves the app document and its policy alongside the full tool data,
outside the compact timeline detail. It attempts capture when the tool completes
and retries on opening an uncaptured app. A saved view can render after the
controller stops; server actions require the originating conversation to be
live. Saving the document does not persist arbitrary JavaScript or form state.

The active conversation branch determines visibility. Rollback or branch changes
revoke access to removed app activities. Approval tokens are single-use, expire
after five minutes, and bind the conversation, activity, controller generation,
method, and exact request parameters. Actions serialize with conversation changes.
Each app activity has its own limit of 16 pending approvals per controller
generation, so one app cannot exhaust another app's approval allowance.

Documents run without the host origin under a restrictive CSP. Only declared
public resource/connect/frame domains are admitted. Desktop navigation and local
network guards also apply. Remote desktop documents use `srcdoc` so the app never
receives the host proxy's capability URL. Mobile keeps its bearer in native
request headers. Device permission metadata is allowlisted; browser and OS
permission restrictions still apply, and device access is not guaranteed on
every platform.

Limits: documents 5 MiB; tool data 8 MiB; action parameters 256 KiB; context
16 KiB per app and 64 KiB across the latest 32 nonempty visible notes; exports
five files totaling 25 MiB. Four background document captures can run at once;
opening an uncaptured view retries loading. Unsupported protocol methods return
an explicit error. App messages and model context currently support text and
structured context, not image or audio messages.

## Verification

Backend tests cover normalization, resource policy, origin binding, approvals,
saved views, context, rollback, and the HTTP boundary. Shared protocol tests
exercise the handshake, late results, confirmation, downloads and teardown.
`frontend/e2e/mcp-apps.spec.ts` runs the real renderer with a sandboxed iframe and
a fake provider API to verify selection, expansion, write approval and export.
Native WebView device permissions and share sheets require device testing.

References: [MCP Apps](https://apps.extensions.modelcontextprotocol.io/api/documents/Overview.html),
[Codex app-server](https://developers.openai.com/codex/app-server).
