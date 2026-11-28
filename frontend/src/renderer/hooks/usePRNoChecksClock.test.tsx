import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { noPRDetails, prHasNoChecks, PR_NO_CHECKS_SETTLE_MS } from "../lib/pr-display";
import { usePRNoChecksClock } from "./usePRNoChecksClock";
import type { SessionPRSummary } from "./useSessionScmSummary";

const changedAt = Date.parse("2026-11-12T10:00:00Z");
const pr: SessionPRSummary = {
	...noPRDetails(),
	url: "https://github.com/acme/repo/pull/7",
	number: 7,
	title: "Fix",
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "fix",
	targetBranch: "main",
	headSha: "abc",
	additions: 1,
	deletions: 0,
	changedFiles: 1,
	ci: { autoInjectCI: false, state: "unknown", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "", conflictFiles: [] },
	updatedAt: new Date(changedAt).toISOString(),
	detailsObservedAt: new Date(changedAt).toISOString(),
};

function Status() {
	usePRNoChecksClock([pr]);
	return <output>{prHasNoChecks(pr) ? "no checks" : "waiting"}</output>;
}

describe("usePRNoChecksClock", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(changedAt + 5_000);
	});
	afterEach(() => vi.useRealTimers());

	it("turns a waiting PR into one with no checks once it settles, with nothing else changing", () => {
		render(<Status />);
		expect(screen.getByText("waiting")).toBeInTheDocument();
		act(() => { vi.advanceTimersByTime(PR_NO_CHECKS_SETTLE_MS); });
		expect(screen.getByText("no checks")).toBeInTheDocument();
	});
});
