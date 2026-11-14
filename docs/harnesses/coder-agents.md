# Coder Agents setup

AO supports installing the official Coder CLI, signing in to a Coder deployment,
checking the native account, and discovering enabled Coder Agents models.
**Coder Agents sessions are unavailable in both Chat and Terminal UI.**
Coder Agents is excluded from advertised Chat capabilities, New Task choices
and defaults, and agent-switch targets.

Coder v2.37.9 adds client-executed dynamic tools alongside built-in server-side
workspace tools. It offers no server-enforced local-only tool policy, and an
asynchronous interrupt cannot prevent those tools' side effects. A Chat driver
can ship separately once that boundary can be enforced. No dormant conversation,
tool execution, approval or replay implementation is included here.

## Install and sign in

Use Settings > Harness > Coder Agents to install the official CLI:
Homebrew or install.sh on macOS, install.sh into ~/.local/bin on Linux, or
winget install Coder.Coder on Windows.

Select **Log in**. The AO login terminal explicitly prompts for the deployment
URL (for example https://coder.example.com), validates it, and passes it as a
separate argument to the native coder login command. Finish Coder's browser
and session-token flow in the same terminal. Coder owns credential storage.
No previous login or saved URL is required.

The equivalent manual command is:

    coder login https://coder.example.com

## Account checks and models

AO reads CODER_URL or the URL in the native CLI config directory
(CODER_CONFIG_DIR, otherwise Coder's platform default). CODER_SESSION_TOKEN
takes precedence; otherwise AO runs coder login token, including native keyring
support. CODER_AGENTS_ORGANIZATION selects an organization when necessary.

Connected means the deployment accepted GET /api/v2/users/me and the selected
organization has an enabled Coder Agents model. It is account/setup evidence,
not evidence that AO can launch a session. A credential that cannot be verified
is Configured; a missing or rejected credential is Not set up.

Models come from GET /api/v2/organizations/{org}/chats/models on a Coder v2.37+
deployment with Coder Agents enabled and administrator-configured models.
AO displays only enabled model configurations and their native reasoning efforts.

Session tokens stay in daemon memory and are not persisted by AO. API requests
refuse redirects to another origin, so tokens are not forwarded off-deployment.
