# Self-hosted remote hosts (experimental)

Sessions stay on the machine that started them. Desktop and mobile are clients.
AO account sign-in is required for desktop remote hosts; Cloud sandbox execution is separate.

## Set up a VM

You need a fresh Ubuntu 24.04 x64 machine with outbound internet and a
non-root user with `sudo`. No inbound ports: devices reach the VM through a
Cloudflare tunnel.

1. SSH in:

   ```bash
   ssh -i /path/to/private-key USER@HOST_ADDRESS
   ```

2. Install AO:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash
   ```

   This installs the prerequisites, the latest stable AO, a user service that
   keeps running after you log out, and the tunnel. For the newest nightly
   instead, end the command with `bash -s -- --nightly`. On an image without
   curl, run `sudo apt-get install -y curl` first.

3. Sign in. The installer ends with a sign-in link and a QR code. Open the link
   on any device, or scan the QR code with your phone, check that the page
   shows the same code as the terminal, and sign in to your AO account. The
   link expires after 5 minutes; run `~/.local/bin/ao remote-host link` for a
   new one. Press Ctrl-C to skip signing in; the installer then prints the
   address and password for pairing by hand.

4. Check on the VM:

   ```bash
   ~/.local/bin/ao remote-host status
   ```

   It shows `Fixed address: it stays the same after restarts.` and an
   `https://h-….useao.dev` address. It also prints the pairing password, so
   do not paste its output anywhere.

5. On your laptop or phone, sign in to the same AO account. The VM appears
   within about 30 seconds, under the VM's hostname. The desktop app looks for
   new machines only while its window is open.

The VM uses your sign-in once, to add itself to your account; it does not keep
it. Re-run the install command to upgrade without deleting sessions; a
linked VM stays linked and skips the sign-in. The
installer uses `~/.ao/host`; it does not copy agent or GitHub credentials from
your laptop.

Use the same channel on the VM and your desktop: stable with stable, nightly
with nightly. Updating your laptop alone does not update the VM. A release
without `ao remote-host link` prints an address and password instead of the
sign-in link; pair it by hand, or install nightly.

### Other install options

**Nightly** (the newest published nightly; experimental):

```bash
curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash -s -- --nightly
```

**An unreleased branch** (builds from that branch): replace both
`codex/your-pr-branch` with the branch name published on GitHub.

```bash
curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/codex/your-pr-branch/scripts/bootstrap-self-hosted.sh | bash -s -- --source-ref codex/your-pr-branch
```

**A trusted LAN/VPN** instead of the tunnel: append `--lan` to the script
arguments (`bash -s -- --lan`, or `bash -s -- --nightly --lan`). This uses plaintext HTTP and pairing
by hand; never expose the port publicly. The VM must stay powered on.

## Set up a VM as an agent

An agent that runs commands over SSH has no terminal, so the installer skips
the sign-in step and prints pairing details instead. The sign-in link needs
the user, and `ao remote-host link` waits up to 5 minutes for them, so run it
in the background and read its output:

1. Install:

   ```bash
   ssh USER@HOST 'set -o pipefail; curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash'
   ```

   For nightly, end it with `bash -s -- --nightly`.

   `pipefail` makes a failed download exit non-zero. The output ends with a
   `Password:` line. Do not repeat it to the user or write it anywhere.

2. Start the sign-in and read the link:

   ```bash
   ssh USER@HOST 'nohup ~/.local/bin/ao remote-host link --no-qr > ~/.ao/link.log 2>&1 < /dev/null & sleep 5; cat ~/.ao/link.log'
   ```

   Add `--label "Name"` to choose the name devices show; the default is the
   name the account already has for the VM, else its hostname.

3. Give the user the `https://…/device?user_code=…` link and its code. Ask them
   to open it, check the code, and sign in within 5 minutes.

4. When they confirm, run `ssh USER@HOST 'cat ~/.ao/link.log'` again. Success
   reads `Linked "<name>" to <email>.` (or `already linked`) followed by
   `Address: https://h-….useao.dev`. If it says the link expired, repeat
   step 2.

5. Remove the log: `ssh USER@HOST 'rm -f ~/.ao/link.log'`.

To check the host later without exposing the password, run
`~/.local/bin/ao remote-host status | grep -v '^Password'`.

## Use the VM

To link a VM that is already installed, or to move it to another account, run
`~/.local/bin/ao remote-host link` on the VM and sign in as above.

For private Git clones, pushes, or PRs, authenticate on the VM as the AO user:

```bash
gh auth login
gh auth setup-git
gh auth status
```

Pairing does not copy GitHub credentials from your laptop.

1. Link the VM with `ao remote-host link` as above. To pair by hand instead,
   sign in to AO on one laptop and in **Settings → Remote hosts → Add host**
   enter the VM's name, exact `Address:` and `Password:`; or sign in on mobile
   under **Settings → Account** and pair it once under **Settings → Machines**.
2. Sign in to the same AO account on your other laptops and phones. Hosts,
   projects, and sessions load automatically. A running client may take up to
   30 seconds to find a newly linked host.
3. Use **Projects +** to clone or import a project on the VM. In desktop
   **Settings → Harness**, select the VM to install/sign in to an agent there.

The host publishes a changed quick-tunnel address to AO Cloud automatically.
Desktop connections saved before account linking keep their original pairing
password and are not attributed to whichever account signs in next. Re-pair
only if you want to replace that local credential with an account-scoped one.

## If the VM does not show up

| Symptom | Check |
| --- | --- |
| Not listed on the laptop | Open the AO window: the desktop looks for new machines only while it is visible. Sign in to the same AO account as the VM. On the VM, `remote-host status` shows `Fixed address`. |
| Listed but does not connect, after you paired it by hand | That desktop keeps the password it paired with. If the VM's password changed since, remove the host in **Settings → Remote hosts**; it comes back with the account's credential within 30 seconds. |
| The sign-in link expired | Run `~/.local/bin/ao remote-host link` again. |
| No sign-in link at the end of the install | You ran it without a terminal, pressed Ctrl-C, or used `--lan`. Run `~/.local/bin/ao remote-host link`; on a `--lan` machine this adds a Cloudflare tunnel next to the LAN listener. A release without `link` needs an update, or nightly. |

## Status and other hosts

On the host, use `~/.local/bin/ao status` for daemon status,
`systemctl --user status ao-self-hosted.service --no-pager` for the service,
or `journalctl --user -u ao-self-hosted.service -n 100 --no-pager` for logs.
Run `~/.local/bin/ao remote-host disable` to stop remote access.

For macOS or another service manager, use the
[lower-level installer](../scripts/setup-self-hosted.sh); pass `--nightly` to
select the nightly channel. On macOS it installs
a LaunchAgent that runs only while the user is logged in; for a container,
use `--install-only` and supervise `ao daemon` yourself. Windows is not a
native host. Projects still need their own build dependencies on the host.

Removing a saved connection on desktop/mobile does not stop the host or its
sessions.

## Security and limits

- AO's unauthenticated listener stays on `127.0.0.1`. The opt-in remote
  endpoint requires a password; `--lan` is plain HTTP, so use only a trusted
  LAN/VPN and never expose its port publicly.
- AO sign-in gates desktop discovery, not the VM's direct pairing endpoint.
  Pairing on one device issues a separate 256-bit host credential that AO Cloud
  stores encrypted and shares only with the signed-in account's devices. The
  original VM password is not uploaded. AO Cloud and signed-in devices can use
  the scoped credential to reach the VM; removing a saved host from an account
  does not invalidate a credential already imported on another device until it
  syncs. Pairing the VM to another account rotates the scoped credential.
  Regenerating the VM connection password revokes the scoped credential, so
  re-pair the host with the account afterward.
- Host-ID checks prevent connecting to the wrong host, but not an active
  network attacker or a copied AO data directory. Desktop passwords are stored
  in `~/.ao/remotes.json` (or `AO_DATA_DIR/remotes.json`) with owner-only access.
- Cloudflare terminates tunnel TLS and can see traffic. Quick tunnels have no
  uptime guarantee and change hostname on restart.
- AO can edit host files; opening them in a client-side external editor needs
  a separate remote workspace connection.
