# Self-hosted remote hosts (experimental)

Sessions stay on the machine that started them. Desktop and mobile are clients.
AO account sign-in is required for desktop remote hosts; Cloud sandbox execution is separate.

## Install on Ubuntu

On a fresh Ubuntu 24.04 x64 machine, SSH in as a non-root user with `sudo`:

```bash
ssh -i /path/to/private-key USER@HOST_ADDRESS
```

Run **one** command on the VM. Each installs AO, prerequisites, a persistent
user service, and a Cloudflare quick tunnel, then prints the connection details.

**Nightly users** (installs the newest published nightly without building from source):

```bash
bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash -s -- --nightly'
```

Use a nightly desktop app with a nightly host. Updating your laptop alone does
not update the VM. Until stable includes account linking, use this command for
the pair-once, automatic-discovery flow below. Nightlies are experimental.

**Testing an unreleased branch** (builds from that branch): replace
`codex/your-pr-branch` with the branch name published on GitHub.

```bash
SOURCE_REF=codex/your-pr-branch bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL "https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/${SOURCE_REF}/scripts/bootstrap-self-hosted.sh" | bash -s -- --source-ref "${SOURCE_REF}"'
```

**Stable users** (installs the latest published stable binary, not nightly):

```bash
bash -c 'set -o pipefail; sudo apt-get update && sudo apt-get install -y curl && curl -fsSL https://raw.githubusercontent.com/Untrivial-ai/agent-orchestrator/main/scripts/bootstrap-self-hosted.sh | bash'
```

For a trusted LAN/VPN instead, append `--lan` to the script arguments
(`bash -s -- --lan` for the release command). This uses plaintext HTTP; never
expose the port publicly. The VM must stay powered on.

Re-run the command to upgrade without deleting sessions. The installer uses
`~/.ao/host`; it does not copy agent or GitHub credentials from your laptop.

## Check and connect

On the VM, get the current address and password (keep it private):

```bash
~/.local/bin/ao remote-host status
```

If the HTTPS address is not ready, rerun the command after a few seconds.

For private Git clones, pushes, or PRs, authenticate on the VM as the AO user:

```bash
gh auth login
gh auth setup-git
gh auth status
```

Pairing does not copy GitHub credentials from your laptop.

1. On one laptop, turn on **Developer mode** in **Settings → General** and sign
   in to AO Cloud. In **Settings → Remote hosts**, add the VM's name, exact
   `Address:` and `Password:`. Alternatively, sign in on mobile under
   **Settings → Account** and pair it once under **Settings → Machines**.
2. Sign in to the same AO account on your other laptops and phones. Hosts,
   projects, and sessions load automatically; enable **Developer mode** on each
   desktop client. A running client may take up to 30 seconds to find a newly
   linked host.
3. Use **Projects +** to clone or import a project on the VM. In desktop
   **Settings → Harness**, select the VM to install/sign in to an agent there.

The host publishes a changed quick-tunnel address to AO Cloud automatically.
Desktop connections saved before account linking keep their original pairing
password and are not attributed to whichever account signs in next. Re-pair
only if you want to replace that local credential with an account-scoped one.

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
