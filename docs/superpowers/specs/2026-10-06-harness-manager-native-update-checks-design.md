# Harness Manager-Native Update Checks Design

## Goal

Make PR #5848's harness update advisory trustworthy across the installation
methods AO already exposes. AO should ask the owning package manager for update
information when that manager offers a non-mutating query, fall back to a
verified registry or vendor channel when necessary, and return `unknown` rather
than guess when ownership, channel, or version ordering cannot be proved.

The independent launch-versus-maintenance race is specified in
`2026-10-06-harness-maintenance-gates-design.md`; both changes belong in the PR
follow-up, but each can be implemented and verified separately.

## Success Criteria

- npm, Homebrew, Winget, Bun, uv, and pipx installations can produce an update
  advisory when their owning manager and an authoritative latest version are
  available.
- pnpm and Yarn installations are recognized. They may report advisories through
  the npm registry because their global packages use npm package identities, but
  AO does not expose automatic update or uninstall actions for those managers in
  this PR.
- Version-manager shims are recognized and never compared with an unrelated
  stable vendor channel. When AO cannot prove the backing package and channel,
  the result remains `unknown`.
- Windows npm command shims are attributed to the corresponding global npm
  package without accepting an unrelated shim from the same prefix.
- Stable releases, prereleases, build metadata, two-component versions, and
  four-component versions are parsed without panics or false `current` results.
  AO reports `behind_latest` only when ordering is provable within the same
  channel.
- A failed or inconclusive lookup is retried by the UI on the backend's five
  minute unknown-result cadence instead of remaining stale for one hour.

## Non-Goals

- Do not add automatic update/uninstall operations for pnpm, Yarn, asdf, mise,
  Volta, nvm, or fnm.
- Do not run a mutating command (`install`, `upgrade`, or `update`) merely to
  discover whether an update exists.
- Do not scrape or invent an undocumented vendor endpoint that cannot be
  verified. Missing sources such as Unreal remain `unknown` with a diagnostic
  reason; Unreal continues to update with AO itself.
- Do not change the loopback/LAN HTTP security model.

## Approach

Introduce a small internal advisory-source boundary instead of extending the
existing npm/Homebrew switch. A source receives the verified binary, package
plan, current parsed version, and command runner. It first proves that the plan's
manager owns the binary, then performs the manager-specific non-mutating query
or registry lookup and returns a normalized latest version and source label.

The manager source registry is ordered with the successful AO install method
first, but the running binary's proven owner remains authoritative. An AO install
record never overrides ownership of the binary sessions actually launch.

This keeps operation planning separate from update discovery: pnpm and Yarn can
be advisory sources without becoming executable update plans. Vendor channels
remain the last fallback and are used only for binaries outside another
manager's layout.

## Manager Checks

### npm

Use npm's global package metadata query as the primary manager-native check. An
exit status that means "outdated entries found" is data, not a failed check.
When npm does not return a usable result, query the npm registry for the selected
distribution tag. For stable installs the tag is `latest`; for prereleases AO
must match an existing dist-tag to the installed version before comparing it.

Ownership continues to use `npm root -g`. On Windows, also accept a `.cmd`,
`.ps1`, or executable shim beside the returned `node_modules` directory only
when its contents or resolved launcher target points into the expected package
root.

### Homebrew

Use Homebrew JSON output. `brew outdated --json=v2` may supply an explicit
outdated verdict; `brew info --json=v2` supplies installed and current stable
versions and remains the fallback. Formula and cask parsing stay separate.

### Winget

Prove ownership from Winget's package layout and the exact allowlisted package
ID. Use `winget upgrade` without a package selector to list available updates,
then filter the exact ID in its output. Never pass `--id` to this discovery
command: that performs an upgrade. Use `winget show --id ... --exact --versions`
as the read-only fallback. Because Winget table output can be localized or
change shape, accept only rows that can be tied to the exact package ID and
contain parseable current/available versions; otherwise return `unknown`.

### Bun

Prove the Bun global layout and use Bun's read-only package metadata when its
output is machine-readable. Since Bun global packages use npm identities, the
npm registry is an allowed latest-version fallback after ownership is proved.

### uv and pipx

Prove the tool directory layout and use each manager's read-only listing output
to confirm the installed distribution. Query PyPI for the allowlisted package's
latest version. Do not invoke `uv tool upgrade` or `pipx upgrade` during an
advisory check.

### pnpm and Yarn

Recognize their global layouts and prove the expected package when their
read-only global listing commands return machine-readable data. Query the npm
registry for the latest version. These sources are advisory-only; the Settings
operation catalog continues to show no automatic maintenance action for them.

### Version Managers

Do not assume a generic shim belongs to a particular package. A shim remains
`unknown` unless a supported manager can resolve it to an exact backing binary
and package. No stable vendor fallback is allowed for unresolved shims because
the selected channel may be beta, nightly, or pinned.

## Version and Channel Semantics

Replace regexp-submatch comparison with a parsed version value. The parser
normalizes an optional `v` prefix, numeric core components, prerelease
identifiers, and build metadata while preserving the original display value.
Missing numeric components are padded for comparison; a fourth numeric
component participates in ordering rather than being discarded.

Stable versions compare only with stable latest releases. A prerelease compares
only when the manager or vendor response identifies the same prerelease channel
or dist-tag. Numeric prerelease identifiers follow semantic-version ordering;
otherwise ambiguous channel relationships return `unknown`. Build metadata does
not affect precedence.

If the installed version is newer than the selected source, AO returns
`unknown`, preserving the current protection against false "up to date"
verdicts from lagging channels.

## Advisory Result and Caching

Keep the public statuses `behind_latest`, `current`, and `unknown`. Add an
optional machine-stable reason to unknown results so diagnostics can distinguish
unproved ownership, unsupported manager, unparseable version, missing channel,
and lookup failure without exposing command output or local paths.

Successful definitive results remain cached for one hour. Unknown results remain
cached for five minutes. The frontend query derives its stale/refetch interval
from the returned status: five minutes for `unknown`, one hour otherwise.

## Missing Vendor Sources

Package-managed Qwen and OpenCode v2 installations are covered by their npm or
Homebrew identities. Copilot on Windows is covered through Winget. npm-only
harnesses are covered whenever npm/pnpm/Yarn ownership is proved. Official
installations continue using verified vendor sources already registered in
`official_versions.go`.

For Qwen official installers, Unreal, or any other target without a verified
release endpoint, keep the advisory `unknown`; do not silently compare against a
different package or repository. The unknown reason makes this limitation
visible and retryable.

## Error Handling

- Command timeouts, missing executables, non-machine-readable output, and network
  failures yield `unknown`; they do not fail Settings or daemon startup.
- Exact manager ownership is required before any manager check.
- Command output is size-limited and never returned through the advisory API.
- Context cancellation still lets an HTTP caller stop waiting without cancelling
  a shared background lookup.

## Testing

Backend table tests cover every provider's argv and representative output,
including npm's outdated exit code, Winget localization/ambiguous tables, Bun
registry fallback, uv/pipx listings, pnpm/Yarn advisory-only ownership, Windows
npm shims, and unsupported version-manager shims.

Version tests cover stable, prerelease, build metadata, two-part, four-part,
ahead-of-source, mismatched-channel, and malformed values. Service tests preserve
single-flight caching and cancellation behavior.

Frontend tests use fake timers to prove five-minute refresh for `unknown`,
one-hour refresh for definitive statuses, and invalidation after successful
maintenance.

Run focused backend and frontend tests first, then the repository's backend,
typecheck, API-generation/drift, race, lint, and build checks required by
`AGENTS.md`.
