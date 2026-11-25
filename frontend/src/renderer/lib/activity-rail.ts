import type { SessionPRSummary } from "../hooks/useSessionScmSummary";

/** What one row on the activity rail says happened. */
export type RailKind =
	| "created"
	| "asked"
	| "approval"
	| "question"
	| "commits"
	| "opened"
	| "draft"
	| "merged"
	| "closed"
	| "comment"
	| "reviewNotes"
	| "reply"
	| "approved"
	| "changes"
	| "reviewed";

export type RailEvent = {
	key: string;
	kind: RailKind;
	/** ISO time of the event; for a group, its newest member's. */
	at: string;
	/** A GitHub login, or a commit's author name. */
	author?: string;
	bot?: boolean;
	/** How many events the row stands for: commits pushed, review notes left. */
	count: number;
	/** The second line: an excerpt, a commit, a file and line. */
	detail?: string;
	/** Every thread in the row is resolved. */
	resolved?: boolean;
	/** A commits row's commits, newest first. */
	commits?: { sha: string; subject: string; at?: string }[];
	pr?: { number: number; url: string };
	/** An asked row: who sent the request, and how the agent's turn on it went. */
	sender?: RailSender;
	turn?: RailTurn;
	/** An approval or a question nobody has answered yet. */
	waiting?: boolean;
	/** The turn an asked row stands for, or the turn an approval or a question was asked in. */
	turnId?: string;
	/** An asked row's answered approvals and questions, folded into it, newest first. */
	answered?: RailAnswered[];
	/** A row standing for several requests in a row: each of them, newest first. */
	requests?: RailRequest[];
};

/** An approval or a question the agent asked during a request, already answered. */
export type RailAnswered = { key: string; at: string; kind: "approval" | "question"; summary?: string };

/** One of the requests a folded row stands for. */
export type RailRequest = { key: string; at: string; text: string; turn: RailTurn };

/** Who started a turn: the person, Orchestrator itself (a failing check, a review), or an automation. */
export type RailSender = "you" | "orchestrator" | "automation";

/** How a turn went: its state, how long the agent worked, and how many files it changed when the agent reports that. */
export type RailTurn = {
	state: "queued" | "running" | "completed" | "stopped" | "failed" | "cancelled";
	ms?: number;
	files?: number;
	/** The files changed, so a folded row counts a file two turns touched once. */
	paths?: readonly string[];
	error?: string;
	/** On a folded row: how many of its earlier requests failed. */
	failures?: number;
};

/** One request to the agent and the turn it started. */
export type RailTurnInput = { id: string; at: string; sender: RailSender; text: string; turn: RailTurn };

/** The agent stopping to ask: for an approval, or a question. */
export type RailAskInput = { id: string; at: string; kind: "approval" | "question"; summary: string; waiting: boolean; turnId?: string };

/** What the agent was asked and did in the session, as far as the app knows it. */
export type RailWork = { turns: RailTurnInput[]; asks: RailAskInput[] };

/** What a row is about, as the rail's filter groups it. */
export type RailGroup = "turns" | "asks" | "talk" | "commits" | "pr" | "session";

export const RAIL_GROUPS: readonly RailGroup[] = ["turns", "asks", "talk", "commits", "pr", "session"];

export function railGroup(kind: RailKind): RailGroup {
	switch (kind) {
		case "commits":
			return "commits";
		case "created":
			return "session";
		case "asked":
			return "turns";
		case "approval":
		case "question":
			return "asks";
		case "opened":
		case "draft":
		case "merged":
		case "closed":
			return "pr";
		default:
			return "talk";
	}
}

/** What the rail leaves out: whole groups of rows, and anything a bot did. */
export type RailFilter = { hidden: readonly RailGroup[]; hideBots: boolean };

export const NO_RAIL_FILTER: RailFilter = { hidden: [], hideBots: false };

export const railFilterActive = (filter: RailFilter) => filter.hidden.length > 0 || filter.hideBots;

export function filterRail(events: RailEvent[], filter: RailFilter): RailEvent[] {
	if (!railFilterActive(filter)) return events;
	return events.filter((event) => !filter.hidden.includes(railGroup(event.kind)) && !(filter.hideBots && event.bot));
}

/** How many rows each group has, before any filter. */
export function railGroupCounts(events: RailEvent[]): Record<RailGroup, number> {
	const counts: Record<RailGroup, number> = { turns: 0, asks: 0, talk: 0, commits: 0, pr: 0, session: 0 };
	for (const event of events) counts[railGroup(event.kind)] += 1;
	return counts;
}

export type RailCommit = { sha: string; subject: string; timestamp: string; author?: string };

type RailInput = {
	createdAt?: string | null;
	/** The session's branch, shown under its creation. */
	branch?: string;
	commits: RailCommit[];
	prs: SessionPRSummary[];
	work?: RailWork;
};

// Commits beyond these are older than anyone scrolls the sidebar for.
const RAIL_COMMIT_LIMIT = 50;
// The same for turns; the chat has the whole history.
const RAIL_TURN_LIMIT = 50;

/**
 * The session's activity, newest first: its creation, what the agent was
 * asked and how each turn went, where it stopped to ask, commits, the PRs'
 * opening and closing, and what people said on them (comments, review notes
 * and replies, approvals and change requests). A person's commits or review
 * notes on one day fold into one row.
 *
 * GitHub records a reply in a review thread as its own review, so a review
 * shows only when it approves, asks for changes or says something itself;
 * otherwise the reply already stands for it. Threads carry no time they were
 * resolved, so "resolved" is a note on the thread's row, not an event.
 */
export function buildActivityRail(input: RailInput, dayOf: (iso: string) => string = localDay, now = Date.now()): RailEvent[] {
	return foldRail(railEvents(input), dayOf, now);
}

/**
 * Every event on its own, newest first, before anything folds: what the
 * filter counts and filters. The rail folds what is left after filtering, so a
 * commit whose request is hidden shows again as a commit.
 */
export function railEvents({ createdAt, branch, commits, prs, work }: RailInput): RailEvent[] {
	const events: RailEvent[] = [];
	if (createdAt) events.push({ key: "created", kind: "created", at: createdAt, count: 1, detail: branch || undefined });
	for (const turn of (work?.turns ?? []).slice(-RAIL_TURN_LIMIT)) {
		events.push({ key: `turn:${turn.id}`, kind: "asked", at: turn.at, count: 1, detail: requestExcerpt(turn.text) || undefined, sender: turn.sender, turn: turn.turn, turnId: turn.id });
	}
	for (const ask of work?.asks ?? []) {
		events.push({ key: `ask:${ask.id}`, kind: ask.kind, at: ask.at, count: 1, detail: ask.summary || undefined, waiting: ask.waiting, turnId: ask.turnId });
	}
	for (const commit of commits.slice(0, RAIL_COMMIT_LIMIT)) {
		events.push({ key: `commit:${commit.sha}`, kind: "commits", at: commit.timestamp, author: commit.author || undefined, count: 1, detail: `${commit.sha.slice(0, 7)} ${commit.subject}`, commits: [{ sha: commit.sha, subject: commit.subject, at: commit.timestamp }] });
	}
	for (const pr of prs) {
		const ref = { number: pr.number, url: pr.url };
		if (pr.createdAt) events.push({ key: `pr:${pr.url}:opened`, kind: pr.state === "draft" ? "draft" : "opened", at: pr.createdAt, author: pr.author || undefined, count: 1, detail: pr.title, pr: ref });
		if (pr.state === "merged" && pr.stateChangedAt) events.push({ key: `pr:${pr.url}:merged`, kind: "merged", at: pr.stateChangedAt, count: 1, detail: pr.title, pr: ref });
		if (pr.state === "closed" && pr.stateChangedAt) events.push({ key: `pr:${pr.url}:closed`, kind: "closed", at: pr.stateChangedAt, count: 1, detail: pr.title, pr: ref });
		for (const comment of pr.conversation ?? []) {
			if (!comment.createdAt) continue;
			events.push({ key: `comment:${comment.id}`, kind: "comment", at: comment.createdAt, author: comment.author, bot: comment.isBot, count: 1, detail: commentExcerpt(comment.body), pr: ref });
		}
		for (const thread of pr.threads ?? []) {
			const where = thread.path ? `${thread.path}${thread.line ? `:${thread.line}` : ""}` : undefined;
			thread.comments.forEach((comment, index) => {
				if (!comment.createdAt) return;
				events.push({
					key: `thread:${thread.id}:${comment.id}`,
					kind: index === 0 ? "reviewNotes" : "reply",
					at: comment.createdAt,
					author: comment.author,
					bot: comment.isBot,
					count: 1,
					detail: index === 0 ? where : commentExcerpt(comment.body),
					resolved: thread.resolved,
					pr: ref,
				});
			});
		}
		for (const review of pr.review.reviews ?? []) {
			const kind: RailKind | undefined = review.verdict === "approved" ? "approved" : review.verdict === "changes_requested" ? "changes" : review.body?.trim() ? "reviewed" : undefined;
			if (!kind) continue;
			events.push({ key: `review:${review.reviewUrl ?? `${review.reviewerId}:${review.submittedAt}`}`, kind, at: review.submittedAt, author: review.reviewerId, bot: review.isBot, count: 1, detail: review.body ? commentExcerpt(review.body) : undefined, pr: ref });
		}
	}
	events.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
	return events;
}

/** How the rail folds: the agent's work into its requests, then a person's commits or review notes on one day. */
export function foldRail(events: RailEvent[], dayOf: (iso: string) => string = localDay, now = Date.now()): RailEvent[] {
	return foldRepeats(foldWork(events, now), dayOf);
}

// Requests from you this close together, with nothing else between them, read as one sitting.
export const REQUEST_BURST_GAP_MS = 30 * 60_000;
// A turn's commit can land a little after the agent reports the turn done.
const COMMIT_GRACE_MS = 2 * 60_000;

const requestEnd = (event: RailEvent, now: number) => {
	const start = Date.parse(event.at);
	if (event.turn?.state === "running" || event.turn?.state === "queued") return now;
	return start + (event.turn?.ms ?? 0);
};

/**
 * Folds the agent's side of the session so a busy one stays short:
 *
 * - an approval or a question that was answered becomes part of the
 *   request it was asked in; one still waiting keeps its own row;
 * - a commit made while the agent worked on a request becomes part of it;
 * - requests from you in one sitting (each within 30 minutes of the last
 *   turn ending, with no other row between them) become one row that shows
 *   the newest and opens to list them all.
 *
 * Requests Orchestrator or an automation sent, what people said, and PR
 * events never fold, and they end a sitting.
 */
function foldWork(events: RailEvent[], now: number): RailEvent[] {
	if (!events.some((event) => event.kind === "asked")) return events;
	const chronological = [...events].reverse().map((event) => ({ ...event }));
	const turns = new Map<string, RailEvent>();
	for (const event of chronological) if (event.kind === "asked" && event.turnId) turns.set(event.turnId, event);
	const requests = chronological.filter((event) => event.kind === "asked");
	const kept: RailEvent[] = [];
	for (const event of chronological) {
		if ((event.kind === "approval" || event.kind === "question") && !event.waiting && event.turnId) {
			const turn = turns.get(event.turnId);
			if (turn) {
				turn.answered = [{ key: event.key, at: event.at, kind: event.kind, summary: event.detail }, ...(turn.answered ?? [])];
				continue;
			}
		}
		if (event.kind === "commits") {
			const at = Date.parse(event.at);
			let turn: RailEvent | undefined;
			for (const request of requests) if (Date.parse(request.at) <= at && at <= requestEnd(request, now) + COMMIT_GRACE_MS) turn = request;
			if (turn) {
				turn.commits = [...(event.commits ?? []), ...(turn.commits ?? [])];
				continue;
			}
		}
		kept.push(event);
	}
	const out: RailEvent[] = [];
	for (const event of kept) {
		const last = out[out.length - 1];
		const mine = event.kind === "asked" && event.sender === "you";
		const burst = mine && last?.kind === "asked" && last.sender === "you" && Date.parse(event.at) - requestEnd(last, now) <= REQUEST_BURST_GAP_MS;
		if (!burst) {
			out.push(event);
			continue;
		}
		out[out.length - 1] = mergeRequests(last, event);
	}
	return out.reverse();
}

const asRequest = (event: RailEvent): RailRequest => ({ key: event.key, at: event.at, text: event.detail ?? "", turn: event.turn ?? { state: "completed" } });

// The older row keeps its key, so a row that is open stays open as requests join it.
function mergeRequests(older: RailEvent, newer: RailEvent): RailEvent {
	const members = [asRequest(newer), ...(older.requests ?? [asRequest(older)])];
	const known = members.filter((member) => member.turn.paths);
	const paths = known.length ? [...new Set(known.flatMap((member) => member.turn.paths ?? []))] : undefined;
	const durations = members.map((member) => member.turn.ms).filter((ms): ms is number => ms !== undefined);
	const failures = members.slice(1).filter((member) => member.turn.state === "failed").length;
	return {
		...older,
		at: newer.at,
		detail: newer.detail,
		count: members.length,
		requests: members,
		// The newest request's turn sets the state; the earlier ones add their time and files.
		turn: { ...newer.turn!, ms: durations.length ? durations.reduce((sum, ms) => sum + ms, 0) : undefined, paths, files: paths?.length, failures: failures || undefined },
		answered: older.answered || newer.answered ? [...(newer.answered ?? []), ...(older.answered ?? [])] : undefined,
		commits: older.commits || newer.commits ? [...(newer.commits ?? []), ...(older.commits ?? [])] : undefined,
	};
}

/** One line in an open request row: one of its requests, or a commit made while the agent worked. */
export type RailWorkItem = { key: string; kind: "request" | "commit"; text: string; failed?: boolean };

/**
 * What an open request row lists: each request folded into it, newest
 * first, then the commits made while the agent worked. Approvals and
 * questions stay counts on the row.
 */
export function railWorkItems(event: RailEvent): RailWorkItem[] {
	if (event.kind !== "asked") return [];
	const requests = (event.requests?.length ?? 0) > 1 ? event.requests! : [];
	return [
		...requests.map((request): RailWorkItem => ({ key: request.key, kind: "request", text: request.text, failed: request.turn.state === "failed" || undefined })),
		...(event.commits ?? []).map((commit): RailWorkItem => ({ key: `commit:${commit.sha}`, kind: "commit", text: commit.subject })),
	];
}

// A person's commits, or review notes, in a row on one day: one line that says how many.
function foldRepeats(events: RailEvent[], dayOf: (iso: string) => string): RailEvent[] {
	const out: RailEvent[] = [];
	for (const event of events) {
		const last = out[out.length - 1];
		const folds = last
			&& (event.kind === "commits" || event.kind === "reviewNotes")
			&& last.kind === event.kind
			&& last.author === event.author
			&& last.pr?.url === event.pr?.url
			&& dayOf(last.at) === dayOf(event.at);
		if (!folds) {
			out.push({ ...event });
			continue;
		}
		last.count += 1;
		if (event.kind === "commits") last.commits = [...(last.commits ?? []), ...(event.commits ?? [])];
		if (event.kind === "reviewNotes") {
			const where = [last.detail, event.detail].filter(Boolean);
			last.detail = [...new Set(where.join(" · ").split(" · "))].join(" · ");
			last.resolved = Boolean(last.resolved && event.resolved);
		}
	}
	return out;
}

/** The words a comment opens with: no quoted text, @mention, markup or HTML comments. */
export function commentExcerpt(body: string): string {
	const lines = body
		.replace(/<!--[\s\S]*?-->/g, "")
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line && !line.startsWith(">"));
	const first = lines[0] ?? "";
	return first
		.replace(/^@[\w-]+\s*/, "")
		.replace(/<[^>]+>/g, "")
		.replace(/[*_`#]+/g, "")
		.trim();
}

/** A request's first line, as it was written: "#42" and a path keep their marks. */
export function requestExcerpt(text: string): string {
	return text.split("\n").map((line) => line.trim()).find(Boolean) ?? "";
}

export function localDay(iso: string): string {
	const date = new Date(iso);
	return Number.isNaN(date.getTime()) ? "" : `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}
