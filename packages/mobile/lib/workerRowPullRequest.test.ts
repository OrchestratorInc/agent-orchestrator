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

	// The +N speaks for the pull requests it counts, not for the lead.
	it("tones the +N by the others: red if any closed, purple when all merged", () => {
		const extra = (prs: unknown[]) => workerRowPullRequest({ prs } as never)!;
		expect(extra([pr(15, "open"), pr(12, "merged"), pr(11, "merged"), pr(10, "merged")])).toMatchObject({ tone: "success", moreTone: "merged", more: 3 });
		expect(extra([pr(15, "open"), pr(12, "merged"), pr(9, "closed")])).toMatchObject({ moreTone: "error" });
		expect(extra([pr(7, "merged"), pr(3, "closed")])).toMatchObject({ tone: "merged", moreTone: "error" });
		expect(extra([pr(15, "open"), pr(16, "open"), pr(12, "merged")])).toMatchObject({ moreTone: "success" });
		expect(extra([pr(15, "open"), pr(18, "open", true), pr(12, "merged")])).toMatchObject({ moreTone: "passive" });
		expect(extra([pr(15, "open")])).toMatchObject({ moreTone: null, moreLifecycles: [] });
		expect(extra([pr(15, "open"), pr(9, "closed"), pr(12, "merged"), pr(11, "merged")]).moreLifecycles).toEqual([
			{ lifecycle: "merged", count: 2 },
			{ lifecycle: "closed", count: 1 },
		]);
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
			pr: { label: "#15 +1", lead: { number: 15, state: "open" } as never, number: 15, lifecycle: "open", more: 1, moreTone: "error", moreLifecycles: [{ lifecycle: "closed", count: 1 }], tone: "success", glyph: "git-pull-request" },
			machine: "Studio",
			updated: "1h",
			unreachable: true,
		});
		expect(label).toBe("Fix login. Ready to merge. Pull request 15, open, and 1 more closed. web on Studio. Updated 1 hour ago. Last known; its machine can't be reached.");
	});

	it("says what a mixed +N counts, since its colour says nothing to a screen reader", () => {
		const pr = workerRowPullRequest({ prs: [{ number: 15, state: "open" }, { number: 9, state: "closed" }, { number: 12, state: "merged" }, { number: 11, state: "merged" }] } as never);
		expect(workerRowAccessibilityLabel({ ...base, indicator: null, pr })).toContain(". Pull request 15, open, and 3 more: 2 merged, 1 closed.");
	});

	// A row reading "Ready to merge. Pull request 15, merging" contradicts itself.
	it("says the worker is merging, not ready to merge, while a merge it started runs", () => {
		const prs = [{ number: 15, state: "open", url: "u15" }];
		const merging = workerRowPullRequest({ prs } as never, () => "merging");
		expect(merging).toMatchObject({ label: "#15", lifecycle: "open", tone: "merged", glyph: "git-merge", merging: true });
		expect(workerRowAccessibilityLabel({ ...base, indicator: "ready", pr: merging })).toBe("Fix login. Merging. Pull request 15, merging. web. Updated 5 minutes ago.");
		expect(workerRowPullRequest({ prs } as never, () => "merged")).toMatchObject({ label: "#15", lifecycle: "merged", tone: "merged", glyph: "git-merge" });
		// A failed merge, or none, leaves the pull request as the board reports it.
		expect(workerRowPullRequest({ prs } as never, () => "failed")).toEqual(workerRowPullRequest({ prs } as never));
		// The board already reports it closed: nothing to lay over.
		expect(workerRowPullRequest({ prs: [{ number: 15, state: "closed", url: "u15" }] } as never, () => "merging")).toMatchObject({ lifecycle: "closed", tone: "error" });
	});

	// #15 leads, but the merge running is #12's: the pill names #12 while it runs.
	it("leads with a pull request being merged, without calling the other one merging", () => {
		const prs = [{ number: 15, state: "open", url: "u15" }, { number: 12, state: "open", url: "u12" }];
		const pill = workerRowPullRequest({ prs } as never, (pr) => (pr.url === "u12" ? "merging" : undefined));
		expect(pill).toMatchObject({ number: 12, label: "#12 +1", merging: true, tone: "merged", more: 1, moreTone: "success" });
		// Once it lands, #15 leads again with a merged +1.
		const after = workerRowPullRequest({ prs } as never, (pr) => (pr.url === "u12" ? "merged" : undefined));
		expect(after).toMatchObject({ number: 15, tone: "success", more: 1, moreTone: "merged" });
		expect(after?.merging).toBeUndefined();
	});

	it("says just now for a fresh row", () => {
		expect(workerRowAccessibilityLabel({ ...base, indicator: "working", updated: "now" })).toBe("Fix login. Working. web. Updated just now.");
	});
});
