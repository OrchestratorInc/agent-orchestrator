import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { buildActivityRail, commentExcerpt, filterRail, foldRail, railEvents, railWorkItems, NO_RAIL_FILTER, railFilterActive, railGroup, railGroupCounts, type RailEvent, type RailTurnInput, type RailWork } from "./activity-rail";
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
		expect(events[0]!.commits).toEqual([{ sha: "c3", subject: "subject c3", at: "2026-10-21T11:00:00Z" }, { sha: "c2", subject: "subject c2", at: "2026-10-21T10:00:00Z" }]);
	});

	it("folds one person's commits on one day around what someone else did in between", () => {
		const events = buildActivityRail({
			commits: [commit("c3", "2026-10-21T11:00:00Z"), commit("c2", "2026-10-21T10:00:00Z", "grace"), commit("c1", "2026-10-21T09:00:00Z")],
			prs: [],
		}, utcDay);
		expect(events.map((event) => [event.author, event.count, event.at])).toEqual([["ada", 2, "2026-10-21T11:00:00Z"], ["grace", 1, "2026-10-21T10:00:00Z"]]);
		expect(events[0]!.commits!.map((item) => item.sha)).toEqual(["c3", "c1"]);
	});

	it("folds a reviewer's notes around a comment, resolved only when every thread is, and not across PRs", () => {
		const note = (id: string, path: string, at: string, resolved = true) => ({ id, path, resolved, comments: [{ id: `${id}-n`, author: "grace", isBot: false, body: "x", createdAt: at }] });
		const other = "https://api.github.com/repos/acme/repo/pulls/8";
		const events = buildActivityRail({
			commits: [],
			prs: [
				pr({
					conversation: [{ id: "c1", author: "ada", isBot: false, body: "Thanks", createdAt: "2026-10-21T10:00:00Z" }],
					threads: [note("t1", "a.ts", "2026-10-21T09:00:00Z", false), note("t2", "b.ts", "2026-10-21T11:00:00Z")],
				}),
				pr({ url: other, number: 8, threads: [note("t3", "c.ts", "2026-10-21T10:30:00Z")] }),
			],
		}, utcDay);
		expect(events.map((event) => [event.kind, event.count, event.detail, event.resolved, event.pr?.number])).toEqual([
			["reviewNotes", 2, "b.ts · a.ts", false, 7],
			["reviewNotes", 1, "c.ts", true, 8],
			["comment", 1, "Thanks", undefined, 7],
		]);
	});

	it("keeps a review its reviewer later followed with another", () => {
		const latest = { reviewerId: "grace", verdict: "none" as const, submittedAt: "2026-10-21T12:00:00Z", autoInjectReview: false };
		const history = [
			{ id: "r1", reviewerId: "grace", verdict: "changes_requested" as const, body: "Needs tests", submittedAt: "2026-10-21T09:00:00Z" },
			{ id: "r2", reviewerId: "grace", verdict: "approved" as const, submittedAt: "2026-10-21T11:00:00Z" },
		];
		const rail = (review: Partial<SessionPRSummary["review"]>) => buildActivityRail({
			commits: [],
			prs: [pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [latest], ...review } })],
		}, utcDay);
		expect(rail({ history }).map((event) => [event.key, event.kind])).toEqual([[`review:${pr().url}:r2`, "approved"], [`review:${pr().url}:r1`, "changes"]]);
		// A host without the history still shows each reviewer's newest review.
		expect(rail({}).map((event) => event.kind)).toEqual([]);
		expect(rail({ reviews: [{ ...latest, verdict: "approved" }] }).map((event) => event.kind)).toEqual(["approved"]);
	});

	it("tells two PRs' approvals apart when the provider names both by who approved", () => {
		const history = [{ id: "approval:grace", reviewerId: "grace", verdict: "approved" as const, submittedAt: "2026-10-21T11:00:00Z" }];
		const review = { decision: "approved" as const, hasUnresolvedHumanComments: false, unresolvedBy: [], history };
		const events = buildActivityRail({ commits: [], prs: [pr({ review }), pr({ url: "https://gitlab.com/acme/repo/-/merge_requests/8", number: 8, review })] }, utcDay);
		expect(events).toHaveLength(2);
		expect(new Set(events.map((event) => event.key)).size).toBe(2);
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

describe("commentExcerpt on a cut body", () => {
	it("drops an HTML comment the cut left open", () => {
		expect(commentExcerpt("Fine overall\n<!-- generated: abc")).toBe("Fine overall");
	});
});

describe("the agent's side of the rail", () => {
	const turnInput = (id: string, at: string, text = "Fix the chart") => ({ id, at, sender: "you" as const, text, turn: { state: "completed" as const, ms: 480_000, files: 2 } });

	it("puts each request, with how its turn went, among the commits and PR events by time", () => {
		const events = buildActivityRail({
			createdAt: "2026-10-20T08:00:00Z",
			commits: [commit("aaaaaaa1", "2026-10-20T09:30:00Z")],
			prs: [pr({ createdAt: "2026-10-20T10:00:00Z" })],
			work: { turns: [turnInput("t1", "2026-10-20T09:00:00Z", "\nFix #7 in ./web\nand the legend")], asks: [{ id: "a1", at: "2026-10-20T09:03:00Z", kind: "approval", summary: "Run npm test", waiting: true }] },
		}, utcDay);
		expect(events.map((event) => event.kind)).toEqual(["opened", "commits", "approval", "asked", "created"]);
		const asked = events[3]!;
		expect(asked).toMatchObject({ key: "turn:t1", sender: "you", detail: "Fix #7 in ./web", turn: { state: "completed", ms: 480_000, files: 2 } });
		expect(events[2]).toMatchObject({ key: "ask:a1", detail: "Run npm test", waiting: true });
	});

	it("keeps only the newest 50 requests", () => {
		const turns = Array.from({ length: 60 }, (_, index) => ({ ...turnInput(`t${index}`, `2026-10-${String(index % 28 + 1).padStart(2, "0")}T09:00:00Z`) }));
		const asked = railEvents({ commits: [], prs: [], work: { turns, asks: [] } }).filter((event) => event.kind === "asked");
		expect(asked).toHaveLength(50);
		expect(asked.map((event) => event.key)).not.toContain("turn:t9");
		expect(asked.map((event) => event.key)).toContain("turn:t10");
	});

	it("is the same rail as before when the app has no history for the session", () => {
		const input = { createdAt: "2026-10-20T08:00:00Z", commits: [commit("aaaaaaa1", "2026-10-20T09:00:00Z")], prs: [pr({ createdAt: "2026-10-20T10:00:00Z" })] };
		expect(buildActivityRail({ ...input, work: undefined }, utcDay)).toEqual(buildActivityRail(input, utcDay));
	});
});

describe("folding the agent's side", () => {
	const base = (id: string, at: string): RailTurnInput => ({ id, at, sender: "you", text: `request ${id}`, turn: { state: "completed", ms: 5 * 60_000, files: 1, paths: [`${id}.ts`] } });
	const mine = (id: string, at: string, over: Partial<RailTurnInput> = {}): RailTurnInput => ({ ...base(id, at), ...over });
	const build = (work: RailWork, extra: { commits?: ReturnType<typeof commit>[]; prs?: SessionPRSummary[] } = {}, now = Date.parse("2026-10-21T00:00:00Z")) =>
		buildActivityRail({ commits: extra.commits ?? [], prs: extra.prs ?? [], work }, utcDay, now);

	it("folds an answered approval or question into the request it was asked in, and keeps one still waiting", () => {
		const events = build({
			turns: [mine("t1", "2026-10-20T09:00:00Z")],
			asks: [
				{ id: "a1", at: "2026-10-20T09:01:00Z", kind: "approval", summary: "Run npm test", waiting: false, turnId: "t1" },
				{ id: "a2", at: "2026-10-20T09:02:00Z", kind: "question", summary: "Which branch?", waiting: false, turnId: "t1" },
				{ id: "a3", at: "2026-10-20T09:03:00Z", kind: "approval", summary: "Run git push", waiting: true, turnId: "t1" },
			],
		});
		expect(events.map((event) => event.key)).toEqual(["ask:a3", "turn:t1"]);
		expect(events[1]?.answered).toEqual([
			{ key: "ask:a2", at: "2026-10-20T09:02:00Z", kind: "question", summary: "Which branch?" },
			{ key: "ask:a1", at: "2026-10-20T09:01:00Z", kind: "approval", summary: "Run npm test" },
		]);
	});

	it("folds a commit made while the agent worked into its request, and leaves one made later", () => {
		const events = build({ turns: [mine("t1", "2026-10-20T09:00:00Z")], asks: [] }, { commits: [commit("aaaaaaa1", "2026-10-20T09:06:00Z"), commit("bbbbbbb2", "2026-10-20T12:00:00Z")] });
		expect(events.map((event) => event.key)).toEqual(["commit:bbbbbbb2", "turn:t1"]);
		expect(events[1]?.commits?.map((entry) => entry.sha)).toEqual(["aaaaaaa1"]);
	});

	it("counts a commit during a turn still running as part of it", () => {
		const running = mine("t1", "2026-10-20T09:00:00Z", { turn: { state: "running" } as never });
		const events = build({ turns: [running], asks: [] }, { commits: [commit("aaaaaaa1", "2026-10-20T11:00:00Z")] }, Date.parse("2026-10-20T11:30:00Z"));
		expect(events.map((event) => event.key)).toEqual(["turn:t1"]);
	});

	it("folds your requests in one sitting into one row that shows the newest", () => {
		const events = build({
			turns: [
				mine("t1", "2026-10-20T09:00:00Z"),
				mine("t2", "2026-10-20T09:20:00Z", { turn: { state: "failed", ms: 60_000, error: "Usage limit reached" } }),
				mine("t3", "2026-10-20T09:40:00Z", { turn: { state: "completed", ms: 4 * 60_000, files: 2, paths: ["t1.ts", "t3.ts"] } }),
			],
			asks: [],
		});
		expect(events).toHaveLength(1);
		const row = events[0]!;
		expect(row).toMatchObject({ key: "turn:t1", at: "2026-10-20T09:40:00Z", detail: "request t3", count: 3 });
		expect(row.requests?.map((request) => request.key)).toEqual(["turn:t3", "turn:t2", "turn:t1"]);
		// Time adds up, a file two turns touched counts once, and the earlier failure is counted.
		expect(row.turn).toMatchObject({ state: "completed", ms: 10 * 60_000, files: 2, failures: 1 });
	});

	it("ends a sitting after a long gap, at someone's comment, or at a request Orchestrator sent", () => {
		const gap = build({ turns: [mine("t1", "2026-10-20T09:00:00Z"), mine("t2", "2026-10-20T09:40:00Z")], asks: [] });
		expect(gap.map((event) => event.key)).toEqual(["turn:t2", "turn:t1"]);
		const comment = build({ turns: [mine("t1", "2026-10-20T09:00:00Z"), mine("t2", "2026-10-20T09:10:00Z")], asks: [] }, {
			prs: [pr({ conversation: [{ id: "c1", author: "grace", isBot: false, body: "Hm", createdAt: "2026-10-20T09:08:00Z" }] })],
		});
		expect(comment.map((event) => event.key)).toEqual(["turn:t2", "comment:c1", "turn:t1"]);
		const sent = build({ turns: [mine("t1", "2026-10-20T09:00:00Z"), { ...mine("t2", "2026-10-20T09:06:00Z"), sender: "orchestrator" }, mine("t3", "2026-10-20T09:12:00Z")], asks: [] });
		expect(sent.map((event) => event.key)).toEqual(["turn:t3", "turn:t2", "turn:t1"]);
	});

	it("lists an open row's requests, newest first, then its commits", () => {
		const [row] = build({
			turns: [
				mine("t1", "2026-10-20T09:00:00Z", { turn: { state: "failed", ms: 60_000 } }),
				mine("t2", "2026-10-20T09:10:00Z", { turn: { state: "completed", ms: 5 * 60_000 } }),
			],
			asks: [{ id: "a1", at: "2026-10-20T09:12:00Z", kind: "question", summary: "Which flag wins?", waiting: false, turnId: "t2" }],
		}, { commits: [commit("aaaaaaa1", "2026-10-20T09:14:00Z")] });
		expect(railWorkItems(row!)).toEqual([
			{ key: "turn:t2", kind: "request", text: "request t2", failed: undefined },
			{ key: "turn:t1", kind: "request", text: "request t1", failed: true },
			{ key: "commit:aaaaaaa1", kind: "commit", text: "subject aaaaaaa1" },
		]);
		// A single request with nothing folded into it has nothing to open; its answered question is a count.
		expect(railWorkItems(build({ turns: [mine("t9", "2026-10-21T09:00:00Z")], asks: [] })[0]!)).toEqual([]);
	});

	it("folds after filtering, so a commit whose request is hidden shows again", () => {
		const flat = railEvents({ commits: [commit("aaaaaaa1", "2026-10-20T09:06:00Z")], prs: [], work: { turns: [mine("t1", "2026-10-20T09:00:00Z")], asks: [] } });
		expect(foldRail(flat, utcDay).map((event) => event.key)).toEqual(["turn:t1"]);
		expect(foldRail(filterRail(flat, { hidden: ["turns"], hideBots: false }), utcDay).map((event) => event.key)).toEqual(["commit:aaaaaaa1"]);
	});
});

describe("commentExcerpt", () => {
	it("reads the first line someone wrote", () => {
		expect(commentExcerpt("<!-- bot marker -->\n\n> earlier text\n@grace I've **fixed** `it`\nmore")).toBe("I've fixed it");
		expect(commentExcerpt("<details><summary>Walkthrough</summary>")).toBe("Walkthrough");
		expect(commentExcerpt("")).toBe("");
	});
});

describe("rail filter", () => {
	const row = (kind: RailEvent["kind"], bot = false): RailEvent => ({ key: `${kind}:${bot}`, kind, at: "2026-10-20T10:00:00Z", count: 1, bot });
	const events = [row("comment"), row("comment", true), row("reviewNotes", true), row("approved"), row("commits"), row("opened"), row("merged"), row("created")];

	it("groups every kind of row", () => {
		expect((["comment", "reviewNotes", "reply", "approved", "changes", "reviewed"] as const).map(railGroup)).toEqual(Array(6).fill("talk"));
		expect((["opened", "draft", "merged", "closed"] as const).map(railGroup)).toEqual(Array(4).fill("pr"));
		expect(railGroup("commits")).toBe("commits");
		expect(railGroup("created")).toBe("session");
		expect(railGroup("asked")).toBe("turns");
		expect((["approval", "question"] as const).map(railGroup)).toEqual(["asks", "asks"]);
	});

	it("counts rows per group before any filter", () => {
		expect(railGroupCounts(events)).toEqual({ turns: 0, asks: 0, talk: 4, commits: 1, pr: 2, session: 1 });
		expect(railGroupCounts([row("asked"), row("approval"), row("question")])).toEqual({ turns: 1, asks: 2, talk: 0, commits: 0, pr: 0, session: 0 });
	});

	it("returns the same rows when nothing is hidden", () => {
		expect(railFilterActive(NO_RAIL_FILTER)).toBe(false);
		expect(filterRail(events, NO_RAIL_FILTER)).toBe(events);
	});

	it("drops hidden groups and, when asked, what bots did", () => {
		expect(filterRail(events, { hidden: ["commits", "session"], hideBots: false }).map((event) => event.kind)).toEqual(["comment", "comment", "reviewNotes", "approved", "opened", "merged"]);
		const noBots = filterRail(events, { hidden: [], hideBots: true });
		expect(noBots).toHaveLength(6);
		expect(noBots.some((event) => event.bot)).toBe(false);
		expect(railFilterActive({ hidden: [], hideBots: true })).toBe(true);
		expect(filterRail(events, { hidden: ["talk", "commits", "pr", "session"], hideBots: false })).toEqual([]);
	});
});
