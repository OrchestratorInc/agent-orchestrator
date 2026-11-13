import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { buildActivityRail, commentExcerpt } from "./activity-rail";
import { noPRDetails } from "./pr-display";

const pr = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: "https://api.github.com/repos/acme/repo/pulls/7",
	htmlUrl: "https://github.com/acme/repo/pull/7",
	number: 7,
	title: "Fix dashboard",
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "fix/dashboard",
	targetBranch: "main",
	headSha: "abc123",
	additions: 10,
	deletions: 3,
	changedFiles: 2,
	ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7", conflictFiles: [] },
	updatedAt: "2026-10-22T00:00:00Z",
	...overrides,
});

// UTC days, so the folding doesn't depend on the machine's time zone.
const utcDay = (iso: string) => iso.slice(0, 10);
const commit = (sha: string, timestamp: string, author = "ada") => ({ sha, subject: `subject ${sha}`, timestamp, author });

describe("buildActivityRail", () => {
	it("puts the newest event first and the session's creation last", () => {
		const events = buildActivityRail({
			createdAt: "2026-10-20T08:00:00Z",
			branch: "fix/dashboard",
			commits: [commit("aaaaaaa1", "2026-10-20T09:00:00Z")],
			prs: [pr({ createdAt: "2026-10-20T10:00:00Z", state: "merged", stateChangedAt: "2026-10-21T12:00:00Z" })],
		}, utcDay);
		expect(events.map((event) => event.kind)).toEqual(["merged", "opened", "commits", "created"]);
		expect(events[3]).toMatchObject({ detail: "fix/dashboard" });
		expect(events[1]).toMatchObject({ author: "ada", detail: "Fix dashboard", pr: { number: 7 } });
	});

	it("marks a draft PR's opening as a draft, and a closed PR's close", () => {
		const draft = buildActivityRail({ commits: [], prs: [pr({ state: "draft", createdAt: "2026-10-20T10:00:00Z" })] }, utcDay);
		expect(draft.map((event) => event.kind)).toEqual(["draft"]);
		const closed = buildActivityRail({ commits: [], prs: [pr({ state: "closed", createdAt: "2026-10-20T10:00:00Z", stateChangedAt: "2026-10-21T10:00:00Z" })] }, utcDay);
		expect(closed.map((event) => event.kind)).toEqual(["closed", "opened"]);
	});

	it("folds one person's commits on one day, not across days or people", () => {
		const events = buildActivityRail({
			commits: [
				commit("c3", "2026-10-21T11:00:00Z"),
				commit("c2", "2026-10-21T10:00:00Z"),
				commit("c1", "2026-10-21T09:00:00Z", "grace"),
				commit("c0", "2026-10-20T09:00:00Z"),
			],
			prs: [],
		}, utcDay);
		expect(events.map((event) => [event.author, event.count])).toEqual([["ada", 2], ["grace", 1], ["ada", 1]]);
		// The folded row reads its newest commit and keeps each one, newest first.
		expect(events[0]!.detail).toBe("c3 subject c3");
		expect(events[0]!.commits).toEqual([{ sha: "c3", subject: "subject c3" }, { sha: "c2", subject: "subject c2" }]);
	});

	it("keeps only the newest 50 commits", () => {
		const commits = Array.from({ length: 60 }, (_, index) => commit(`sha${String(index).padStart(5, "0")}`, new Date(Date.UTC(2026, 9, 1) + index * 86_400_000).toISOString()));
		const events = buildActivityRail({ commits: [...commits].reverse(), prs: [] }, utcDay);
		expect(events).toHaveLength(50);
		expect(events.at(-1)!.at).toBe(commits[10]!.timestamp);
	});

	it("shows comments, review notes with replies, and folds one reviewer's notes", () => {
		const events = buildActivityRail({
			commits: [],
			prs: [pr({
				conversation: [{ id: "c1", author: "bot", isBot: true, body: "<!-- tag -->\n> quoted\n@ada **Looks** good", createdAt: "2026-10-21T12:00:00Z" }],
				threads: [
					{ id: "t1", path: "src/a.ts", line: 4, resolved: true, comments: [
						{ id: "n1", author: "grace", isBot: false, body: "Rename this", createdAt: "2026-10-21T09:00:00Z" },
						{ id: "n2", author: "ada", isBot: false, body: "Done", createdAt: "2026-10-21T11:00:00Z" },
					] },
					{ id: "t2", path: "src/b.ts", resolved: true, comments: [{ id: "n3", author: "grace", isBot: false, body: "Typo", createdAt: "2026-10-21T09:30:00Z" }] },
				],
			})],
		}, utcDay);
		expect(events.map((event) => event.kind)).toEqual(["comment", "reply", "reviewNotes"]);
		expect(events[0]).toMatchObject({ author: "bot", bot: true, detail: "Looks good" });
		expect(events[1]).toMatchObject({ author: "ada", detail: "Done", resolved: true });
		expect(events[2]).toMatchObject({ author: "grace", count: 2, detail: "src/b.ts · src/a.ts:4", resolved: true });
	});

	it("says a folded row is resolved only when all its threads are", () => {
		const events = buildActivityRail({
			commits: [],
			prs: [pr({ threads: [
				{ id: "t1", path: "a.ts", resolved: true, comments: [{ id: "n1", author: "grace", isBot: false, body: "x", createdAt: "2026-10-21T09:00:00Z" }] },
				{ id: "t2", path: "b.ts", resolved: false, comments: [{ id: "n2", author: "grace", isBot: false, body: "y", createdAt: "2026-10-21T10:00:00Z" }] },
			] })],
		}, utcDay);
		expect(events).toHaveLength(1);
		expect(events[0]!.resolved).toBe(false);
	});

	it("shows a review that approves, asks for changes or says something, not one that only stands for a reply", () => {
		const review = (reviewerId: string, verdict: "none" | "approved" | "changes_requested", submittedAt: string, body?: string) =>
			({ reviewerId, verdict, submittedAt, body, autoInjectReview: false, reviewUrl: `https://github.com/acme/repo/pull/7#${reviewerId}-${submittedAt}` });
		const events = buildActivityRail({
			commits: [],
			prs: [pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [
				review("grace", "approved", "2026-10-21T12:00:00Z"),
				review("linus", "changes_requested", "2026-10-21T11:00:00Z", "Needs tests"),
				review("ada", "none", "2026-10-21T10:00:00Z", "Overall fine"),
				review("ada", "none", "2026-10-21T09:00:00Z"),
			] } })],
		}, utcDay);
		expect(events.map((event) => [event.kind, event.author])).toEqual([["approved", "grace"], ["changes", "linus"], ["reviewed", "ada"]]);
		expect(events[1]!.detail).toBe("Needs tests");
	});
});

describe("commentExcerpt", () => {
	it("reads the first line someone wrote", () => {
		expect(commentExcerpt("<!-- bot marker -->\n\n> earlier text\n@grace I've **fixed** `it`\nmore")).toBe("I've fixed it");
		expect(commentExcerpt("<details><summary>Walkthrough</summary>")).toBe("Walkthrough");
		expect(commentExcerpt("")).toBe("");
	});
});
