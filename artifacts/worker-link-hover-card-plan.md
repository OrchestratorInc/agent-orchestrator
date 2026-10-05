# AO worker link hover card

## Recommendation

Render a compact, read only worker summary above canonical `ao://sessions/{projectId}/{sessionId}` links. Reuse the existing Radix hover card and the shared workspace query. Mount the data reader only while one preview is open. This gives an immediate cached result, joins any in flight workspace refresh, and keeps daemon event updates live without adding an endpoint or issuing a request for every link.

The card should contain only identity and decision useful state:

1. Worker title, with the session id as a quiet secondary label when the title differs.
2. Project name, paired with the real harness logo.
3. One status line using the daemon supplied `displayStatus` when present, otherwise the existing derived status label.
4. Relative last update time.
5. Pull request count and compact PR rows. Each row contains `PR #number`, a small semantic dot, and a plain status label.

Do not invent an agent monogram or descriptive activity text. For example, `Implementing session link preview` is available only when the daemon supplied that exact `displayStatus` value. Reuse `AgentAvatar` for the official harness asset and accessible provider name. The visible project line does not need to repeat the harness as text.

The PR label collapses lifecycle and readiness into one headline. Use this priority: `Merged`, `Closed`, `Draft`, `Mergeable`, `Blocked`, `Checking`, then `Open`. CI and review facts may determine whether an open PR is blocked, matching `prCanMerge()` and `prCardPresentation()` behavior, but the card never renders separate CI or review labels. Keep merged PRs in the list. Render the label as ordinary compact text, not a capsule or badge.

The card is informational. It contains no buttons or nested links. The original link remains the only action and keeps the current navigation contract.

## Current path

1. `frontend/src/renderer/components/chat/ChatMarkdown.tsx` renders Markdown through `react-markdown` and `remark-gfm`. `remarkSessionLinks` also turns bare canonical AO URLs into Markdown link nodes. The `MarkdownLink` override detects canonical session links and routes clicks to `onSessionLinkOpen`.
2. `frontend/src/renderer/lib/session-links.ts` owns strict parsing, bare text detection, punctuation trimming, and resolution against workspaces. It accepts exactly two decoded path segments and rejects query strings, fragments, empty values, invalid encoding, and embedded slashes.
3. `frontend/src/renderer/components/AppLink.tsx` renders the anchor and context menu. Today it attaches `HoverCard` only to web links for Open Graph previews. AO session links receive the context menu but no preview.
4. `frontend/src/renderer/components/chat/SessionChatSurface.tsx` and `frontend/src/renderer/components/chat/ReviewerChatSurface.tsx` pass `useSessionLinkNavigation()` into Chat.
5. `frontend/src/renderer/lib/use-session-link-navigation.ts` parses the URL, resolves it against the workspace list, rejects missing or inaccessible sessions, blocks terminated sessions with a toast, and calls `useNavigateToSession()` for a live session.
6. `frontend/src/renderer/hooks/useWorkspaceQuery.ts` fetches `/api/v1/projects` and `/api/v1/sessions`, maps them into `WorkspaceSummary[]`, merges cloud projects and sessions, and exposes the shared `workspaceQueryKey`. Local data is fresh for 10 seconds and normally polls every 15 seconds. Checking sessions poll every 300 milliseconds. Cloud sessions poll every 5 seconds.
7. `frontend/src/renderer/lib/event-transport.ts` listens to `/api/v1/events`, coalesces change events, and invalidates `workspaceQueryKey`. This is the primary live update path for local worker state.
8. `frontend/src/renderer/hooks/useWorkspaceQuery.ts` also exposes `useWorkspaceSession()`. Its direct fallback uses `GET /api/v1/sessions/{sessionId}` and writes a newly found local session into the shared workspace cache. That fallback is useful for routed detail recovery, but a hover card should not invoke it.
9. `frontend/src/renderer/components/ui/hover-card.tsx` wraps Radix Hover Card with 300 millisecond open delay, 150 millisecond close delay, portal rendering, collision handling, and AO popover styling. `frontend/src/renderer/components/ui/tooltip.tsx` is intentionally noninteractive and too small for this content. `frontend/src/renderer/components/ui/popover.tsx` is click controlled and is not the correct desktop hover behavior.
10. `frontend/src/renderer/components/LinkPreviewCard.tsx` and `frontend/src/renderer/hooks/useLinkPreview.ts` are useful precedents for skeletons, lazy mounting, shared query caching, and suppressed transient preview errors.

## Smallest safe architecture

### Components

Add `frontend/src/renderer/components/SessionLinkPreviewCard.tsx` with these exports:

* `SessionLinkPreviewCard`, which receives a parsed target and reads the merged workspace query.
* `SessionLinkPreviewCardLoading`, matching the dimensions of the resolved card.
* A pure `findSessionLinkPreview(workspaces, target)` helper, either colocated or in `frontend/src/renderer/lib/session-links.ts`, for deterministic tests.

Reuse `sessionPRDisplaySummaries()` from `frontend/src/renderer/lib/pr-display.ts` so the preview sees the same normalized PR set as the inspector. Add a small pure presenter for the single capsule instead of duplicating provider fact parsing. Its result should be based on the existing `prCanMerge()` and `prCardPresentation()` output.

Extend `frontend/src/renderer/components/AppLink.tsx` with a narrow optional preview slot, for example `renderPreview?: () => ReactNode`. When present, use the same `HoverCard`, trigger, portal, collision padding, and context menu composition already used by web previews. Render the slot only while open. Web preview behavior should remain unchanged.

In `frontend/src/renderer/components/chat/ChatMarkdown.tsx`, pass the slot only when `isSessionLink(href)` is true. Parse once with `parseSessionLink`. Keep click handling exactly as it is.

This arrangement avoids a second anchor, avoids wrapping a Radix context menu root in another `asChild` trigger, and avoids nested interactive content.

### Data behavior

The mounted card calls `useWorkspaceQuery()` once. In normal Chat use, the workspace query is already active, so the card renders cached data immediately and only subscribes to updates while open. TanStack Query deduplicates any coincident refresh. Do not call `GET /api/v1/sessions/{sessionId}` from hover. Do not add hover timers that prefetch every link.

Resolve by both `projectId` and `sessionId`, never by session id alone. This prevents a valid session in another project from satisfying a mismatched URL. Cross project links work because the shared query already includes every registered local workspace and every currently accessible cloud workspace.

Use the query states as follows:

| State | Card behavior |
| --- | --- |
| Cached match | Render immediately and update in place from the shared query. |
| Initial query pending | Show a fixed size skeleton. |
| Project exists, session absent | Show `Worker unavailable` and the project name. |
| Project absent | Show `Worker unavailable` and the encoded project id. |
| Query failed with old data | Keep the old card and show `Last updated …`. Do not replace useful data with an error. |
| Query failed without data | Show `Worker unavailable` with `Could not verify this worker`. |
| Terminated | Show a neutral terminated state and last update. Preserve the current click toast and block behavior. |
| Status readiness checking | Show `Checking status` without guessing that the worker is dead. |
| Status readiness unavailable | Show `Status unavailable`; retain worker and project identity. |

Missing and unauthorized must use the same public copy. The renderer should not infer which one occurred. Cloud queries already expose only resources available to the signed in organization. If cloud sign out leaves old React Query data resident, `useWorkspaceQuery()` already gates it with current readiness and organization state, so the preview must use the merged hook result rather than reading raw cloud query caches.

### Freshness and request control

Do not create a separate cache for hover cards. The workspace cache is the canonical projection used by the board, sidebar, route navigation, and card. Daemon events invalidate it, local fallback polling bounds staleness, and cloud polling bounds cloud staleness.

The open card should update in place when the workspace query changes. Show relative freshness from `updatedAt`, but do not display a spinner for every background refetch. If cached data is older than 30 seconds while a refetch is in progress, a quiet `Refreshing` suffix is acceptable. Never remove or disable the link because preview data is stale. Click navigation performs its existing validation independently.

### Interaction contract

* Pointer: open after the existing 300 millisecond delay, above the link by default. Moving into the card may keep it open, though the content is not interactive. Close after the existing 150 millisecond grace period.
* Keyboard: open when the link receives focus. Associate the card with the link using `aria-describedby` while open. Escape closes the card and focus stays on the link. Enter activates the link. Do not move focus into the card.
* Multiple PRs: constrain the entire hover card to a compact maximum height and make the whole card the only vertical scroll container. Do not add a nested scrollbar or visible instructional copy. The scrollbar is the affordance. The card content is focusable only when it overflows so keyboard users can use Arrow Up, Arrow Down, Page Up, and Page Down. Controlled open state must keep the card open while focus is inside it. Escape returns focus to the source link and closes the card.
* Touch: do not intercept the first tap. A tap activates the link exactly as today. Do not invent a long press gesture.
* Collision: use Radix portal rendering, `collisionPadding={8}`, `side="top"`, `align="start"`, and `sticky="partial"`. Allow Radix to flip below near the top edge and shift horizontally near side edges.
* Nested links: Markdown links are already not recursively visited by `remarkSessionLinks`, and `InsideMarkdownLink` prevents inline file buttons inside a link. Keep the preview free of anchors and buttons so no nested interaction is introduced.
* Streaming: rendering a link during token streaming is safe. Hover may show current data, but streaming must never auto activate the link.
* Reduced motion: retain the existing `motion-reduce` conventions when adding any transition class.

## Visual contract

Use the existing `bg-popover`, `text-popover-foreground`, `border-border`, `shadow-md`, `text-muted-foreground`, and semantic status tokens. Keep the standard `w-72` shell and 12 pixel interior padding. Render `frontend/src/renderer/components/AgentAvatar.tsx` with `session.provider`; it already maps official assets from `frontend/src/renderer/assets/agents/` and provides an initial fallback. Reuse the sidepanel semantic tones, but render PR state as a dot plus plain text rather than copying its capsule. No invented harness monogram, chart, cost, token count, PR title, CI row, review row, prompt excerpt, or action row in the first version.

Status should have a 6 pixel semantic dot and print `displayStatus` when available. The title gets at most two lines. Project and freshness each get one line with truncation. Separate the PR area with a quiet border. Its header reads `1 PR` or `3 PRs`. The whole card has one scrollbar only when its natural content exceeds the compact maximum height.

## Exact product code touch points

* Modify `frontend/src/renderer/components/AppLink.tsx` to support a caller supplied lazy hover preview without changing web preview behavior.
* Modify `frontend/src/renderer/components/chat/ChatMarkdown.tsx` to attach the worker preview only to canonical session links.
* Add `frontend/src/renderer/components/SessionLinkPreviewCard.tsx` for query state mapping and presentation.
* Extend `frontend/src/renderer/lib/session-links.ts` only if the pure exact project plus session lookup belongs beside the parser.
* Add English strings to `frontend/src/renderer/i18n/en.json`, then follow the repository localization policy for other locale files.
* Add tests in `frontend/src/renderer/components/AppLink.test.tsx`, `frontend/src/renderer/components/chat/ChatMarkdown.test.tsx`, `frontend/src/renderer/components/SessionLinkPreviewCard.test.tsx`, and `frontend/src/renderer/lib/session-links.test.ts` if the resolver changes.

No backend, OpenAPI, generated schema, route, or storage change is needed.

## Implementation ready React structure

The preview should be composed from AO primitives and semantic classes only. It must not import `useUiStore`, branch on light or dark mode, read `themeStyle`, or contain color literals. The portal inherits the document theme because `applyDocumentTheme()` and `applyDocumentThemeStyle()` set `data-theme` and `data-style-theme` on the root element.

```tsx
<HoverCardContent
  align="start"
  collisionPadding={8}
  side="top"
  sideOffset={6}
  sticky="partial"
  className="max-h-44 w-72 overflow-y-auto p-0 shadow-popover"
>
  <div className="p-3">
    <div className="flex min-w-0 items-start gap-2.5">
      <AgentAvatar className="size-7 shrink-0" decorative provider={session.provider} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-1.5">
          <p className="truncate text-sm font-semibold text-popover-foreground">{session.title}</p>
          <span className="shrink-0 font-mono text-2xs text-passive">#{shortId}</span>
        </div>
        <p className="truncate text-2xs text-muted-foreground">{session.workspaceName}</p>
      </div>
    </div>

    <div className="mt-2.5 flex min-w-0 items-center gap-2">
      <span className={cn("size-dot-sm shrink-0 rounded-full", sessionDot.className)} />
      <span className="min-w-0 truncate text-xs font-medium text-foreground">{statusLabel}</span>
      <span className="ml-auto shrink-0 text-2xs text-passive">{updatedLabel}</span>
    </div>

    {prs.length > 0 ? (
      <section className="mt-2.5 border-t border-border pt-2.5" aria-label={t("sessionLink.pullRequests", { count: prs.length })}>
        <p className="text-2xs font-semibold text-muted-foreground">
          {t("sessionLink.pullRequestCount", { count: prs.length })}
        </p>
        <div className="mt-1.5 grid gap-1.5">
          {prs.map((pr) => (
            <div className="flex min-w-0 items-center gap-2" key={pr.url}>
              <span className="font-mono text-2xs font-medium text-foreground">PR #{pr.number}</span>
              <span className={cn("ml-auto size-dot-sm shrink-0 rounded-full", pr.dotClassName)} />
              <span className={cn("w-[76px] shrink-0 text-2xs font-medium", pr.textClassName)}>{pr.label}</span>
            </div>
          ))}
        </div>
      </section>
    ) : null}
  </div>
</HoverCardContent>
```

Use `getSessionStatusDotView(session)` for the worker dot so the preview matches board and inspector status color and motion. For the status label, prefer `session.displayStatus`; fall back to `getSessionStatusView(session.status).label` only when absent.

The PR presenter should return only semantic classes:

| Result | Dot class | Text class |
| --- | --- | --- |
| Merged | `bg-status-merged` | `text-status-merged` |
| Closed | `bg-status-exited` | `text-status-exited` |
| Draft or Open | `bg-status-in-review` | `text-status-in-review` |
| Mergeable | `bg-status-ready` | `text-status-ready` |
| Blocked | `bg-status-needs-you` | `text-status-needs-you` |
| Checking | `bg-status-idle` | `text-status-idle` |

Use existing radius, type, spacing, border, and shadow utilities. Do not create a worker card CSS file unless scrollbar treatment proves impossible with existing utilities. The default browser scrollbar already follows `color-scheme`; if explicit styling is needed, use `--color-scrollbar`, never a fixed color.

### Theme verification matrix

Visual verification must cover Orchestrate, GitHub, Catppuccin, Dracula, Tokyo Night, Rose Pine, Nord, Gruvbox, and Solarized in both light and dark where supported. At minimum, capture Orchestrate dark, Orchestrate light, GitHub light, Catppuccin dark, and Solarized light. Verify popover surface contrast, hairline visibility, muted text contrast, every status tone, Codex and Claude logo legibility, scrollbar visibility, focus ring, and Radix arrow color.

## Test plan

### Unit and component tests

1. Canonical AO links render a worker card on pointer hover and keyboard focus. Web previews still render their existing card. File links and malformed AO text do not render a worker card.
2. One open card mounts one workspace observer. Several closed links issue no fetch caused by the preview. Rapidly moving between duplicate links shares one query and never calls the single session endpoint.
3. Exact project plus session matching succeeds across projects and rejects a mismatched project with a coincident session id.
4. Loading, active, needs input, checking, unavailable, terminated, missing, inaccessible shaped failure, stale cached data, and no data failure each render the specified copy.
5. A workspace cache update changes status and freshness while the card remains open.
6. Link click, Enter, modified click behavior where applicable, context menu, and terminated click behavior remain unchanged.
7. Focus opens the card, Escape closes it, focus remains on the anchor, and `aria-describedby` is present only while the description exists.
8. The card contains no nested anchors or buttons. Bare AO URLs inside existing Markdown links remain untouched.
9. Long titles, project names, percent encoded ids, and non Latin text truncate without increasing card width.
10. Zero PRs omit the PR section. One PR has no scrolling affordance. Multiple PRs show the correct count, retain merged entries, produce one scrollbar on the whole card, and scroll by pointer and keyboard.
11. Plain status label priority covers merged, closed, draft, mergeable, review blocked, checks blocked, conflict blocked, unknown readiness, and plain open without exposing separate CI or review text.

### Integration and visual checks

Run:

```bash
cd frontend
npm run typecheck
npm test -- --run src/renderer/components/AppLink.test.tsx src/renderer/components/chat/ChatMarkdown.test.tsx src/renderer/components/SessionLinkPreviewCard.test.tsx src/renderer/lib/session-links.test.ts
npm run build
```

Then use the real desktop lab flow for final visual verification. Check pointer and keyboard behavior at every viewport edge, at 100 and 200 percent zoom, in dark and light themes, with reduced motion, and with a narrow Chat column. Use a live worker, a terminated worker, a link to another project, and an inaccessible or deleted target.

## Explicit non goals

The first version should not preview terminal output, conversation text, worker prompts, token usage, costs, changed files, PR checks, or controls. It should not fetch on pointer entry, add a new daemon route, change session URL syntax, or alter navigation rules.
