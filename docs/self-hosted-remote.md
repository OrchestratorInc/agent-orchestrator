# Self-hosted remote hosts (experimental)

An AO session belongs to the daemon that started it. A desktop or phone is a
client of that daemon; closing or switching clients does not move the session.
Each client saves its own host connections. There is no shared host directory
or new global database. The existing AO Cloud path is separate.

## Set up a host

These are complete commands for a **fresh Ubuntu 24.04 x64 host**, run as a
normal user with `sudo` access. The machine needs internet access and a working
systemd user session. Do not run the installer as root. Windows is not a native
host for this feature. macOS arm64/x64 is supported by the installer, but the
fresh-machine build commands below are Linux-specific.

| When | Host steps | Laptop app |
| --- | --- | --- |
| Right now, before this PR is released | 1 → 2A → 3 | Desktop build from this PR |
| After this PR is merged **and** published | 1 → 2B → 3 | Updated released desktop app |

The release one-liner cannot install this PR's unpublished daemon. Both paths
use the same host installer and desktop-pairing steps.

### 1. SSH in and prepare Ubuntu (both choices)

From your laptop, replace the key path, user, and host address:

```bash
ssh -i /path/to/private-key USER@HOST_ADDRESS
```

Run the following **on the host**, not on the laptop:

```bash
uname -s    # must print Linux
uname -m    # must print x86_64 for these commands
sudo apt-get update
sudo apt-get install -y ca-certificates curl git python3
sudo loginctl enable-linger "$(id -un)"
systemctl --user show-environment >/dev/null
```

If the last command fails, fix the host's systemd user session before
continuing; the installer needs it to start AO and keep it running after SSH
logout. The host must also remain powered on.

For access **away from the host's private network**, install `cloudflared`
before step 2 and use `--tunnel` in your chosen install command. On Ubuntu
24.04, these are [Cloudflare's package-repository steps](https://pkg.cloudflare.com/):

```bash
sudo mkdir -p --mode=0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared noble main' | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt-get update
sudo apt-get install -y cloudflared
```

On a trusted LAN or private VPN, skip `cloudflared` and omit `--tunnel`; never
expose the direct plaintext listener to the public internet. `--tunnel` uses
an HTTPS Cloudflare quick tunnel and binds AO's authenticated listener only
to `127.0.0.1`. The quick-tunnel URL changes when it restarts.

### 2A. Install the current PR before release

The source directory does **not** exist on a fresh host. First install build
tools, then clone this PR branch into `~/ao-host-build`. The build is native:
do it on the Ubuntu x64 host (or another Linux x64 builder), not on a macOS
laptop for a Linux host. Go 1.27.1 is required by `backend/go.mod`; the
commands below install Go and Node only under your user account. The Go
checksum comes from [go.dev/dl](https://go.dev/dl/) and the Node checksum is
checked against Node's published `SHASUMS256.txt`.

```bash
sudo apt-get install -y build-essential pkg-config xz-utils
mkdir -p "$HOME/.local/ao-build-tools"
cd "$HOME/.local/ao-build-tools"
curl -fsSLO https://go.dev/dl/go1.27.1.linux-amd64.tar.gz
printf '%s  %s\n' 63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445 go1.27.1.linux-amd64.tar.gz | sha256sum -c -
tar -xzf go1.27.1.linux-amd64.tar.gz
curl -fsSLO https://nodejs.org/dist/v22.23.2/node-v22.23.2-linux-x64.tar.xz
curl -fsSLO https://nodejs.org/dist/v22.23.2/SHASUMS256.txt
grep '  node-v22.23.2-linux-x64.tar.xz$' SHASUMS256.txt | sha256sum -c -
tar -xJf node-v22.23.2-linux-x64.tar.xz
export PATH="$HOME/.local/ao-build-tools/go/bin:$HOME/.local/ao-build-tools/node-v22.23.2-linux-x64/bin:$PATH"
go version
node --version
npm --version
```

Stay in the **same SSH shell** so that `PATH` still includes those build
tools. Clone and build the PR, then run its installer with the bundle you
just created:

```bash
git clone --single-branch --branch codex/remote-hosts-integrated https://github.com/Untrivial-ai/agent-orchestrator.git "$HOME/ao-host-build"
cd "$HOME/ao-host-build/frontend"
npm ci
npm run build:daemon
npm run build:tmux
npm run build:acp-runtime
npm run build:host
cd "$HOME/ao-host-build"
./scripts/setup-self-hosted.sh --bundle "$HOME/ao-host-build/frontend/dist-host/ao-host-linux-x64.tar.gz" --tunnel
```

If you chose a trusted LAN/private VPN instead, omit `--tunnel` on the last
line. The script installs AO under `~/.ao/host`, starts the systemd user
service, and prints the Host ID, address, and connection password. It does
not install a desktop window on the host. On a machine with an existing AO
daemon, resolve that conflict instead of overwriting it; this walkthrough
assumes a fresh host.

### 2B. Install after this PR is merged **and published**

Skip all of step 2A: no repository checkout, Go, Node, npm, or local build is
needed. After step 1, run this on the host for an off-network connection:

```bash
bash -c 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/setup-self-hosted.sh | bash -s -- --tunnel'
```

For a trusted LAN/private VPN, omit `--tunnel`:

```bash
bash -c 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/setup-self-hosted.sh | bash'
```

The script fetches the latest published desktop release, verifies its SHA-256
digest, and installs only its daemon, AO Chat adapter runtime (currently
including the Claude Code ACP bridge), and tmux. It does **not** install
Claude Code, OpenCode, Codex, or their credentials. Run it again later to
upgrade AO; project and conversation data remain under `~/.ao/data`.

### 3. Check the host and connect your desktop (both choices)

On the host, run this whenever you need the current address, password, or
service status. Keep the password private:

```bash
~/.ao/host/current/resources/daemon/ao remote-host status
systemctl --user status ao-self-hosted.service --no-pager
```

The tunnel may take a few seconds to publish its HTTPS address. If it does
not appear yet, rerun `remote-host status`. For service errors, run
`journalctl --user -u ao-self-hosted.service -n 100 --no-pager`.

1. On your laptop, use a desktop build **from this PR** for step 2A, or an
   updated released AO desktop app for step 2B. An older released desktop
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

On macOS arm64/x64, the released installer in step 2B also installs a
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
private Git clones, pushes, and PR tracking, install `gh`, then run
`gh auth login` and `gh auth setup-git` as the host user.

To disconnect later, run `~/.ao/host/current/resources/daemon/ao remote-host
disable` on the host. Removing a host in a client only removes that client's
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
