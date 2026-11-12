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

		// Each group is a Summary-style section: a header that folds it, then one line a child.
		const workingGroup = screen.getByRole("group", { name: "Working" });
		expect(within(workingGroup).getByRole("button", { name: "Working · 1" })).toHaveAttribute("aria-expanded", "true");
		expect(within(workingGroup).getByRole("button", { name: /Audit: testing gaps/ })).toBeInTheDocument();

		const finishedGroup = screen.getByRole("group", { name: "Finished" });
		expect(within(finishedGroup).getByRole("button", { name: "Finished · 2" })).toBeInTheDocument();
		expect(within(finishedGroup).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
			expect.stringContaining("Audit: cart"),
			expect.stringContaining("Audit: info pages"),
		]);
	});

	it("lists the newest ten of a section and folds the rest behind a counted control", async () => {
		const user = userEvent.setup();
		const many: SubagentRef[] = Array.from({ length: 12 }, (_, index) => ({
			launch: launch(`m${index}`, `Audit: page ${index}`),
			steps: [handback(`m${index}h`, `tool-m${index}`, "Done.")],
			live: false,
		}));
		panel({ subagents: many });

		const finishedGroup = screen.getByRole("group", { name: "Finished" });
		expect(within(finishedGroup).getByRole("button", { name: "Finished · 12" })).toBeInTheDocument();
		// Newest first: the two oldest wait behind the fold.
		expect(within(finishedGroup).getByRole("button", { name: /Audit: page 11/ })).toBeInTheDocument();
		expect(within(finishedGroup).queryByRole("button", { name: /Audit: page 1(?!\d)/ })).not.toBeInTheDocument();
		expect(within(finishedGroup).queryByRole("button", { name: /Audit: page 0/ })).not.toBeInTheDocument();

		// The last two shown rows fade toward the control, which says how many wait.
		const items = within(finishedGroup).getAllByRole("listitem");
		expect(items[8]).toHaveClass("opacity-60");
		expect(items[9]).toHaveClass("opacity-25");
		const more = within(finishedGroup).getByRole("button", { name: "Show 2 more agents" });
		expect(more).toHaveAttribute("aria-expanded", "false");
		await user.click(more);
		expect(within(finishedGroup).getByRole("button", { name: /Audit: page 0/ })).toBeInTheDocument();
		expect(within(finishedGroup).getByRole("button", { name: "Show fewer agents" })).toHaveAttribute("aria-expanded", "true");
		// Open, nothing fades.
		for (const item of within(finishedGroup).getAllByRole("listitem")) expect(item).not.toHaveClass("opacity-25", "opacity-60");
	});

	it("opens a child from the list", async () => {
		const user = userEvent.setup();
		const { onOpen } = panel();
		await user.click(screen.getByRole("button", { name: /Audit: info pages/ }));
		expect(onOpen).toHaveBeenCalledWith("a");
	});

	it("shows one child's whole conversation, read-only, under a header that leads back", async () => {
		const user = userEvent.setup();
		// The catalog's default is not named: the composer reads "Choose a model".
		const { onBack } = panel({ openId: "a", models: [{ id: "opus", displayName: "Opus", default: true }] });

		expect(screen.getByRole("heading", { name: "Audit: info pages" })).toBeInTheDocument();
		// The parent's instructions open the conversation, attributed to the parent.
		expect(screen.getByText("Sent by the parent agent")).toBeInTheDocument();
		expect(screen.getByTestId("subagent-prompt")).toHaveTextContent("Read-only. Audit: info pages.");
		// What it ran and said, in order, drawn as the main chat draws them.
		expect(screen.getByText("Checked repository")).toBeInTheDocument();
		expect(screen.getByText("Looking at the FAQ next.")).toBeInTheDocument();
		// The hand-back is its report, not another step.
		expect(screen.getByText("Report")).toBeInTheDocument();
		expect(screen.getByText("The FAQ never mentions shipping.")).toBeInTheDocument();
		expect(screen.queryByText("SubagentHandback")).not.toBeInTheDocument();
		// Nothing to type into: the child answers to its parent. The composer says so in
		// place of its placeholder, over the side chat's tools, every one switched off.
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		const composer = within(screen.getByTestId("subagent-read-only-composer"));
		expect(composer.getByText(/Read-only: a subagent works on its own/)).toBeInTheDocument();
		expect(composer.getByRole("button", { name: "Attach a file" })).toBeDisabled();
		expect(composer.getByRole("button", { name: "Send message" })).toBeDisabled();
		expect(composer.getByText("Choose a model")).toBeInTheDocument();
		expect(composer.queryByText("Opus")).not.toBeInTheDocument();
		for (const button of composer.getAllByRole("button")) expect(button).toBeDisabled();

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

	it("opens another child fresh, not with the last one's prompt unfolded", async () => {
		const user = userEvent.setup();
		const longPrompt = (id: string, title: string): SubagentRef => ({
			...working,
			launch: launch(id, title, "completed", `Check every page.\n${"- one more page to check\n".repeat(12)}`),
		});
		const first = longPrompt("first", "Audit: everything");
		const second = longPrompt("second", "Audit: everything else");
		const props = { subagents: [first, second], onOpen: vi.fn(), onBack: vi.fn(), harness: "claude-code" };
		const { rerender } = render(<SubagentPanel {...props} openId="first" />);
		await user.click(screen.getByRole("button", { name: "Show full message" }));
		expect(screen.getByRole("button", { name: "Show less" })).toBeInTheDocument();

		rerender(<SubagentPanel {...props} openId="second" />);
		expect(screen.getByRole("heading", { name: "Audit: everything else" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show full message" })).toHaveAttribute("aria-expanded", "false");
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
		expect(screen.getByRole("group", { name: "Finished" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "All subagents" })).not.toBeInTheDocument();
	});

	it("says so when the chat has no subagents", () => {
		panel({ subagents: [] });
		expect(screen.getByText("This chat has no subagents yet.")).toBeInTheDocument();
	});
});
