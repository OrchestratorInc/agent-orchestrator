import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { resetPRTabs, usePRTabs } from "../lib/pr-tabs";
import type { WorkspaceSession } from "../types/workspace";
import { ActivityRail } from "./ActivityRail";

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
	updatedAt: "2026-10-22T00:00:00Z",
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
	createdAt: "2026-10-20T08:00:00Z",
	updatedAt: "2026-10-20T08:00:00Z",
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
				commits={[{ sha: "aaaaaaa1", subject: "Fix the chart", timestamp: "2026-10-20T09:00:00Z", author: "ada" }]}
				prs={[pr({
					createdAt: "2026-10-20T10:00:00Z",
					conversation: [{ id: "c1", author: "grace", isBot: false, body: "Looks good", createdAt: "2026-10-21T10:00:00Z" }],
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
		expect(rows[2]).toContain("ada pushed a commit");
		expect(rows[2]).toContain("aaaaaaa Fix the chart");
		expect(rows[3]).toContain("Created workspace");
		expect(rows[3]).toContain("fix/dashboard");
		// Two days, each its own list under its divider.
		expect(within(rail).getAllByRole("list")).toHaveLength(2);
		// No profile pictures: every node is a glyph.
		expect(rail.querySelector("img")).toBeNull();
	});

	it("lists a row's folded commits when it is opened", async () => {
		render(
			<ActivityRail
				commits={[
					{ sha: "bbbbbbb2", subject: "Second", timestamp: "2026-10-20T10:00:00Z", author: "ada" },
					{ sha: "aaaaaaa1", subject: "First", timestamp: "2026-10-20T09:00:00Z", author: "ada" },
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
						createdAt: "2026-10-20T10:00:00Z",
						conversation: [{ id: "c1", author: "grace", isBot: false, body: "Looks good", createdAt: "2026-10-21T10:00:00Z" }],
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
});
