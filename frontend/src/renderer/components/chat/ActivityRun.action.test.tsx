import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { ActivityRun } from "./ActivityRun";
import { ActivityRow } from "./ChatTimelineItems";
import { ChatLinkProvider } from "./ChatMarkdown";

function action(id: string, actionName: string, detail: ConversationActivity["detail"] = {}): ConversationActivity {
	return {
		kind: "activity",
		id,
		sequence: Number(id.replace(/\D/g, "")),
		revision: 1,
		activityKind: "ao_action",
		status: "completed",
		summary: "unused prose",
		detail: { action: actionName, operationId: id, ...detail },
		createdAt: "2026-10-10T00:00:00Z",
	};
}

describe("AO action batching", () => {
	it("keeps reads, commands, and every AO action type in one run", async () => {
		const user = userEvent.setup();
		render(
			<ChatLinkProvider>
				<ActivityRun
					activities={[
						{
							kind: "activity", id: "1", sequence: 1, revision: 1, activityKind: "command",
							status: "completed", summary: "rg auth", detail: { command: "rg auth" }, createdAt: "",
						},
						{
							kind: "activity", id: "2", sequence: 2, revision: 1, activityKind: "command",
							status: "completed", summary: "npm test", detail: { command: "npm test" }, createdAt: "",
						},
						action("3", "session.spawned", { displayName: "Reviewer", harness: "codex", href: "ao://sessions/mer/child" }),
						action("4", "session.spawned", { displayName: "Worker", harness: "claude" }),
						action("5", "session.terminated"),
						action("6", "pull_request.claimed", { prNumber: 7, prTitle: "Readable", href: "https://github.com/acme/repo/pull/7" }),
						action("7", "pull_request.created", { prNumber: 8, href: "https://github.com/acme/repo/pull/8" }),
					]}
				/>
			</ChatLinkProvider>,
		);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls · Spawned 2 agents · Terminated an agent/i }));
		expect(screen.getByRole("button", { name: "Spawned 2 agents" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Spawned 2 agents" }));
		expect(screen.getByRole("button", { name: /Spawned an agent, Completed, Reviewer/i })).toHaveAttribute("aria-expanded", "false");
		await user.click(screen.getByRole("button", { name: /Spawned an agent, Completed, Reviewer/i }));
		expect(screen.getByRole("link", { name: "Open session" })).toHaveAttribute("href", "ao://sessions/mer/child");
		expect(screen.getByText("Reviewer")).toBeInTheDocument();
		expect(screen.getByText("codex")).toBeInTheDocument();
		expect(screen.queryByText("unused prose")).not.toBeInTheDocument();
		expect(screen.getByText("Terminated an agent")).toBeInTheDocument();
		expect(screen.getByText("Claimed a pull request")).toBeInTheDocument();
		expect(screen.getByText("Created a pull request")).toBeInTheDocument();
	});

	it("updates one row when a replay changes revision and status", async () => {
		const user = userEvent.setup();
		const base = action("9", "session.renamed", { previousDisplayName: "Old", displayName: "New" });
		const { rerender } = render(<ActivityRow activity={base} />);
		expect(screen.getAllByRole("button", { name: /Renamed a session/i })).toHaveLength(1);
		await user.click(screen.getByRole("button", { name: /Renamed a session/i }));
		expect(screen.getByText("Old -> New")).toBeInTheDocument();
		rerender(<ActivityRow activity={{ ...base, revision: 3, status: "failed", detail: { ...base.detail, error: "name rejected" } }} />);
		expect(screen.getAllByRole("button", { name: /Renamed a session/i })).toHaveLength(1);
		expect(screen.getByText("name rejected")).toBeInTheDocument();
		expect(screen.getAllByText("Failed").length).toBeGreaterThan(0);
	});
});

describe("a failed spawn", () => {
	const reason = "The default account is signed out or now holds another login, and no other account is signed in.";
	const activities: ConversationActivity[] = [
		{
			kind: "activity", id: "1", sequence: 1, revision: 1, activityKind: "auto_review", status: "completed",
			summary: "ao spawn --project market-place --name 'background config snapshot'",
			detail: { status: "approved", riskLevel: "medium", command: "ao spawn --project market-place --name 'background config snapshot'" },
			createdAt: "",
		},
		{
			kind: "activity", id: "2", sequence: 2, revision: 1, activityKind: "command", status: "failed",
			summary: "ao spawn --project market-place --name 'background config snapshot'",
			detail: { command: "ao spawn --project market-place --name 'background config snapshot'", exitCode: 1, output: reason },
			createdAt: "",
		},
		action("3", "session.spawned", { operationId: "failed:req-1", error: reason }),
	];
	activities[2]!.status = "failed";

	it("reads as one failed step that opens onto the daemon's reason", () => {
		render(<ChatLinkProvider><ActivityRun activities={activities} /></ChatLinkProvider>);
		// The spawn record joins its call: no second row, and the exit is not counted again.
		const run = screen.getByRole("button", { name: /Ran 2 tool calls/ });
		expect(run).toHaveTextContent("1 failed");
		expect(run).not.toHaveTextContent("exited");
		expect(run).not.toHaveTextContent("Spawned an agent");
		// Folded, the run still shows the call that failed, open onto why.
		const step = screen.getByRole("button", { name: /Starting\s*background config snapshot\s*failed/ });
		expect(step).toHaveAttribute("aria-expanded", "true");
		expect(step.querySelector("svg.icon-square-terminal")).not.toBeNull();
		expect(screen.getByText(reason)).toBeInTheDocument();
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("opens onto the daemon's reason when the call printed nothing", () => {
		const bare: ConversationActivity = {
			kind: "activity", id: "2", sequence: 2, revision: 1, activityKind: "command", status: "failed",
			summary: "ao spawn --project market-place --name 'background config snapshot'",
			detail: { exitCode: 1 },
			createdAt: "",
		};
		render(<ChatLinkProvider><ActivityRun activities={[activities[0]!, bare, activities[2]!]} /></ChatLinkProvider>);
		const step = screen.getByRole("button", { name: /background config snapshot'\s*failed$/ });
		expect(step).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText(reason)).toBeInTheDocument();
	});

	it("opens the approval onto the command it allowed, its chevron turned down", async () => {
		const user = userEvent.setup();
		render(<ActivityRow activity={activities[0]!} />);
		const row = screen.getByRole("button", { name: /Auto-approved\s*medium risk/ });
		expect(row).toHaveAttribute("aria-expanded", "false");
		expect(row.querySelector("svg.icon-chevron-right")).not.toHaveClass("rotate-90");
		expect(screen.queryByText("ao spawn --project market-place --name 'background config snapshot'")).not.toBeInTheDocument();
		await user.click(row);
		expect(row).toHaveAttribute("aria-expanded", "true");
		expect(row.querySelector("svg.icon-chevron-right")).toHaveClass("rotate-90");
		expect(screen.getByTitle("ao spawn --project market-place --name 'background config snapshot'")).toBeInTheDocument();
	});
});
