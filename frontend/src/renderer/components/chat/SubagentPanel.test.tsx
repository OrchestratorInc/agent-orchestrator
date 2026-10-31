import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderChat as render } from "../../test/render-chat";
import {
	subagentCommand as command,
	subagentHandback as handback,
	subagentLaunch as launch,
	subagentMessage as message,
} from "../../test/subagent-fixtures";
import { liveTaskLabel } from "./ChatTimelineItems";
import { SubagentPanel } from "./SubagentPanel";
import type { SubagentRef } from "./subagent-team";

// Launched in this order: a finished, b still working, c refused by the provider.
const finished: SubagentRef = {
	launch: launch("a", "Audit: info pages"),
	steps: [
		command("a1", "tool-a", "cd /repo/app; git ls-files"),
		message("a2", "tool-a", "Looking at the FAQ next."),
		handback("a3", "tool-a", "The FAQ never mentions shipping."),
	],
	live: true,
};
const working: SubagentRef = {
	launch: launch("b", "Audit: testing gaps"),
	steps: [command("b1", "tool-b", "cd /repo/app; grep -rn checkout src", "running")],
	live: true,
};
const failed: SubagentRef = { launch: launch("c", "Audit: cart", "failed"), steps: [], live: true };

function panel(props: Partial<Parameters<typeof SubagentPanel>[0]> = {}) {
	const onOpen = vi.fn();
	const onBack = vi.fn();
	render(
		<SubagentPanel
			subagents={[finished, working, failed]}
			onOpen={onOpen}
			onBack={onBack}
			harness="claude-code"
			{...props}
		/>,
	);
	return { onOpen, onBack };
}

describe("SubagentPanel", () => {
	it("lists working children first, then finished ones, the newest at the top of each", () => {
		panel();

		const workingGroup = screen.getByRole("region", { name: "Working" });
		expect(within(workingGroup).getByRole("heading")).toHaveTextContent("Working · 1");
		expect(within(workingGroup).getByRole("button", { name: /Audit: testing gaps/ })).toBeInTheDocument();

		const finishedGroup = screen.getByRole("region", { name: "Finished" });
		expect(within(finishedGroup).getByRole("heading")).toHaveTextContent("Finished · 2");
		expect(within(finishedGroup).getAllByRole("button").map((row) => row.textContent)).toEqual([
			expect.stringContaining("Audit: cart"),
			expect.stringContaining("Audit: info pages"),
		]);
	});

	it("opens a child from the list", async () => {
		const user = userEvent.setup();
		const { onOpen } = panel();
		await user.click(screen.getByRole("button", { name: /Audit: info pages/ }));
		expect(onOpen).toHaveBeenCalledWith("a");
	});

	it("shows one child's whole conversation, read-only, under a header that leads back", async () => {
		const user = userEvent.setup();
		const { onBack } = panel({ openId: "a" });

		expect(screen.getByRole("heading", { name: "Audit: info pages" })).toBeInTheDocument();
		// The parent's instructions open the conversation, attributed to the parent.
		expect(screen.getByText("Sent by the parent agent")).toBeInTheDocument();
		expect(screen.getByTestId("subagent-prompt")).toHaveTextContent("Read-only. Audit: info pages.");
		// What it ran and said, in order, drawn as the main chat draws them.
		expect(screen.getByText("Ran command")).toBeInTheDocument();
		expect(screen.getByText("Looking at the FAQ next.")).toBeInTheDocument();
		// The hand-back is its report, not another step.
		expect(screen.getByText("Report")).toBeInTheDocument();
		expect(screen.getByText("The FAQ never mentions shipping.")).toBeInTheDocument();
		expect(screen.queryByText("SubagentHandback")).not.toBeInTheDocument();
		// Nothing to type into: the child answers to its parent.
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		expect(screen.getByText(/Read-only: a subagent works on its own/)).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "All subagents" }));
		expect(onBack).toHaveBeenCalled();
	});

	it("ends a working child's conversation with the live row naming the step it is on", () => {
		panel({ openId: "b" });
		// Named the way a turn's live row names the agent's step, with its timer.
		expect(screen.getByTestId("live-working-task")).toHaveTextContent(liveTaskLabel(working.steps[0]!));
		expect(screen.getByTestId("live-working-timer")).toBeInTheDocument();
		// At the live edge, after the work.
		const live = screen.getByTestId("live-working-label");
		expect(screen.getByTestId("subagent-prompt").compareDocumentPosition(live) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.getByLabelText("Working")).toBeInTheDocument();
		expect(screen.queryByText("Report")).not.toBeInTheDocument();
	});

	it("folds a long prompt until the reader asks for all of it", async () => {
		const user = userEvent.setup();
		const long: SubagentRef = {
			...working,
			launch: launch("long", "Audit: everything", "completed", `Check every page.\n${"- one more page to check\n".repeat(12)}`),
		};
		panel({ subagents: [long], openId: "long" });

		const toggle = screen.getByRole("button", { name: "Show full message" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByTestId("subagent-prompt")).toHaveClass("max-h-36");
		await user.click(toggle);
		expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByTestId("subagent-prompt")).not.toHaveClass("max-h-36");
	});

	it("keeps a long path inside the prompt bubble and paints no scroll track", () => {
		const longPath: SubagentRef = {
			...finished,
			launch: launch("path", "Audit: paths", "completed", "Read-only. Repo: /Users/someone/.ao/dev/data/worktrees/market-place/orchestrator/market-place-orchestrator."),
		};
		panel({ subagents: [longPath], openId: "path" });

		// The bubble sizes to its content, so only `overflow-wrap: anywhere` lets the path break inside it.
		expect(screen.getByTestId("subagent-prompt").parentElement).toHaveClass("wrap-anywhere");
		const body = screen.getByTestId("subagent-panel").querySelector(".cursor-chat-timeline");
		expect(body).toHaveClass("scrollbar-none", "overflow-x-hidden", "overflow-y-auto");
	});

	it("states a refused launch in words", () => {
		panel({ openId: "c" });
		expect(screen.getByText("Failed")).toBeInTheDocument();
		expect(screen.getByText("Concurrent subagent limit reached. You can run 20 subagents at once.")).toBeInTheDocument();
	});

	it("falls back to the list when the open child is gone", () => {
		panel({ openId: "rolled-back" });
		expect(screen.getByRole("region", { name: "Finished" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "All subagents" })).not.toBeInTheDocument();
	});

	it("says so when the chat has no subagents", () => {
		panel({ subagents: [] });
		expect(screen.getByText("This chat has no subagents yet.")).toBeInTheDocument();
	});
});
