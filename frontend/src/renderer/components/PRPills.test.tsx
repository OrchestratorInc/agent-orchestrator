import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { PRPills, prStatusDot, sortSessionPRs } from "./PRPills";

const pr = (number: number, overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: `https://github.com/acme/repo/pull/${number}`,
	number,
	title: `PR ${number}`,
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: `feat/${number}`,
	targetBranch: "main",
	headSha: `sha-${number}`,
	additions: 1,
	deletions: 0,
	changedFiles: 1,
	ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "", conflictFiles: [] },
	updatedAt: "2026-11-01T00:00:00Z",
	...overrides,
});

describe("PR pills", () => {
	it("orders open PRs, then drafts, then merged and closed, newest first in each", () => {
		const sorted = sortSessionPRs([pr(1, { state: "merged" }), pr(2, { state: "draft" }), pr(3), pr(4), pr(5, { state: "closed" })]);
		expect(sorted.map((item) => item.number)).toEqual([4, 3, 2, 5, 1]);
	});

	it("dots the one fact that matters, the blocker before the review before readiness", () => {
		expect(prStatusDot(pr(1, { mergeability: { state: "conflicting", reasons: [], prUrl: "", conflictFiles: [] }, ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [] } })).labelKey).toBe("pr.card.mergeConflict");
		expect(prStatusDot(pr(2, { ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [] } })).labelKey).toBe("pr.card.checksFailing");
		expect(prStatusDot(pr(3, { review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [] } })).labelKey).toBe("pr.review.changesRequested");
		expect(prStatusDot(pr(4)).labelKey).toBe("pr.card.readyToMerge");
		expect(prStatusDot(pr(5, { ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [] } }))).toMatchObject({ labelKey: "pr.card.checksPending", breathe: true });
		expect(prStatusDot(pr(6, { state: "merged" })).labelKey).toBe("pr.state.merged");
	});

	it("shows each pill's number only, leaving the title to the tooltip and screen readers", () => {
		const undo = pr(48, { title: "feat: add taskr undo" });
		const clearDone = pr(46, { title: "Add taskr clear-done command" });
		render(<PRPills focused={undo} onFocus={() => {}} prs={[undo, clearDone]} />);
		const pills = within(screen.getByTestId("pr-pills"));
		const pressed = pills.getByRole("button", { pressed: true });
		// Visible text is the number; the card below the pills shows the full title.
		expect(within(pressed).getByText("#48")).toBeVisible();
		expect(within(pressed).queryByText("feat: add taskr undo")).toBeNull();
		expect(pressed.getAttribute("title")).toContain("feat: add taskr undo");
		// Screen readers still hear which PR each pill is.
		expect(pressed).toHaveAccessibleName(expect.stringContaining("feat: add taskr undo"));
		expect(pills.getByRole("button", { pressed: false })).toHaveAccessibleName(expect.stringContaining("Add taskr clear-done command"));
	});

	it("names a PR in another repository by its short name, which gives way before it can cover the earlier menu", () => {
		const site = pr(12, { state: "merged", repo: "acme-platform/landing-site", url: "https://github.com/acme-platform/landing-site/pull/12", title: "docs: tidy the landing page" });
		const others = [3, 4, 5].map((number) => pr(number, { state: "merged", repo: "acme-platform/control-plane", url: `https://github.com/acme-platform/control-plane/pull/${number}` }));
		render(<PRPills focused={site} onFocus={() => {}} prs={[site, ...others]} showRepository />);
		const pills = within(screen.getByTestId("pr-pills"));
		const chip = pills.getByRole("button", { pressed: true });
		expect(chip).toHaveTextContent("landing-site#12");
		expect(chip).not.toHaveTextContent("acme-platform");
		expect(chip.getAttribute("title")).toContain("acme-platform/landing-site");
		// The repository can shrink and truncate; the number never does.
		expect(within(chip).getByText("landing-site")).toHaveClass("min-w-0", "truncate");
		expect(within(chip).getByText("#12")).toHaveClass("shrink-0");
		// The three other merged PRs stay one click away.
		expect(pills.getByTestId("pr-pills-earlier")).toHaveTextContent("+3 earlier");
	});

	it("names the repository from the PR's URL when the PR doesn't carry one", () => {
		const bare = pr(7, { repo: "", url: "https://github.com/acme-platform/landing-site/pull/7" });
		const other = pr(8, { repo: "acme-platform/control-plane", url: "https://github.com/acme-platform/control-plane/pull/8" });
		render(<PRPills focused={bare} onFocus={() => {}} prs={[bare, other]} showRepository />);
		const chip = within(screen.getByTestId("pr-pills")).getByRole("button", { pressed: true });
		expect(chip).toHaveTextContent("landing-site#7");
		expect(chip.getAttribute("title")).toContain("acme-platform/landing-site");
	});
});
