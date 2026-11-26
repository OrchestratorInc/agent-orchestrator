import { describe, expect, it } from "vitest";
import { workerRowAccessibilityLabel, workerRowPullRequest } from "./workerRowPullRequest";

const pr = (number: number, state: "open" | "merged" | "closed", isDraft = false) => ({ number, state, isDraft });

describe("workerRowPullRequest", () => {
	it("is null without a real pull request", () => {
		expect(workerRowPullRequest({} as never)).toBeNull();
		expect(workerRowPullRequest({ pr: { number: 0 } } as never)).toBeNull();
	});

	it("leads with the open one, then a draft, merged and closed, the newest within each", () => {
		const lead = (prs: unknown[]) => workerRowPullRequest({ prs } as never)!;
		expect(lead([pr(12, "closed"), pr(15, "open")]).lead).toEqual(pr(15, "open"));
		expect(lead([pr(12, "closed"), pr(15, "open")])).toMatchObject({ label: "#15 +1", lifecycle: "open", tone: "success", glyph: "git-pull-request", more: 1 });
		expect(lead([pr(20, "merged"), pr(18, "open", true)])).toMatchObject({ label: "#18 +1", lifecycle: "draft", tone: "passive", glyph: "git-pull-request-draft" });
		expect(lead([pr(3, "merged"), pr(7, "merged"), pr(9, "closed")])).toMatchObject({ label: "#7 +2", lifecycle: "merged", tone: "merged", glyph: "git-merge" });
		expect(lead([pr(4, "closed")])).toMatchObject({ label: "#4", lifecycle: "closed", tone: "error", glyph: "git-pull-request-closed", more: 0 });
	});

	it("falls back to the single pr field", () => {
		expect(workerRowPullRequest({ pr: pr(1253, "open") } as never)?.label).toBe("#1253");
	});
});

describe("workerRowAccessibilityLabel", () => {
	const base = { title: "Fix login", project: "web", displayStatus: null, statusLabel: "PR open", pr: null, updated: "5m", unreachable: false };

	it("says the mark's state rather than the raw status", () => {
		expect(workerRowAccessibilityLabel({ ...base, indicator: "review" })).toBe("Fix login. Review next. web. Updated 5 minutes ago.");
		expect(workerRowAccessibilityLabel({ ...base, indicator: "attention", displayStatus: "Changes requested" })).toContain(". Changes requested.");
		expect(workerRowAccessibilityLabel({ ...base, indicator: "attention" })).toContain(". Needs you.");
		expect(workerRowAccessibilityLabel({ ...base, indicator: null })).toContain(". PR open.");
	});

	it("names the pull request, the machine and an unreachable machine", () => {
		const label = workerRowAccessibilityLabel({
			...base,
			indicator: "ready",
			pr: { label: "#15 +1", lead: { number: 15, state: "open" } as never, number: 15, lifecycle: "open", more: 1, tone: "success", glyph: "git-pull-request" },
			machine: "Studio",
			updated: "1h",
			unreachable: true,
		});
		expect(label).toBe("Fix login. Ready to merge. Pull request 15, open, and 1 more. web. on Studio. Updated 1 hour ago. Last known; its machine can't be reached.");
	});

	it("says just now for a fresh row", () => {
		expect(workerRowAccessibilityLabel({ ...base, indicator: "working", updated: "now" })).toBe("Fix login. Working. web. Updated just now.");
	});
});
