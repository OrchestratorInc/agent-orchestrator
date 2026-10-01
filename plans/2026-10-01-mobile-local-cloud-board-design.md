# Mobile Local + Cloud Board Design

## Intent and scope

The mobile app should let a person see work from AO Cloud and their one currently paired desktop in the same Workers and Projects views, and choose where a new worker will run at spawn time. The source of an existing worker or project must remain clear and must determine which API handles later navigation and actions. This replaces the global Local/Cloud view switch in the drawer, without introducing a multiple-host selector.

Success means that a signed-in, paired user can see both sets at once; filter Workers to All, Cloud, or Local; spawn onto the intended destination; open either source's session without first switching modes; and continue using Cloud if the desktop is offline (and vice versa). The UI must never imply a failed source has no workers. iOS and Android must behave alike. No backend or Cloud API changes are in scope.

The only Local source is the active paired desktop. The Cloud source is the current signed-in Cloud organization. We retain the underlying pairing, sign-in, and source-specific APIs. Multi-host browsing, moving sessions between sources, Cloud PR parity, and expanding Cloud-only actions to Local (or the reverse) are not in scope.

## User experience

### Workers

Workers defaults to **All**. The existing filter surface gains an Environment choice: All, Cloud, or Local. A worker's project line includes a compact source label (Cloud or the paired desktop's short name), including in search results, recent workers, and accessibility text. Project filtering only offers projects in the currently selected environment; All can show source-qualified project choices so identical project names are unambiguous. The source label is informational, not a global mode switch.

Each source keeps its own readiness, loading, last successful snapshot, and error. An unavailable desktop shows a Local connection notice and any retained Local rows as stale; Cloud rows remain usable. A Cloud auth or API failure shows a Cloud notice and does not erase Local rows. If neither source is configured, empty-state actions point to pairing and Cloud sign-in. The board must not flash an empty state while one source is still resolving.

### Projects and Pull Requests

Projects lists both sources, clearly labeling each project. Tapping a project opens its source-specific detail and actions. The Cloud import action remains Cloud-only; the mobile app does not attempt Local project creation. Opening an orchestrator follows its project's source. A project page's New task action carries that source and project into the spawn sheet.

Pull Requests remains a Local-only surface until Cloud PR support exists. Its title or explanatory copy says that clearly; it is not controlled by a global environment setting. The existing Local PR functionality stays intact.

### New worker

The spawn sheet has a prominent **Run on** choice above project selection: **Cloud** or **Local · [paired desktop]**. From a project or worker context, destination and project are preselected. From the general New worker action, if both destinations are available the person explicitly chooses one before selecting a project; if only one is available, it is preselected and still visible. An unavailable destination remains identifiable with its reason and a route to sign in or pair, but cannot submit a spawn.

Changing destination clears the project, harness, model, mode, attachments, pending idempotency key, and destination-specific loading/errors before loading the chosen source's options. The destination determines the project list, available harnesses/models, supported mode and attachments, credential-readiness messaging, and the `SessionSource` used to spawn. The submit button stays disabled until a valid destination, project, and harness are selected. After spawn, navigation carries the destination so the new session opens against the same source even if other background state changes.

## Architecture and data flow

`AppProvider` currently stores Local and Cloud boards separately but publishes only the globally selected board; its Local and Cloud pollers also depend on the global environment. Replace that selection with a dual-source read model. Both configured sources refresh while the app is active, under their existing independent polling rules. Preserve source-specific errors and last good snapshots. Resolve each source from its own configuration/auth state rather than the drawer picker. Do not add backend endpoints or change the daemon's listener/security boundary.

Expose source-qualified board entries to combined views. A stable key contains source kind plus source identity and resource ID: the current desktop's opaque machine identity for Local, and the current Cloud organization ID for Cloud. This prevents collisions when both sources have the same project or session ID and prevents retained Local data from appearing under a newly paired desktop. A single explicit source reference is passed through project, session, orchestrator, and spawn routes; a route never infers origin from whichever source happened to be selected last. Deep links without a source may resolve only when the ID is unique across the currently loaded sources; ambiguous IDs produce a choice or explanatory error, never a guess.

The combined Workers and Projects screens compose the two read models, but source-specific mutations stay behind their existing boundaries. Local rename, pin, kill, restore, and PR actions remain Local-only. Cloud sessions retain their Cloud open-only controls and sandbox lifecycle. Shared rows can vary interaction capability per entry instead of imposing one interaction mode on the entire list. Pull-to-refresh asks both available sources; one rejection does not cancel the other's refresh. Search and grouping operate on the combined, filtered set and use source-qualified row keys and project lookups.

The drawer loses its Environment picker and its recent-workers list uses the same source-qualified entries as the Workers board. Settings retain desktop pairing/status and Cloud account/sign-in controls; the global Environment row is removed once no screen depends on it. Any internal migration of the saved environment preference should be one-time and must not switch the new combined board's source. The visible Cloud/Local availability indicators represent their own connections, not the previously selected view.

## Failure and transition handling

- Signing out of Cloud removes Cloud-only data from the visible combined board immediately, clears Cloud route/spawn state, and leaves Local usable. Signing into another organization must not leak the prior organization's cached rows.
- Re-pairing to another desktop removes the former desktop's rows from view until the new host responds. No action may target an old host through a new pairing.
- A single-source error, stalled first load, or background/foreground transition must not blank the other source or falsely show “No workers.” Stale data is labeled where retained.
- Source-specific routes that are no longer available offer Retry and the appropriate sign-in/pair action, rather than silently opening the other source.
- A destination change during async catalog loading discards late results from the previous destination. A spawn response cannot navigate to the wrong source.

## Verification

Use test-first changes around board composition, source-qualified identity, filtering, routing, and spawn validation. Cover same-ID Local/Cloud fixtures, one source offline, sign-out and re-pair, late catalog responses, destination switching, and Local-only action gating. Run the mobile typecheck and full Vitest suite. Then review on both iOS Simulator and Android Emulator: All/filtered Workers, combined Projects, Cloud import, Local PRs, direct session opens, and Cloud/Local spawn. No production deployment is part of this work.
