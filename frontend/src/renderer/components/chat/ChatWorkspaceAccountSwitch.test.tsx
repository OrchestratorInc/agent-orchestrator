import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
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
});
