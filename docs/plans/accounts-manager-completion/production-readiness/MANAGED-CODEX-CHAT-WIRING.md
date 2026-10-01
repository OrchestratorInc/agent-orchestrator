# Managed Codex Chat wiring

Explicit managed account selection now reaches the managed app-server driver.
Native Chat keeps its existing driver and device login. Managed preflight uses
the installed protocol version, with a separate capability cache, and does not
require a second device login.

The private launch route carries the durable binding revision. A fresh daemon
controller gets a fresh event generation while a surviving provider may retain
the same account binding and private profile. A changed binding, endpoint or
conversation scope cannot adopt that provider. Route preparation checks the
binding again immediately before process birth. Managed account events do not
update the native device account's quota or authentication state.

Ordinary Chat shutdown authenticates one exact host and joins its direct provider.
Its durable stop receipt permits a lost-response retry without stopping a
replacement. The receipt does not certify escaped descendants and is never used
by permanent account removal. Unjoined managed owners block cold replacement,
including a crash before descriptor publication. Unknown ownership remains an
explicit recovery error.
An executable that never starts records confirmed absence, so repairing the
installation permits retry without treating a crashed process as absent.

This narrows the earlier blanket production admission block in RESUME-PLAN.md to
the actual destructive retirement boundary. Permanent removal still requires
ShutdownExact and retains all five containment-required acceptance failures.
This implementation does not establish escaped-descendant containment or close
the native Windows, macOS, live-account or whole-release gates.

Validation covers selected-driver admission, missing/stale route rejection,
independent native and managed preflight, durable host identity after SQLite
reopen, queue handoff controls, actual supervisor reconnect/replacement, and an
installed client using two synthetic accounts in a network-isolated process
namespace. The installed workflow checks overlapping replies, private history
after provider restart, revoked-account failure with the other account surviving,
native profile preservation and ambient proxy exclusion.

No renderer or public DTO shape changes are included. Linux execution evidence
does not substitute for a fresh run on either Mac architecture or Windows.
The managed adapter still rejects extra workspace directories and explicit tool
server configurations; this change does not add those capabilities.
