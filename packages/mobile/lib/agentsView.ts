// Board vocabulary for the Agents tab. Pure — no React Native or Expo imports —
// so the zoning, archive rule and copy are unit-testable, the same split as
// prView.ts / orchestratorView.ts.
//
// Shares desktop's language (frontend/src/renderer/components/SessionsBoard.tsx
// and lib/session-presentation.ts): same column derivation, same archive rule,
// same "PR #12, #13 open" phrasing. The board's order is mobile's own — see
// groupSessions.
import type { DashboardPR, DashboardSession, KanbanColumn } from "./api";
import { relativeTime } from "./notificationView";
import { prLifecycle, type Tone } from "./prView";
import { attentionOf, sessionTitle } from "./sessionStatus";
import { statusVisual, type Theme } from "./theme";

/**
 * The board's sections: desktop's delivery lanes, plus one mobile-only section
 * above them.
 *
 * Four of these are the daemon's own Kanban columns, so the two apps place a
 * session identically. `needs_you` is deliberately mobile's: a worker blocked
 * on a person has no PR yet, so desktop files it under Building alongside every
 * other agent that happens to be running. That is right for a pipeline view and
 * wrong for a phone, which is opened to find what is stuck.
 */
export type BoardZone = "needs_you" | "needs_review" | "ready" | "building" | "validating";

/**
 * Statuses where the agent itself is waiting on a person.
 *
 * `exited` is not here: a stopped agent needs no decision from a person, and the
 * daemon leaves every agent stopped after a restart; the desktop and mobile chat
 * screens resume it when the session is opened. Counting it would put the whole board under "Needs you".
 *
 * Deliberately agent-level only. `ci_failed` and `changes_requested` are PR
 * facts, and the daemon already decides whether AO or a person owns their next
 * turn — lifting them here would second-guess that and split one PR's lifecycle
 * across two sections.
 */
const AGENT_BLOCKED = new Set(["needs_input", "stuck", "errored"]);

export function agentBlocked(session: Pick<DashboardSession, "status" | "displayStatus">): boolean {
	return AGENT_BLOCKED.has(session.status ?? "") || session.displayStatus === "Blocked";
}

/**
 * The daemon's column, falling back to deriving one.
 *
 * Mirrors desktop's `toKanbanColumn`: trust the server's placement when it sends
 * one, because it is derived from durable delivery facts that the client cannot
 * see — whether AO's review pass is mid-run, whether auto-inject is configured.
 * The fallback only covers a daemon too old to send the field.
 */
export function kanbanColumnOf(session: DashboardSession): KanbanColumn {
	if (session.kanbanColumn) return session.kanbanColumn;
	switch (attentionOf(session)) {
		case "merge":
			return "ready";
		case "pending":
			return "validating";
		case "respond":
		case "action":
		case "review":
			return "needs_review";
		case "done":
			return "archive";
		default:
			return "building";
	}
}

/**
 * Which section a session belongs in.
 *
 * Agent-level blockage outranks delivery placement: a worker waiting on your
 * reply is the reason the app was opened, whether or not it has produced a PR.
 */
export function boardZoneOf(session: DashboardSession): BoardZone {
	if (agentBlocked(session)) return "needs_you";
	const column = kanbanColumnOf(session);
	// `archive` never reaches a section — isArchived routes terminated runtimes
	// to the archive strip before grouping.
	return column === "archive" ? "building" : column;
}

/**
 * A shape for each status, so state does not rest on colour alone.
 *
 * The row already tints its trailing label by status, which is invisible to a
 * colour-blind reader and weak in bright sun. A glyph adds a second channel
 * carrying the same fact.
 *
 * Feather names rather than an icon component, so this stays a pure mapping the
 * row can render however it likes — and so it is testable without React Native.
 */
export type WorkerStatusGlyph = "alert-circle" | "message-square" | "x-octagon" | "check-circle" | "git-pull-request" | "loader" | "moon";

export function workerStatusGlyph(status?: string | null): WorkerStatusGlyph | null {
	switch (status) {
		case "needs_input":
			return "message-square";
		case "changes_requested":
		case "commented":
			return "message-square";
		case "stuck":
		case "errored":
		case "exited":
			return "alert-circle";
		case "ci_failed":
			return "x-octagon";
		case "mergeable":
		case "approved":
			return "check-circle";
		case "merged":
		case "pr_open":
		case "draft":
		case "review_pending":
			return "git-pull-request";
		case "working":
		case "detecting":
		case "spawning":
			return "loader";
		case "idle":
			return "moon";
		default:
			// No glyph beats a meaningless one: an unknown status has nothing
			// specific to say, and a generic dot would only add noise.
			return null;
	}
}

/**
 * Whether a session belongs in the archive rather than on the board.
 *
 * Desktop's exact rule (`isArchivedSession`): a dead *runtime*, not a finished
 * outcome. Deliberately not `attentionOf(s) === "done"` — a session that has
 * merged but whose agent is still running belongs in Ready to merge, and only
 * a terminated runtime is archive.
 */
export function isArchived(session: DashboardSession): boolean {
	return session.isTerminated === true || session.status === "terminated";
}

/**
 * The board's sections. Not desktop's delivery lanes: those answer "how far
 * along is each PR", a pipeline question. A phone is opened to answer "what
 * happened since I last looked", so below Needs you everything is one list,
 * newest event first, and the row's own status says which lane it is in.
 */
export type BoardSectionZone = "needs_you" | "recent";

export type BoardSection = { zone: BoardSectionZone; label: string; color: string; data: DashboardSession[] };

export function sectionMeta(t: Theme, zone: BoardSectionZone): { label: string; color: string } {
	return zone === "needs_you" ? { label: "Needs you", color: t.amber } : { label: "Recent", color: t.textTertiary };
}

/**
 * When something a person would notice last happened to the session: the
 * daemon's `lastEventAt` (an activity-state change, a PR lifecycle or CI change,
 * a review). Older daemons don't send it; their activity timestamp only moves
 * on state changes too, so it is the honest fallback.
 */
export function eventAtOf(session: DashboardSession): string {
	return session.lastEventAt || session.lastActivityAt || session.createdAt || "";
}

function time(value: string | null | undefined): number {
	if (!value) return 0;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? 0 : parsed;
}

export type WorkerRowPresentation = {
	title: string;
	project: string;
	branch: string | null;
	trailing: string;
	trailingKind: "status" | "time";
};

function compactProjectLabel(value: string, max = 20): string {
	if (value.length <= max) return value;
	const keep = max - 1;
	const head = Math.ceil(keep / 2);
	const tail = Math.floor(keep / 2);
	return `${value.slice(0, head)}…${value.slice(value.length - tail)}`;
}

/**
 * The compact identity and state shown by the Workers list.
 *
 * Active states earn a semantic label. Quiet states use the last-activity age
 * instead, because repeating "Idle" down an entire section adds less context
 * than showing which worker changed most recently.
 */
export function workerRowPresentation(
	t: Theme,
	session: DashboardSession,
	projectName?: string,
	now: number = Date.now(),
): WorkerRowPresentation {
	const title = sessionTitle(session);
	const visual = statusVisual(t, session.status);
	const elapsedStatuses = new Set(["idle", "no_signal", "unknown", "done", "killed", "terminated"]);
	// The time the board is ordered by, so a row says why it sits where it does.
	const elapsed = relativeTime(eventAtOf(session), now);
	const useElapsed = elapsedStatuses.has(session.status ?? "") && Boolean(elapsed);

	return {
		title,
		// A standalone agent session has no project at all, so there is nothing to
		// abbreviate — say what it is rather than showing an empty slot.
		project: projectName?.trim() || (session.projectId ? compactProjectLabel(session.projectId) : "Standalone"),
		branch: showBranch(session.branch, title) ? session.branch : null,
		trailing: useElapsed ? elapsed : visual.label,
		trailingKind: useElapsed ? "time" : "status",
	};
}

function comparePinned(a: DashboardSession, b: DashboardSession): number {
	return Number(Boolean(b.isPinned)) - Number(Boolean(a.isPinned));
}

// Compared as instants: the daemon emits RFC 3339 with variable fractional
// digits, which do not sort as strings ("10:00:00.5Z" < "10:00:00Z").
function compareEvent(a: DashboardSession, b: DashboardSession, newestFirst: boolean): number {
	const delta = time(eventAtOf(a)) - time(eventAtOf(b));
	return newestFirst ? -delta : delta;
}

/**
 * The board: Pinned, Needs you, Recent, and the archive.
 *
 * Needs you reads oldest first, so whoever has been waiting longest is at the
 * top. Recent reads newest first. A busy agent does not float up for being
 * busy — see eventAtOf. Empty sections are dropped rather than rendered as
 * empty headers; on a phone a run of empty titles is most of the screen.
 */
export function groupSessions(
	t: Theme,
	sessions: DashboardSession[],
): { pinned: DashboardSession[]; sections: BoardSection[]; archived: DashboardSession[] } {
	const pinned: DashboardSession[] = [];
	const needsYou: DashboardSession[] = [];
	const recent: DashboardSession[] = [];
	const archived: DashboardSession[] = [];
	for (const s of sessions) {
		if (isArchived(s)) archived.push(s);
		else if (s.isPinned) pinned.push(s);
		else if (boardZoneOf(s) === "needs_you") needsYou.push(s);
		else recent.push(s);
	}
	// Pinning is a deliberate bookmark, so the most recently pinned worker gets
	// the first slot. Activity is the fallback for older daemon versions.
	pinned.sort((a, b) => (b.pinnedAt ?? b.lastActivityAt ?? "").localeCompare(a.pinnedAt ?? a.lastActivityAt ?? ""));
	needsYou.sort((a, b) => compareEvent(a, b, false));
	recent.sort((a, b) => compareEvent(a, b, true));

	const sections = (["needs_you", "recent"] as const)
		.map((zone) => ({ zone, ...sectionMeta(t, zone), data: zone === "needs_you" ? needsYou : recent }))
		.filter((section) => section.data.length > 0);

	// Pin history deliberately kept close, then show the newest remaining history.
	archived.sort((a, b) => comparePinned(a, b) || compareEvent(a, b, true));
	return { pinned, sections, archived };
}

/**
 * The order the user is looking at, held still while they look.
 *
 * Re-sorting on every poll would move a row out from under a thumb. The board
 * takes a snapshot when it is opened or refreshed and keeps each section in
 * that order; rows still update in place. Section membership stays live, so a
 * worker that starts waiting on you still moves to Needs you at once.
 */
export type OrderSnapshot = { rank: Record<string, number>; eventAt: Record<string, string>; pinned: Record<string, true> };

export function snapshotOrder(ordered: DashboardSession[], keyOf: (s: DashboardSession) => string): OrderSnapshot {
	const rank: Record<string, number> = {};
	const eventAt: Record<string, string> = {};
	const pinned: Record<string, true> = {};
	ordered.forEach((s, i) => {
		rank[keyOf(s)] = i;
		eventAt[keyOf(s)] = eventAtOf(s);
		if (s.isPinned) pinned[keyOf(s)] = true;
	});
	return { rank, eventAt, pinned };
}

/**
 * One section's sessions in a snapshot's order. Sessions the snapshot has not
 * seen go first, where a new one belongs anyway; Array.sort is stable, so they
 * keep their fresh order among themselves.
 */
export function holdOrder(
	snapshot: OrderSnapshot,
	data: DashboardSession[],
	keyOf: (s: DashboardSession) => string,
): DashboardSession[] {
	return [...data].sort((a, b) => (snapshot.rank[keyOf(a)] ?? -1) - (snapshot.rank[keyOf(b)] ?? -1));
}

/**
 * How many sessions have news since the snapshot: an event newer than the one
 * it recorded, or not in it at all. The count on the "N updated" pill.
 */
export function updatedSince(
	snapshot: OrderSnapshot,
	sessions: DashboardSession[],
	keyOf: (s: DashboardSession) => string,
): number {
	return sessions.filter((s) => {
		const before = snapshot.eventAt[keyOf(s)];
		return before === undefined || time(eventAtOf(s)) > time(before);
	}).length;
}

/**
 * Whether the board should take a new snapshot from the fresh order.
 *
 * - No snapshot yet, or one taken before anything loaded.
 * - The user pinned or unpinned a worker. It is their own action, so it applies
 *   at once, even while the pill is holding other news back.
 * - The held order differs from the fresh one with no news behind it.
 * - News arrived without moving anything (the row was already in place). It is
 *   already in view, so the snapshot absorbs it. Otherwise the pill would count
 *   it later, and a pin made after it would stay ranked by its old position.
 *
 * Only a reorder with news behind it is held, behind the "N updated" pill. A
 * snapshot taken from `fresh` never asks for another, so this cannot loop.
 */
export function shouldResnapshot(
	snapshot: OrderSnapshot | null,
	fresh: DashboardSession[],
	stale: boolean,
	keyOf: (s: DashboardSession) => string,
): boolean {
	if (snapshot === null || Object.keys(snapshot.rank).length === 0) return true;
	if (fresh.some((s) => !!s.isPinned !== (snapshot.pinned[keyOf(s)] === true))) return true;
	const news = updatedSince(snapshot, fresh, keyOf) > 0;
	return stale !== news;
}

/**
 * Whether the branch line says anything the title didn't.
 *
 * Desktop's `sameLabel`, unchanged — it strips only conventional git prefixes.
 *
 * Two earlier versions of this were stricter and both hid too much. Comparing
 * against the SESSION ID stopped making sense once titles came from `issueId`,
 * because the id then appeared nowhere on the card. And normalising away AO's
 * own `ao/<id>/root` scaffolding hid the branch on every unnamed session — but
 * that string is the worktree, it is the only place the card names it, and
 * desktop shows it. Only a branch that genuinely restates the title is dropped.
 */
export function showBranch(branch: string | null | undefined, title: string): boolean {
	const b = branch?.trim();
	if (!b) return false;
	const normalize = (v: string) =>
		v
			.toLowerCase()
			.replace(/^(feat|fix|chore|refactor|session)\//, "")
			.replace(/[^a-z0-9]+/g, "");
	return normalize(b) !== normalize(title);
}

// Tracker providers whose ids the intake daemon stamps sessions with, in
// "<provider>:<native>" form. Ported from desktop's TRACKER_PROVIDER_PREFIXES;
// adding Linear or Jira later is one more prefix here.
const TRACKER_PROVIDER_PREFIXES = ["github:"];

/**
 * The issue id when it came from tracker intake, or null for a manually created
 * session.
 *
 * `issueId` is free text — the daemon stores whatever the spawn caller passed
 * (`seedRecord`: `IssueID: cfg.IssueID`, no validation), so a session created by
 * hand carries the task name typed at spawn rather than a tracker reference.
 * Desktop shows the chip only for real tracker ids and hides the rest, and the
 * chip is a poor home for a sentence anyway.
 */
export function trackerIssueId(issueId?: string | null): string | null {
	const id = issueId?.trim();
	if (!id) return null;
	return TRACKER_PROVIDER_PREFIXES.some((prefix) => id.startsWith(prefix)) ? id : null;
}

/**
 * The card's PR line, grouped by lifecycle the way desktop's board card does:
 * `PR #12, #13 open`. Returns null when the session has no PR, so the card
 * renders nothing rather than an empty row.
 */
export function prLine(session: DashboardSession): { text: string; tone: Tone } | null {
	const list: DashboardPR[] = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	const real = list.filter((pr) => pr?.number > 0);
	if (real.length === 0) return null;

	// Group in first-seen order, matching desktop's groupPRsByLifecycle.
	const groups = new Map<string, number[]>();
	for (const pr of real) {
		const life = prLifecycle(pr);
		const nums = groups.get(life);
		if (nums) nums.push(pr.number);
		else groups.set(life, [pr.number]);
	}

	const parts = [...groups.entries()].map(([life, nums]) => `${nums.map((n) => `#${n}`).join(", ")} ${life}`);
	// One tone for the whole line: the worst lifecycle present.
	const lifecycles = [...groups.keys()];
	const tone: Tone = lifecycles.includes("closed")
		? "error"
		: lifecycles.includes("open")
			? "success"
			: lifecycles.includes("merged")
				? "merged"
				: "passive";
	return { text: `PR ${parts.join(" · ")}`, tone };
}
