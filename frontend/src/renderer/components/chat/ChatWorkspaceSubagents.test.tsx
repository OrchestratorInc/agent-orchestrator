/**
 * Subagents in the chat timeline.
 *
 * The rows below are the shape Claude's ACP adapter records: the launch is filed as
 * reasoning, its result is a note to the model, and each child step points back at
 * the launch. What is asserted is what a reader sees: one team where the parent
 * fanned out, the children's steps under it instead of loose in the parent's turn,
 * and none of the provider's internal notes.
 */

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import type { ConversationActivity, ConversationItem, ConversationSnapshot } from "../../types/conversation";
import { renderChat as render } from "../../test/render-chat";

const turnId = "turn-subagents";
const at = (seconds: number) => new Date(Date.UTC(2026, 9, 24, 10, 0, seconds)).toISOString();

function launch(id: string, sequence: number, description: string): ConversationActivity {
	return {
		kind: "activity", id, turnId, sequence, revision: 0,
		providerItemId: `tool-${id}`,
		activityKind: "reasoning",
		status: "completed",
		summary: description,
		detail: {
			providerToolName: "Agent",
			nestedAgent: true,
			input: { description, prompt: `Read-only. ${description}.`, subagent_type: "Explore" },
			output: JSON.stringify([{ text: "Async agent launched successfully.\nagentId: a82259 (internal ID - do not mention to user.)", type: "text" }]),
		} as ConversationActivity["detail"],
		createdAt: at(1),
	};
}

function child(id: string, sequence: number, parent: string, command: string): ConversationActivity {
	return {
		kind: "activity", id, turnId, sequence, revision: 0,
		providerItemId: `tool-${id}`,
		activityKind: "command",
		status: "completed",
		summary: command,
		detail: { parentProviderItemId: parent, command },
		createdAt: at(sequence),
	};
}

function handback(id: string, sequence: number, parent: string, message: string): ConversationActivity {
	return {
		kind: "activity", id, turnId, sequence, revision: 0,
		providerItemId: `tool-${id}`,
		activityKind: "mcp_tool",
		status: "completed",
		summary: "SubagentHandback",
		detail: { parentProviderItemId: parent, providerToolName: "SubagentHandback", input: { message } } as ConversationActivity["detail"],
		createdAt: at(sequence),
	};
}

function text(id: string, sequence: number, role: "user" | "assistant", body: string): ConversationItem {
	return {
		kind: "message", id, turnId, sequence, revision: 0,
		role, origin: role === "user" ? "human" : "provider",
		text: body, streaming: false, createdAt: at(sequence),
	};
}

function steer(id: string, sequence: number, body: string): ConversationActivity {
	return {
		kind: "activity", id, turnId, sequence, revision: 0,
		activityKind: "system",
		status: "completed",
		summary: body,
		detail: { event: "steer", text: body, origin: "human" },
		createdAt: at(sequence),
	};
}

function snapshot(state: "running" | "completed"): ConversationSnapshot {
	const items: ConversationItem[] = [
		text("prompt", 1, "user", "Audit the storefront with subagents"),
		text("plan", 2, "assistant", "Launching two read-only agents."),
		launch("pages", 3, "Audit: info pages"),
		launch("tests", 4, "Audit: testing gaps"),
		child("p1", 5, "tool-pages", "cd /repo; grep -rn shipping src/app"),
		child("t1", 6, "tool-tests", "cd /repo; git ls-files"),
		text("progress", 7, "assistant", "One agent reported back."),
		handback("t2", 8, "tool-tests", "Ranked 5 tests to write first."),
		child("p2", 9, "tool-pages", "cd /repo; cat src/app/faq/page.tsx"),
		...(state === "completed" ? [text("final", 10, "assistant", "Both audits are summarized above.")] : []),
	];
	return {
		...chatFixtureEmpty,
		harness: "claude-code",
		controller: { state: state === "running" ? "busy" : "ready" },
		latestSequence: items.length,
		turns: [{
			id: turnId,
			state,
			requestedAt: at(0),
			startedAt: at(0),
			...(state === "completed" ? { completedAt: at(20) } : {}),
		}],
		items,
	};
}

describe("ChatWorkspace subagents", () => {
	it("draws the launches as one team and keeps the children's steps out of the parent's turn", () => {
		render(<ChatWorkspace snapshot={snapshot("running")} />);

		const header = screen.getByRole("button", { name: /2 subagents/ });
		expect(within(header).getByText("1 working")).toBeInTheDocument();
		expect(within(header).getByText("1 done")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Audit: info pages/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Audit: testing gaps/ })).toBeInTheDocument();

		// The parent's own prose stays in order around the team.
		expect(screen.getByText("Launching two read-only agents.")).toBeInTheDocument();
		expect(screen.getByText("One agent reported back.")).toBeInTheDocument();

		// The children's calls are not loose rows in the parent's turn any more.
		expect(screen.queryByText(/tool calls$/)).not.toBeInTheDocument();
		expect(screen.queryByText("SubagentHandback")).not.toBeInTheDocument();
		expect(screen.queryByText(/Async agent launched/)).not.toBeInTheDocument();
	});

	it("shows a child's own steps when its row is opened", async () => {
		const user = userEvent.setup();
		render(<ChatWorkspace snapshot={snapshot("running")} />);

		await user.click(screen.getByRole("button", { name: /Audit: info pages/ }));
		// Two commands with no prose between them: one run, open while the child works.
		expect(await screen.findByRole("button", { name: /Ran 2 tool calls/ })).toHaveAttribute("aria-expanded", "true");
	});

	it("folds a finished team into the settled turn's work, like any other run", async () => {
		const user = userEvent.setup();
		render(<ChatWorkspace snapshot={snapshot("completed")} />);

		expect(screen.getByText("Both audits are summarized above.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /2 subagents/ })).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: /Worked for/ }));
		const header = await screen.findByRole("button", { name: /2 subagents/ });
		// The background child never handed back, so its outcome is unknown, not done.
		expect(within(header).getByText("1 done")).toBeInTheDocument();
		await waitFor(() =>
			expect(within(screen.getByRole("button", { name: /Audit: info pages/ })).getByText("No report")).toBeInTheDocument(),
		);
	});

	it("keeps a child's steps and report with its launch when the user steers mid-run", async () => {
		const user = userEvent.setup();
		// The steer opens a new visual turn, but the child keeps going in the same AO
		// turn, so its later command and its hand-back land after the steer.
		const steered = (items: ConversationItem[]): ConversationSnapshot => ({
			...chatFixtureEmpty,
			harness: "claude-code",
			controller: { state: "busy" },
			latestSequence: items.length,
			turns: [{ id: turnId, state: "running", requestedAt: at(0), startedAt: at(0) }],
			items: [
				text("prompt", 1, "user", "Audit the storefront with a subagent"),
				launch("pages", 2, "Audit: info pages"),
				steer("steer", 3, "Check the FAQ too"),
				child("p1", 4, "tool-pages", "cd /repo; cat src/app/faq/page.tsx"),
				...items,
			],
		});
		const { rerender } = render(<ChatWorkspace snapshot={steered([])} />);

		// The real turn is still running, so the child is working, not "No report", and
		// its command is not a loose row in the steer's part of the turn.
		const header = screen.getByRole("button", { name: /1 subagent/ });
		expect(within(header).getByText("1 working")).toBeInTheDocument();
		expect(screen.queryByText(/Ran \d+ tool call/)).not.toBeInTheDocument();

		rerender(<ChatWorkspace snapshot={steered([handback("p2", 5, "tool-pages", "The FAQ never mentions shipping.")])} />);

		// Done, the team folds into the work before the steer, carrying the report.
		await user.click(screen.getByRole("button", { name: /Worked for/ }));
		const settled = await screen.findByRole("button", { name: /1 subagent/ });
		expect(within(settled).getByText("1 done")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Audit: info pages/ })).toHaveTextContent("The FAQ never mentions shipping.");
		expect(screen.queryByText("SubagentHandback")).not.toBeInTheDocument();
	});
});
