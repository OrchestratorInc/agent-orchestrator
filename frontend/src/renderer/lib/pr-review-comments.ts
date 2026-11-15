import type { SessionPRSummary } from "../hooks/useSessionScmSummary";

type CommentReviewer = SessionPRSummary["review"]["unresolvedBy"][number];

/** A login as GitHub's two APIs write it: REST adds "[bot]" to an app's login, GraphQL does not. */
const loginKey = (login: string) => authorKey(login).replace(/\[bot\]$/i, "");

/**
 * A login for telling the PR's author apart: "[bot]" stays, so a
 * \`release-helper[bot]\` app is not taken for a \`release-helper\` person.
 */
const authorKey = (login: string) => login.trim().replace(/^@+/, "").toLowerCase();

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
		[...(pr.review?.unresolvedBy ?? []), ...(pr.review?.resolvedBy ?? [])]
			.flatMap((reviewer) => reviewer.links.map((link) => link.url))
			.filter((url): url is string => Boolean(url)),
	);
	// A bot's comments sit under its review, so they take that review's spelling of its name.
	const reviewByLogin = new Map((pr.review?.reviews ?? []).map((review) => [loginKey(review.reviewerId), review]));
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
 * notes are not feedback to send back to it. A bot's comment is kept only
 * when that bot also reviewed the PR (a review or an inline thread), so a
 * review bot's walkthrough lists and a deploy preview, coverage report or
 * CLA check does not.
 */
export function prConversationComments(pr: SessionPRSummary): NonNullable<SessionPRSummary["conversation"]> {
	const author = pr.author ? authorKey(pr.author) : "";
	const reviewers = new Set([
		...(pr.review?.reviews ?? []).map((review) => loginKey(review.reviewerId)),
		...(pr.threads ?? []).flatMap((thread) => thread.comments.map((comment) => loginKey(comment.author))),
	]);
	return (pr.conversation ?? []).filter((comment) => comment.body.trim()
		&& !(author && authorKey(comment.author) === author)
		&& (!comment.isBot || reviewers.has(loginKey(comment.author))));
}

/**
 * One bucket per reviewer: the summary's list and the threads' list can each
 * hold the same bot (the summary listing one of its comments, the threads the
 * rest), and the Reviews tab keys reviewers by name, so a second bucket would
 * hide the first's comments. The first list's spelling of the name is kept.
 */
export function mergeReviewers(...lists: CommentReviewer[][]): CommentReviewer[] {
	const byLogin = new Map<string, CommentReviewer>();
	for (const reviewer of lists.flat()) {
		const key = loginKey(reviewer.reviewerId);
		const existing = byLogin.get(key);
		if (!existing) {
			byLogin.set(key, { ...reviewer, links: [...reviewer.links] });
			continue;
		}
		existing.links.push(...reviewer.links);
		existing.count += reviewer.count;
		existing.reviewUrl ??= reviewer.reviewUrl;
	}
	return [...byLogin.values()];
}

/**
 * Whether a PR has anything for the Reviews tab to show: a review, an inline
 * comment or a top-level comment from someone other than the PR's author,
 * which is what the tab lists (the author's own notes and replies are left out).
 */
export function prHasReviewActivity(pr: SessionPRSummary): boolean {
	const author = pr.author ? authorKey(pr.author) : "";
	const external = (login: string) => !author || authorKey(login) !== author;
	const bots = botThreadReviewers(pr);
	return (pr.review?.reviews ?? []).some((review) => external(review.reviewerId))
		|| [...(pr.review?.unresolvedBy ?? []), ...(pr.review?.resolvedBy ?? []), ...bots.unresolved, ...bots.resolved]
			.some((reviewer) => reviewer.count > 0 && external(reviewer.reviewerId))
		|| prConversationComments(pr).length > 0;
}
