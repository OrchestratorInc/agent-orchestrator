/**
 * Messages queued while a turn ran are drained together into the next turn's
 * input. They were that turn's prompt, so its reply must read below them, not
 * above them where their own later sequence numbers would otherwise put them.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import type { BatchedDelivery, ConversationItem, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

const at = (second: number) => new Date(Date.UTC(2026, 10, 10, 10, 20, second)).toISOString();

function message(id: string, turnId: string, sequence: number, role: "user" | "assistant", origin: ConversationMessage["origin"], text: string): ConversationMessage {
	return { kind: "message", id, turnId, sequence, revision: 0, role, origin, streaming: false, createdAt: at(sequence), text };
}

function renderChat(items: ConversationItem[], batchedDeliveries: BatchedDelivery[]) {
	const snapshot: ConversationSnapshot = {
		...chatFixtureEmpty,
		latestSequence: Math.max(...items.map((item) => item.sequence)),
		// The daemon leaves batched queued turns out of `turns`; only the turn that ran is listed.
		turns: [{ id: "run", state: "completed", providerTurnId: "provider-run", requestedAt: at(1), startedAt: at(8), completedAt: at(30) }],
		items,
		batchedDeliveries,
	};
	render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider>
				<ChatWorkspace snapshot={snapshot} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

const follows = (earlier: HTMLElement, later: HTMLElement) =>
	Boolean(earlier.compareDocumentPosition(later) & Node.DOCUMENT_POSITION_FOLLOWING);

describe("messages drained into a turn's input", () => {
	const items: ConversationItem[] = [
		message("prompt", "run", 1, "user", "human", "The orchestrator banner shows up here"),
		message("flicker", "queued-1", 3, "user", "human", "The screen flickers at 0:32"),
		message("relay", "queued-2", 5, "user", "automation", "[from proj-46] Gallery is DONE"),
		message("soundtrack", "queued-3", 6, "user", "human", "Make the soundtrack less festive"),
		message("answer", "run", 10, "assistant", "provider", "A new cut is rendering with all of that addressed."),
	];

	it("draws them above the reply that answered them", () => {
		renderChat(items, [
			{ turnId: "queued-1", deliveredWithTurnId: "run" },
			{ turnId: "queued-2", deliveredWithTurnId: "run" },
			{ turnId: "queued-3", deliveredWithTurnId: "run" },
		]);

		const answer = screen.getByText("A new cut is rendering with all of that addressed.");
		for (const text of ["The screen flickers at 0:32", "Gallery is DONE", "Make the soundtrack less festive"]) {
			expect(follows(screen.getByText(text, { exact: false }), answer), `${text} should read above the reply`).toBe(true);
		}
		expect(follows(screen.getByText("The orchestrator banner shows up here"), screen.getByText("The screen flickers at 0:32"))).toBe(true);
	});

	it("leaves a message a steer carried where it arrived", () => {
		renderChat(items, [{ turnId: "queued-3", deliveredWithTurnId: "run", steerActivityId: "steer-1" }]);

		const answer = screen.getByText("A new cut is rendering with all of that addressed.");
		expect(follows(answer, screen.getByText("Make the soundtrack less festive"))).toBe(true);
	});
});
