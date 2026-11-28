import { buildReviewBlocks } from "../../shared/review/review-comments";
import type { DashboardPR, DashboardSession, PRReviewState, ReviewRun, SessionPRSummary, SessionReviews } from "./api";

export function reviewRouteForSession(session: DashboardSession, hostId?: string) {
	const pr = session.prs?.[0] ?? session.pr;
	return pr ? reviewRouteForPR(session.id, pr, hostId) : undefined;
}

export function reviewRouteForPR(sessionId: string, pr: Pick<DashboardPR, "number" | "url">, hostId?: string) {
	return {
		pathname: "/review/[sessionId]" as const,
		params: { sessionId, prNumber: String(pr.number), prUrl: pr.url, ...(hostId ? { hostId } : {}) },
	};
}

/** The chat of the worker that owns a pull request, which its PR page and PR row both open. */
export function workerRouteForSession(session: Pick<DashboardSession, "id" | "projectId">, hostId?: string) {
	return {
		pathname: "/session/[id]" as const,
		params: { id: session.id, projectId: session.projectId, ...(hostId ? { hostId } : {}) },
	};
}

/**
 * Whether the screen under the PR page is already this worker's chat. The chat
 * opens its PR page, so opening the worker from there should go back rather than
 * stack a second copy of the same chat.
 */
export function previousRouteIsWorker(
	routes: readonly { name: string; params?: object }[],
	index: number,
	sessionId: string,
): boolean {
	const previous = index > 0 ? routes[index - 1] : undefined;
	return previous?.name === "session/[id]" && (previous.params as { id?: unknown } | undefined)?.id === sessionId;
}

/** The chat shortcut is for PRs that can still be reviewed. */
export function sessionPRReadyForReview(session: DashboardSession) {
	const prs = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	return prs.find((pr) => pr.state !== "closed" && pr.state !== "merged");
}

export function reviewForPullRequest(
	reviews: PRReviewState[],
	prUrl: string | undefined,
	prNumber: number | undefined,
): PRReviewState | undefined {
	if (prUrl) {
		const exact = reviews.find((review) => review.prUrl === prUrl);
		if (exact) return exact;
	}
	return prNumber ? reviews.find((review) => review.prNumber === prNumber) : undefined;
}

export function pullRequestSummaryForURL(
	prs: SessionPRSummary[],
	prUrl: string,
): SessionPRSummary | undefined {
	const exact = prs.find((pr) => pr.url === prUrl || pr.htmlUrl === prUrl);
	if (exact) return exact;
	const requested = pullRequestIdentity(prUrl);
	return requested
		? prs.find((pr) => pr.number === requested.number && pr.repo.toLowerCase() === requested.repo)
		: undefined;
}

function pullRequestIdentity(value: string): { repo: string; number: number } | undefined {
	try {
		const parts = new URL(value).pathname.split("/").filter(Boolean);
		const marker = parts.findIndex((part) => part === "pull" || part === "merge_requests");
		if (marker < 2 || marker + 1 >= parts.length) return undefined;
		const number = Number(parts[marker + 1]);
		if (!Number.isInteger(number) || number <= 0) return undefined;
		const repoParts = parts.slice(0, marker);
		if (repoParts.at(-1) === "-") repoParts.pop();
		return { repo: repoParts.join("/").toLowerCase(), number };
	} catch {
		return undefined;
	}
}

export function reviewStatusVisual(status: PRReviewState["status"]): {
	icon: "alert-circle" | "check-circle" | "clock" | "loader";
	tone: "amber" | "blue" | "green" | "muted";
} {
	switch (status) {
		case "running": return { icon: "loader", tone: "blue" };
		case "up_to_date": return { icon: "check-circle", tone: "green" };
		case "changes_requested": return { icon: "alert-circle", tone: "amber" };
		case "ineligible": return { icon: "alert-circle", tone: "muted" };
		default: return { icon: "clock", tone: "muted" };
	}
}

/** The newest actionable automatic-review failure across every PR in a session. */
export function latestAutoReviewFailure(reviews: PRReviewState[], autoReviewEnabled: boolean): ReviewRun | undefined {
	if (!autoReviewEnabled) return undefined;
	return reviews
		.map((review) => review.latestRun)
		.filter((run): run is ReviewRun => run?.triggerSource === "auto" && run.status === "failed" && Boolean(run.body.trim()))
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
}

export function reviewerDestination(data: SessionReviews, review: PRReviewState, sessionId: string, hostId?: string) {
	const surface = data.reviewerSurface;
	if (!surface) return undefined;
	const hostParam = hostId ? { hostId } : {};
	if (surface.mode === "chat") {
		return { pathname: "/reviewer/[reviewId]" as const, params: { reviewId: surface.reviewId, sessionId, title: review.title, ...hostParam } };
	}
	const handleId = surface.handleId || data.reviewerHandleId;
	if (!handleId) return undefined;
	return { pathname: "/shell/[handleId]" as const, params: { handleId, sessionId, title: `Review · PR #${review.prNumber}`, kind: "reviewer", ...hostParam } };
}

export type ReviewerControls = {
	/** Open the live reviewer; set only when there is a surface to open. */
	open?: "chat" | "terminal";
	/** Relaunch a reviewer that exited or has nothing to open. */
	restore: boolean;
	/** Stop the live reviewer; never offered next to Restore. */
	stop: boolean;
};

/**
 * Which reviewer controls to show. "Open" needs a surface it can actually
 * navigate to: a stopped terminal reviewer keeps its surface but loses its
 * handle, and an Open button then did nothing. Anything else with review
 * history gets Restore instead, and Stop only applies to a live reviewer.
 */
export function reviewerControls(data: SessionReviews, review: PRReviewState, sessionId: string): ReviewerControls {
	const exited = data.reviewerActivityState === "exited" || Boolean(data.reviewerSurface?.controllerError);
	const destination = exited ? undefined : reviewerDestination(data, review, sessionId);
	if (destination) {
		return { open: data.reviewerSurface?.mode === "chat" ? "chat" : "terminal", restore: false, stop: Boolean(data.reviewerHandleId) };
	}
	const hasReviewer = Boolean(data.reviewerSurface || data.reviewerHandleId || data.runs?.length);
	return { restore: hasReviewer, stop: false };
}

/** A login as GitHub's two APIs write it: REST adds "[bot]" to an app's login, GraphQL does not. */
const loginKey = (login: string) => login.trim().replace(/^@+/, "").toLowerCase().replace(/\[bot\]$/i, "");

/**
 * The provider reviewers a PR's reviewer can be asked to review again, with
 * how many inline comments each left. Read from the shared comment model, so
 * the phone offers whom the desktop's Reviewers menu does: not a bot (GitHub
 * refuses review requests for apps), not one who approved or is already asked,
 * not the PR's author, and not the Orchestrator's own reviews. `asked` holds the
 * reviewers asked from here, left out until the provider's next read says so.
 */
export function rerequestableReviewers(
	pr: SessionPRSummary | undefined,
	reviews: Pick<SessionReviews, "reviews" | "runs"> | undefined,
	asked: ReadonlySet<string> = new Set(),
): { reviewerId: string; count: number }[] {
	if (!pr) return [];
	const [block] = buildReviewBlocks({
		openPRs: [{ url: pr.url, number: pr.number, title: pr.title, repo: pr.repo }],
		providerPRs: [pr],
		reviewStates: reviews?.reviews ?? [],
		runs: reviews?.runs ?? [],
	});
	if (!block) return [];
	return block.reviewers
		.filter((reviewer) => !reviewer.orchestrator && reviewer.canRerequest && !asked.has(loginKey(reviewer.id)))
		.map((reviewer) => ({
			reviewerId: reviewer.id,
			// A person's own comments: an app sharing their base login ("dana[bot]") is not them.
			count: block.comments.filter((comment) => comment.source === "provider" && comment.kind === "inline" && !comment.isBot && loginKey(comment.author) === loginKey(reviewer.id)).length,
		}));
}

/** The key `rerequestableReviewers` matches an asked reviewer by. */
export function rerequestKey(reviewerId: string): string {
	return loginKey(reviewerId);
}
