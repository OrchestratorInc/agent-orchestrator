# ao remote-host

Turn this machine into a remote host that the user's other devices can use, and
link it to their AO account so it shows up on every desktop and phone signed in
to that account.

## Syntax

```
ao remote-host status
ao remote-host enable [--tunnel-only | --tunnel]
ao remote-host disable
ao remote-host link [--label NAME] [--no-qr]
```

## Commands

| Command | What it does |
|---|---|
| `status` | Show the host ID, addresses, and whether the address is fixed. It also prints the pairing password. |
| `enable` | Turn on the authenticated remote listener. `--tunnel-only` serves it only through a Cloudflare tunnel. |
| `disable` | Turn off remote access. Sessions keep running. |
| `link` | Print a sign-in link and a QR code, wait up to 5 minutes for the user to sign in from any device, then add this machine to their AO account with a fixed `https://h-….useao.dev` address. Turns on tunnel-only remote access if it is off, and adds a Cloudflare tunnel to a LAN-only listener. Safe to run again: the same account keeps its credential and the name it set, unless `--label` gives a new one. |

## Rules

- Never show the user, or write anywhere, the `Password:` line from `status`,
  `enable`, or the installer. Filter it: `ao remote-host status | grep -v '^Password'`.
- The user must open the sign-in link themselves. Give them the link and the
  code it shows; never sign in for them.
- `link` blocks until the sign-in finishes. Without a terminal, run it in the
  background and read its log.

## Set up a fresh VM over SSH

The VM needs Ubuntu 24.04 x64, outbound internet, and a non-root user with
`sudo`. No inbound ports.

1. Install AO and its service. With no terminal the installer skips sign-in and
   prints pairing details; do not repeat its `Password:` line. `pipefail` makes
   a failed download exit non-zero:

   ```bash
   ssh USER@HOST 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash'
   ```

   This installs stable. For nightly, end it with `bash -s -- --nightly`.

2. Start the sign-in in the background, then read the link:

   ```bash
   ssh USER@HOST 'nohup ~/.local/bin/ao remote-host link --no-qr > ~/.ao/link.log 2>&1 < /dev/null & sleep 5; cat ~/.ao/link.log'
   ```

3. Give the user the `https://…/device?user_code=…` link and its code. Ask them
   to open it, check that the page shows the same code, and sign in within 5
   minutes.

4. When they confirm, read the log again. Success is
   `Linked "<name>" to <email>.` or `already linked`, then
   `Address: https://h-….useao.dev`. If it says the link expired, repeat step 2.

5. Remove the log: `ssh USER@HOST 'rm -f ~/.ao/link.log'`.

Tell the user that the machine appears within about 30 seconds on devices
signed in to that account, and that the desktop app looks for new machines only
while its window is open.

## If it does not show up

| Symptom | Fix |
|---|---|
| Not listed on the user's desktop | The AO window must be open and signed in to the same account. On the host, `status` shows `Fixed address`. |
| Listed but does not connect, on a desktop that paired by hand earlier | The host's password changed since. The user removes the host in **Settings → Remote hosts**; it comes back with the account's credential within 30 seconds. |
| Link expired | Run `ao remote-host link` again. |

Full guide: [docs/self-hosted-remote.md](https://github.com/OrchestratorInc/agent-orchestrator/blob/main/docs/self-hosted-remote.md).
