# Self-hosted remote hosts (experimental)

An AO session belongs to the daemon that started it. A desktop or phone is a
client of that daemon; closing or switching clients does not move the session.
Each client saves its own host connections. There is no shared host directory
or new global database. The existing AO Cloud path is separate.

## Set up a host

On the machine that will run the agents, run this as your normal user (macOS
arm64/x64 or Linux x64):

```bash
bash -c 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/setup-self-hosted.sh | bash'
```

The script downloads AO's latest desktop release, verifies its published
SHA-256 digest, and installs **only the daemon, Claude Chat ACP runtime, and
tmux** under `~/.ao/host`. It starts `ao daemon` as a systemd user service on
Linux or LaunchAgent on macOS, then enables the authenticated LAN listener.
It prints the host ID, LAN address, and pairing password. The host needs
`curl`, `python3`, and `git`; Linux additionally needs a working systemd user session.
No desktop window or Electron process runs on the host. Run the script again
to install a later AO release. Project and conversation data remain under
`~/.ao/data`; restarting the daemon may exit running terminals.

For access outside your private network, install `cloudflared` on the host and
pass `--tunnel`:

```bash
bash -c 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/setup-self-hosted.sh | bash -s -- --tunnel'
```

The HTTPS tunnel address may take a few seconds to appear. On the host, run
`~/.ao/host/current/resources/daemon/ao remote-host status` to retrieve it and
the password. This uses AO's existing Connect Mobile quick tunnel. The URL
changes when the tunnel restarts; update it on each client. Linux hosts must
enable systemd lingering to keep the user service alive after logout; the
installer prints the required `sudo loginctl enable-linger <user>` command when
needed. A macOS LaunchAgent runs while that user is logged in; logging the
host user out stops it. The host itself must remain powered on.

Before this PR is in a published AO release, or on Linux arm64, build a native
host bundle from this checkout and supply it to the same script:

```bash
cd frontend
npm ci
npm run build:daemon && npm run build:tmux && npm run build:acp-runtime && npm run build:host
../scripts/setup-self-hosted.sh --bundle dist-host/ao-host-$(node -p 'process.platform')-$(node -p 'process.arch').tar.gz
```

The bundle preserves the same runtime layout as the desktop release. Use
`--install-only` when another service manager or container entrypoint will
run the installed `resources/daemon/ao daemon` command. Neither path installs
Claude Code itself or copies credentials from your laptop.

## Connect a desktop or phone

1. On your laptop, open **Settings → Remote hosts**, add a label, and paste
   the address and password printed on the host. Add more hosts the same way.
2. Projects on each host appear in the normal sidebar with a host badge. Use
   **Projects → +** or **Add project on [machine]** to register code on that
   machine, then use the project's **New task** action.
3. On mobile, pair each machine in **Settings → Machines**. Projects and workers
   from connected hosts appear together; opening one targets its owning host
   without switching machines. Pair the same host on a second laptop to
   continue the same session.

The laptop's **Settings → Harness** host picker installs Claude Code, OpenCode,
or another supported harness on the selected host, not on the laptop. Its
install progress and sign-in terminal also run on that host. Each host keeps
its own harness binaries and credentials. Installing Claude Code does not
install AO's Chat adapter: the host setup above already includes that adapter
and its Node runtime. Browser-callback provider logins may still require a
browser or forwarding on the host.

To disconnect later, run `~/.ao/host/current/resources/daemon/ao remote-host
disable` on the host. Removing a host in a client only removes that client's
saved connection; it does not stop the host or its sessions.

The daemon's normal unauthenticated listener remains on `127.0.0.1`. The
opt-in remote listener is password-protected but plain HTTP, intended only for
a trusted private network or an encrypted tunnel. Do not expose it directly
to the public internet. Clients remember a stable daemon-installation ID and
reject an address that later answers as a different host when connecting.
The desktop checks the host ID before each new HTTP or WebSocket connection;
the host also rejects mismatched IDs before handling a request. Older clients
may not perform these checks. Use only a trusted private network or encrypted
tunnel; this is not protection against an active network attacker or a copied
AO data directory. Desktop connection passwords live in
`~/.ao/remotes.json` (or `AO_DATA_DIR/remotes.json`) with owner-only permissions.

The optional Cloudflare quick tunnel encrypts traffic in transit, but Cloudflare
terminates TLS and can see the connection password, conversations, and terminal
traffic. It has no uptime guarantee and its HTTPS hostname changes on restart;
update the saved address on each client if that happens. The host daemon still
needs an OS service manager to survive logout or reboot. The desktop refreshes
remote data while a quick tunnel is active because these tunnels buffer SSE
events; terminals continue over WebSocket.

The desktop reuses the normal project creation, settings, board, Chat,
inspector, and file surfaces, with requests routed to the owning host. The
host badge indicates where the daemon runs; it does not change the project or
session workflow. The Browser tab can show host
workspace files and managed app previews started with `ao preview start`.
Manually registered host-local URLs are not proxied to the client. AO's Files
editor works remotely; opening a host file in an editor installed on the
client still needs a separately configured remote workspace connection.
Previewed apps should use relative asset and API URLs; hard-coded
`localhost` URLs still point at the viewing device.

Push notifications carry the owning host ID; a tap for a different selected
host opens the board instead of acting on a same-ID session there. Older pushes
without a host ID also open the board.
