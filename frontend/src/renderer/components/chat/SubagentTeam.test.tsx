import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { SubagentTeam } from "./SubagentTeam";

const at = (seconds: number) => new Date(Date.UTC(2026, 9, 24, 10, 0, seconds)).toISOString();

function launch(id: string, description: string, status: ConversationActivity["status"] = "completed"): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 1,
		revision: 0,
		activityKind: "reasoning",
		status,
		summary: description,
		detail: {
			providerToolName: "Agent",
			input: { description, prompt: `Read-only. ${description}.`, subagent_type: "Explore" },
			output: status === "failed"
				? "Concurrent subagent limit reached. You can run 20 subagents at once."
				: JSON.stringify([{ text: "Async agent launched successfully.\nagentId: a82259 (internal ID - do not mention to user.)", type: "text" }]),
		} as ConversationActivity["detail"],
		createdAt: at(0),
	};
}

function command(id: string, parent: string, text: string, status: ConversationActivity["status"] = "completed"): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 2,
		revision: 0,
		activityKind: "command",
		status,
		summary: text,
		detail: { parentProviderItemId: parent, command: text },
		createdAt: at(3),
	};
}

function handback(id: string, parent: string, message: string): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId: `tool-${id}`,
		sequence: 3,
		revision: 0,
		activityKind: "mcp_tool",
		status: "completed",
		summary: "SubagentHandback",
		detail: { parentProviderItemId: parent, providerToolName: "SubagentHandback", input: { message } } as ConversationActivity["detail"],
		createdAt: at(12),
	};
}

function team(live = true) {
	const pages = launch("pages", "Audit: info pages");
	const tests = launch("tests", "Audit: testing gaps");
	const steps = new Map<string, ConversationActivity[]>([
		["pages", [command("p1", "tool-pages", "cd /repo/app; grep -rn shipping src/app", "running")]],
		[
			"tests",
			[
				command("t1", "tool-tests", "cd /repo/app; git ls-files"),
				handback("t2", "tool-tests", "**Ranked 5 tests** to write first.\n\n1. Checkout totals"),
			],
		],
	]);
	return render(<SubagentTeam launches={[pages, tests]} steps={steps} live={live} harness="claude-code" />);
}

describe("SubagentTeam", () => {
	it("summarizes the team and lists every child with its current state", () => {
		team();

		const header = screen.getByRole("button", { name: /2 subagents/ });
		expect(header).toHaveAttribute("aria-expanded", "true");
		expect(within(header).getByText("1 working")).toBeInTheDocument();
		expect(within(header).getByText("1 done")).toBeInTheDocument();

		const working = screen.getByRole("button", { name: /Audit: info pages/ });
		expect(within(working).getByText("Explore")).toBeInTheDocument();
		expect(within(working).getByText("grep -rn shipping src/app")).toBeInTheDocument();
		expect(within(working).getByText(/Running/)).toBeInTheDocument();
		expect(within(working).getByLabelText("Working")).toBeInTheDocument();

		// A finished child shows the first line of its report, as a sentence.
		const done = screen.getByRole("button", { name: /Audit: testing gaps/ });
		expect(within(done).getByText("Ranked 5 tests to write first.")).toBeInTheDocument();
		expect(within(done).getByText("Done")).toBeInTheDocument();
		expect(within(done).getByText("12s")).toBeInTheDocument();
	});

	it("never shows the provider's internal launch note", () => {
		team();
		expect(screen.queryByText(/Async agent launched/)).not.toBeInTheDocument();
		expect(screen.queryByText(/agentId/)).not.toBeInTheDocument();
	});

	it("opens a child onto its prompt, its steps and its report", async () => {
		const user = userEvent.setup();
		team();

		const done = screen.getByRole("button", { name: /Audit: testing gaps/ });
		expect(done).toHaveAttribute("aria-expanded", "false");
		await user.click(done);
		expect(done).toHaveAttribute("aria-expanded", "true");

		expect(await screen.findByText("Report")).toBeInTheDocument();
		expect(screen.getByText("Checkout totals")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Prompt" })).toHaveAttribute("aria-expanded", "false");
		// The child's one command renders as an ordinary activity row.
		expect(screen.getByText("Ran command")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Prompt" }));
		expect(await screen.findByText("Read-only. Audit: testing gaps.")).toBeInTheDocument();
	});

	it("folds the list from its header", async () => {
		const user = userEvent.setup();
		team();
		const header = screen.getByRole("button", { name: /2 subagents/ });
		await user.click(header);
		expect(header).toHaveAttribute("aria-expanded", "false");
		await waitFor(() =>
			expect(screen.queryByRole("button", { name: /Audit: info pages/ })).not.toBeInTheDocument(),
		);
	});

	it("states a failed launch in words, not color alone", () => {
		render(
			<SubagentTeam
				launches={[launch("over", "Audit: cart", "failed")]}
				steps={new Map()}
				live
				harness="claude-code"
			/>,
		);
		const row = screen.getByRole("button", { name: /Audit: cart/ });
		expect(within(row).getByText("Failed")).toBeInTheDocument();
		expect(within(row).getByText(/Concurrent subagent limit reached/)).toBeInTheDocument();
		expect(within(screen.getByRole("button", { name: /1 subagent/ })).getByText("1 failed")).toBeInTheDocument();
	});

	it("keeps a child working after its turn settles while one of its steps still runs", () => {
		team(false);
		const row = screen.getByRole("button", { name: /Audit: info pages/ });
		expect(within(row).getByLabelText("Working")).toBeInTheDocument();
	});

	it("says a background child gave no report once its turn has settled", () => {
		const quiet = launch("quiet", "Audit: admin");
		render(
			<SubagentTeam
				launches={[quiet]}
				steps={new Map([["quiet", [command("q1", "tool-quiet", "ls")]]])}
				live={false}
				harness="claude-code"
			/>,
		);
		const row = screen.getByRole("button", { name: /Audit: admin/ });
		expect(within(row).getByText("No report")).toBeInTheDocument();
		expect(within(row).queryByLabelText("Working")).not.toBeInTheDocument();
	});

	it("keeps a disclosure choice in the caller's overrides", async () => {
		const user = userEvent.setup();
		const overrides: Record<string, boolean> = {};
		const pages = launch("pages", "Audit: info pages");
		render(
			<SubagentTeam
				launches={[pages]}
				steps={new Map()}
				live
				harness="claude-code"
				disclosureKey="turn:subagents-1"
				disclosureOverrides={overrides}
				onDisclosureChange={(key, open) => {
					overrides[key] = open;
				}}
			/>,
		);
		await user.click(screen.getByRole("button", { name: /1 subagent/ }));
		expect(overrides).toEqual({ "turn:subagents-1": false });
		await user.click(screen.getByRole("button", { name: /Audit: info pages/ }));
		expect(overrides).toMatchObject({ "turn:subagents-1:agent:pages": true });
	});
});
