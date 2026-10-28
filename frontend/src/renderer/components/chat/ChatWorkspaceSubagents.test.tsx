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
import { describe, expect, it, vi } from "vitest";
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

function snapshot(state: "running" | "completed", later: ConversationItem[] = []): ConversationSnapshot {
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
		...later,
		...(state === "completed" ? [text("final", 20, "assistant", "Both audits are summarized above.")] : []),
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

	it("keeps a finished team and its outcomes in view instead of folding it into the work", () => {
		render(<ChatWorkspace snapshot={snapshot("completed")} />);

		expect(screen.getByText("Both audits are summarized above.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Worked for/ })).toHaveAttribute("aria-expanded", "false");
		const header = screen.getByRole("button", { name: /2 subagents/ });
		// The background child never handed back, so its outcome is unknown, not done.
		expect(within(header).getByText("1 done")).toBeInTheDocument();
		expect(within(screen.getByRole("button", { name: /Audit: info pages/ })).getByText("No report")).toBeInTheDocument();
	});

	it("keeps the team in view, and a report the reader opened still open, when the turn settles", async () => {
		const user = userEvent.setup();
		const { rerender } = render(<ChatWorkspace snapshot={snapshot("running")} />);

		await user.click(screen.getByRole("button", { name: /Audit: testing gaps/ }));
		// Open, the report shows in full under the row's one-line preview of it.
		expect(await screen.findByText("Report")).toBeInTheDocument();
		expect(screen.getAllByText("Ranked 5 tests to write first.")).toHaveLength(2);

		rerender(<ChatWorkspace snapshot={snapshot("completed")} />);

		// The live window that held the turn's work folds away under "Worked for"; the
		// team stays in view outside it, and the reader's disclosure choice comes along.
		expect(await screen.findByRole("button", { name: /Worked for/ })).toBeInTheDocument();
		await waitFor(() => expect(screen.getAllByTestId("subagent-team")).toHaveLength(1));
		expect(screen.getByRole("button", { name: /Audit: testing gaps/ })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Report")).toBeInTheDocument();
		expect(screen.getAllByText("Ranked 5 tests to write first.")).toHaveLength(2);
		await waitFor(() =>
			expect(within(screen.getByRole("button", { name: /Audit: info pages/ })).getByText("No report")).toBeInTheDocument(),
		);
	});

	it("keeps a child's steps and report with its launch when the user steers mid-run", () => {
		// The steer opens a new visual turn, but the child keeps going in the same AO
		// turn, so its first command and its hand-back land after the steer.
		const steered = (items: ConversationItem[]): ConversationSnapshot => ({
			...chatFixtureEmpty,
			harness: "claude-code",
			controller: { state: "busy" },
			latestSequence: 3 + items.length,
			turns: [{ id: turnId, state: "running", requestedAt: at(0), startedAt: at(0) }],
			items: [
				text("prompt", 1, "user", "Audit the storefront with a subagent"),
				launch("pages", 2, "Audit: info pages"),
				steer("steer", 3, "Check the FAQ too"),
				...items,
			],
		});
		const firstCommand = child("p1", 4, "tool-pages", "cd /repo; cat src/app/faq/page.tsx");
		const { rerender } = render(<ChatWorkspace snapshot={steered([])} />);

		// No step yet, but the real turn is still running: the child is working, not
		// "No report", and its team stays in view above the steer.
		expect(within(screen.getByRole("button", { name: /1 subagent/ })).getByText("1 working")).toBeInTheDocument();
		expect(screen.queryByText("No report")).not.toBeInTheDocument();

		rerender(<ChatWorkspace snapshot={steered([firstCommand])} />);

		// Its first command hangs under it, not loose in the steer's part of the turn.
		expect(within(screen.getByRole("button", { name: /1 subagent/ })).getByText("1 working")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Audit: info pages/ })).toHaveTextContent("cat src/app/faq/page.tsx");
		expect(screen.queryByText(/Ran \d+ tool call/)).not.toBeInTheDocument();

		rerender(<ChatWorkspace snapshot={steered([firstCommand, handback("p2", 5, "tool-pages", "The FAQ never mentions shipping.")])} />);

		// Done, and still in view: the report reaches the launch before the steer.
		expect(within(screen.getByRole("button", { name: /1 subagent/ })).getByText("1 done")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Audit: info pages/ })).toHaveTextContent("The FAQ never mentions shipping.");
		expect(screen.queryByText("SubagentHandback")).not.toBeInTheDocument();
	});

	it("opens a child in the Subagents panel and keeps it current as the child works", async () => {
		const user = userEvent.setup();
		const tabBody = document.createElement("div");
		document.body.append(tabBody);
		const reveal = vi.fn();
		const onAvailableChange = vi.fn();
		const host = (container: HTMLElement | null) => ({ container, reveal, onAvailableChange });
		const { rerender, unmount } = render(<ChatWorkspace snapshot={snapshot("running")} subagentPanel={host(null)} />);

		// The chat says it has subagents, so the inspector can offer the tab.
		expect(onAvailableChange).toHaveBeenLastCalledWith(true);

		const row = screen.getByRole("button", { name: /Audit: info pages/ });
		expect(row).not.toHaveAttribute("aria-expanded");
		await user.click(row);
		expect(reveal).toHaveBeenCalledTimes(1);
		// Nothing opens inline: the conversation belongs to the panel.
		expect(screen.queryByText("Sent by the parent agent")).not.toBeInTheDocument();

		// The inspector mounts the tab body, and the panel opens on the child clicked.
		rerender(<ChatWorkspace snapshot={snapshot("running")} subagentPanel={host(tabBody)} />);
		const panel = within(tabBody);
		expect(panel.getByRole("heading", { name: "Audit: info pages" })).toBeInTheDocument();
		expect(panel.getByTestId("subagent-prompt")).toHaveTextContent("Read-only. Audit: info pages.");
		expect(panel.getByRole("button", { name: /Ran 2 tool calls/ })).toBeInTheDocument();
		expect(panel.queryByRole("textbox")).not.toBeInTheDocument();

		// A step that lands later, after other rows, shows up in the open conversation.
		rerender(
			<ChatWorkspace
				snapshot={snapshot("running", [child("p3", 10, "tool-pages", "cd /repo; cat src/app/shipping/page.tsx")])}
				subagentPanel={host(tabBody)}
			/>,
		);
		expect(panel.getByRole("button", { name: /Ran 3 tool calls/ })).toBeInTheDocument();

		await user.click(panel.getByRole("button", { name: "All subagents" }));
		expect(panel.getByRole("region", { name: "Working" })).toHaveTextContent("Audit: info pages");
		expect(panel.getByRole("region", { name: "Finished" })).toHaveTextContent("Audit: testing gaps");

		unmount();
		expect(onAvailableChange).toHaveBeenLastCalledWith(false);
		tabBody.remove();
	});

	it("does not name a child's running step as the parent's own in the turn's live row", () => {
		const childStep = { ...child("p3", 10, "tool-pages", "cd /repo; grep -rn returns src/app"), status: "running" as const };
		render(<ChatWorkspace snapshot={snapshot("running", [childStep])} />);

		// The child's row says what it is running; the parent is only waiting on it.
		expect(screen.getByRole("button", { name: /Audit: info pages/ })).toHaveTextContent("grep -rn returns src/app");
		expect(screen.getByTestId("live-working-label")).toBeInTheDocument();
		expect(screen.queryByTestId("live-working-task")).not.toBeInTheDocument();
	});

	it("does not offer the panel when the chat has no subagents", () => {
		const onAvailableChange = vi.fn();
		const items: ConversationItem[] = [text("prompt", 1, "user", "Hi"), text("reply", 2, "assistant", "Hello.")];
		render(
			<ChatWorkspace
				snapshot={{ ...snapshot("completed"), items, latestSequence: items.length }}
				subagentPanel={{ container: null, reveal: vi.fn(), onAvailableChange }}
			/>,
		);
		expect(onAvailableChange).toHaveBeenLastCalledWith(false);
	});
});
