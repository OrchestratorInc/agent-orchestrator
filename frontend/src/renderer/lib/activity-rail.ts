import type { SessionPRSummary } from "../hooks/useSessionScmSummary";

/** What one row on the activity rail says happened. */
export type RailKind =
	| "created"
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
	commits?: { sha: string; subject: string }[];
	pr?: { number: number; url: string };
};

export type RailCommit = { sha: string; subject: string; timestamp: string; author?: string };

type RailInput = {
	createdAt?: string | null;
	/** The session's branch, shown under its creation. */
	branch?: string;
	commits: RailCommit[];
	prs: SessionPRSummary[];
};

// Commits beyond these are older than anyone scrolls the sidebar for.
const RAIL_COMMIT_LIMIT = 50;

/**
 * The session's activity, newest first: its creation, commits, the PRs'
 * opening and closing, and what people said on them (comments, review notes
 * and replies, approvals and change requests). A person's commits or review
 * notes on one day fold into one row.
 *
 * GitHub records a reply in a review thread as its own review, so a review
 * shows only when it approves, asks for changes or says something itself;
 * otherwise the reply already stands for it. Threads carry no time they were
 * resolved, so "resolved" is a note on the thread's row, not an event.
 */
export function buildActivityRail({ createdAt, branch, commits, prs }: RailInput, dayOf: (iso: string) => string = localDay): RailEvent[] {
	const events: RailEvent[] = [];
	if (createdAt) events.push({ key: "created", kind: "created", at: createdAt, count: 1, detail: branch || undefined });
	for (const commit of commits.slice(0, RAIL_COMMIT_LIMIT)) {
		events.push({ key: `commit:${commit.sha}`, kind: "commits", at: commit.timestamp, author: commit.author || undefined, count: 1, detail: `${commit.sha.slice(0, 7)} ${commit.subject}`, commits: [{ sha: commit.sha, subject: commit.subject }] });
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
	return foldRepeats(events, dayOf);
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

export function localDay(iso: string): string {
	const date = new Date(iso);
	return Number.isNaN(date.getTime()) ? "" : `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}
