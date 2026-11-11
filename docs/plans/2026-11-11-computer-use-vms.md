# Plan: Computer use in local virtual machines (Cua VMs)

Status: proposed (2026-11-11). Not implemented yet.

## Summary

Today, turning on **Let agents use this computer** gives chat agents tools that
read the screen and operate apps on the user's own desktop, through Cua Driver
installed on the host. Some users will not grant that, and will not send their
work to a cloud computer either.

This plan adds a third place for computer use to happen: a virtual machine on
the user's own machine.

- **Same tools, different computer.** Agents keep the eight computer-use tools
  they have now. Cua Driver runs inside the VM's guest desktop, and AO reaches
  it over SSH on the VM's private address. This is the arrangement Cua
  documents for VMs: "run the driver there and carry MCP over SSH".
- **Nothing on the host desktop is touched.** VM mode needs no CuaDriver.app on
  the host and no host Accessibility or Screen Recording grant. Nothing leaves
  the machine.
- **One seam changes in the backend.** `service/computeruse` already runs the
  driver through a three-method `Driver` interface. VM mode supplies a second
  implementation of it and a small VM lifecycle service; the tool surface,
  action checks, observation, screenshots, and MCP rendering stay as they are.
- **First release:** Apple silicon Macs, a macOS guest, managed with
  [Lume](https://github.com/trycua/cua/tree/main/libs/lume) (MIT), one
  AO-managed VM shared by the sessions that use computer use. Per-session
  disposable VMs, an in-app viewer, file transfer, and Linux and Windows hosts
  follow in later phases.

The decisions that still need an owner are in
[Open questions](#open-questions). The first one, whether isolating desktop
control is enough or the agent process must move into the VM too, changes the
size of the work.

## Inputs

1. The customer request: computer use without giving agents the host computer
   or a cloud computer.
2. The current integration in this repository (cited by path below).
3. Cua's documentation source at `trycua/cua` commit `f9a2768` (2026-11-11):
   Cua Driver 0.47.11, Lume 0.7.0. Facts taken from it are marked **(Cua
   docs)**. They describe another project's current behaviour and must be
   re-checked in the Phase 0 spike before anything is built on them.

## What a VM protects, and what it does not

This is the part most likely to be misread, so it comes first.

| Asset | Host mode (today) | VM mode (this plan) |
| --- | --- | --- |
| The user's screen, windows, and keyboard focus | Agents read and act on them | Never touched |
| Apps and accounts signed in on the host | Reachable through the desktop | Not reachable; the guest has its own apps and sign-ins |
| Host privacy grants (Accessibility, Screen Recording) | Required, held by CuaDriver.app | Not required |
| Files on the host | Visible through any app the agent opens | Not visible to the guest; no folder is shared in the first release |
| The session's worktree and shell | The agent process runs on the host | **Unchanged: the agent process still runs on the host** |
| The local network | Reachable | Reachable from the guest through NAT (see [Network](#network)) |

VM mode isolates **desktop control**: the screen, pointer, keyboard, and
apps that the computer-use tools drive. It does not move the agent harness
(Claude Code, Codex, and the rest), which keeps running where the session
runs, with its worktree and terminal. Putting the whole session inside a VM is
a different project, closer to
[self-hosted remote hosts](../self-hosted-remote.md); see
[Alternatives](#alternatives-considered).

## How computer use works today

```text
chat agent
  └─ ao mcp (stdio tool server, AO_COMPUTER_USE=1)        backend/internal/cli/mcp_cua.go
       └─ POST /api/v1/computer-use/call (loopback only)   backend/internal/httpd/controllers/computeruse.go
            └─ computeruse.Service.Call                    backend/internal/service/computeruse/
                 ├─ setting check, tool allowlist, action checks
                 ├─ Driver.Run(binary, "call", tool, json)  backend/internal/adapters/cuadriver/
                 │    └─ cua-driver CLI → CuaDriver daemon → host desktop
                 └─ screenshots saved under ~/.ao/computer-use/<session>
```

Properties the VM design keeps:

- **One switch.** `app_settings.computer_use` gates everything. New chat
  sessions get the tools and the standing prompt
  (`session_manager/computer_use_prompt.go`); `Service.Call` re-checks the
  setting on every call, so turning it off cuts off running sessions too.
- **A short tool list.** `list_windows`, `launch_app`, `bring_to_front`,
  `get_window_state`, `click`, `type_text`, `press_key`, `run_steps`. Their
  descriptions and schemas are read from the installed driver
  (`agenttools.go`).
- **Refusals that protect the person at the keyboard.** No foreground input,
  no whole-desktop scope, no pixel action without a window, no typing without
  a field (`actions.go`). The code says plainly that these catch agent
  mistakes and "are not a sandbox".
- **Images never travel inline to the agent's context as text.** The service
  writes screenshots to files under AO's data directory and the tool server
  attaches them as images (`images.go`).
- **Local control only.** `/api/v1/computer-use` and
  `/api/v1/settings/computer-use` are blocked on the LAN listener
  (`httpd/lan_listener.go`).

The seam this plan uses is the interface the service already depends on:

```go
// backend/internal/service/computeruse/computeruse.go
type Driver interface {
	Locate(ctx context.Context) (string, error)
	Run(ctx context.Context, binary string, args ...string) (stdout, stderr []byte, err error)
	StartDaemon(ctx context.Context, binary string) error
}
```

## Target architecture

```text
                          HOST (trusted)                         │        GUEST VM (untrusted)
                                                                 │
chat agent ── ao mcp ── POST /computer-use/call                  │
                              │                                  │
                   computeruse.Service                           │
                   (setting, allowlist, action checks,           │
                    observation, image store)                    │
                              │ picks the driver for the target  │
              ┌───────────────┴────────────────┐                 │
        host Driver                      vm Driver ── SSH ───────┼──► cua-driver CLI
   (adapters/cuadriver,           (adapters/cuadriver,           │        │ local socket
        unchanged)                 guest exec + image pull)      │    CuaDriver daemon
              │                          │                       │        │
        host desktop              computervm.Service             │    guest desktop,
                                  (ensure running, idle stop,    │    guest apps
                                   reset, facts in SQLite)       │
                                         │                       │
                                  VMHost port ── lume CLI ───────┼──► Virtualization.framework
                                  (adapters/vmhost/lume)         │
                                                                 │
        VM disks and keys: ~/.ao/vms/                            │
```

### Components

| Piece | Where | New or changed | Responsibility |
| --- | --- | --- | --- |
| Target setting | `app_settings`, `service/settings` | Changed | Where computer use happens: `host` or `vm`. The existing on/off switch stays. |
| Driver routing | `service/computeruse` | Changed | Hold one `Driver` per target and use the one the setting names, on every call. |
| Guest driver | `adapters/cuadriver` | New type beside `Driver` | Run `cua-driver` inside the guest over SSH and bring screenshots back. Process concerns only, as today. |
| VM lifecycle | `service/computervm` | New | Set up, start on demand, stop when idle, reset, delete. Owns the VM's durable facts. |
| VM host port | `ports`, `adapters/vmhost/lume` | New | The hypervisor tool's verbs: create, clone, run, stop, delete, address, open viewer. |
| Installer target | `service/systeminstall` | Changed | A `lume` target on the fixed installer allowlist, next to `cua-driver`. |
| Routes | `httpd/controllers/computeruse.go` | Changed | `/api/v1/computer-use/vm/...` for the settings page. |
| Settings page | `ComputerUseSection.tsx` | Changed | Choose the target; set up and manage the VM. |
| Agent guidance | `computer_use_prompt.go`, `skillassets/.../cua.md` | Changed | A VM variant of the standing prompt and a VM section in the guide. |

Nothing changes in `ao mcp`, the MCP rendering, the `call` DTOs, or the tool
allowlist.

### A call in VM mode

1. The agent calls `click`. `ao mcp` posts to `/api/v1/computer-use/call`, as
   today. The request carries no target; the caller cannot choose one.
2. `Service.Call` checks the on/off setting, the tool allowlist, and the
   action checks, unchanged.
3. The service reads the target setting and takes the `vm` driver.
4. The driver asks `computervm` for a running VM. If the VM is stopped it is
   started and the call waits for the guest's driver to answer; the route's
   existing two-minute budget covers a cold boot.
5. The driver runs `cua-driver call click '<json>'` in the guest over SSH and
   returns stdout, stderr, and the exit status, the same triple the host
   driver returns.
6. Session naming, revival of an ended driver session, the post-action window
   read, and the tree diff all run in the service as they do today; each is
   another guest command.
7. Screenshots come back over the same SSH connection (below) and are saved
   under `~/.ao/computer-use/<session>`. `ao mcp` attaches them as images.

### Why the CLI over SSH, not MCP over SSH

Cua's guide registers `ssh … cua-driver mcp` as a stdio server **(Cua docs)**.
AO's service is built on one-shot CLI commands instead (`call`, `describe`,
`status`, `--version`, `doctor`), and everything it adds on top (sessions,
revival, observation, refusal hints) is written against that output. On macOS
the CLI and `mcp` both proxy to the daemon in the logged-in guest session, and
Cua's Windows-over-SSH recipe uses `cua-driver call` directly **(Cua docs)**.
Running the same commands remotely keeps one code path for both targets.

The cost is one SSH exec per command. AO holds one long-lived SSH connection
per VM and opens a channel per command, so the cost is a channel open on a
loopback-speed link. A persistent MCP session over that connection is a later
optimisation if the spike shows it matters.

### Screenshots cross the boundary as bytes

In host mode the service hands the driver a file path under `~/.ao` and the
driver writes the PNG there. A guest cannot write to a host path, and sharing
a host folder into the guest for this purpose is the wrong fix: a guest that
can write to a shared folder can also put a symlink there, and the host would
follow it when it reads the "screenshot". That turns a compromised guest into
a way to read host files and hand them to a model.

So, for the VM driver:

- The driver gives the guest a path in a guest temp folder in place of the
  host path, then pulls that file's bytes over the SSH connection.
- AO checks the size against the 10 MB cap the tool server already applies
  to an attached screenshot and the content type against the image types
  `images.go` already accepts, then writes the file itself, under the name
  AO chose.
- A path that appears in guest output is never opened on the host. Guest
  output is data.

This needs two small additions to the `Driver` contract (map an image path
for the target; collect an image afterwards) that are no-ops for the host
driver. The exact shape is an implementation detail for the PR.

## The VM

### Substrate for the first release: Lume

Lume is Cua's MIT-licensed CLI for macOS and Linux VMs on Apple's
Virtualization.framework **(Cua docs)**. It is the documented way to run Cua
Driver in a macOS VM, and it has the verbs AO needs: `create`, `pull`,
`clone`, `run --detach --display none`, `stop`, `delete`, `get --format json`
(including the guest's IP address), `attach`, `ssh`, `cp`.

Constraints that shape the design **(Cua docs)**:

- Apple silicon and macOS 13 or later only. Intel Macs are not supported.
- At most two macOS guests run at once. This is Apple's limit and it rules
  out one macOS VM per session as the default.
- A guest defaults to 4 CPUs, 8 GB of memory, and a 100 GB sparse disk. Lume
  asks for 16 GB of host memory for an 8 GB guest and 50 GB of free disk per
  macOS guest, plus its restore image.
- macOS privacy consent for CuaDriver.app must be given in the VM's display.
  It cannot be granted over SSH.
- Lume's HTTP API listens on `localhost:7777` with no authentication, and its
  installer starts that service at login unless told not to.
- Networking is NAT or bridged. There is no "no network" mode.

AO drives the `lume` CLI as one-shot commands and does not use the HTTP API.
The installer target passes `--no-background-service`, so AO does not leave
an unauthenticated VM control port running that it has no use for.

### Where VM state lives

Lume keeps VMs in `~/.lume` by default. AO-created VMs are AO state, and AO
state lives under `~/.ao` only (`AGENTS.md`, hard rules). Every Lume command
AO runs passes `--storage <AO data dir>/vms`, which Lume accepts as a direct
path **(Cua docs)**:

```text
~/.ao/vms/
  ao-computer-base/      golden image: set up once, kept stopped
  ao-computer/           the working VM agents use; a clone of the base
  keys/                  AO's SSH key pair and the guest's pinned host key
```

The `lume` binary and Lume's own configuration belong to Lume, the same way
CuaDriver.app belongs to Cua. AO does not write to `~/.lume`.

### One-time setup

Setup is a guided flow in **Settings > Computer use agents**, because one step
needs the user:

1. **Preflight.** Apple silicon, macOS version, free memory, free disk. Refuse
   early with the number that is short.
2. **Install Lume** through the daemon's installer allowlist, like Cua Driver.
3. **Create the base VM.** Default: build it from Apple's restore image with
   Lume's unattended Tahoe preset (`lume create … --ipsw latest --unattended
   tahoe`), which leaves System Integrity Protection on and puts nothing in
   the image that did not come from Apple or from AO's own steps. Faster
   alternative: pull Cua's published `macos-tahoe-cua` image, which ships with
   SIP disabled **(Cua docs)**. Which is the default is an
   [open question](#open-questions).
4. **Install Cua Driver in the guest** with its official installer, over SSH.
5. **Grant consent in the VM window.** AO opens the VM's display and polls
   `cua-driver permissions status --json` in the guest until Accessibility
   and Screen Recording are granted, the same status the host flow shows
   today. On Tahoe there are two more prompts to approve (System Events
   control and direct screen capture) **(Cua docs)**; setup runs one read of
   each kind so they appear now and not in an agent's first call.
6. **Lock down access.** Install AO's public key, record the guest's SSH host
   key, and turn off password sign-in over SSH. The unattended preset's
   account is `lume` with password `lume` **(Cua docs)**; see
   [Open questions](#open-questions) for whether AO also rotates it.
7. **Stop and clone.** The base stays stopped as the golden image. Agents use
   the clone.

A clone keeps the base's privacy grants as long as CuaDriver.app keeps its
release signature **(Cua docs)**, so consent is given once.

### Lifecycle

`computervm` stores facts and derives status, following the rule the rest of
the backend follows.

- **Stored** (new table, new migration): VM id, provider (`lume`), guest OS,
  base name, working VM name, resources, created and last-reset times, the
  Cua Driver version installed in the guest.
- **Derived at read time:** `absent`, `setting_up`, `stopped`, `starting`,
  `ready`, `error`, from the provider's report and whether the guest's driver
  answers. A probe that fails or times out is `unknown`; it is never treated
  as proof the VM is gone, and it never triggers a delete.

Behaviour:

- **Start on demand.** The first call after a stop boots the VM headless
  (`--detach --display none`) and waits for SSH and the driver.
- **Stop when idle.** After a period with no calls and no open viewer, the VM
  is shut down to give back its memory. The period is a setting.
- **Reset.** Delete the working VM and clone the base again. This is the
  answer to "an agent left it in a mess" and to "something in there looks
  compromised". It discards everything in the working VM, so the settings
  page confirms it.
- **Daemon restart.** The VM is a detached process owned by `lume run` and
  outlives the daemon, like Cua Driver's own service. On start the daemon
  re-derives status; it does not assume.
- **Delete.** Removes the working VM, the base, and the keys under
  `~/.ao/vms`.

### Watching and stepping in

Users need to see the VM, and sometimes to act in it: sign in to an app, pass
a captcha, approve a prompt.

- **First release:** an **Open VM window** action that runs `lume attach`,
  which opens Lume's own viewer. The VNC listener stays off (`--vnc
  disabled`) because nothing needs it.
- **Later:** a view of the VM inside the app. This is a visual decision and
  goes through `DESIGN.md` in its own PR; it is out of scope here.

Lume syncs the clipboard both ways automatically when its native display is
open **(Cua docs)**. That gives the guest the host's clipboard while the user
watches. The spike must find out whether this can be turned off; if not, the
first release says so next to the button.

## Settings, API, and CLI

### Setting

A new column in `app_settings` (new migration; the merged ones are not
edited):

```sql
computer_use_target TEXT NOT NULL DEFAULT 'host'  -- 'host' | 'vm'
```

- `computer_use` stays the on/off switch and keeps its meaning.
- Turning computer use on checks that the chosen target is ready. Today that
  check is "Cua Driver is installed" (`settings.SetCuaDriverInstalled`); it
  becomes "the target is ready", which for `vm` means the VM is set up.
- The target is read on every call, in the daemon. Switching to `vm` takes
  the host desktop away from every session at once, including sessions that
  started in host mode, the same way turning the switch off does today.
- VM mode does not need the host driver. If CuaDriver.app is installed on the
  host with its grants while the target is `vm`, the settings page says so:
  the daemon will not use it, but any local process still could.

A per-project override is deliberately left out of the first release.

### Routes

All under the existing prefix, so they inherit the LAN block and stay
loopback-only:

| Route | Purpose |
| --- | --- |
| `GET /api/v1/computer-use/status` | Gains `target` and, in VM mode, the guest driver's status in place of the host's. |
| `GET /api/v1/computer-use/vm` | VM facts and derived status, host preflight result. |
| `POST /api/v1/computer-use/vm/setup` | Start or resume one-time setup; progress is read from `GET …/vm`. |
| `POST /api/v1/computer-use/vm/start`, `…/stop` | Manual start and stop. |
| `POST /api/v1/computer-use/vm/reset` | Re-clone the working VM from the base. |
| `POST /api/v1/computer-use/vm/viewer` | Open the VM window. |
| `DELETE /api/v1/computer-use/vm` | Remove the VM and its keys. |
| `PATCH /api/v1/settings/computer-use` | Gains `target`. |

DTOs go in `controllers/dto.go` and the operation registry, then `npm run api`.

### CLI

`ao` stays a thin client. A small `ao computer-use vm status|start|stop|reset`
group over the routes above is useful for support and scripting and can ship
after the settings page.

### Errors

New codes, each with a fault-code entry where it reflects a broken machine
and not a user choice:

- `COMPUTER_USE_VM_NOT_SET_UP`: target is `vm` and no VM exists.
- `COMPUTER_USE_VM_UNSUPPORTED_HOST`: this machine cannot run the VM, with
  the reason.
- `COMPUTER_USE_VM_START_FAILED`: the VM or its guest driver did not come up.
- `COMPUTER_USE_VM_UNREACHABLE`: the VM runs but SSH or the driver does not
  answer.

Existing codes keep their meaning; in VM mode
`COMPUTER_USE_NOT_INSTALLED` and the permission states describe the guest.

## What agents are told

The tools are the same, so the guide's workflow stands. What changes is what
the agent should assume about the computer.

- **Standing prompt.** A VM variant of `computerUsePrompt`: the computer is a
  virtual machine set aside for agents, not the user's computer. The user's
  files, apps, and sign-ins are not there. If a task needs an account, ask
  the user to open the VM window and sign in.
- **Guide.** A "When the computer is a virtual machine" section in
  `commands/cua.md` covering the same points and the new errors.
- **Tool results.** A session that started in host mode and now runs against
  the VM has a stale prompt. Its first result after the switch carries a
  one-line note saying which computer it is acting on.

### Action checks in a VM

| Rule today | Why it exists | First release (shared VM) | Later (one VM per session) |
| --- | --- | --- | --- |
| Foreground input refused | It types into whatever the user has in front | Keep: another session may own the front window | Can be allowed |
| Desktop scope refused | It acts wherever the user is working | Keep, same reason | Can be allowed |
| Pixel actions need a window; typing needs a field | Stops input landing in the wrong place | Keep | Keep |
| "Ask before `bring_to_front`" | It takes the user's keyboard focus | **Dropped in the VM prompt:** nobody is typing there | Dropped |

Dropping the last rule matters in practice. The largest reliability problem
in host mode is hidden Chromium and Electron windows that stop painting; in
the VM an agent can bring a window forward without asking.

## Security model

### Trust

- The **host daemon** is trusted.
- The **guest** is not: not its screen contents, not what it prints, not the
  files it offers. Prompt injection from a web page or an app in the guest
  can make an agent do anything the tools allow inside the guest.
- **Hypervisor escape is out of scope.** The boundary is Apple's
  Virtualization.framework, and AO adds nothing to it.

### What crosses the boundary

| Channel | Direction | Control |
| --- | --- | --- |
| Driver commands (SSH exec) | Host to guest | AO's key under `~/.ao/vms/keys`, pinned guest host key, the VM's NAT address only. Tool names are already limited to `^[a-z][a-z0-9_]{0,63}$`; arguments are quoted by one function with its own tests. |
| Tool output | Guest to host | Parsed as data with a size cap. Terminal escapes are already stripped. No path in it is opened on the host. |
| Screenshots | Guest to host | Bytes over the SSH connection, size and type checked, written by AO under a name AO chose. |
| Shared folders | None | Not used in the first release. |
| Clipboard | None by default | Off while headless. Behaviour with the viewer open is a spike item. |
| Viewer | Host to guest | Opened by the user, on the host, through Lume. |
| Credentials | None | AO copies no agent, GitHub, or AO account credential into the guest. |

### Network

The guest uses NAT. It can reach the internet, which most tasks need. It can
also reach other devices on the user's network and any host service that
listens beyond loopback.

- AO's primary listener binds `127.0.0.1` and is not reachable from the
  guest. The opt-in LAN listener is reachable while Connect Mobile is on, and
  it is behind bearer authentication.
- Lume has no "no network" mode, and filtering the guest's traffic on the
  host needs root. The first release documents this limit and does not claim
  network isolation. Rules inside the guest would be advisory only, since the
  guest is the thing not trusted.

### Risks that remain

- **Accounts signed in inside the VM** are available to every agent that uses
  it, across sessions. The settings page and the docs recommend dedicated
  accounts.
- **Sessions share one VM** in the first release. One session can see and
  disturb another's windows. Reset is the remedy; per-session VMs are the
  fix.
- **The agent process is on the host.** An agent with a shell could also SSH
  into the VM or run `lume` itself. That grants nothing it lacks today, since
  the VM exists for agents, but it means the VM is not a boundary around the
  agent.
- **A host Cua Driver left installed and granted** remains usable by local
  processes outside AO's checks. VM mode does not need it.

## Platforms

| Host | Guest | Substrate | Phase |
| --- | --- | --- | --- |
| Apple silicon, macOS 13+ | macOS Tahoe | Lume, Cua Driver in the guest | 1 |
| Linux x86_64 with KVM | Linux x86_64 desktop | QEMU, Cua Driver in the guest | 3 |
| Windows 10/11 | Linux, or Windows with the user's own licence | QEMU on WHPX | Later |
| Apple silicon | Linux | Not planned: Lume's Linux guests are ARM64 and Cua Driver's Linux build is x86_64 **(Cua docs)** | None |
| Intel Mac | Any | Not planned: Lume does not support it | None |

The `VMHost` port is kept to the verbs listed under
[Components](#components) so a second provider fits without changing
`computervm` or the service.

For Linux and Windows hosts, Cua's own sandboxes (`cua sb create … --kind
vm`) are a candidate provider. They are cross-platform, bundle the driver in
the guest, offer checkpoint and fork, and on QEMU can run with no network.
Their guest service, `cua-spacesd`, is under FSL-1.1-MIT, not MIT **(Cua
docs)**, and it exposes the driver through its own endpoint, which would be a
second tool path. That choice needs a licence review and belongs to Phase 3.

## Alternatives considered

| Alternative | Why not now |
| --- | --- |
| Cua's computer-server, Lumier, and the Docker desktop images | Deprecated and unmaintained upstream **(Cua docs)**. Pixel-only tools would also mean a second tool vocabulary and guide. |
| Cua sandboxes for the first release | Licence review pending and a second tool path, as above. Lume plus the driver is MIT end to end and matches Cua's documented VM recipe. |
| A persistent `cua-driver mcp` session over SSH | Requires rewriting the service against MCP. Worth it only if per-command SSH cost shows up in the spike. |
| Run the whole session in the VM (AO daemon in the guest, as a remote host) | It isolates the agent process too, which this plan does not. It is also a much larger change: worktrees, credentials, harness installs, and remote-host pairing inside a local VM. Tracked as the follow-up if the answer to the first open question is "the agent must be inside". |
| A shared folder for screenshots | A writable share lets the guest plant symlinks the host would follow. |
| A second macOS user account or a virtual display on the host | Same kernel, same disks, same privacy database. Not the boundary the customer is asking for. |
| A cloud computer | Ruled out by the request. Cua Cloud also ended on 18 October 2026 **(Cua docs)**. |

## Phases

**Phase 0: spike (no product code).** Answer, on an Apple silicon Mac:

- Do `call`, `describe`, `status`, `permissions status`, and `doctor` behave
  the same over SSH as locally, including the "daemon is not running" and
  "session has ended" texts the service matches on?
- Per-command latency over one held SSH connection, and time from cold boot
  to the first answered call.
- Do privacy grants survive `lume clone`, and a guest driver update?
- Does `--storage` with a path under `~/.ao` keep every VM file there?
- What `lume clone` costs in time and disk (copy-on-write or full copy).
- Clipboard behaviour with the native viewer open, and whether it can be
  disabled.
- Guest memory in use when idle, to set the idle-stop default.
- Does the guest need a logged-in desktop session after every boot, and does
  the unattended preset's auto-login provide it?

**Phase 1: one shared macOS VM.** Target setting and migration; `VMHost` port
and Lume adapter; guest driver; `computervm` with start on demand, idle stop,
and reset; setup flow and VM rows in Settings; Open VM window; VM prompt and
guide section; error codes; docs page.

**Phase 2: living with it.** In-app view of the VM; explicit file transfer
between a session and the VM (a tool, or a read-only share of the session's
worktree, opt-in); guest driver updates from Settings; per-project target.

**Phase 3: isolation between sessions, and other hosts.** A lease table
(session to VM) with release on session termination; disposable clones per
session, bounded by Apple's two-guest limit on macOS; relaxed action checks
for exclusive VMs; Linux hosts.

## Testing

- **Unit tests** use a fake `VMHost` and a fake guest driver, in the style of
  `computeruse_test.go`. No network and no real VM, per the repository rule.
  They cover: target routing on every call; refusal when the VM is not set
  up; start on demand; a failed probe not counting as gone; image pull size
  and type checks; a guest-reported path never being opened; the setting
  refusing to turn on when the target is not ready.
- **Route tests** for the new endpoints and the LAN block
  (`lan_listener_test.go` already covers the prefix).
- **Manual acceptance** on an Apple silicon Mac for everything that needs a
  real guest: setup end to end, consent, cold start, idle stop, reset, daemon
  restart with the VM running, and a full agent task. CI does not run a VM.

## Open questions

1. **Is isolating desktop control enough, or must the agent process be in the
   VM too?** This plan does the first. If the customer needs the second, the
   remote-host alternative becomes the main line and this becomes a part of
   it.
2. **Which guest does the customer need?** This plan assumes a macOS guest on
   an Apple silicon Mac. A Linux or Windows requirement moves Phase 3 forward
   and brings the sandbox licence question with it.
3. **Base image: Apple's restore image or Cua's published image?**
   Recommended: build from the restore image (SIP on, nothing third-party in
   the base), and offer the published image as the fast path.
4. **Is "the guest can reach the local network" acceptable for the first
   release?** If not, the first release needs host-side filtering, which
   needs root and its own design.
5. **Should AO rotate the guest account's password?** Key-only SSH closes the
   remote path. Rotating also covers the console, but Lume's own `ssh`, `cp`,
   and `shutdown` commands assume the default credentials.
6. **Shared VM or per-session VMs first?** Recommended: shared first. It
   fits Apple's two-guest limit and the memory of a typical laptop, and reset
   covers the common failure.
7. **Does the original proposal differ from this?** This document was drafted
   from the request and the code. Any decision the proposal already made
   should replace the matching section here.
