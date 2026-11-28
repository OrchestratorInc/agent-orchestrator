import { commentTextLines, summaryPreview } from "../../product-ui/src/comment-text";
import { scmUserAvatarUrl } from "../../product-ui/src/scm-avatar";
import { reviewCommentIsOpen, type ReviewComment, type ReviewPRBlock, type ReviewReviewerStatus } from "../../shared/review/review-comments";
import type { PRReviewState, ReviewRun, SessionPRSummary, SessionReviews } from "./api";
import { relativeTime } from "./notificationView";

// What the PR page's Comments section shows, worked out apart from drawing it.
// The rules are the desktop Reviews tab's (ReviewComments.tsx), so a comment
// reads the same, and is sent or skipped the same, on both.

export type MobileReviewBlock = ReviewPRBlock<SessionPRSummary, PRReviewState>;

export const verdictLabels: Record<ReviewReviewerStatus, string> = {
	approved: "Approved",
	changes_requested: "Changes requested",
	commented: "Commented",
	pending: "Pending",
};

export type VerdictTone = "green" | "amber" | "muted";

export function verdictTone(status: ReviewReviewerStatus): VerdictTone {
	return status === "approved" ? "green" : status === "changes_requested" ? "amber" : "muted";
}

/** The folded row's one line: the text's first words, less a verdict it opens by restating. */
export function commentPreview(comment: Pick<ReviewComment, "body" | "kind" | "verdict">): string {
	const verdict = comment.kind === "summary" && comment.verdict ? [verdictLabels[comment.verdict]] : [];
	return summaryPreview(commentTextLines(comment.body), verdict);
}

/** `src/app.ts:12`, or just `app.ts:12` where the row is short of room. */
export function commentLocation(comment: Pick<ReviewComment, "file" | "line">, short = false): string {
	if (!comment.file) return "";
	const file = short ? comment.file.split("/").pop() ?? comment.file : comment.file;
	return `${file}${comment.line ? `:${comment.line}` : ""}`;
}

/** An approval asks nothing of the worker: it can be sent by hand, but Send all skips it and it starts folded. */
export function isApproval(comment: Pick<ReviewComment, "kind" | "verdict">): boolean {
	return comment.kind === "summary" && comment.verdict === "approved";
}

export function commentIsSent(comment: Pick<ReviewComment, "delivered" | "sentKey">, sent: ReadonlySet<string>): boolean {
	return comment.delivered || sent.has(comment.sentKey);
}

/** Open, with words, and not yet with the worker: what Send to agent is for. */
export function commentCanSend(comment: ReviewComment): boolean {
	return reviewCommentIsOpen(comment) && Boolean(comment.body);
}

/** An open comment still waiting for the worker starts open, so Send to agent is in reach; the rest start folded. */
export function commentStartsExpanded(comment: ReviewComment, sent: ReadonlySet<string>): boolean {
	return commentCanSend(comment) && !isApproval(comment) && !commentIsSent(comment, sent);
}

/** What Send all delivers: every open comment with words, no approval, nothing already sent or on its way. */
export function unsentOpenComments(comments: readonly ReviewComment[], sent: ReadonlySet<string>, sending: ReadonlySet<string>): ReviewComment[] {
	return comments.filter((comment) => commentStartsExpanded(comment, sent) && !sending.has(comment.sentKey));
}

export function blockOnGitLab(block: Pick<MobileReviewBlock, "provider" | "webUrl">): boolean {
	return block.provider?.provider ? block.provider.provider === "gitlab" : /\/-\/merge_requests\/\d+/.test(block.webUrl);
}

/** A thread takes a reply in place; a provider review or comment is answered by quoting it. GitHub only, as on desktop. */
export function commentReplyOffered(comment: ReviewComment, block: Pick<MobileReviewBlock, "provider" | "webUrl">): boolean {
	if (comment.resolved || blockOnGitLab(block)) return false;
	return comment.kind === "inline" ? Boolean(comment.threadId) : comment.source === "provider";
}

/** "Reply to maya-chen · login.ts:4". The Orchestrator's comments carry its agent as their author; the row calls it Orchestrator, and so does this. */
export function commentReplyLabel(comment: Pick<ReviewComment, "author" | "source" | "file" | "line">): string {
	const who = comment.source === "orchestrator" ? "Orchestrator" : comment.author;
	const where = commentLocation(comment, true);
	return `Reply to ${who}${where ? ` · ${where}` : ""}`;
}

export function commentCanResolve(comment: ReviewComment): boolean {
	return comment.kind === "inline" && !comment.resolved && Boolean(comment.url);
}

/** A person's or bot's picture on the forge, for the PR the comment is on. */
export function commentAvatarUrl(author: string, block: Pick<MobileReviewBlock, "provider" | "webUrl">): string | undefined {
	return scmUserAvatarUrl(blockOnGitLab(block) ? "gitlab" : "github", block.webUrl, author.replace(/\[bot\]$/i, ""));
}

/** "3m ago", "now", or "" for a missing time. */
export function commentAge(iso: string | undefined, now = Date.now()): string {
	const age = iso ? relativeTime(iso, now) : "";
	return age && age !== "now" ? `${age} ago` : age;
}

export type ReviewerStatusLine =
	| { kind: "working"; label: string; facts: string[] }
	| { kind: "failed" | "quiet"; label: string; facts: string[] };

const ACTIVE = new Set<ReviewRun["status"]>(["queued", "launching", "running", "cancelling"]);

function elapsed(ms: number): string {
	const secs = Math.max(0, Math.floor(ms / 1000));
	if (secs < 60) return `${secs}s`;
	const mins = Math.floor(secs / 60);
	return mins < 60 ? `${mins}m ${secs % 60}s` : `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

/**
 * One line under the reviewer: what it is doing now (with the commit and how
 * long it has run), that the last pass failed, or when it last reviewed. The
 * verdict lives on the review in Comments, as on the desktop.
 */
export function reviewerStatusLine(
	data: Pick<SessionReviews, "reviews" | "runs" | "reviewerActivityState">,
	review: PRReviewState,
	now = Date.now(),
): ReviewerStatusLine {
	const running = data.reviews.filter((state) => state.status === "running" || (state.latestRun && ACTIVE.has(state.latestRun.status)));
	const phaseRun = running.map((state) => state.latestRun).find((run): run is ReviewRun => Boolean(run && ACTIVE.has(run.status)));
	if (running.length) {
		const label = phaseRun?.status === "cancelling" ? "Stopping review"
			: phaseRun?.status === "queued" || phaseRun?.status === "launching" ? "Starting review"
			: data.reviewerActivityState === "waiting_input" ? "Waiting for input"
			: `Reviewing ${running.map((state) => `#${state.prNumber}`).join(", ")}`;
		const startedAt = phaseRun ? phaseRun.startedAt || phaseRun.launchingAt || phaseRun.createdAt : undefined;
		const facts = [phaseRun?.targetSha.slice(0, 7) ?? "", startedAt ? elapsed(now - Date.parse(startedAt)) : ""].filter(Boolean);
		return { kind: "working", label, facts };
	}
	const runs = [...data.runs, ...[review.latestRun, review.previousRun].filter((run): run is ReviewRun => Boolean(run))]
		.filter((run) => run.prUrl === review.prUrl)
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
	if (runs[0]?.status === "failed") return { kind: "failed", label: "Last review failed.", facts: [] };
	const completed = runs.find((run) => run.status === "completed");
	if (!completed) return { kind: "quiet", label: "Not reviewed yet", facts: [] };
	return { kind: "quiet", label: `Last reviewed ${commentAge(completed.createdAt, now)}`, facts: [completed.targetSha.slice(0, 7)].filter(Boolean) };
}

export type ReviewNowAction = "review" | "retry" | "stop" | "none";

/** One button for the whole session, as on desktop: Review now, Retry review after a failure, Stop review while one runs. */
export function reviewNowAction(data: Pick<SessionReviews, "reviews" | "runs" | "reviewerActivityState">, review: PRReviewState): ReviewNowAction {
	const line = reviewerStatusLine(data, review);
	if (line.kind === "working") return "stop";
	if (review.status === "ineligible") return "none";
	return line.kind === "failed" ? "retry" : "review";
}

export const reviewNowLabels: Record<Exclude<ReviewNowAction, "none">, string> = {
	review: "Review now",
	retry: "Retry review",
	stop: "Stop review",
};
