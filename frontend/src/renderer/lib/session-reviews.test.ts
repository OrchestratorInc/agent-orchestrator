import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import type { PullRequestFacts, WorkspaceSession } from "../types/workspace";
import {
	openReviewStatesFor,
	reviewIsRunning,
	reviewPhase,
	reviewPhaseRun,
	reviewRunDisabled,
	reviewSessionRunAction,
	sessionReviewsQueryOptions,
	type PRReviewState,
} from "./session-reviews";

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		workspaceId: "proj-1",
		workspaceName: "app",
		title: "review work",
		provider: "codex",
		kind: "worker",
		branch: "feature/review-work",
		status: "pr_open",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
		id: "session-1",
		...overrides,
	};
}

describe("sessionReviewsQueryOptions", () => {
	it("owns the shared reviews query key", () => {
		expect(sessionReviewsQueryOptions(session(), true).queryKey).toEqual([
			"session-reviews",
			"session-1",
		]);
	});

	it("lets callers opt into a longer staleTime", () => {
		expect(
			sessionReviewsQueryOptions(session(), true).staleTime,
		).toBeUndefined();
		expect(sessionReviewsQueryOptions(session(), true, 60_000).staleTime).toBe(
			60_000,
		);
	});
});

const pr = (number: number, state: PullRequestFacts["state"] = "open"): PullRequestFacts => ({
	url: `https://github.com/o/r/pull/${number}`,
	number,
	state,
	ci: "passing",
	review: "pending",
	mergeability: "clean",
	reviewComments: false,
	updatedAt: "2026-06-10T00:00:00Z",
});

const reviewState = (number: number, status: PRReviewState["status"]): PRReviewState => ({
	prNumber: number,
	prUrl: `https://github.com/o/r/pull/${number}`,
	status,
	targetSha: "sha",
	title: `PR ${number}`,
});

// The Reviews tab and the command palette both read their eligibility from these
// helpers. They previously lived as a private copy inside SessionInspector, and a
// merge silently re-forked them — pin the behaviour so a second copy can't drift
// back in unnoticed.
describe("shared review eligibility helpers", () => {
	it("keeps only the review states belonging to a session's open or draft PRs", () => {
		const target = session({ prs: [pr(1), pr(2, "draft"), pr(3, "merged"), pr(4, "closed")] });
		const states = [
			reviewState(1, "needs_review"),
			reviewState(2, "needs_review"),
			reviewState(3, "up_to_date"),
			reviewState(4, "up_to_date"),
		];
		expect(openReviewStatesFor(target, states).map((state) => state.prNumber)).toEqual([1, 2]);
	});

	it("reports a running review across the session's open PRs", () => {
		expect(reviewIsRunning([reviewState(1, "needs_review"), reviewState(2, "running")])).toBe(true);
		expect(reviewIsRunning([reviewState(1, "needs_review")])).toBe(false);
	});

	it("polls only while a run is still active", () => {
		const interval = sessionReviewsQueryOptions(session(), true).refetchInterval as (query: { state: { data: unknown } }) => number | false;
		const data = (status: string) => ({ reviewerHandleId: "", runs: [{ status }], reviews: [], activeReviewers: [] });
		for (const status of ["queued", "launching", "running", "cancelling"]) {
			expect(interval({ state: { data: data(status) } })).toBe(2500);
		}
		for (const status of ["completed", "failed", "cancelled", "superseded"]) {
			expect(interval({ state: { data: data(status) } })).toBe(false);
		}
	});

	it("disables the run when triggering, with no open states, or with every state ineligible", () => {
		expect(reviewRunDisabled([reviewState(1, "needs_review")], true)).toBe(true);
		expect(reviewRunDisabled([], false)).toBe(true);
		expect(reviewRunDisabled([reviewState(1, "ineligible"), reviewState(2, "ineligible")], false)).toBe(true);
		expect(reviewRunDisabled([reviewState(1, "ineligible"), reviewState(2, "needs_review")], false)).toBe(false);
	});

	it("labels the run action from the shared catalog rather than hardcoded English", () => {
		expect(reviewSessionRunAction([reviewState(1, "needs_review")], true)).toBe(
			appI18n.t("inspector.review.reviewing"),
		);
		expect(reviewSessionRunAction([reviewState(1, "running")], false)).toBe(
			appI18n.t("inspector.review.reviewing"),
		);
		expect(reviewSessionRunAction([reviewState(1, "changes_requested")], false)).toBe(
			appI18n.t("inspector.review.rerun"),
		);
		expect(reviewSessionRunAction([reviewState(1, "needs_review")], false)).toBe(
			appI18n.t("inspector.review.runLatest"),
		);
	});
});

describe("reviewPhaseRun", () => {
	const running = (n: number, harness: string, status: string) =>
		({ prUrl: `https://github.com/acme/app/pull/${n}`, prNumber: n, targetSha: `sha-${n}`, status: "running", latestRun: { id: `run-${n}`, harness, status, targetSha: `sha-${n}`, createdAt: "2026-06-10T00:00:00Z" } }) as unknown as PRReviewState;

	it("names the run waiting for input, not the first PR's", () => {
		const states = [running(3, "codex", "launching"), running(4, "claude-code", "running")];
		const phase = reviewPhase(states, "waiting_input", "claude-code");
		expect(phase).toBe("waiting_input");
		expect(reviewPhaseRun(states, phase, "claude-code")?.id).toBe("run-4");
	});

	it("names the launching run while a pass starts", () => {
		const states = [running(3, "codex", "stopping"), running(4, "codex", "queued")];
		expect(reviewPhaseRun(states, "starting")?.id).toBe("run-4");
	});
});
