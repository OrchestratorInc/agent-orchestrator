# Stakpak foreground cancellation source fix — 2026-10-12

Actual implementation: [native Stakpak PR767](https://github.com/stakpak/agent/pull/767), fork branch `nikhilachale:fix/foreground-command-cancellation`, source `62bd8833600b0f906ff841930433264ece36dbab`. The PR is draft. [AO6550](https://github.com/OrchestratorInc/agent-orchestrator/pull/6550) remains draft and unregistered; this report does not establish production harness admission.

The patch owns the real local foreground child, synchronously signals its established Unix group before reaping on cancel/timeout/future drop, bounds explicit cleanup at five seconds, and clears group/direct-child authority after successful reap or ECHILD. Direct-child fallback runs only while ownership is known, avoiding a deferred stale-PID kill. It does not kill the persistent MCP worker or scan arbitrary processes. A real follow-up-output regression also exposed early pipe EOF and cancellation-unsafe `read_line`; the executor now drains both pipes with cancellation-safe `Lines::next_line`. Normal success never group-kills; background children retaining foreground pipes delay EOF, so use the native background-task tool for detached work.

## Verification and explicit gaps

[Current-head fork qualification](https://github.com/nikhilachale/agent/actions/runs/38207103576) is green:9/9 real executor/lifetime regressions, compiled stock executor negative control, affected-package lint, release build and formatting drift. Tests exercise actual ToolContainer/MCP calls, both timeout-configured/unconfigured cancellation branches, timeout, coordinated disconnect, execution-future abort, leader exit with open pipes, wait/try_wait lifetime and actual external reap/ECHILD. They check nested shell/sleep start witnesses, PID/kernel-start identity, no delayed write, an unrelated live control and same-MCP-session follow-up stdout.

The complete upstream workspace/feature job is **CANCELLED/NOT_VERIFIED**, not passed. Three hosted attempts stalled beyond declared bounds; cancelled job logs returned404 BlobNotFound or empty output. The final attempt used upstream `ubuntu-latest`, Rust1.94.1, one Cargo build worker and no debug symbols (assertions retained) to bound linker memory. Full-suite failure attribution is unavailable. No local test/lint/typecheck/vet/race/workflow suites or native compilation ran on the VPS. This CI/log availability gap keeps the native fix draft and stage blocked from complete validation.

Exact Linux CI artifact: Rust1.94.1, source62bd883, executable SHA-256 `882e7dce06ce64afe76249befff732a5f17b86c5594291526b5302a323965ac2`. It reports version0.3.88 because no release/version bump was published. It is **unreleased patched source**, not repaired stock0.3.88. Stock `d3eba6f502a79b993b826ef3ab4065eec9e38dfa`, executable SHA5329d4... remains the original failed release. Earlier patched source16e63ed's direct native18/18 passing control and later62bd883 controls remain separate.

## Actual native execution inside AO's existing standalone terminal

This is supplementary real Electron/native runtime proof through existing loopback shell-terminal/mux APIs, **not** a registered agent-manager session, Chat, restore, handoff, catalog/auth-state or agent-session Kill claim. The ordinary native permission dialogs were inspected and approved for fixture operations; default native instructions/security/secret redaction remained enabled. No native installed binary was patched.

The current artifact passed22 recorded checks: leading-dash task once in native history, actual cwd, independently consumed private standing token, project AGENTS token, preserved AGENTS, initial/second/third active-to-settled turns, second mutation, Escape after child-authored shell/sleep markers, immediate physical PID absence, unchanged native identity, no25-second delayed artifact, unrelated PID/start and continuing heartbeat, third turn in the same native composer, and unchanged serving/native hashes. Native tool timeout was35 seconds; Escape occurred before expiry. The proof JSON count moved1→2→3. Full raw checkpoints, credentials, profiles, DBs and logs are excluded.

[Curated receipt](evidence/stakpak-native-fix-20261012/receipt.json) contains PID/start witnesses, gates, image hashes and cleanup responses. Native identity digest: `8082f814ca93f1963292a78cc9d01f18f4ed3fc1fce8ec7b7f8fe7d8fa20d198`. Runtime handle: `ptyhost-v1:shellterm-8968a375bddfe0aa`; host985380/start82945904, native SID985501. The standalone shell has no managed agent launch generation; the earlier closed shell had another handle/SID/native identity. Do not invent a manager generation or AO agent session ID.

AO backend and UI source: `fb60bd4097ebd4109bbe7ee68e908f722e6ee03f`; actual serving PID979004 and executable SHA `b30f2dfa7b1f1c796c5772cb96d7d5c32b4bfb53c39f2c4ff7ca669f2cca8ded` before/after capture. Electron replaced the initially started PID978528 with this same-hash daemon before shells were created. Runfile: `/home/azureuser/.ao/audits/native-harness-fixes-20261012/reports/stakpak/ao-shell-001/running.json`. Native model: configured `zai/glm-5.3-flash`, rendered GLM-5.3-Flash/provider Zai/profile audit. Auth was exercised by native provider responses; no AO registered auth-state is claimed.

### Settled native turn

Real AO Electron, `scrot` on owned Xvfb display, UTC2026-10-12T05:55:19Z. Shows native second mutation, empty composer, model, AO navigation and explicit62bd883 prerelease tab. SHA-256 `1b1880c93d8362429d087751db7020c4e3c121404a733c81a29ded869b78cfd4`.

![Unreleased source62bd883 Stakpak second turn in actual AO standalone terminal](evidence/stakpak-native-fix-20261012/current-settled.png)

### Running foreground shell before Escape

Real AO Electron/scrot, UTC2026-10-12T05:56:59Z. Child markers and PID/start receipt establish execution; the screenshot alone does not prove cleanup. SHA-256 `01432b5596ea1d7c7700d454386279154d0d13fcf82a029668c18fee54497786`.

![Actual AO standalone terminal with unreleased Stakpak foreground shell running](evidence/stakpak-native-fix-20261012/current-executing.png)

### Same native session accepts third turn after cancellation

Real AO Electron/scrot, UTC2026-10-12T06:03:07Z. Shows count3, settled empty composer, unchanged native identity/model and prerelease tab. SHA-256 `526ccf4f6a99cf97bbf45b362b2080fda640c93123083b21005cbf760e54da10`.

![Same unreleased Stakpak native session after Escape and third turn in actual AO](evidence/stakpak-native-fix-20261012/current-after-cancel-third.png)

## Retained failures and scope limits

Original stock0.3.88 Escape failure (child alive/late write) and restore standing-message duplication remain failures. Original AO screenshot: unavailable because that attempt never established a registered AO session; no later image retroactively fills it. Source fix does not repair native restore/ACP history load. Fresh-only non-restorable scope remains deliberate. Unix group cleanup is not universal descendant containment: deliberate new groups/sessions and Windows tree behavior are unqualified; no developer Mac work occurred.

Earlier source16e63ed standalone-shell startup after a **forced renderer reload** put OSC11 color reply text before the typed task. That startup control is **FAIL**, with its actual screen below; source ownership is not attributed from this observation. Default security also classified the harmless `project=AGENTS.md` text assignment as a generic secret; it was not a real credential and redaction was not disabled. A clean new terminal62bd883, refreshing the renderer before native launch and using native bracketed-paste input, passed the exact leading-dash/history check. This later fresh control does not repair or erase reconnect behavior. Protect pending permissions/user drafts; never clear them blindly to force readiness.

Real AO Electron/scrot, UTC2026-10-12T05:17:23Z, source16e63ed, SHA-256 `17fec7ab24e4acf6253a7d0b81669eb7ac122e01106b5e2d3511895120173948`.

![Retained failed startup after forced renderer reload: OSC11 text before native task](evidence/stakpak-native-fix-20261012/retained-osc-reload-failure.png)

Earlier source16e63ed independent **AO standalone-shell close** also passed: HTTP204, owned shell/sleep PIDs absent immediately and after deadline, no delayed artifact, unrelated control PID/start and heartbeat alive. This is runtime-close proof, not agent-session Kill.

## Cleanup and next dependency

Both final shell closes returned204; shell list returned200/empty; both runtime hosts, native SID leader and control PID are absent. Owned Electron main978778 and its recorded launch SID members stopped; scratch daemon979004 returned202 to shutdown and is absent. All mux helpers stopped and the compute lock released. Production PID625660/ao-self-hosted were not signalled; profiles/source/dirty worktrees/shared git/credentials/prior evidence remain. No cache cleanup was needed; disk stayed above750MiB.

Next: obtain complete current-head upstream suites/logs on a functioning CI runner, retain native PR767 draft until that gap is resolved, then upstream review/release. AO6550 remains unregistered pending the released fix and actual fresh-only adapter/agent-manager/Kill/UI qualification; renderer reconnect behavior needs a separate rooted investigation. Do not claim the existing draft is a production integration.
