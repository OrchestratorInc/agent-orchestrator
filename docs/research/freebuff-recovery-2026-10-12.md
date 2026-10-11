# Freebuff recovery: explicit native host instructions prerequisite

Recovery queue date: 2026-10-12; actual inspection UTC: 2026-10-11T21:56:25Z.
Existing [PR6553](https://github.com/OrchestratorInc/agent-orchestrator/pull/6553)
remains draft, launch-gated and unregistered. No AO integration success or
screenshot is claimed. Prior failed native evidence is preserved.

The unchanged stock Freebuff0.2.26 binary excludes nested AGENTS.md contents
from system context even though discovery persists them. Independently checked
native-005 state includes root project instructions but excludes the private
mandatory receipt instructions. hello.txt exists; private-receipt.txt does not.
The earlier explicit-read control succeeded because its visible task caused a
file read, and remains distinct from independent standing instruction delivery.

[Detailed source diagnosis](freebuff-recovery-diagnosis-2026-10-12.md)
and [sanitized immutable state receipt](freebuff-recovery-2026-10-12.json)
record source paths, binary offsets and hashes without credentials or raw state.
The public source revision is not the private release gitHead.

[Concrete unapplied upstream source patch](https://github.com/nikhilachale/agent-orchestrator/blob/fcdc3db20/docs/freebuff-upstream-append-instructions.patch)
and [acceptance criteria](https://github.com/nikhilachale/agent-orchestrator/blob/fcdc3db20/docs/freebuff-qualification.md)
propose an explicit bounded private-file input, SDK transport, and isolated host
knowledge key while preserving defaults, home/root rules, tools and permissions.
The proposed environment variable is **not supported by stock0.2.26**. Included
loader regression source is unexecuted; SDK/default-root and released-native
conformance remain prerequisites. No installed code was modified, no custom
native root selected, and no upstream third-party PR was published.

Alternatives checked: CLI flags, native profile/settings, custom-agent resolver,
internal userKnowledgeFiles transport, home-loader behavior, nested/lazy rules,
on-demand skills, current released package metadata. None supplies a supported
per-session additive private input without altering user/default instructions or
visible tasks. A newer official release was not established. No identical model
probe was repeated after the deterministic exclusion was confirmed.

AO runtime, AO Kill, permission/draft admission and real Electron PNG: NOT_RUN.
Screenshot: unavailable because mandatory independent standing instructions fail
before safe AO registration. No image assets are added by this recovery.
Prior native coding/cancellation controls remain historical evidence only.

Remaining external prerequisite: official released additive host instruction
input (or an equivalent documented native route), then independent native
instruction/cancellation proof and actual AO lifecycle/UI qualification. Narrow
non-restorable TUI scope is allowed but cannot bypass this prerequisite.

Cleanup: no native/daemon/Electron processes or sessions started, no builds,
downloaded binaries, dependency installs or routine local suites. Existing
native-005 owned PIDs were absent; no process was killed. Production service,
protected audit PID625660, profiles, sources and old evidence were untouched.
Routine validation belongs to exact-head GitHub Actions; record current checks
in the handoff comment, never reuse historical5/5 or21/21 as current-head proof.


Current recovery CI first failed one historical mux fixture (149/150): the
120ms operation deadline raced real fixture-child startup, so it failed before
entering the WebSocket timeout branch asserted by that test. The two short-window
fixtures now wait for their actual child-start marker before starting the tested
operation. Their80ms/120ms bounds, timeout assertions, production runner and
native gates are unchanged. This is fixture setup synchronization, not native
provider conformance. The full fixture suite is rerun only in GitHub Actions;
preserve the first failed run38178527237 separately from the later result.
