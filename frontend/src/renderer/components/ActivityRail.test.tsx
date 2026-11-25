import { render as renderBare, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { resetPRTabs, usePRTabs } from "../lib/pr-tabs";
import type { WorkspaceSession } from "../types/workspace";
import { InspectorSection } from "@aoagents/product-ui";
import { ActivityRail } from "./ActivityRail";
import { TooltipProvider } from "./ui/tooltip";

// The rail's filter button has a tooltip, which the app's root provides for.
// The rail sits in the inspector's Activity section, whose header holds its filter.
const render = (ui: ReactElement) => renderBare(<InspectorSection title="Activity">{ui}</InspectorSection>, { wrapper: TooltipProvider });

// A time on this machine's clock, so a test's days are the same days in every time zone.
const local = (time: string) => new Date(time).toISOString();

const URL = "https://api.github.com/repos/acme/repo/pulls/7";

const pr = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: URL,
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
	updatedAt: local("2026-10-22T00:00:00"),
	...overrides,
});

const session: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "ws-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "fix/dashboard",
	status: "needs_input",
	createdAt: local("2026-10-20T08:00:00"),
	updatedAt: local("2026-10-20T08:00:00"),
	prs: [],
};

function ActiveTab() {
	const tabs = usePRTabs("sess-1");
	return <output data-testid="request">{tabs.request ? `${tabs.request.url}#${tabs.request.section}` : ""}</output>;
}

describe("ActivityRail", () => {
	afterEach(() => resetPRTabs());

	it("puts people on the line under day dividers, newest first", () => {
		render(
			<ActivityRail
				commits={[{ sha: "aaaaaaa1", subject: "Fix the chart", timestamp: local("2026-10-20T09:00:00"), author: "ada" }]}
				prs={[pr({
					createdAt: local("2026-10-20T10:00:00"),
					conversation: [{ id: "c1", author: "grace", isBot: false, body: "Looks good", createdAt: local("2026-10-21T10:00:00") }],
				})]}
				session={session}
				status={<span>Idle</span>}
			/>,
		);
		const rail = screen.getByTestId("activity-rail");
		expect(within(rail).getByText("Idle")).toBeInTheDocument();
		const rows = within(rail).getAllByRole("listitem").map((row) => row.textContent ?? "");
		expect(rows).toHaveLength(4);
		expect(rows[0]).toContain("grace commented");
		expect(rows[0]).toContain("Looks good");
		expect(rows[1]).toContain("ada opened #7");
		// A commit by someone on the PR is their face and its subject.
		expect(rows[2]).toContain("ada: Fix the chart");
		expect(rows[2]).not.toContain("pushed a commit");
		expect(rows[3]).toContain("Created workspace");
		expect(rows[3]).toContain("fix/dashboard");
		// Each day its own list under its divider, after today's, which holds only
		// the agent's state now. (Counted in this machine's time zone, as the rail does.)
		const days = new Set([local("2026-10-20T08:00:00"), local("2026-10-20T09:00:00"), local("2026-10-20T10:00:00"), local("2026-10-21T10:00:00")].map((at) => new Date(at).toDateString()));
		expect(within(rail).getAllByRole("list")).toHaveLength(days.size + 1);
		expect(rail.textContent?.startsWith("Today")).toBe(true);
		// The state is the first row, but not one of the list's events.
		expect(within(rail).getByTestId("activity-rail-now").closest("li")).toBeNull();
		// The filter is the section's control, beside its title, not a line above the rows.
		const filter = screen.getByTestId("activity-rail-filter");
		expect(rail.contains(filter)).toBe(false);
		expect(filter.closest("[data-testid='inspector-section']")).not.toBeNull();
		// Three weights: what someone wrote is loud and leads with their face, a PR
		// milestone leads with its glyph, and machine rows are quiet one-liners (a
		// commit by someone on the PR still carries their face).
		const items = within(rail).getAllByRole("listitem");
		expect(items.map((row) => row.querySelector("[data-tier]")?.getAttribute("data-tier"))).toEqual(["talk", "state", "quiet", "quiet"]);
		expect(items.map((row) => Boolean(row.querySelector("img")))).toEqual([true, false, true, false]);
		expect(rail.querySelector("img")?.getAttribute("src")).toBe("https://github.com/grace.png?size=40");
	});

	it("keeps colour for what it means on a PR: opened and approved, merged, closed and changes requested", () => {
		const review = (reviewerId: string, verdict: "approved" | "changes_requested", submittedAt: string) => ({ reviewerId, verdict, submittedAt, autoInjectReview: false, reviewUrl: `https://github.com/acme/repo/pull/7#${reviewerId}` });
		render(
			<ActivityRail
				commits={[{ sha: "aaaaaaa1", subject: "Fix the chart", timestamp: local("2026-10-20T09:00:00"), author: "ada" }]}
				prs={[
					pr({
						state: "merged",
						createdAt: local("2026-10-20T10:00:00"),
						stateChangedAt: local("2026-10-20T15:00:00"),
						conversation: [{ id: "c1", author: "grace", isBot: false, body: "Looks good", createdAt: local("2026-10-20T11:00:00") }],
						review: {
							decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [],
							reviews: [review("linus", "changes_requested", local("2026-10-20T12:00:00")), review("grace", "approved", local("2026-10-20T13:00:00"))],
						},
					}),
					pr({ url: `${URL}8`, number: 8, state: "closed", createdAt: local("2026-10-20T10:30:00"), stateChangedAt: local("2026-10-20T14:00:00") }),
				]}
				session={session}
				status={<span>Idle</span>}
			/>,
		);
		const toneOf = (text: string) => screen.getByText((_, node) => node?.tagName === "LI" && (node.textContent ?? "").includes(text)).querySelector("[data-tone]")?.getAttribute("data-tone");
		expect(toneOf("Merged #7")).toBe("merged");
		// Red is kept for a failed request: a closed PR and a change request are neutral.
		expect(toneOf("Closed #8")).toBe("neutral");
		expect(toneOf("grace approved")).toBe("success");
		expect(toneOf("linus requested changes")).toBe("neutral");
		expect(toneOf("ada opened #7")).toBe("success");
		expect(toneOf("grace commented")).toBe("neutral");
		expect(toneOf("ada: Fix the chart")).toBe("neutral");
		expect(toneOf("Created workspace")).toBe("neutral");
	});

	it("names a commit's author in words when they aren't anyone on the PR, with no face", () => {
		render(
			<ActivityRail
				commits={[{ sha: "bbbbbbb1", subject: "Tidy", timestamp: local("2026-10-20T09:00:00"), author: "Ada Lovelace" }, { sha: "ccccccc1", subject: "More", timestamp: local("2026-10-19T09:00:00"), author: "torvalds" }]}
				prs={[pr({ createdAt: local("2026-10-20T10:00:00") })]}
				session={session}
				status={<span>Idle</span>}
			/>,
		);
		const rows = within(screen.getByTestId("activity-rail")).getAllByRole("listitem");
		const commitRows = rows.filter((row) => (row.textContent ?? "").includes("pushed a commit"));
		expect(commitRows).toHaveLength(2);
		for (const row of commitRows) expect(row.querySelector("img")).toBeNull();
	});

	it("lists a row's folded commits when it is opened", async () => {
		render(
			<ActivityRail
				commits={[
					// Half an hour apart at an hour no time zone turns midnight in: one day everywhere.
					{ sha: "bbbbbbb2", subject: "Second", timestamp: local("2026-10-20T12:30:00"), author: "ada" },
					{ sha: "aaaaaaa1", subject: "First", timestamp: local("2026-10-20T12:00:00"), author: "ada" },
				]}
				prs={[]}
				session={session}
				status={null}
			/>,
		);
		const row = screen.getByRole("button", { name: /ada pushed 2 commits/ });
		expect(row).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("First")).toBeNull();
		await userEvent.click(row);
		expect(row).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Second")).toBeInTheDocument();
		expect(screen.getByText("First")).toBeInTheDocument();
	});

	it("opens a comment's PR at its comments and an opening at its summary", async () => {
		render(
			<>
				<ActivityRail
					commits={[]}
					prs={[pr({
						createdAt: local("2026-10-20T10:00:00"),
						conversation: [{ id: "c1", author: "grace", isBot: false, body: "Looks good", createdAt: local("2026-10-21T10:00:00") }],
					})]}
					session={session}
					status={null}
				/>
				<ActiveTab />
			</>,
		);
		await userEvent.click(screen.getByRole("button", { name: /grace commented/ }));
		expect(screen.getByTestId("request")).toHaveTextContent(`${URL}#comments`);
		await userEvent.click(screen.getByRole("button", { name: /ada opened #7/ }));
		expect(screen.getByTestId("request")).toHaveTextContent(`${URL}#summary`);
		// The session's creation is about no PR, so it opens nothing.
		expect(screen.queryByRole("button", { name: /Created workspace/ })).toBeNull();
	});

	it("says a standalone session was created, not a workspace", () => {
		render(<ActivityRail commits={[]} prs={[]} session={{ ...session, workspaceId: "__standalone__" }} status={null} />);
		expect(screen.getByText("Created session")).toBeInTheDocument();
	});

	// A session keeps its filter while the app is open, so each test filters its own.
	const filtered = (id: string) => (
		<ActivityRail
			commits={[{ sha: "aaaaaaa1", subject: "Fix the chart", timestamp: local("2026-10-20T09:00:00"), author: "ada" }]}
			prs={[pr({
				createdAt: local("2026-10-20T10:00:00"),
				conversation: [
					{ id: "c1", author: "grace", isBot: false, body: "Looks good", createdAt: local("2026-10-21T10:00:00") },
					{ id: "c2", author: "review-bot", isBot: true, body: "Summary", createdAt: local("2026-10-21T11:00:00") },
				],
			})]}
			session={{ ...session, id }}
			status={<span>Idle</span>}
		/>
	);
	const rowTexts = () => within(screen.getByTestId("activity-rail")).queryAllByRole("listitem").map((row) => row.textContent ?? "");

	it("filters the rail by kind from a menu that counts each kind", async () => {
		render(filtered("sess-kinds"));
		expect(rowTexts()).toHaveLength(5);
		const button = screen.getByRole("button", { name: "Filter activity" });
		expect(button).not.toHaveAttribute("data-filtered");

		await userEvent.click(button);
		expect(screen.getByRole("menuitemcheckbox", { name: /Comments and reviews\s*2/ })).toBeChecked();
		// Ticking several kinds keeps the menu open.
		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /Commits\s*1/ }));
		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /Session events\s*1/ }));
		expect(screen.getByRole("menuitemcheckbox", { name: /Commits/ })).not.toBeChecked();
		await userEvent.keyboard("{Escape}");

		const rows = rowTexts();
		expect(rows).toHaveLength(3);
		expect(rows.join(" ")).not.toContain("Fix the chart");
		expect(rows.join(" ")).not.toContain("Created workspace");
		expect(screen.getByRole("button", { name: "Filter activity" })).toHaveAttribute("data-filtered", "true");
	});

	it("hides what bots did, and brings everything back", async () => {
		render(filtered("sess-bots"));
		await userEvent.click(screen.getByRole("button", { name: "Filter activity" }));
		expect(screen.getByRole("menuitem", { name: "Show everything" })).toHaveAttribute("aria-disabled", "true");
		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: "Hide bots" }));
		expect(rowTexts().join(" ")).not.toContain("review-bot");
		expect(rowTexts()).toHaveLength(4);

		await userEvent.click(screen.getByRole("menuitem", { name: "Show everything" }));
		expect(rowTexts()).toHaveLength(5);
		expect(screen.getByRole("button", { name: "Filter activity" })).not.toHaveAttribute("data-filtered");
	});

	it("says so when the filter hides every row, with a way back", async () => {
		render(<ActivityRail commits={[]} prs={[]} session={{ ...session, id: "sess-empty" }} status={null} />);
		await userEvent.click(screen.getByRole("button", { name: "Filter activity" }));
		// A kind with no rows is not offered.
		expect(screen.queryByRole("menuitemcheckbox", { name: /Commits/ })).toBeNull();
		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /Session events/ }));
		await userEvent.keyboard("{Escape}");
		expect(rowTexts()).toHaveLength(0);
		expect(screen.getByTestId("activity-rail-empty")).toHaveTextContent("Nothing matches the filter.");

		await userEvent.click(screen.getByRole("button", { name: "Show everything" }));
		expect(rowTexts()).toHaveLength(1);
	});

	it("keeps a session's filter when the rail is shown again", async () => {
		const kept = { ...session, id: "sess-kept" };
		const view = render(<ActivityRail commits={[]} prs={[]} session={kept} status={null} />);
		await userEvent.click(screen.getByRole("button", { name: "Filter activity" }));
		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /Session events/ }));
		view.unmount();

		render(<ActivityRail commits={[]} prs={[]} session={kept} status={null} />);
		expect(rowTexts()).toHaveLength(0);
		// Another session starts unfiltered.
		render(<ActivityRail commits={[]} prs={[]} session={{ ...session, id: "sess-other" }} status={null} />);
		expect(screen.getAllByText("Created workspace")).toHaveLength(1);
	});

	describe("the agent's side", () => {
		const work = {
			turns: [
				{ id: "t1", at: local("2026-10-20T09:00:00"), sender: "you" as const, text: "Fix the chart", turn: { state: "completed" as const, ms: 480_000, files: 2 } },
				{ id: "t2", at: local("2026-10-20T11:00:00"), sender: "orchestrator" as const, text: "The lint check failed", turn: { state: "failed" as const, ms: 60_000, error: "Rate limited" } },
				{ id: "t3", at: local("2026-10-20T12:00:00"), sender: "automation" as const, text: "Nightly tidy", turn: { state: "running" as const } },
			],
			asks: [{ id: "a1", at: local("2026-10-20T09:03:00"), kind: "approval" as const, summary: "Run npm test", waiting: true }, { id: "a2", at: local("2026-10-20T09:05:00"), kind: "question" as const, summary: "Which branch?", waiting: false }],
		};

		it("shows what was asked, who sent it and how the turn went", () => {
			render(<ActivityRail commits={[]} prs={[]} session={session} status={<span>Idle</span>} work={work} />);
			const rows = within(screen.getByTestId("activity-rail")).getAllByRole("listitem");
			const text = rows.map((row) => row.textContent ?? "");
			expect(text[0]).toContain("An automation sent");
			expect(text[0]).toContain("Nightly tidy");
			expect(text[0]).toContain("Working…");
			expect(text[1]).toContain("Orchestrator sent");
			expect(text[1]).toContain("Failed · Rate limited");
			expect(text[2]).toContain("Asked a question");
			expect(text[2]).toContain("Which branch?");
			expect(text[3]).toContain("Fix the chart");
			expect(text[3]).toContain("Worked 8m · 2 files");
			// Your own request leads with what you wrote; the words "You asked" are only read out.
			expect(rows[3]!.querySelector(".sr-only")?.textContent).toBe("You asked: ");
			expect(text[4]).toContain("Created workspace");
			// The approval still waiting joins the status line instead of a row under it.
			expect(text.join("")).not.toContain("Run npm test");
			expect(screen.getByTestId("activity-rail-waiting").textContent).toBe("Waiting for your approval: Run npm test");
			// It sits in the first row, under what the agent is doing.
			expect(within(screen.getByTestId("activity-rail-now")).getByText("Idle")).toBeInTheDocument();
			// A request is as loud as a comment; an approval or a question is a state.
			expect(rows.map((row) => row.querySelector("[data-tier]")?.getAttribute("data-tier"))).toEqual(["talk", "talk", "state", "talk", "quiet"]);
			const failed = rows[1]!.querySelector('[data-testid="rail-turn-outcome"]');
			expect(failed?.getAttribute("data-turn-state")).toBe("failed");
			// A newest request that failed is the one red thing on the row.
			expect(failed?.className).toContain("text-error");
			expect(rows[2]!.querySelector("[data-tone]")?.getAttribute("data-tone")).toBe("neutral");
			// None of these rows opens anything.
			expect(within(rows[3]!).queryByRole("button")).toBeNull();
		});

		it("has a tick in the filter for requests and for approvals and questions", async () => {
			const user = userEvent.setup();
			render(<ActivityRail commits={[]} prs={[]} session={session} status={<span>Idle</span>} work={work} />);
			const rail = screen.getByTestId("activity-rail");
			await user.click(screen.getByTestId("activity-rail-filter"));
			await user.click(await screen.findByRole("menuitemcheckbox", { name: /Requests to the agent\s*3/ }));
			// The answered question and the creation; the waiting approval stays on the status line.
			expect(within(rail).getAllByRole("listitem")).toHaveLength(2);
			expect(rail.textContent).not.toContain("You asked");
			await user.click(screen.getByRole("menuitemcheckbox", { name: /Approvals and questions\s*2/ }));
			expect(within(rail).getAllByRole("listitem")).toHaveLength(1);
		});

		it("offers neither tick when the app has no history for the session", async () => {
			const user = userEvent.setup();
			render(<ActivityRail commits={[]} prs={[]} session={session} status={<span>Idle</span>} />);
			await user.click(screen.getByTestId("activity-rail-filter"));
			await screen.findByRole("menuitemcheckbox", { name: /Session events/ });
			expect(screen.queryByRole("menuitemcheckbox", { name: /Requests to the agent/ })).toBeNull();
			expect(screen.queryByRole("menuitemcheckbox", { name: /Approvals and questions/ })).toBeNull();
		});

		it("folds a sitting of your requests into one row that opens to a short list of them and the commits made", async () => {
			const user = userEvent.setup();
			const sitting = {
				turns: [
					{ id: "t0", at: local("2026-10-20T08:50:00"), sender: "you" as const, text: "Sketch the chart", turn: { state: "completed" as const, ms: 120_000 } },
					{ id: "t1", at: local("2026-10-20T09:00:00"), sender: "you" as const, text: "Add the chart", turn: { state: "completed" as const, ms: 300_000, files: 1, paths: ["chart.ts"] } },
					{ id: "t2", at: local("2026-10-20T09:10:00"), sender: "you" as const, text: "Run the tests", turn: { state: "failed" as const, ms: 60_000, error: "Usage limit reached" } },
					{ id: "t3", at: local("2026-10-20T09:15:00"), sender: "you" as const, text: "Push it", turn: { state: "completed" as const, ms: 120_000, files: 1, paths: ["chart.ts"] } },
				],
				asks: [{ id: "a1", at: local("2026-10-20T09:16:00"), kind: "approval" as const, summary: "Run git push", waiting: false, turnId: "t3" }],
			};
			render(<ActivityRail commits={[{ sha: "aaaaaaa1", subject: "feat: chart", timestamp: local("2026-10-20T09:04:00"), author: "ada" }]} prs={[]} session={{ ...session, id: "sess-sitting" }} status={<span>Idle</span>} work={sitting} />);
			const rail = screen.getByTestId("activity-rail");
			const rows = within(rail).getAllByRole("listitem");
			// One row for the sitting, plus the session's creation.
			expect(rows).toHaveLength(2);
			expect(rows[0]!.textContent).toContain("Push it");
			expect(rows[0]!.textContent).toContain("4 requests · 1 failed · Worked 10m · 1 file · 1 commit · 1 approval");
			// The earlier try failed, but you asked again: history, not an alarm.
			expect(rows[0]!.querySelector('[data-testid="rail-turn-outcome"]')?.className).not.toContain("text-error");
			expect(rows[0]!.textContent).not.toContain("Run the tests");
			expect(within(rail).queryByTestId("rail-work-list")).toBeNull();
			const toggle = within(rows[0]!).getByRole("button");
			expect(toggle.getAttribute("aria-expanded")).toBe("false");
			await user.click(toggle);
			expect(toggle.getAttribute("aria-expanded")).toBe("true");
			const list = within(rail).getByTestId("rail-work-list");
			// The requests newest first, then the commit; only the first three until asked. No times, no marks.
			const lines = () => Array.from(list.querySelectorAll("li[data-kind]")).map((line) => [line.getAttribute("data-kind"), line.textContent]);
			expect(lines()).toEqual([["request", "Push it"], ["request", "Run the testsFailed"], ["request", "Add the chart"]]);
			expect(list.textContent).not.toMatch(/\d:\d\d/);
			expect(list.querySelector("svg")).toBeNull();
			// An earlier failed try reads plainly.
			expect(within(list).getByText("Failed").className).not.toContain("text-error");
			expect(within(list).getByTestId("rail-work-more").textContent).toBe("Show 2 more");
			await user.click(within(list).getByTestId("rail-work-more"));
			expect(within(list).queryByTestId("rail-work-more")).toBeNull();
			expect(lines()).toEqual([["request", "Push it"], ["request", "Run the testsFailed"], ["request", "Add the chart"], ["request", "Sketch the chart"], ["commit", "feat: chart"]]);
			// Closing the row puts the list away.
			await user.click(toggle);
			expect(within(rail).queryByTestId("rail-work-list")).toBeNull();
		});

		it("shows a list one line past the preview whole, rather than hide one line behind a button", async () => {
			const user = userEvent.setup();
			const four = {
				turns: ["09:00", "09:05", "09:10", "09:15"].map((time, index) => ({ id: `t${index}`, at: local(`2026-10-20T${time}:00`), sender: "you" as const, text: `Step ${index}`, turn: { state: "completed" as const, ms: 60_000 } })),
				asks: [{ id: "a1", at: local("2026-10-20T09:16:00"), kind: "question" as const, summary: "Which one?", waiting: false, turnId: "t3" }],
			};
			render(<ActivityRail commits={[]} prs={[]} session={{ ...session, id: "sess-four" }} status={<span>Idle</span>} work={four} />);
			const rail = screen.getByTestId("activity-rail");
			await user.click(within(within(rail).getAllByRole("listitem")[0]!).getByRole("button"));
			const list = within(rail).getByTestId("rail-work-list");
			// Four requests, one past the preview of three: all of them show. The answered question stays a count.
			expect(list.querySelectorAll("li[data-kind]")).toHaveLength(4);
			expect(within(list).queryByTestId("rail-work-more")).toBeNull();
			expect(list.textContent).not.toContain("Which one?");
		});

		it("shows the two newest days and folds older ones behind a button that says how many rows", async () => {
			const user = userEvent.setup();
			const days = { turns: [local("2026-10-18T09:00:00"), local("2026-10-19T09:00:00"), local("2026-10-20T09:00:00"), local("2026-10-21T09:00:00")].map((at, index) => ({ id: `t${index}`, at, sender: "orchestrator" as const, text: `Check ${index}`, turn: { state: "completed" as const, ms: 60_000 } })), asks: [] };
			render(<ActivityRail commits={[]} prs={[]} session={{ ...session, id: "sess-days", createdAt: local("2026-10-18T08:00:00") }} status={<span>Idle</span>} work={days} />);
			const rail = screen.getByTestId("activity-rail");
			expect(within(rail).getAllByRole("listitem")).toHaveLength(2);
			const more = within(rail).getByTestId("activity-rail-earlier");
			expect(more.textContent).toBe("Show 3 earlier events");
			await user.click(more);
			expect(within(rail).getAllByRole("listitem")).toHaveLength(5);
			expect(within(rail).queryByTestId("activity-rail-earlier")).toBeNull();
		});
	});
});
