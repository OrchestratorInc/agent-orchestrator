import { describe, expect, it } from "vitest";
import type { PullRequestFacts } from "../types/workspace";
import { prMixBucket, sessionPRMix } from "./session-pr-mix";

function pr(number: number, overrides: Partial<PullRequestFacts> = {}): PullRequestFacts {
	return {
		url: `https://github.com/acme/app/pull/${number}`,
		number,
		state: "open",
		ci: "passing",
		review: "review_required",
		mergeability: "blocked",
		reviewComments: false,
		updatedAt: "2026-11-16T00:00:00Z",
		...overrides,
	};
}

describe("prMixBucket", () => {
	it.each([
		[{ state: "draft" }, "draft"],
		[{ ci: "failing" }, "ciFailing"],
		[{ mergeability: "conflicting" }, "conflicts"],
		[{ review: "changes_requested" }, "changesRequested"],
		[{}, "awaitingReview"],
		[{ review: "none", mergeability: "mergeable" }, "mergeable"],
		[{ review: "approved", mergeability: "blocked" }, "approved"],
		[{ review: "none", mergeability: "mergeable", stackParent: 8277 }, "waitingOnBase"],
		// Its own failing checks speak before the stack it sits on.
		[{ ci: "failing", stackParent: 8277 }, "ciFailing"],
		[{ state: "merged" }, undefined],
		[{ state: "closed" }, undefined],
	] as const)("reads %o as %s", (overrides, bucket) => {
		expect(prMixBucket(pr(1, overrides))).toBe(bucket);
	});
});

describe("sessionPRMix", () => {
	it("counts two PRs awaiting review and one stacked on another session's PR", () => {
		expect(
			sessionPRMix({
				displayStatus: "Awaiting review",
				prs: [
					pr(8484),
					pr(8485),
					pr(8487, { review: "none", mergeability: "mergeable", stackParent: 8277 }),
				],
			}),
		).toEqual({ lead: "2 awaiting review", detail: "1 waiting on #8277" });
	});

	it("keeps the daemon's phrase for a single PR in the lead state", () => {
		expect(
			sessionPRMix({
				displayStatus: "CI failing",
				prs: [
					pr(8501, { ci: "failing", review: "none" }),
					pr(8502, { review: "approved", mergeability: "mergeable" }),
					pr(8503, { state: "draft" }),
				],
			}),
		).toEqual({ detail: "1 draft · 1 mergeable" });
	});

	it("mentions merged PRs beside a live one", () => {
		expect(
			sessionPRMix({
				displayStatus: "Awaiting review",
				prs: [pr(8410, { state: "merged" }), pr(8412)],
			}),
		).toEqual({ detail: "1 merged" });
	});

	it("says nothing when every live PR is in the same state", () => {
		expect(
			sessionPRMix({
				displayStatus: "Mergeable",
				prs: [pr(1, { review: "none", mergeability: "mergeable" }), pr(2, { review: "none", mergeability: "mergeable" })],
			}),
		).toBeUndefined();
	});

	it("says nothing for a single PR", () => {
		expect(sessionPRMix({ displayStatus: "Awaiting review", prs: [pr(1)] })).toBeUndefined();
	});

	it("says nothing while the status describes the worker rather than a PR", () => {
		expect(
			sessionPRMix({
				displayStatus: "Working",
				prs: [pr(1), pr(2, { review: "none", mergeability: "mergeable" })],
			}),
		).toBeUndefined();
	});

	it("says nothing when no PR reads the way the daemon placed the session", () => {
		expect(
			sessionPRMix({
				displayStatus: "Changes requested",
				prs: [pr(1), pr(2, { review: "none", mergeability: "mergeable" })],
			}),
		).toBeUndefined();
	});

	it("lists PRs waiting on different parents separately", () => {
		expect(
			sessionPRMix({
				displayStatus: "Waiting on base PR",
				prs: [
					pr(10, { review: "none", mergeability: "mergeable", stackParent: 9 }),
					pr(11, { review: "none", mergeability: "mergeable", stackParent: 7 }),
				],
			}),
		).toEqual({ detail: "1 waiting on #9" });
	});
});
