# ao remote-host

Turn this machine into a remote host that the user's other devices can use, and
link it to their Orchestrator account so it shows up on every desktop and phone signed in
to that account.

## Syntax

```
ao remote-host status [--show-password]
ao remote-host enable [--tunnel-only | --tunnel]
ao remote-host disable
ao remote-host link [--label NAME] [--no-qr]
ao remote-host pair
ao remote-host devices
ao remote-host devices revoke <ID> | --all
ao remote-host require-device-credentials on|off
```

## Commands

| Command | What it does |
|---|---|
| `status` | Show the host ID, addresses, and whether the address is fixed. `--show-password` also prints the pairing password. |
| `enable` | Turn on the authenticated remote listener. `--tunnel-only` serves it only through a Cloudflare tunnel. Prints a one-time pairing code. |
| `disable` | Turn off remote access. Sessions keep running. |
| `link` | Print a sign-in link and a QR code, wait up to 5 minutes for the user to sign in from any device, then add this machine to their Orchestrator account with a fixed `https://h-….useao.dev` address. Turns on tunnel-only remote access if it is off, and adds a Cloudflare tunnel to a LAN-only listener. Safe to run again: the same account keeps its credential and the name it set, unless `--label` gives a new one. |

| `pair` | Print the address and a one-time pairing code. One device can use the code, within 10 minutes; it gets its own token. |
| `devices` | List what can reach this machine: devices with their own token, phones still on the pairing password, and the linked account's devices as one shared row. |
| `devices revoke` | Revoke one device by its ID, or with `--all` every device, the pairing password and the account link. |
| `require-device-credentials` | `on` stops accepting the pairing password, so only device tokens and the account token work. |

## Rules

- Never write anywhere the `Pairing code:` line from `enable`, `pair` or the
  installer, or a `Password:` line from an older release. Give a pairing code
  only to the user who asked to pair a device. `status` does not print the
  password; do not pass `--show-password`.
- The user must open the sign-in link themselves. Give them the link and the
  code it shows; never sign in for them.
- `link` blocks until the sign-in finishes. Without a terminal, run it in the
  background and read its log.

## Set up a fresh VM over SSH

The VM needs Linux on x64 or arm64 with glibc 2.34 or newer and `apt-get` or
`dnf` (for example Ubuntu 24.04 or Amazon Linux 2023), outbound internet, and a
non-root user with `sudo`. No inbound ports.

1. Check the machine. Expect `x86_64` or `aarch64`, glibc 2.34 or newer, a
   package manager path, and `sudo ok`:

   ```bash
   ssh USER@HOST 'uname -m; getconf GNU_LIBC_VERSION; command -v apt-get dnf; sudo -n true && echo sudo ok'
   ```

   Without `sudo ok`, `sudo` needs a password you cannot type. Give the user the
   step 2 command without `ssh USER@HOST` to run in their own SSH session, then
   continue at step 3.

2. Install Orchestrator and its service. With no terminal the installer skips sign-in and
   prints pairing details; do not repeat its `Pairing code:` line. `pipefail` makes
   a failed download exit non-zero:

   ```bash
   ssh USER@HOST 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash'
   ```

   This installs stable. For nightly, end it with `bash -s -- --nightly`. On
   arm64, if it stops with `No verified release asset: ao-host-linux-arm64.tar.gz`,
   that release predates arm64 support: end it with `bash -s -- --source-ref main`
   to build from the main branch (several minutes).

   If the user's network does not allow a Cloudflare tunnel, end it with
   `bash -s -- --lan` instead and skip steps 3 to 6. The user then reaches the VM
   over a VPN or trusted LAN and adds it in **Settings → Remote hosts → Add
   host** with the `Address:` and code from `~/.local/bin/ao remote-host pair`.

3. Start the sign-in in the background, then read the link:

   ```bash
   ssh USER@HOST 'nohup ~/.local/bin/ao remote-host link --no-qr > ~/.ao/link.log 2>&1 < /dev/null & sleep 5; cat ~/.ao/link.log'
   ```

4. Give the user the `https://…/device?user_code=…` link and its code. Ask them
   to open it, check that the page shows the same code, and sign in within 5
   minutes.

5. When they confirm, read the log again. Success is
   `Linked "<name>" to <email>.` or `already linked`, then
   `Address: https://h-….useao.dev`. If it says the link expired, repeat step 3.

6. Remove the log: `ssh USER@HOST 'rm -f ~/.ao/link.log'`.

7. Make the VM ready for work. Run each check over SSH and fix what it reports:
   - `~/.local/bin/ao agent ls --refresh` must show the user's coding agent
     `installed` and `authorized`. You cannot install or sign in to an agent for
     them: ask them to open **Settings → Harness** in the desktop app, select
     the VM, and install and sign in there.
   - `gh auth status` must show a signed-in account, for private clones and pull
     requests. Otherwise ask the user to run `gh auth login` and then
     `gh auth setup-git` in their own SSH session; never handle their token.
   - Add their repository on the VM with steps 3 to 5 of [setup.md](setup.md):
     run `git clone` on the VM and each `ao` command as
     `ssh USER@HOST '~/.local/bin/ao …'`.

Tell the user that the machine appears within about 30 seconds on devices
signed in to that account, and that the desktop app looks for new machines only
while its window is open.

## If it does not show up

| Symptom | Fix |
|---|---|
| Not listed on the user's desktop | The Orchestrator window must be open and signed in to the same account. On the host, `status` shows `Fixed address`. |
| Listed but does not connect, on a desktop that paired by hand earlier | The host's password changed since. The user removes the host in **Settings → Remote hosts**; it comes back with the account's credential within 30 seconds. |
| Link expired | Run `ao remote-host link` again. |

Full guide: [docs/self-hosted-remote.md](https://github.com/OrchestratorInc/agent-orchestrator/blob/main/docs/self-hosted-remote.md).
