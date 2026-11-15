import type { SessionPRSummary } from "../hooks/useSessionScmSummary";

type CommentReviewer = SessionPRSummary["review"]["unresolvedBy"][number];

/** A login as GitHub's two APIs write it: REST adds "[bot]" to an app's login, GraphQL does not. */
const loginKey = (login: string) => login.trim().replace(/^@+/, "").replace(/\[bot\]$/i, "").toLowerCase();

/**
 * A bot's inline review comments, grouped by bot as the Reviews tab groups a
 * person's. The PR summary's unresolved/resolved lists leave bots out (they
 * drive the board's "needs attention" counts, which bots should not raise), so
 * without this a CodeRabbit review shows its summary and none of the comments
 * it is about. They come from the PR's stored review threads instead; one the
 * summary already lists (a host that includes bots) is not repeated.
 */
export function botThreadReviewers(pr: SessionPRSummary): { unresolved: CommentReviewer[]; resolved: CommentReviewer[] } {
	const listed = new Set(
		[...(pr.review.unresolvedBy ?? []), ...(pr.review.resolvedBy ?? [])]
			.flatMap((reviewer) => reviewer.links.map((link) => link.url))
			.filter((url): url is string => Boolean(url)),
	);
	// A bot's comments sit under its review, so they take that review's spelling of its name.
	const reviewByLogin = new Map((pr.review.reviews ?? []).map((review) => [loginKey(review.reviewerId), review]));
	const group = (resolved: boolean) => {
		const byReviewer = new Map<string, CommentReviewer>();
		for (const thread of pr.threads ?? []) {
			const first = thread.comments[0];
			if (!first?.isBot || thread.resolved !== resolved) continue;
			if (first.url && listed.has(first.url)) continue;
			const review = reviewByLogin.get(loginKey(first.author));
			const reviewerId = review?.reviewerId ?? first.author;
			const reviewer = byReviewer.get(reviewerId) ?? { count: 0, isBot: true, links: [], reviewerId, reviewUrl: review?.reviewUrl };
			reviewer.links.push({
				// Not known to have reached the worker, so it can be sent from here.
				autoInjectReview: false,
				body: first.body,
				file: thread.path,
				line: thread.line,
				url: first.url,
			});
			reviewer.count += 1;
			byReviewer.set(reviewerId, reviewer);
		}
		return [...byReviewer.values()];
	};
	return { unresolved: group(false), resolved: group(true) };
}

/**
 * The PR's top-level comments (a bot's walkthrough, a person's note), oldest
 * first, for the Reviews tab to list beside its reviews. The PR author's own
 * comments are left out, as their reviews are: a worker's \`gh pr comment\`
 * notes are not feedback to send back to it.
 */
export function prConversationComments(pr: SessionPRSummary): NonNullable<SessionPRSummary["conversation"]> {
	const author = pr.author ? loginKey(pr.author) : "";
	return (pr.conversation ?? []).filter((comment) => comment.body.trim() && !(author && loginKey(comment.author) === author));
}

/** Whether a PR has anything for the Reviews tab: a review, an inline comment, or a top-level comment. */
export function prHasReviewActivity(pr: SessionPRSummary): boolean {
	return (pr.review?.reviews?.length ?? 0) > 0
		|| (pr.review?.unresolvedBy ?? []).some((reviewer) => reviewer.count > 0)
		|| (pr.review?.resolvedBy ?? []).some((reviewer) => reviewer.count > 0)
		|| (pr.threads ?? []).some((thread) => thread.comments.length > 0)
		|| prConversationComments(pr).length > 0;
}
