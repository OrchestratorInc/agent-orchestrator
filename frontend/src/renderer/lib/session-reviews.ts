import { apiErrorException } from "./api-error-exception";
import { queryOptions } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { appI18n } from "../i18n";
import { sortedPRs, type WorkspaceSession } from "../types/workspace";
import { clientForSessionHost } from "./host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "./preview-mode";

export type PRReviewState = components["schemas"]["PRReviewState"];
export type ReviewsResponse = components["schemas"]["ListReviewsResponse"];
export type ReviewRunFacts = components["schemas"]["ReviewRun"];
export type ReviewerActivityState = ReviewsResponse["reviewerActivityState"];

/**
 * Shared query options for a session's AO review states. The query key is the
 * single source of truth for review data — the SessionInspector Reviews tab
 * and the command palette both subscribe through this, so React Query shares
 * one cache entry per session and one fetch path (including the preview mock).
 */
export const sessionReviewsQueryKey = (sessionId: string, hostId?: string) =>
	hostId ? ["session-reviews", hostId, sessionId] as const : ["session-reviews", sessionId] as const;

export function sessionReviewsQueryOptions(session: WorkspaceSession, enabled: boolean, staleTime?: number, hostId = session.hostId) {
	return queryOptions({
		queryKey: sessionReviewsQueryKey(session.id, hostId),
		enabled,
		...(staleTime !== undefined ? { staleTime } : {}),
		refetchInterval: (query) => {
			const data = query.state.data as ReviewsResponse | undefined;
			// Every active run has a daemon owner and a time limit, so polling
			// while one exists always ends.
			return reviewsHaveActiveRun(data) ? 2500 : false;
		},
		queryFn: async () => {
			if (usePreviewData) return mockReviewsResponse(session);
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw apiErrorException(error, "Unable to load reviews");
			return data ?? ({ reviewerHandleId: "", reviews: [], runs: [], activeReviewers: [] } satisfies ReviewsResponse);
		},
	});
}

/** Review states for a session's active PRs. Open and draft PRs can be reviewed; merged/closed PRs cannot. */
export function openReviewStatesFor(session: WorkspaceSession, reviewStates: PRReviewState[]): PRReviewState[] {
	const openPRURLs = new Set(
		sortedPRs(session)
			.filter((pr) => pr.state === "open" || pr.state === "draft")
			.map((pr) => pr.url),
	);
	return reviewStates.filter((reviewState) => openPRURLs.has(reviewState.prUrl));
}

export type ReviewRunStatus = ReviewRunFacts["status"];

/** Run states the daemon still owns: each one has a time limit and always ends. */
export function reviewRunIsActive(status: ReviewRunStatus | undefined): boolean {
	return status === "queued" || status === "launching" || status === "running" || status === "cancelling";
}

function reviewsHaveActiveRun(data: ReviewsResponse | undefined): boolean {
	if (!data) return false;
	return (
		(data.reviews ?? []).some((review) => review.status === "running" || reviewRunIsActive(review.latestRun?.status)) ||
		(data.runs ?? []).some((run) => reviewRunIsActive(run.status))
	);
}

export function reviewIsRunning(openReviewStates: PRReviewState[]): boolean {
	return openReviewStates.some((reviewState) => reviewState.status === "running");
}

/** What the review is doing right now, read from the run state alone. */
export type ReviewPhase = "starting" | "reviewing" | "waiting_input" | "stopping";

export function reviewPhase(
	openReviewStates: PRReviewState[],
	reviewerActivityState: ReviewerActivityState | undefined,
): ReviewPhase | null {
	const statuses = openReviewStates
		.filter((reviewState) => reviewState.status === "running")
		.map((reviewState) => reviewState.latestRun?.status);
	if (statuses.length === 0) return null;
	if (statuses.some((status) => status === "running" || status === undefined)) {
		// The daemon keeps a run alive while its reviewer waits for input, so say
		// so instead of implying it is working.
		return reviewerActivityState === "waiting_input" ? "waiting_input" : "reviewing";
	}
	if (statuses.some((status) => status === "queued" || status === "launching")) return "starting";
	return "stopping";
}

export function reviewRunDisabled(openReviewStates: PRReviewState[], isTriggering: boolean): boolean {
	return (
		isTriggering ||
		openReviewStates.length === 0 ||
		openReviewStates.every((reviewState) => reviewState.status === "ineligible")
	);
}

export function reviewSessionRunAction(reviewStates: PRReviewState[], isTriggering: boolean): string {
	if (isTriggering || reviewStates.some((reviewState) => reviewState.status === "running")) {
		return appI18n.t("inspector.review.reviewing");
	}
	if (reviewStates.some((reviewState) => reviewState.status === "needs_review")) {
		return appI18n.t("inspector.review.runLatest");
	}
	if (reviewStates.some((reviewState) => reviewState.status === "changes_requested" || reviewState.latestRun)) {
		return appI18n.t("inspector.review.rerun");
	}
	return appI18n.t("inspector.review.run");
}

// Preview-only pins so the reviews section can be seen mid-run and with a verdict
// left behind by an earlier commit — neither follows from a PR's review decision.
const MOCK_RUNNING_PR = 322;
const MOCK_STALE_PR = 324;

function mockReviewsResponse(session: WorkspaceSession): ReviewsResponse {
	const states: PRReviewState[] = sortedPRs(session).map((pr, index) => {
		const targetSha = `demo${pr.number}${index}`;
		const reviewedAt = new Date(Date.now() - (index + 1) * 11 * 60 * 1000).toISOString();
		const latestRun =
			pr.review === "approved" || pr.review === "changes_requested"
				? {
						autoInjectReview: session.autoInjectReview ?? true,
						batchId: `demo-batch-${session.id}`,
						body:
							pr.review === "approved"
								? "Demo review **approved** the README screenshot flow.\n\n- Layout is stable\n- Browser preview opens cleanly"
								: "Demo review found **polish feedback** for the terminal presentation.\n\n- Tighten toolbar density\n- Recheck contrast",
						createdAt: reviewedAt,
						githubReviewId: `${pr.number}01`,
						harness: "codex",
						id: `demo-review-run-${pr.number}`,
						prUrl: pr.url,
						reviewId: `demo-review-${pr.number}`,
						sessionId: session.id,
						status: "completed" as const,
						targetSha,
						triggerSource: "manual" as const,
						verdict: pr.review === "approved" ? "approved" : "changes_requested",
					}
				: undefined;
	const run = (over: Record<string, unknown>) => ({
		autoInjectReview: session.autoInjectReview ?? true,
		batchId: `demo-batch-${session.id}`,
		body: "",
		triggerSource: "manual" as const,
			createdAt: reviewedAt,
			githubReviewId: "",
			harness: "codex",
			id: `demo-review-run-${pr.number}`,
			prUrl: pr.url,
			reviewId: `demo-review-${pr.number}`,
			sessionId: session.id,
			status: "completed" as const,
			targetSha,
			verdict: "",
			...over,
		});
		// A couple of PRs are pinned to states the review decision alone cannot
		// produce, so the preview shows every shape the panel can render.
		if (pr.number === MOCK_RUNNING_PR) {
			return {
				latestRun: run({ status: "running" as const, id: `demo-review-run-${pr.number}-live` }),
				prNumber: pr.number,
				prUrl: pr.url,
				status: "running",
				targetSha,
				title: mockReviewTitle(pr.number),
			};
		}
		if (pr.number === MOCK_STALE_PR) {
			// Reviewed, then a new commit landed: the verdict is about code that
			// has since changed, so the panel demotes it to "Previous".
			return {
				previousRun: run({
					status: "completed",
					verdict: "changes_requested",
					githubReviewId: `${pr.number}09`,
					body: "Demo review asked for a tighter activity sample before the last commit.",
					targetSha: `${targetSha}-old`,
				}),
				prNumber: pr.number,
				prUrl: pr.url,
				status: "needs_review",
				targetSha,
				title: mockReviewTitle(pr.number),
			};
		}
		return {
			latestRun,
			prNumber: pr.number,
			prUrl: pr.url,
			status:
				pr.review === "approved"
					? "up_to_date"
					: pr.review === "changes_requested"
						? "changes_requested"
						: pr.state === "draft"
							? "ineligible"
							: "needs_review",
			targetSha,
			title: mockReviewTitle(pr.number),
		};
	});
	// Earlier passes, so the history control has something to open. Two reviewers
	// on the same PR is the case the control exists for.
	const runs: ReviewRunFacts[] = states.flatMap((state) => {
		const base = {
			autoInjectReview: session.autoInjectReview ?? true,
			batchId: `demo-batch-${session.id}`,
			githubReviewId: "",
			prUrl: state.prUrl,
			triggerSource: "manual" as const,
			reviewId: `demo-review-${state.prNumber}`,
			sessionId: session.id,
			status: "completed" as const,
			targetSha: state.targetSha,
		};
		return [
			...(state.latestRun?.body?.trim() ? [state.latestRun] : []),
			{
				...base,
				id: `demo-hist-${state.prNumber}-a`,
				harness: "codex",
				verdict: "changes_requested",
				body: "Earlier codex pass asked for tests around the discount edge cases.",
				createdAt: new Date(Date.now() - 55 * 60 * 1000).toISOString(),
			},
			{
				...base,
				id: `demo-hist-${state.prNumber}-b`,
				harness: "claude-code",
				verdict: "approved",
				body: "Earlier claude-code pass found nothing blocking.",
				createdAt: new Date(Date.now() - 95 * 60 * 1000).toISOString(),
			},
		];
	});
	return { reviewerHandleId: `${session.id}-reviewer`, reviews: states, runs, activeReviewers: [] };
}

function mockReviewTitle(prNumber: number): string {
	switch (prNumber) {
		case 319:
			return "Browser preview rail renders inside AO";
		case 320:
			return "Review tab keeps stacked PR rows visible";
		case 321:
			return "Draft child PR waits for parent review";
		case 318:
			return "Terminal polish feedback";
		case 323:
			return "README screenshot assets ready";
		default:
			return `Demo pull request ${prNumber}`;
	}
}
