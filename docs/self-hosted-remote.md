# Self-hosted remote hosts (experimental)

An AO session belongs to the daemon that started it. A desktop or phone is a
client of that daemon; closing or switching clients does not move the session.
Each client saves its own host connections. There is no shared host directory
or new global database. The existing AO Cloud path is separate.

## Connect a machine

1. Run the AO daemon on the machine that will own the sessions (`ao daemon`).
   Use your OS service manager if it must survive logout/reboot.
2. Run `ao remote-host enable` **on that machine** for private-network access.
   For access from elsewhere, install `cloudflared` on the host and use
   `ao remote-host enable --tunnel` to opt into the existing Connect Mobile
   Cloudflare quick tunnel. It prints the stable
   Host ID, connection password, and available addresses; the HTTPS tunnel
   address may take a few seconds to appear in `ao remote-host status`. Paste
   that address into the desktop's host settings. `ao remote-host disable`
   closes the listener and tunnel.
3. On each desktop client, enable **Settings → General → Remote hosts
   (experimental)**, then add the address/password in **Settings → Remote hosts**.
   Repeat for as many remote machines as needed. Projects on each host appear
   in the normal sidebar with a host badge. Use **Projects → +** or **Add project
   on [machine]** to register code on a host, then use that project's **New
   task** action to start a worker there.
4. On mobile, pair each machine in **Settings → Machines**. Projects and workers
   from connected hosts appear together; opening one targets its owning host
   without switching machines. Pair the same machine on a second laptop to
   continue the same host-owned session.

The daemon's normal unauthenticated listener remains on `127.0.0.1`. The
opt-in remote listener is password-protected but plain HTTP, intended only for
a trusted private network or an encrypted tunnel. Do not expose it directly
to the public internet. Clients remember a stable daemon-installation ID and
reject an address that later answers as a different host when connecting.
An address reassigned while a client stays connected may receive one
credentialed request before the client detects it. Use only a trusted private
network or encrypted tunnel; this is not protection against an active network
attacker or a copied AO data directory. Desktop connection passwords live in
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
session workflow. Choose the host in desktop Harness settings to install and
sign in to agents there. Device-code and terminal login flows can run on a
headless host; provider logins that require a browser callback on the host
still need a browser or forwarding there. The Browser tab can show host
workspace files and managed app previews started with `ao preview start`.
Manually registered host-local URLs are not proxied to the client. AO's Files
editor works remotely; opening a host file in an editor installed on the
client still needs a separately configured remote workspace connection.
Previewed apps should use relative asset and API URLs; hard-coded
`localhost` URLs still point at the viewing device.

Push notifications carry the owning host ID; a tap for a different selected
host opens the board instead of acting on a same-ID session there. Older pushes
without a host ID also open the board.
