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
	/**
	 * What "sent to the worker" is remembered by. An inline comment read from the
	 * summary's links before its thread loads becomes a thread row with a new key,
	 * so it is keyed by its comment URL, which both rows share.
	 */
	sentKey: string;
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

/**
 * A PR's forge host and full repository path from its page or API URL. GitLab
 * nests projects in groups ("group/sub/app"), so the path is everything before
 * the PR part; GitHub's API URLs name the same repository as its pages.
 */
function prLocation(raw: string | undefined): { host: string; path: string } | undefined {
	if (!raw) return undefined;
	try {
		const url = new URL(raw);
		const api = url.hostname === "api.github.com";
		const path = (api ? url.pathname.replace(/^\/repos\//, "/") : url.pathname)
			.match(/^\/(.+?)\/(?:pulls?|-\/merge_requests)\/\d+(?:\/|$)/)?.[1]?.toLowerCase();
		return path ? { host: api ? "github.com" : url.hostname.toLowerCase(), path } : undefined;
	} catch {
		return undefined;
	}
}

const prRepositoryPath = (raw: string | undefined) => prLocation(raw)?.path;

type PRRef = { urls: Array<string | undefined>; number: number; repo?: string };

/**
 * Who a PR is: its forge host, repository path and number, from its URLs. A
 * GitHub PR and a GitLab MR can share a slug and number, so the host counts. A
 * stated repository, which names no host, stands in only when no URL parses.
 */
function prIdentities(ref: PRRef): Set<string> {
	const located = ref.urls.map(prLocation).filter((location): location is { host: string; path: string } => Boolean(location));
	if (located.length > 0) return new Set(located.map(({ host, path }) => `${host}/${path}#${ref.number}`));
	return ref.repo ? new Set([`${ref.repo.toLowerCase()}#${ref.number}`]) : new Set();
}

/**
 * The item that is the same PR as the target: an exact URL first, then the same
 * repository and number. A number alone pairs only when one item has it and one
 * side names no repository (a forge the parser does not know).
 */
function findPR<T>(target: PRRef, items: T[], ref: (item: T) => PRRef): T | undefined {
	const urls = new Set(target.urls.filter(Boolean));
	const exact = items.find((item) => ref(item).urls.some((url) => url && urls.has(url)));
	if (exact) return exact;
	const ids = prIdentities(target);
	const sameNumber = items.filter((item) => ref(item).number === target.number);
	const sameRepo = sameNumber.find((item) => [...prIdentities(ref(item))].some((id) => ids.has(id)));
	if (sameRepo) return sameRepo;
	const unknown = sameNumber.filter((item) => ids.size === 0 || prIdentities(ref(item)).size === 0);
	return unknown.length === 1 ? unknown[0] : undefined;
}

/** Completed Orchestrator passes with something to read, newest first. Falls back to the state's own runs against a daemon predating the runs list. */
function aoRunsFor(state: PRReviewState | undefined, prUrl: string, runs: ReviewRunFacts[]): ReviewRunFacts[] {
	const readable = (run: ReviewRunFacts | undefined): run is ReviewRunFacts =>
		Boolean(run) && run!.status === "completed" && Boolean(run!.body?.trim());
	const fromRuns = runs.filter((run) => run.prUrl === (state?.prUrl ?? prUrl) && readable(run));
	// The state's own runs count too: a runs list need not hold every run a state embeds.
	const list = [...[state?.latestRun, state?.previousRun].filter(readable), ...fromRuns];
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
	const summaryRef = (summary: SessionPRSummary): PRRef => ({ urls: [summary.url, summary.htmlUrl], number: summary.number, repo: summary.repo });
	const paired = openPRs.map((pr) => {
		const target: PRRef = { urls: [pr.url], number: pr.number, repo: pr.repo };
		return { pr, target, provider: findPR(target, providerPRs, summaryRef) };
	});
	// The SCM summary can report an open PR before the session's list does; it gets a card too.
	const claimed = new Set(paired.map(({ provider }) => provider));
	const unclaimed = providerPRs
		.filter((summary) => !claimed.has(summary) && (summary.state === "open" || summary.state === "draft"))
		.map((summary) => {
			const pr: ReviewOpenPR = { url: summary.htmlUrl || summary.url, number: summary.number, title: summary.title, repo: summary.repo };
			return { pr, target: { urls: [pr.url, summary.url], number: pr.number, repo: pr.repo } as PRRef, provider: summary as SessionPRSummary | undefined };
		});
	const all = [...paired, ...unclaimed];
	const repositoryOf = (pr: ReviewOpenPR) => pr.repo || prRepositoryFromUrl(pr.url) || prRepositoryPath(pr.url);
	const repositories = new Set(all.map(({ pr }) => (repositoryOf(pr) || "").toLowerCase()).filter(Boolean));
	const severalRepositories = repositories.size > 1;
	return all.map(({ pr, target, provider }) => {
		const aoState = findPR(target, reviewStates, (state) => ({ urls: [state.prUrl], number: state.prNumber }));
		const aoRuns = aoRunsFor(aoState, pr.url, runs);
		const comments = buildComments(provider, aoRuns);
		const reviewers = buildReviewers(provider, aoState, aoRuns);
		const repository = severalRepositories ? repositoryOf(pr) : undefined;
		const openCount = comments.filter(reviewCommentIsOpen).length;
		return {
			// Two projects can share a PR number, so the key names the repository, or the URL when none parses.
			key: [...prIdentities(target)][0] ?? `${pr.url}#${pr.number}`,
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
			sentKey: `run:${run.id}`,
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
		// A bodyless approval or change request still replaces the reviewer's older
		// review; a bodyless comment (inline comments only) does not.
		if (!body && providerVerdict(review.verdict) === "commented") continue;
		const reviewer = loginKey(review.reviewerId);
		const earlier = newestByReviewer.has(reviewer);
		newestByReviewer.add(reviewer);
		if (!body) continue;
		const id = providerReviewId(review.reviewUrl);
		const reviewKey = `review:${review.reviewUrl ?? `${review.reviewerId}:${review.submittedAt}`}`;
		drafts.push({
			key: reviewKey,
			sentKey: reviewKey,
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
		/** The inline comment's own URL, when known (not its review's). */
		commentUrl?: string;
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
			sentKey: input.commentUrl ? `url:${input.commentUrl}` : input.key,
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
				commentUrl: first.url || undefined,
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
						commentUrl: link.url || undefined,
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
			sentKey: `comment:${comment.id}`,
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
