import { render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}
import type {
	ConversationActivity,
	ConversationItem,
	ConversationSnapshot,
	ConversationTurn,
} from "../../types/conversation";

// Compaction is the difference between a session that works for an hour and one
// that works for a day: every turn re-sends the history, so context fills on its
// own until the conversation cannot accept another turn. These cover the two halves
// the user sees — that a compaction happened, and how to cause one.

beforeEach(() => localStorage.clear());

function compaction(overrides: Partial<ConversationActivity> = {}): ConversationActivity {
	return {
		kind: "activity",
		id: "cc-1",
		sequence: 2,
		revision: 0,
		activityKind: "system",
		status: "completed",
		summary: "Compacted history, freeing 11.0k tokens",
		detail: {
			event: "compaction",
			tokensBefore: 15650,
			tokensAfter: 4632,
			tokensReclaimed: 11018,
			contextWindow: 258400,
		},
		createdAt: "2026-08-02T10:00:00Z",
		...overrides,
	};
}

function snapshot(
	items: ConversationItem[],
	turns: ConversationTurn[] = [],
	extra: Partial<ConversationSnapshot> = {},
): ConversationSnapshot {
	return {
		conversationId: "c1",
		sessionId: "p1-1",
		harness: "codex",
		mode: "chat",
		controller: { state: "ready" },
		turns,
		items,
		latestSequence: items.length,
		oldestSequence: items[0]?.sequence ?? 1,
		hasMoreBefore: false,
		settings: {},
		...extra,
	};
}

const assistantSaid: ConversationItem = {
	kind: "message",
	id: "m1",
	sequence: 1,
	revision: 0,
	role: "assistant",
	origin: "provider",
	text: "Earlier work.",
	streaming: false,
	createdAt: "2026-08-02T09:00:00Z",
};

describe("compaction in the timeline", () => {
	it("shows the summary mark, what happened and what it freed, with no rule", () => {
		const { container } = render(<ChatWorkspace snapshot={snapshot([assistantSaid, compaction()])} />);

		const row = screen.getByTestId("compaction-row");
		expect(within(row).getByText("Compacted conversation")).toBeInTheDocument();
		expect(within(row).getByText("−11.0k · 2% full")).toHaveClass("text-muted-foreground/70");
		expect(row.querySelector(".icon-scroll-text")).not.toBeNull();
		expect(screen.queryByText("The conversation history was compacted")).not.toBeInTheDocument();
		expect(container.querySelector(".h-px.w-full.bg-border")).toBeNull();
	});

	// A compaction right after a daemon restart genuinely does not know what it
	// saved, because AO has seen no token report yet. Showing "0 freed" would be a
	// lie rather than a gap.
	it("claims no figures when the provider never reported any", () => {
		render(
			<ChatWorkspace
				snapshot={snapshot([
					assistantSaid,
					compaction({
						summary: "Compacted the conversation history",
						detail: { event: "compaction" },
					}),
				])}
			/>,
		);

		expect(screen.getByTestId("compaction-row")).toHaveTextContent(/^Compacted conversation$/);
		expect(screen.queryByText(/0 tokens/)).not.toBeInTheDocument();
		expect(screen.queryByText(/% full/)).not.toBeInTheDocument();
	});

	it("says why a compaction could not run, in the row's own muted tone", () => {
		render(
			<ChatWorkspace
				snapshot={snapshot([
					assistantSaid,
					compaction({
						status: "failed",
						summary: "Could not compact the conversation history: Conversation too long",
						detail: { event: "compaction", error: "Conversation too long" },
					}),
				])}
			/>,
		);

		const row = screen.getByTestId("compaction-row");
		expect(within(row).getByText("Couldn't compact conversation")).toBeInTheDocument();
		expect(within(row).getByText("Conversation too long")).toBeInTheDocument();
		expect(row.querySelector(".text-destructive")).toBeNull();
		expect(row).not.toHaveClass("text-destructive");
	});

	it("says a stopped compaction stopped", () => {
		render(<ChatWorkspace snapshot={snapshot([assistantSaid, compaction({ status: "cancelled", detail: { event: "compaction" } })])} />);

		expect(screen.getByTestId("compaction-row")).toHaveTextContent(/^Couldn't compact conversationStopped$/);
	});
});

describe("a compaction under way", () => {
	const turnId = "compacting-turn";
	const at = "2026-08-02T10:00:00Z";
	const prompt: ConversationItem = {
		kind: "message", id: "prompt", turnId, sequence: 1, revision: 0,
		role: "user", origin: "human", text: "Read the remaining API handlers",
		streaming: false, createdAt: at,
	};
	const said: ConversationItem = { ...assistantSaid, id: "said", turnId, sequence: 2, text: "Next I'll read the orders route." };
	const liveTurn = (items: ConversationItem[], state: ConversationTurn["state"] = "running"): ConversationSnapshot =>
		snapshot(items, [{ id: turnId, state, requestedAt: at, startedAt: at }], { controller: { state: state === "running" ? "busy" : "ready" } });

	it("takes over the live row: the loader in front, the label and its own time, with no second row", () => {
		const running = compaction({
			id: "cc-live", turnId, sequence: 3, status: "running", summary: "Compacting the conversation history",
			detail: { event: "compaction", tokensBefore: 180000, contextWindow: 200000 },
			createdAt: new Date(Date.now() - 65_000).toISOString(),
		});
		const { container } = render(<ChatWorkspace snapshot={liveTurn([prompt, said, running])} />);

		expect(screen.getByTestId("live-working-task")).toHaveTextContent(/^Compacting conversation$/);
		// "Compacting conversation 1m": the compaction's own time, no dot between.
		expect(screen.getByTestId("live-working-label")).toHaveTextContent(/^Compacting conversation1m/);
		// The loader stays in front while it compacts; the summary mark is the settled row's.
		const liveRow = screen.getByTestId("live-working-label").parentElement;
		expect(liveRow?.querySelector(".icon-scroll-text")).toBeNull();
		expect(liveRow?.querySelector("[data-testid='response-spinner']")).not.toBeNull();
		expect(screen.queryByTestId("compaction-row")).not.toBeInTheDocument();
		expect(screen.getAllByText("Compacting conversation")).toHaveLength(1);
		expect(container.textContent).not.toMatch(/Compacting\.\.\./);
	});

	// The live row says the provider is retrying first, so the compaction keeps
	// its own row meanwhile rather than vanishing from both.
	it("keeps its own running row while the provider retries", () => {
		const running = compaction({
			id: "cc-live", turnId, sequence: 3, status: "running", summary: "Compacting the conversation history",
			detail: { event: "compaction", tokensBefore: 180000, contextWindow: 200000 },
		});
		const retrying: ConversationItem = {
			kind: "activity", id: "provider-retry", turnId, sequence: 4, revision: 0,
			activityKind: "system", status: "running", summary: "Reconnecting to the provider",
			detail: { event: "provider.failure", text: "stream disconnected" }, createdAt: at,
		};
		render(<ChatWorkspace snapshot={liveTurn([prompt, said, running, retrying])} />);

		expect(screen.getByTestId("compaction-row")).toHaveTextContent(/^Compacting conversation$/);
		expect(screen.getByTestId("live-working-task")).not.toHaveTextContent(/Compacting/);
	});

	// A tool call can still be running when the agent starts compacting. The live
	// row names the compaction, so the step keeps its own row with its command.
	it("leaves a step that is still running in its own row", () => {
		const reading: ConversationItem = {
			kind: "activity", id: "read", turnId, sequence: 3, revision: 0,
			activityKind: "command", status: "running", summary: "sed -n '1,40p' orders.ts",
			detail: { command: "sed -n '1,40p' orders.ts" }, createdAt: at,
		};
		const running = compaction({
			id: "cc-live", turnId, sequence: 4, status: "running", summary: "Compacting the conversation history",
			detail: { event: "compaction" },
		});
		const { container } = render(<ChatWorkspace snapshot={liveTurn([prompt, said, reading, running])} />);

		expect(screen.getByTestId("live-working-task")).toHaveTextContent(/^Compacting conversation$/);
		expect(container.querySelector("[data-chat-activity-id='read']")).not.toBeNull();
	});

	it("settles in place, and the live row goes back to the turn's work", () => {
		const settled = compaction({ id: "cc-live", turnId, sequence: 3 });
		render(<ChatWorkspace snapshot={liveTurn([prompt, said, settled])} />);

		expect(screen.getByTestId("compaction-row")).toHaveTextContent("Compacted conversation");
		expect(screen.getByTestId("live-working-task")).toHaveTextContent(/^Thinking$/);
	});

	// Where the agent's earlier history was summarized explains why it may have
	// lost details, so the row stays out of the turn's folded steps.
	it("stays visible after the turn folds its steps", () => {
		const read: ConversationItem = {
			kind: "activity", id: "read", turnId, sequence: 3, revision: 0,
			activityKind: "command", status: "completed", summary: "sed -n '1,40p' orders.ts",
			detail: { command: "sed -n '1,40p' orders.ts" }, createdAt: at,
		};
		const settled = compaction({ id: "cc-live", turnId, sequence: 4 });
		const answer: ConversationItem = { ...assistantSaid, id: "answer", turnId, sequence: 5, text: "The orders route is done." };
		render(<ChatWorkspace snapshot={liveTurn([prompt, said, read, settled, answer], "completed")} />);

		expect(screen.queryByRole("button", { name: /^Read orders\.ts/ })).not.toBeInTheDocument();
		expect(screen.getByTestId("compaction-row")).toHaveTextContent("Compacted conversation");
	});

	it("tells a failed compaction once, on its row", () => {
		const failedTurn = "compact-only";
		const failed = compaction({
			id: "cc-failed", turnId: failedTurn, sequence: 2, status: "failed",
			detail: { event: "compaction", error: "Conversation too long" },
		});
		render(
			<ChatWorkspace
				snapshot={snapshot([assistantSaid, failed], [{
					id: failedTurn, state: "failed", requestedAt: at, startedAt: at, completedAt: at, errorMessage: "Conversation too long",
				}])}
			/>,
		);

		expect(within(screen.getByTestId("compaction-row")).getByText("Conversation too long")).toBeInTheDocument();
		expect(screen.getAllByText("Conversation too long")).toHaveLength(1);
	});

	it("still offers Retry when the failure is told on its row", async () => {
		const failedTurn = "compact-asked";
		const asked: ConversationItem = {
			kind: "message", id: "asked", turnId: failedTurn, sequence: 2, revision: 0,
			role: "user", origin: "human", text: "/compact", streaming: false, createdAt: at,
		};
		const failed = compaction({
			id: "cc-failed", turnId: failedTurn, sequence: 3, status: "failed",
			detail: { event: "compaction", error: "Conversation too long" },
		});
		const onRetry = vi.fn();
		render(
			<ChatWorkspace
				snapshot={snapshot([assistantSaid, asked, failed], [{
					id: failedTurn, providerTurnId: "provider-compact", state: "failed",
					requestedAt: at, startedAt: at, completedAt: at, errorMessage: "Conversation too long",
				}])}
				retryControl={{ retry: onRetry }}
			/>,
		);

		expect(screen.getAllByText("Conversation too long")).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { name: "Retry this turn" }));
		expect(onRetry).toHaveBeenCalledWith(failedTurn);
	});

	it("offers that Retry on the answer's row when the turn wrote one", async () => {
		const failedTurn = "worked-then-failed";
		const asked: ConversationItem = {
			kind: "message", id: "asked", turnId: failedTurn, sequence: 2, revision: 0,
			role: "user", origin: "human", text: "Read the remaining API handlers", streaming: false, createdAt: at,
		};
		const wrote: ConversationItem = { ...assistantSaid, id: "wrote", turnId: failedTurn, sequence: 3, text: "Next I'll read the orders route." };
		const failed = compaction({
			id: "cc-failed", turnId: failedTurn, sequence: 4, status: "failed",
			detail: { event: "compaction", error: "Conversation too long" },
		});
		const onRetry = vi.fn();
		render(
			<ChatWorkspace
				snapshot={snapshot([assistantSaid, asked, wrote, failed], [{
					id: failedTurn, providerTurnId: "provider-worked", state: "failed",
					requestedAt: at, startedAt: at, completedAt: at, errorMessage: "Conversation too long",
				}])}
				retryControl={{ retry: onRetry }}
			/>,
		);

		expect(screen.getAllByText("Conversation too long")).toHaveLength(1);
		const retry = screen.getAllByRole("button", { name: "Retry this turn" });
		expect(retry).toHaveLength(1);
		await userEvent.click(retry[0]!);
		expect(onRetry).toHaveBeenCalledWith(failedTurn);
	});
});

describe("the compact control", () => {
	// Compaction lives on `/compact` rather than a toolbar button: the composer
	// tools stay for attach/settings, and compact is an AO slash command.
	it("is offered in the slash menu, not the message tools", async () => {
		render(<ChatWorkspace snapshot={snapshot([assistantSaid])} onCompact={vi.fn()} />);

		const tools = screen.getByRole("group", { name: "Message tools" });
		expect(
			within(tools).queryByRole("button", { name: "Compact conversation history" }),
		).not.toBeInTheDocument();

		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "/");
		expect(screen.getByRole("option", { name: /compact/i })).toBeInTheDocument();
	});

	it("offers compact alongside provider skills in the slash menu", async () => {
		render(<ChatWorkspace snapshot={snapshot([assistantSaid])} onCompact={vi.fn()} />);

		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "/");
		expect(screen.getByRole("option", { name: /compact/i })).toBeInTheDocument();
	});

	// Measured against a live app-server: thread/compact/start mid-turn silently
	// interrupts the running turn, then compacts. The command refuses locally and
	// preserves the draft rather than letting the user discover the loss afterwards.
	it("refuses while a turn is in flight and keeps the command editable", async () => {
		const user = userEvent.setup();
		const onCompact = vi.fn();
		render(
			<ChatWorkspace
				snapshot={snapshot([assistantSaid], [
					{ id: "t1", state: "running", requestedAt: "2026-08-02T10:00:00Z" },
				])}
				onCompact={onCompact}
			/>,
		);

		const field = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(field, "/compact");
		await user.keyboard("{Enter}");

		expect(onCompact).not.toHaveBeenCalled();
		expect(field).toHaveTextContent("/compact");
		await waitFor(() =>
			expect(screen.getByRole("alert")).toHaveTextContent("Stop the current turn"),
		);
	});

	it("surfaces a provider refusal only when the command is invoked", async () => {
		const user = userEvent.setup();
		render(
			<ChatWorkspace
				snapshot={snapshot([assistantSaid])}
				onCompact={vi.fn()}
				compactUnavailable="This agent cannot compact its history"
			/>,
		);

		expect(screen.queryByText("This agent cannot compact its history")).not.toBeInTheDocument();
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "/compact");
		await user.keyboard("{Enter}");
		await waitFor(() =>
			expect(screen.getByRole("alert")).toHaveTextContent("This agent cannot compact its history"),
		);
	});

	it("does not start a second compaction while one is running", async () => {
		const user = userEvent.setup();
		const onCompact = vi.fn();
		render(
			<ChatWorkspace snapshot={snapshot([assistantSaid])} onCompact={onCompact} compacting />,
		);

		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "/compact");
		await user.keyboard("{Enter}");
		expect(onCompact).not.toHaveBeenCalled();
		await waitFor(() =>
			expect(screen.getByRole("alert")).toHaveTextContent("already being compacted"),
		);
	});
});
