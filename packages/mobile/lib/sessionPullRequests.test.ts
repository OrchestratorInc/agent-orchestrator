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
		// The row already draws `#N` beside the title, so the fallback does not repeat it.
		expect(rows.map((row) => row.title)).toEqual(FIVE.map(() => "Pull request"));
		expect(rows[0].accessibilityLabel).toBe(`Pull request 9137, ${rows[0].status.text}`);
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

// A cached summary can lag the live PR row (or lead it): the row's icon and its
// status always come from one resolved state, and an ended PR reads as ended.
describe("pullRequestRows with a stale summary", () => {
	const openSummary = (number: number) => ({ ...summary(number, "Doc"), state: "open" }) as SessionPRSummary;
	const mergedSummary = (number: number) => ({ ...summary(number, "Doc"), state: "merged" }) as SessionPRSummary;

	it("reads a PR merged since its summary was cached as merged", () => {
		const [row] = pullRequestRows([pr(9137, "merged")], (n) => openSummary(n));
		expect(row).toMatchObject({ lifecycle: "merged", glyph: "git-merge", glyphTone: "merged", status: { text: "Merged", tone: "merged" } });
	});

	it("reads a PR closed since its summary was cached as closed", () => {
		const [row] = pullRequestRows([pr(9001, "closed")], (n) => openSummary(n));
		expect(row).toMatchObject({ lifecycle: "closed", glyph: "git-pull-request-closed", status: { text: "Closed" } });
	});

	it("draws a summary that saw the merge first with the merged icon too", () => {
		const [row] = pullRequestRows([pr(9137, "open")], (n) => mergedSummary(n));
		expect(row).toMatchObject({ lifecycle: "merged", glyph: "git-merge", glyphTone: "merged", status: { text: "Merged" } });
	});
});

// A merge started from the PR page: the chat's chip and its list say so while
// it runs, and show it merged before the board or the summaries agree.
describe("sessionPullRequestModel with merges the phone started", () => {
	const lead = pr(9137, "open");
	const other = pr(9125, "merged");
	const summaries = (n: number) => (n === 9137 ? summary(9137, "Create brand doc") : undefined);

	it("says Merging… for the lead and its row while the merge runs", () => {
		const model = sessionPullRequestModel({ prs: [lead, other] }, summaries, (p) => (p.number === 9137 ? "merging" : undefined))!;
		expect(model.leadMerging).toBe(true);
		expect(model.rows[0]).toMatchObject({ number: 9137, lifecycle: "open", status: { text: "Merging…", tone: "merged" }, accessibilityLabel: "Pull request 9137, Create brand doc, Merging…" });
		expect(model.rows[1].status.text).toBe("Merged");
	});

	it("reads a finished merge as merged although the board and the summary still say open", () => {
		const model = sessionPullRequestModel({ prs: [lead, pr(9140, "open")] }, summaries, (p) => (p.number === 9137 ? "merged" : undefined))!;
		// The other open PR leads now, as it would once the board catches up.
		expect(model.lead.number).toBe(9140);
		const merged = model.rows.find((row) => row.number === 9137)!;
		expect(merged).toMatchObject({ lifecycle: "merged", glyph: "git-merge", status: { text: "Merged", tone: "merged" } });
		expect(model.leadMerging).toBeUndefined();
	});

	// A session can raise PRs in several repositories: app#12 merging is not lib#12.
	it("marks only the pull request that merged, not another with the same number", () => {
		const app = { ...pr(12, "open"), url: "https://forge.test/acme/app/pull/12" } as DashboardPR;
		const lib = { ...pr(12, "open"), url: "https://forge.test/acme/lib/pull/12" } as DashboardPR;
		const model = sessionPullRequestModel({ prs: [app, lib] }, () => undefined, (p) => (p.url === app.url ? "merged" : undefined))!;
		expect(model.rows.find((row) => row.pr.url === lib.url)).toMatchObject({ lifecycle: "open" });
		expect(model.rows.find((row) => row.pr.url === app.url)).toMatchObject({ lifecycle: "merged", status: { text: "Merged", tone: "merged" } });
		expect(model.lead.url).toBe(lib.url);
	});

	// The chip names the PR being merged, as the board's pill does.
	it("leads with a pull request being merged although another would lead", () => {
		const model = sessionPullRequestModel({ prs: [pr(15, "open"), pr(12, "open")] }, () => undefined, (p) => (p.number === 12 ? "merging" : undefined))!;
		expect(model.lead.number).toBe(12);
		expect(model.leadMerging).toBe(true);
		expect(model.rows.find((row) => row.number === 15)!.status.text).not.toBe("Merging…");
	});

	it("leaves the model as it was for a failed merge or none", () => {
		const plain = sessionPullRequestModel({ prs: [lead] }, summaries);
		expect(sessionPullRequestModel({ prs: [lead] }, summaries, () => "failed")).toEqual(plain);
	});
});
