import { describe, expect, it } from "vitest";
import type { DashboardPR, SessionPRSummary } from "./api";
import { prReviewPromptHeadline } from "./prView";
import { pullRequestMenuAction, pullRequestRows, sessionPullRequestModel, sessionPullRequests } from "./sessionPullRequests";

const pr = (number: number, state: "open" | "merged" | "closed", isDraft = false): DashboardPR =>
	({ number, url: `https://forge.test/pr/${number}`, state, isDraft }) as DashboardPR;
const summary = (number: number, title: string): SessionPRSummary =>
	({ number, title, url: `https://forge.test/pr/${number}`, state: "open", repo: "o/r", author: "a", sourceBranch: "b", targetBranch: "main", additions: 0, deletions: 0, changedFiles: 0, ci: { state: "passing", failingChecks: [] }, mergeability: { state: "mergeable" }, review: { decision: "none", hasUnresolvedHumanComments: false } }) as unknown as SessionPRSummary;

const FIVE = [pr(9001, "closed"), pr(9120, "merged"), pr(9137, "open"), pr(9125, "merged"), pr(9140, "open", true)];

describe("sessionPullRequests", () => {
	it("orders open, draft, merged, closed, newest first", () => {
		expect(sessionPullRequests({ prs: FIVE }).map((p) => p.number)).toEqual([9137, 9140, 9125, 9120, 9001]);
		expect(sessionPullRequests({ pr: pr(5, "open") }).map((p) => p.number)).toEqual([5]);
		expect(sessionPullRequests({ prs: [pr(0, "open")] })).toEqual([]);
		expect(sessionPullRequests({})).toEqual([]);
	});
});

describe("pullRequestRows", () => {
	it("builds a row per PR with its title, glyph, tones and words", () => {
		const lead = pr(9137, "open");
		const sum = summary(9137, "Create brand doc");
		const [row] = pullRequestRows([lead], (n) => (n === 9137 ? sum : undefined));
		const status = prReviewPromptHeadline(lead, sum);
		expect(row).toEqual({
			pr: lead,
			number: 9137,
			title: "Create brand doc",
			lifecycle: "open",
			glyph: "git-pull-request",
			glyphTone: "success",
			status,
			accessibilityLabel: `Pull request 9137, Create brand doc, ${status.text}`,
		});
	});

	// Review Focus 1: before summaries load (first open, offline) a row is never blank.
	it("falls back to the number before summaries load", () => {
		const rows = pullRequestRows(sessionPullRequests({ prs: FIVE }), () => undefined);
		expect(rows.map((row) => row.title)).toEqual(FIVE.map(() => expect.stringMatching(/^Pull request #\d+$/)));
		expect(rows.find((row) => row.number === 9125)?.status).toEqual({ text: "Merged", tone: "merged" });
		expect(rows.find((row) => row.number === 9140)).toMatchObject({ lifecycle: "draft", glyph: "git-pull-request-draft", glyphTone: "passive" });
		expect(rows.find((row) => row.number === 9001)).toMatchObject({ glyph: "git-pull-request-closed", glyphTone: "error" });
		expect(rows.every((row) => row.status.text.length > 0)).toBe(true);
	});
});

describe("sessionPullRequestModel", () => {
	it("is undefined with no PR and leads with the badge's lead", () => {
		expect(sessionPullRequestModel({}, () => undefined)).toBeUndefined();
		const sum = summary(9137, "Create brand doc");
		const model = sessionPullRequestModel({ prs: FIVE }, (n) => (n === 9137 ? sum : undefined))!;
		expect(model.lead.number).toBe(9137);
		expect(model.leadSummary).toBe(sum);
		expect(model.badge).toMatchObject({ more: 4, moreTone: "error" });
		expect(model.rows.map((row) => row.number)).toEqual([9137, 9140, 9125, 9120, 9001]);
	});
});

describe("pullRequestMenuAction", () => {
	it("menu opens the review for one PR and the list for several", () => {
		expect(pullRequestMenuAction(0)).toBe("none");
		expect(pullRequestMenuAction(1)).toBe("review");
		expect(pullRequestMenuAction(4)).toBe("list");
	});
});
