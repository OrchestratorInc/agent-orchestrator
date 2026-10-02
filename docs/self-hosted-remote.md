# Self-hosted remote hosts (experimental)

An AO session belongs to the daemon that started it. A desktop or phone is a
client of that daemon; closing or switching clients does not move the session.
Each client saves its own host connections. There is no shared host directory
or new global database. The existing AO Cloud path is separate.

## Set up a host

These commands run on a **fresh Ubuntu 24.04 x64 VM** as a normal user with
`sudo` access. SSH from your laptop first:

```bash
ssh -i /path/to/private-key USER@HOST_ADDRESS
```

Then run **one** of the following on the VM. The first uses this PR's source
branch before release; the second uses the verified published binary **only
after this PR is merged and released**. Both install Ubuntu prerequisites,
Cloudflare quick tunnel, an always-on user service, and print the address and
password to enter on the laptop. The source build also installs Go and Node
under your user account; the release path does not need build tools.

Current PR/dev desktop:

```bash
bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/codex/remote-hosts-integrated/scripts/bootstrap-self-hosted.sh | bash -s -- --source-ref codex/remote-hosts-integrated'
```

Published release/updated desktop:

```bash
bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash'
```

For a trusted LAN or private VPN only, add `--lan` after the script arguments
(`bash -s -- --lan` for the release command). This skips Cloudflare and uses
AO's password-protected but plaintext private-network listener; never expose
that port to the public internet. Quick-tunnel URLs change when the tunnel
restarts. The VM must remain powered on, and its systemd user session must be
available. Windows is not a native host for this feature.

The bootstrap calls [the lower-level installer](../scripts/setup-self-hosted.sh),
which verifies a published release's SHA-256 digest or uses the locally built
host bundle. It installs AO under `~/.ao/host`, not a desktop window. It does
**not** install an agent harness or copy provider/GitHub credentials from your
laptop. It installs the `gh` CLI, but you must authenticate it separately.
Re-run the same command to upgrade AO without deleting conversations.

### Check the host and connect your desktop

On the host, run this whenever you need the current address, password, or
service status. Keep the password private:

```bash
~/.local/bin/ao remote-host status
~/.local/bin/ao status
systemctl --user status ao-self-hosted.service --no-pager
```

The tunnel may take a few seconds to publish its HTTPS address. If it does
not appear yet, rerun `remote-host status`. For service errors, run
`journalctl --user -u ao-self-hosted.service -n 100 --no-pager`.

1. On your laptop, use a desktop build **from this PR** for the source command,
   or an updated released AO desktop app for the release command. An older app
   may not have the Remote hosts screen.
2. Open **Settings → Remote hosts**, turn on **Connect to remote hosts**, and
   enter a label, the exact `Address:` and `Password:` from the host's status.
   Select **Add host**. Do not enter the host's public IP with plain HTTP.
3. The host should appear in the sidebar. Use **Add project on [host]** to
   register or clone code on its filesystem, then start a task in that
   project. In **Settings → Harness**, select that host to install/sign in to
   an agent there; laptop harness binaries and credentials are not copied.

For private Git clones, pushes, or PRs, configure Git/GitHub access separately
as that same host user; pairing AO does not forward laptop GitHub credentials.
On a quick tunnel, update the saved address in desktop Settings if the tunnel
URL changes after a restart; the Host ID and project data stay on the host.

On mobile, pair each machine in **Settings → Machines** using its current
address and password. Projects and workers from connected hosts appear
together; opening one targets its owning host. Pair a second desktop the
same way to continue a session.

## Other host notes

On macOS arm64/x64, the lower-level released installer also installs a
LaunchAgent, but it runs only while the host user is logged in. Install
`curl`, `git`, and `python3` first, plus `cloudflared` (for example with
`brew install cloudflared`) if using `--tunnel`. Before release, build the
PR's host bundle from a macOS checkout with Go 1.27.1+, Node 20.19+, npm,
and Xcode Command Line Tools, then pass the resulting
`frontend/dist-host/ao-host-darwin-<arch>.tar.gz` to the same
`scripts/setup-self-hosted.sh --bundle` command. A macOS bundle cannot be
used on Linux or vice versa.

For a container or another service manager, use `--install-only` with the
bundle or release installer and run the printed `ao daemon` command with
your own supervisor. To intentionally allow both direct LAN and the quick
tunnel, run `ao remote-host enable --tunnel` on the host; switching an
existing LAN listener to tunnel-only requires `ao remote-host disable`
first. Project builds and dev servers still need their own dependencies
on the host (for example, npm and `npm ci` for a Next.js project).

Harness install progress and sign-in terminals run on the selected host. Each
host keeps its own harness binaries and credentials; AO's bundled Chat adapter
is separate from installing Claude Code itself. Browser-callback provider
logins may require a browser or callback-port forwarding on the host. For
private Git clones, pushes, and PR tracking, run
`gh auth login` and `gh auth setup-git` as the host user.

To disconnect later, run `~/.local/bin/ao remote-host disable` on the host.
Removing a host in a client only removes that client's
saved connection; it does not stop the host or its sessions.

The daemon's normal unauthenticated listener remains on `127.0.0.1`. The
opt-in remote listener is password-protected but plain HTTP. Direct mode is
intended only for a trusted private network; tunnel-only mode binds it to
loopback. Do not expose its direct port to the public internet. Clients remember
a stable daemon-installation ID and
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
needs an OS service manager to survive logout or reboot. The desktop first
probes the normal SSE stream and falls back to two-second polling if stream
events are not delivered; terminals continue over WebSocket.

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
