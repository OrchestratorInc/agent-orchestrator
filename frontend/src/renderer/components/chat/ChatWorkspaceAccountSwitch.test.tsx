import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { agentAccountsQueryKey } from "../../hooks/useAgentAccounts";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

/** turn-2 was stopped by an account switch to acct-b. */
function switchedSnapshot(): ConversationSnapshot {
	const turns = chatFixture.turns.map((turn) =>
		turn.id === "turn-2"
			? { ...turn, state: "interrupted" as const, completedAt: turn.requestedAt }
			: turn.state === "running"
				? { ...turn, state: "completed" as const, completedAt: turn.requestedAt }
				: turn,
	);
	return {
		...chatFixture,
		controller: { state: "ready" },
		turns,
		continueTurnId: "turn-2",
		items: [
			...chatFixture.items,
			{
				kind: "activity",
				id: "switch-1",
				turnId: "turn-2",
				sequence: chatFixture.latestSequence + 1,
				revision: 0,
				activityKind: "system",
				status: "completed",
				summary: "Switched account",
				detail: { event: "account.switched", accountId: "acct-b" },
				createdAt: chatFixture.items[0]!.createdAt,
			},
		],
		latestSequence: chatFixture.latestSequence + 1,
	};
}

/** turn-1 was refused for a usage limit; AO moved the session to acct-b. */
function handoffSnapshot(retried: boolean): ConversationSnapshot {
	const at = chatFixture.items[0]!.createdAt;
	const source = chatFixture.turns.find((turn) => turn.id === "turn-1")!;
	return {
		...chatFixture,
		controller: { state: "ready" },
		turns: [
			{
				...source,
				state: "failed",
				completedAt: source.requestedAt,
				errorMessage: "You've hit your session limit",
				hasHumanPrompt: true,
				usageLimit: { resetsAt: new Date(Date.now() + 60 * 60_000).toISOString(), autoResume: false },
			},
			...(retried ? [{ id: "retry-1", state: "completed" as const, requestedAt: at, completedAt: at, retryOfTurnId: "turn-1" }] : []),
		],
		items: [
			...chatFixture.items.filter((item) => item.turnId === "turn-1"),
			{
				kind: "activity",
				id: "handoff-1",
				turnId: "turn-1",
				sequence: chatFixture.latestSequence + 1,
				revision: 0,
				activityKind: "system",
				status: "completed",
				summary: "Moved account",
				detail: { event: "account.limit_handoff", accountId: "acct-b" },
				createdAt: at,
			},
		],
		latestSequence: chatFixture.latestSequence + 1,
	};
}

function renderWithAccounts(snapshot: ConversationSnapshot) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
	queryClient.setQueryData(agentAccountsQueryKey, [
		{ id: "acct-b", provider: "claude", device: false, default: false, signedIn: true, email: "b@example.com" },
	]);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={snapshot} onSend={vi.fn()} retryControl={{ retry: vi.fn(), setAutoResume: vi.fn() }} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("ChatWorkspace usage-limit account handoff", () => {
	it("names the account a usage limit moved the session to", () => {
		renderWithAccounts(handoffSnapshot(false));
		expect(screen.getByText("Usage limit reached. Moved to b@example.com")).toBeInTheDocument();
		expect(screen.getByText(/retries the refused prompt once on this account/)).toBeInTheDocument();
		// The limit stays in the thread's history.
		expect(screen.getAllByTestId("usage-limit-notice").length).toBeGreaterThan(0);
	});

	it("offers no usage-limit recovery once AO has retried the prompt", () => {
		renderWithAccounts(handoffSnapshot(true));
		expect(screen.getByText("Usage limit reached. Moved to b@example.com")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Auto-resume at reset" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Resume" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});
});

describe("ChatWorkspace account switch", () => {
	it("shows a turn stopped by an account switch as the switch, not as a stop or a failure", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
		queryClient.setQueryData(agentAccountsQueryKey, [
			{ id: "acct-b", provider: "claude", device: false, default: false, signedIn: true, email: "b@example.com" },
		]);
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace snapshot={switchedSnapshot()} onSend={vi.fn()} onContinueTurn={vi.fn()} />
				</TooltipProvider>
			</QueryClientProvider>,
		);

		expect(screen.getByText("Switched to b@example.com")).toBeInTheDocument();
		expect(screen.getByText(/Press Continue to resume this turn/)).toBeInTheDocument();
		expect(screen.queryByText("The agent was interrupted by you")).not.toBeInTheDocument();
		expect(screen.queryByText("The agent ran into a problem")).not.toBeInTheDocument();
		// The stopped turn can still be continued from the composer.
		expect(screen.getByRole("button", { name: "Continue turn" })).toBeInTheDocument();
	});

	// The switch marker stands in for the stop outcome, so the outcome's action row is
	// never drawn: the answer has to keep its own copy and roll back.
	it("keeps copy and roll back on the answer of a turn stopped by an account switch", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
		const switched = switchedSnapshot();
		// The stop settled the answer the switch interrupted.
		const snapshot: ConversationSnapshot = {
			...switched,
			// Without the fixture's steer, the answer is the turn's last reply rather
			// than the end of an earlier, already-settled segment.
			items: switched.items
				.filter((item) => item.id !== "a-steer-1")
				.map((item) => (item.kind === "message" && item.id === "m-5" ? { ...item, streaming: false } : item)),
		};
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace snapshot={snapshot} onSend={vi.fn()} onContinueTurn={vi.fn()} onRollback={vi.fn()} />
				</TooltipProvider>
			</QueryClientProvider>,
		);

		const answer = screen.getByText(/Tests are still running/).closest<HTMLElement>("[data-chat-message-id]")!;
		expect(within(answer).getByRole("button", { name: "Copy message as markdown" })).toBeInTheDocument();
		expect(within(answer).getByRole("button", { name: "Roll back to here" })).toBeInTheDocument();
	});
});
