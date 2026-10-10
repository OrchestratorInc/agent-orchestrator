# MiniMax Code TUI harness qualification

MiniMax Code (`mcode`, `MiniMax-AI/minimax-code`) is separate from Xiaomi's
MiMo Code (`mimo`). This integration is TUI-only; it does not register Chat,
reviewer support, or interface handoff.

## Qualified native contract

The tested GitHub release is **v0.6.5**, commit
`309e28554ec091bb17b6c4f67111c8c01d688b94`. The published
`minimax-code-0.6.5.tar.gz` SHA-256 is
`16377ae92d6e1c0752ea0a791eb5dc859584ca31d0c1fe67d643c834feda9f02`.
AO requires that version; the independently published npm `latest` is not
assumed equivalent. Install from the [upstream release](https://github.com/MiniMax-AI/minimax-code/releases/tag/v0.6.5).
The VPS qualification used Linux x64 and Node 24.21.0.

- A positional task follows `--`, including tasks beginning with a dash.
- `--append-system-prompt-file` adds AO's private standing instructions without
  replacing the provider defaults. Restore reapplies the file. Provider-authored
  proof files consumed both an initial token and a fresh token introduced after
  confirmed process termination. This proves delivery, not secrecy from model echo.
- Native identity is `mvs_` followed by 32 hexadecimal characters. Restore uses
  `--session <exact-id>` and never selects the latest conversation.
- Default, auto, and bypass-permissions map to native `default` (Ask), `auto`,
  and `bypassPermissions` (Full access). Accept-edits and unknown modes are rejected.
- The TUI exposes no permission launch flag. AO creates a private, persistent
  profile below `AO_DATA_DIR/agents/minimax-code/<session-id>`, preserving config,
  auth, global AGENTS.md, supported custom resource directories, and keybindings.
  It overrides permission mode there. Source config and credentials remain
  untouched. Unsupported symlinked or oversized resources fail explicitly.
  Snapshot initialization is staged and atomically renamed; failures can retry.
- Native hooks report prompt acceptance, active tool work, permission requests,
  and normal completion. MiniMax omits Stop on cancellation and uses SessionEnd
  for idle expiry; AO does not interpret SessionEnd as process exit.
- Escape cancellation restores cancelled text into the composer. Terminal
  detection observes the settled state separately from an empty composer.
- Auth probes return `configured` for saved selected-provider credentials, never
  `authorized` from cached connectivity. Models come from configured provider
  model entries, not provider names or an invented catalog.

## Exact-restore containment

Upstream v0.6.5 catches explicit session-open failures, re-enables the editor,
and suppresses only the launch prompt (`packages/tui/src/tui/launcher.ts`).
AO therefore validates the native read-only SQLite identity/workspace and
bounded message envelopes before launching restore. Its private native plugin
also pins the first SessionStart ID and rejects missing or mismatched identity
on later hooks before provider work. MiniMax executes hooks from an immutable
plugin cache and strips AO-prefixed environment variables. Each launch therefore
uses its own preserved `TMPDIR` to locate immutable routing and the private profile;
a cached hook or delayed child from an older launch cannot acquire a newer generation.

A native missing-session reproduction initially confirmed the plugin prevented
replacement work but its `stopReason` was not displayed. The retained follow-up
uses `systemMessage`: the TUI visibly refuses the replacement, stays alive, and
creates no replacement proof artifact. Neither result is presented as native
fail-closed restore without AO's guard.

## Evidence and scope

VPS evidence is under
`/home/azureuser/.ao/audits/harness-watch-20261010/reports/`:
`minimax-native-001` contains initial argv/private-instruction proof, exact
restore, fresh-instruction proof, continuation, and cancellation logs;
`minimax-missing-001` preserves the first guard reproduction;
`minimax-missing-002` preserves visible fail-closed guard evidence.
`minimax-ao-001` preserves the first failed AO audit: the original hook derived
its profile from the cached script location, could not read its identity file,
and visibly refused the initial turn before provider work. The cached-copy
regression reproduces that failure; the fix locates the profile through the
validated launch-specific `TMPDIR`. This first attempt remains failed.
Credentials are retained only in the isolated VPS provider profile.

Unit tests cover argv, unsupported permissions, private profile preservation,
failed snapshot retry, exact/missing/corrupt history, guard identity pinning,
configured auth, model parsing, terminal activity, and migration reversal.
Native logs are not screenshots and do not prove AO's UI. AO lifecycle audit,
real AO screenshots, complete backend/CI checks, and other OS coverage must be
reported separately with the final integration evidence.
