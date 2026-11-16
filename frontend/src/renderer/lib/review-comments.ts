import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prRepositoryFromUrl } from "./pr-display";
import { prConversationComments } from "./pr-review-comments";
import type { PRReviewState, ReviewRunFacts } from "./session-reviews";

/**
 * The Reviews tab's comment list: for each open PR, one list holding the
 * Orchestrator reviewer's passes and inline comments beside the provider's
 * reviews, review threads and top-level comments. A comment is a comment;
 * where it came from is shown on its row, not by splitting the list.
 *
 * Everything here is read from data the renderer already has: the session's
 * review runs (GET /sessions/{id}/reviews) and the PR summary the SCM observer
 * stores (GET /sessions/{id}/pr). An Orchestrator inline comment is told apart
 * from a person's by the provider review it was posted in: the summary's
 * comment links carry that review's id, and each run records the id of the
 * review it posted.
 */

export type ReviewVerdict = "approved" | "changes_requested" | "commented";

export type ReviewReply = { id: string; author: string; isBot: boolean; body: string; createdAt?: string };

export type ReviewComment = {
	/** Unique within its PR. */
	key: string;
	/** A review's summary, an inline review thread, or a top-level PR comment. */
	kind: "summary" | "inline" | "comment";
	/** Orchestrator's own reviewer, or a person or bot on the provider. */
	source: "orchestrator" | "provider";
	/** The provider login, or the reviewer agent for the Orchestrator. */
	author: string;
	/** The agent that ran an Orchestrator review. */
	harness?: string;
	isBot: boolean;
	body: string;
	/** When it was written (ISO). */
	at?: string;
	file?: string;
	line?: number;
	/** The comment or review on the provider. */
	url?: string;
	/** A review summary's verdict. */
	verdict?: ReviewVerdict;
	/** An inline thread resolved on the provider. */
	resolved: boolean;
	/** A review a later one from the same reviewer replaced. Listed with resolved ones. */
	earlier: boolean;
	/** The provider thread, so a reply nests under it. */
	threadId?: string;
	replies: ReviewReply[];
	/** Already sent to the worker by automatic review injection. */
	delivered: boolean;
	/** The Orchestrator run that wrote it, for Cloud's stored-review delivery. */
	runId?: string;
	/** The provider reviewer to ask again, for a summary. */
	rerequestReviewerId?: string;
	/** An Orchestrator pass an agent asked for, rather than a person or auto-review. */
	requestedByAgent?: boolean;
};

export type ReviewReviewerStatus = ReviewVerdict | "pending";

export type ReviewReviewer = {
	id: string;
	/** The Orchestrator reviewer, drawn with its agent's logo. */
	orchestrator: boolean;
	harness?: string;
	isBot: boolean;
	isTeam?: boolean;
	status: ReviewReviewerStatus;
	/** A provider reviewer who can be asked to review again. */
	canRerequest: boolean;
};

export type ReviewPRBlock = {
	key: string;
	number: number;
	title: string;
	/** Set when the session's PRs span repositories, so equal numbers stay apart. */
	repository?: string;
	prUrl: string;
	/** The PR's page on the provider. */
	webUrl: string;
	/** The provider PR, when its summary has loaded. */
	provider?: SessionPRSummary;
	aoState?: PRReviewState;
	comments: ReviewComment[];
	reviewers: ReviewReviewer[];
	openCount: number;
	resolvedCount: number;
};

export type ReviewOpenPR = { url: string; number: number; title?: string; repo?: string };

/** A comment that still asks something of the worker: not resolved and not replaced. */
export function reviewCommentIsOpen(comment: ReviewComment): boolean {
	return !comment.resolved && !comment.earlier;
}

const time = (value?: string | null) => {
	const parsed = value ? Date.parse(value) : Number.NaN;
	return Number.isNaN(parsed) ? 0 : parsed;
};

/** A login as GitHub's two APIs write it: REST adds "[bot]" to an app's login, GraphQL does not. */
const loginKey = (login: string) => login.trim().replace(/^@+/, "").toLowerCase().replace(/\[bot\]$/i, "");

/** GitHub anchors a review at #pullrequestreview-<id> on the PR page. */
export function providerReviewId(reviewUrl: string | undefined): string | undefined {
	return reviewUrl?.match(/#pullrequestreview-(\d+)/)?.[1];
}

function runVerdict(verdict: string | undefined): ReviewVerdict {
	return verdict === "approved" || verdict === "changes_requested" ? verdict : "commented";
}

function providerVerdict(verdict: string | undefined): ReviewVerdict {
	return verdict === "approved" || verdict === "changes_requested" ? verdict : "commented";
}

function samePR(a: { url: string; number: number }, b: { url?: string; htmlUrl?: string; number: number; repo?: string }): boolean {
	if (a.url && (a.url === b.url || a.url === b.htmlUrl)) return true;
	if (a.number !== b.number) return false;
	const left = prRepositoryFromUrl(a.url)?.toLowerCase();
	const right = (b.repo || prRepositoryFromUrl(b.htmlUrl || b.url))?.toLowerCase();
	// A URL that names no repository (a forge the parser does not know) pairs by number.
	return !left || !right || left === right;
}

/** Completed Orchestrator passes with something to read, newest first. Falls back to the state's own runs against a daemon predating the runs list. */
function aoRunsFor(state: PRReviewState | undefined, prUrl: string, runs: ReviewRunFacts[]): ReviewRunFacts[] {
	const readable = (run: ReviewRunFacts | undefined): run is ReviewRunFacts =>
		Boolean(run) && run!.status === "completed" && Boolean(run!.body?.trim());
	const fromRuns = runs.filter((run) => run.prUrl === (state?.prUrl ?? prUrl) && readable(run));
	const list = runs.length > 0 ? fromRuns : [state?.latestRun, state?.previousRun].filter(readable);
	const byId = new Map(list.map((run) => [run.id, run]));
	return [...byId.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

export function buildReviewBlocks({
	openPRs,
	providerPRs,
	reviewStates,
	runs,
}: {
	/** The session's open and draft PRs, in display order. */
	openPRs: ReviewOpenPR[];
	/** The provider's PR summaries (threads, reviews, comments). Empty for Cloud. */
	providerPRs: SessionPRSummary[];
	reviewStates: PRReviewState[];
	runs: ReviewRunFacts[];
}): ReviewPRBlock[] {
	const repositories = new Set(
		openPRs.map((pr) => (pr.repo || prRepositoryFromUrl(pr.url) || "").toLowerCase()).filter(Boolean),
	);
	const severalRepositories = repositories.size > 1;
	return openPRs.map((pr) => {
		const provider = providerPRs.find((summary) => samePR(pr, summary));
		const aoState = reviewStates.find((state) => samePR({ url: state.prUrl, number: state.prNumber }, { ...pr }));
		const aoRuns = aoRunsFor(aoState, pr.url, runs);
		const comments = buildComments(provider, aoRuns);
		const reviewers = buildReviewers(provider, aoState, aoRuns);
		const repository = severalRepositories ? pr.repo || prRepositoryFromUrl(pr.url) : undefined;
		const openCount = comments.filter(reviewCommentIsOpen).length;
		return {
			key: `${(pr.repo || prRepositoryFromUrl(pr.url) || "").toLowerCase()}#${pr.number}`,
			number: pr.number,
			title: (aoState?.title || provider?.title || pr.title || "").trim() || `#${pr.number}`,
			repository,
			prUrl: pr.url,
			webUrl: provider?.htmlUrl || pr.url,
			provider,
			aoState,
			comments,
			reviewers,
			openCount,
			resolvedCount: comments.length - openCount,
		};
	});
}

type Draft = ReviewComment & { groupAt: number; group: string };

function buildComments(pr: SessionPRSummary | undefined, aoRuns: ReviewRunFacts[]): ReviewComment[] {
	const drafts: Draft[] = [];
	const author = pr?.author ? loginKey(pr.author) : "";
	const byAuthor = (login: string) => Boolean(author) && loginKey(login) === author;
	const aoRunByReviewId = new Map(aoRuns.filter((run) => run.githubReviewId).map((run) => [run.githubReviewId, run]));

	// The Orchestrator's passes: the newest reads as current, earlier ones are history.
	aoRuns.forEach((run, index) => {
		drafts.push({
			key: `run:${run.id}`,
			kind: "summary",
			source: "orchestrator",
			author: run.harness || "reviewer",
			harness: run.harness || undefined,
			isBot: false,
			body: run.body.trim(),
			at: run.createdAt,
			url: run.githubReviewId && run.prUrl ? `${run.prUrl}#pullrequestreview-${run.githubReviewId}` : undefined,
			verdict: runVerdict(run.verdict),
			resolved: false,
			earlier: index > 0,
			replies: [],
			delivered: run.autoInjectReview,
			runId: run.id,
			requestedByAgent: run.triggerSource === "agent",
			groupAt: time(run.createdAt),
			group: run.githubReviewId ? `review:${run.githubReviewId}` : `run:${run.id}`,
		});
	});
	if (!pr) return sortComments(drafts);

	// Provider reviews with something to read, less the ones the Orchestrator posted
	// (its run reads as the summary) and the PR author's own.
	const reviewAt = new Map<string, number>();
	const reviews = (pr.review?.reviews ?? [])
		.filter((review) => !byAuthor(review.reviewerId) && !aoRunByReviewId.has(providerReviewId(review.reviewUrl) ?? ""))
		.sort((a, b) => b.submittedAt.localeCompare(a.submittedAt));
	for (const review of reviews) {
		const id = providerReviewId(review.reviewUrl);
		if (id) reviewAt.set(id, time(review.submittedAt));
	}
	const newestByReviewer = new Set<string>();
	for (const review of reviews) {
		const body = (review.body ?? "").trim();
		if (!body) continue;
		const reviewer = loginKey(review.reviewerId);
		const earlier = newestByReviewer.has(reviewer);
		newestByReviewer.add(reviewer);
		const id = providerReviewId(review.reviewUrl);
		drafts.push({
			key: `review:${review.reviewUrl ?? `${review.reviewerId}:${review.submittedAt}`}`,
			kind: "summary",
			source: "provider",
			author: review.reviewerId,
			isBot: Boolean(review.isBot),
			body,
			at: review.submittedAt,
			url: review.reviewUrl,
			verdict: providerVerdict(review.verdict),
			resolved: false,
			earlier,
			replies: [],
			delivered: review.autoInjectReview,
			rerequestReviewerId: review.verdict !== "approved" && pr.url ? review.reviewerId : undefined,
			groupAt: time(review.submittedAt),
			group: id ? `review:${id}` : `reviewer:${reviewer}`,
		});
	}

	// The provider's comment links name the review each inline comment was posted in.
	const linkByUrl = new Map<string, { reviewId?: string; autoInjectReview: boolean }>();
	for (const reviewer of [...(pr.review?.unresolvedBy ?? []), ...(pr.review?.resolvedBy ?? [])]) {
		for (const link of reviewer.links) {
			if (link.url) linkByUrl.set(link.url, { reviewId: link.reviewId, autoInjectReview: link.autoInjectReview });
		}
	}
	const inlineDraft = (input: {
		key: string;
		first: { author: string; isBot: boolean; body: string; createdAt?: string | null; url?: string };
		file?: string;
		line?: number;
		resolved: boolean;
		threadId?: string;
		replies: ReviewReply[];
		reviewId?: string;
		autoInjectReview?: boolean;
	}): Draft | null => {
		const run = input.reviewId ? aoRunByReviewId.get(input.reviewId) : undefined;
		// The PR author's own threads are notes, not feedback for the worker; the
		// Orchestrator posts as the same account, so its comments are kept by review id.
		if (!run && byAuthor(input.first.author)) return null;
		const at = input.first.createdAt ?? undefined;
		const groupAt = run ? time(run.createdAt) : input.reviewId && reviewAt.has(input.reviewId) ? reviewAt.get(input.reviewId)! : time(at);
		return {
			key: input.key,
			kind: "inline",
			source: run ? "orchestrator" : "provider",
			author: run ? run.harness || "reviewer" : input.first.author,
			harness: run?.harness || undefined,
			isBot: run ? false : input.first.isBot,
			body: input.first.body.trim(),
			at: at ?? run?.createdAt,
			file: input.file || undefined,
			line: input.line || undefined,
			url: input.first.url,
			resolved: input.resolved,
			earlier: false,
			threadId: input.threadId,
			replies: input.replies,
			delivered: Boolean(input.autoInjectReview),
			runId: run?.id,
			groupAt,
			group: input.reviewId ? `review:${input.reviewId}` : `reviewer:${loginKey(input.first.author)}`,
		};
	};
	if (pr.threads) {
		for (const thread of pr.threads) {
			const [first, ...rest] = thread.comments;
			if (!first) continue;
			const link = first.url ? linkByUrl.get(first.url) : undefined;
			const draft = inlineDraft({
				key: `thread:${thread.id}`,
				first: { ...first, createdAt: first.createdAt ?? undefined },
				file: thread.path,
				line: thread.line,
				resolved: thread.resolved,
				threadId: thread.id,
				replies: rest.map((reply) => ({ id: reply.id, author: reply.author, isBot: reply.isBot, body: reply.body, createdAt: reply.createdAt ?? undefined })),
				reviewId: link?.reviewId,
				autoInjectReview: link?.autoInjectReview,
			});
			if (draft) drafts.push(draft);
		}
	} else {
		// Before the PR's details are read, the summary's comment links are all there is.
		const fromLinks = (reviewers: NonNullable<SessionPRSummary["review"]["resolvedBy"]>, resolved: boolean) => {
			for (const reviewer of reviewers) {
				reviewer.links.forEach((link, index) => {
					const draft = inlineDraft({
						key: `link:${link.url ?? `${reviewer.reviewerId}:${link.file ?? ""}:${link.line ?? ""}:${index}`}`,
						first: { author: reviewer.reviewerId, isBot: Boolean(reviewer.isBot), body: link.body ?? "", url: link.url || reviewer.reviewUrl },
						file: link.file,
						line: link.line,
						resolved,
						replies: [],
						reviewId: link.reviewId,
						autoInjectReview: link.autoInjectReview,
					});
					if (draft) drafts.push(draft);
				});
			}
		};
		fromLinks(pr.review?.unresolvedBy ?? [], false);
		fromLinks(pr.review?.resolvedBy ?? [], true);
	}

	for (const comment of prConversationComments(pr)) {
		drafts.push({
			key: `comment:${comment.id}`,
			kind: "comment",
			source: "provider",
			author: comment.author,
			isBot: comment.isBot,
			body: comment.body.trim(),
			at: comment.createdAt ?? undefined,
			url: comment.url,
			resolved: false,
			earlier: false,
			replies: [],
			delivered: false,
			groupAt: time(comment.createdAt),
			group: `comment:${comment.id}`,
		});
	}
	return sortComments(drafts);
}

const kindOrder: Record<ReviewComment["kind"], number> = { summary: 0, inline: 1, comment: 2 };

/**
 * Newest review first; inside a review its summary, then its inline comments
 * down the files. A comment with no review of its own sorts by when it was written.
 */
function sortComments(drafts: Draft[]): ReviewComment[] {
	return drafts
		.sort((a, b) =>
			b.groupAt - a.groupAt
			|| a.group.localeCompare(b.group)
			|| kindOrder[a.kind] - kindOrder[b.kind]
			|| (a.file ?? "").localeCompare(b.file ?? "")
			|| (a.line ?? 0) - (b.line ?? 0)
			|| time(a.at) - time(b.at))
		.map(withoutSortKeys);
}

function withoutSortKeys(draft: Draft): ReviewComment {
	const comment: Partial<Draft> = { ...draft };
	delete comment.groupAt;
	delete comment.group;
	return comment as ReviewComment;
}

function buildReviewers(pr: SessionPRSummary | undefined, aoState: PRReviewState | undefined, aoRuns: ReviewRunFacts[]): ReviewReviewer[] {
	const reviewers: ReviewReviewer[] = [];
	const latestRun = aoRuns[0];
	if (latestRun || aoState?.status === "up_to_date" || aoState?.status === "changes_requested") {
		reviewers.push({
			id: "orchestrator",
			orchestrator: true,
			harness: latestRun?.harness || aoState?.latestRun?.harness || undefined,
			isBot: false,
			status: latestRun
				? runVerdict(latestRun.verdict)
				: aoState?.status === "up_to_date" ? "approved" : aoState?.status === "changes_requested" ? "changes_requested" : "commented",
			canRerequest: false,
		});
	}
	if (!pr) return reviewers;
	const author = pr.author ? loginKey(pr.author) : "";
	const aoReviewIds = new Set(aoRuns.map((run) => run.githubReviewId).filter(Boolean));
	const seen = new Set<string>();
	const latestFirst = [...(pr.review?.reviews ?? [])].sort((a, b) => b.submittedAt.localeCompare(a.submittedAt));
	for (const review of latestFirst) {
		const login = loginKey(review.reviewerId);
		if (seen.has(login) || login === author || aoReviewIds.has(providerReviewId(review.reviewUrl) ?? "")) continue;
		seen.add(login);
		const status = providerVerdict(review.verdict);
		reviewers.push({
			id: review.reviewerId,
			orchestrator: false,
			isBot: Boolean(review.isBot),
			status,
			canRerequest: Boolean(pr.url) && status !== "approved",
		});
	}
	for (const requested of pr.requestedReviewers ?? []) {
		const login = loginKey(requested.login);
		if (seen.has(login)) {
			// Asked again after reviewing: the provider now waits on them.
			const existing = reviewers.find((reviewer) => !reviewer.orchestrator && loginKey(reviewer.id) === login);
			if (existing) {
				existing.status = "pending";
				existing.canRerequest = false;
			}
			continue;
		}
		seen.add(login);
		reviewers.push({ id: requested.login, orchestrator: false, isBot: false, isTeam: requested.isTeam, status: "pending", canRerequest: false });
	}
	return reviewers;
}
