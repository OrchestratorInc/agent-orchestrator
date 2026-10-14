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
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		expect(screen.getByRole("button", { name: /Spawned 2 agents/i })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: /Spawned 2 agents/i }));
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
		expect(screen.getByText("Failed")).toBeInTheDocument();
	});
});
