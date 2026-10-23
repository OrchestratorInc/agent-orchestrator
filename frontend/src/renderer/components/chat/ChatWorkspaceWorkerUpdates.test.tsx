/**
 * The orchestrator chat draws worker traffic as rows. These pin what a reader
 * keeps when it does: a quiet reply still copies and rolls back, a reply that asks
 * something stays a full reply, and a turn with no reply still shows its work.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import type { ConversationActivity, ConversationItem, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

const at = (minute: number) => `2026-10-22T03:${String(minute).padStart(2, "0")}:00Z`;

function report(turnId: string, minute: number, entry: string): ConversationMessage {
	return {
		kind: "message", id: `${turnId}-report`, turnId, sequence: minute * 10, revision: 0, role: "user", origin: "automation",
		streaming: false, createdAt: at(minute), text: `Reports since your previous turn:\n\n${entry}`,
	};
}

function command(turnId: string, minute: number, text: string): ConversationActivity {
	return {
		kind: "activity", id: `${turnId}-command`, turnId, sequence: minute * 10 + 1, revision: 0, activityKind: "command",
		status: "completed", summary: text, detail: { command: text, output: "ok" }, createdAt: at(minute),
	};
}

function reply(turnId: string, minute: number, text: string): ConversationMessage {
	return {
		kind: "message", id: `${turnId}-reply`, turnId, sequence: minute * 10 + 2, revision: 0, role: "assistant", origin: "provider",
		streaming: false, createdAt: at(minute), text,
	};
}

function snapshot(items: ConversationItem[]): ConversationSnapshot {
	const turnIds = [...new Set(items.map((item) => item.turnId as string))];
	return {
		...chatFixtureEmpty,
		latestSequence: Math.max(...items.map((item) => item.sequence)),
		turns: turnIds.map((id) => ({
			id, state: "completed" as const, providerTurnId: `provider-${id}`,
			requestedAt: items.find((item) => item.turnId === id)!.createdAt,
			completedAt: items.filter((item) => item.turnId === id).at(-1)!.createdAt,
		})),
		items,
	};
}

function renderOrchestrator(items: ConversationItem[], onRollback = vi.fn()) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, []);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={snapshot(items)} sessionRole="orchestrator" onRollback={onRollback} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onRollback;
}

describe("orchestrator worker updates", () => {
	it("draws a report as a row and an answer that asks nothing as one line that keeps copy and rollback", async () => {
		const onRollback = renderOrchestrator([
			report("t1", 1, "[done] [Check regressions](ao://sessions/proj/proj-288)\nOne strong candidate: #7043\nScreened 58 PRs."),
			command("t1", 1, "gh pr merge 7054"),
			reply("t1", 2, "Merged [#7054](https://github.com/o/r/pull/7054)."),
		]);

		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("Check regressions");
		expect(row).toHaveTextContent("One strong candidate: #7043");
		expect(screen.queryByText(/Reports since your previous turn/)).not.toBeInTheDocument();

		const answer = screen.getByTestId("orchestrator-reply");
		expect(answer).toHaveTextContent("Merged #7054.");
		expect(screen.queryByRole("button", { name: /^Worked for/ })).not.toBeInTheDocument();
		expect(within(answer).getByRole("button", { name: "Copy message as markdown" })).toBeInTheDocument();

		const work = within(answer).getByRole("button", { name: "Show the work behind this reply" });
		await userEvent.click(work);
		expect(work).toHaveAttribute("aria-expanded", "true");

		await userEvent.click(within(answer).getByRole("button", { name: "Roll back to here" }));
		await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Roll back" }));
		expect(onRollback).toHaveBeenCalledWith("t1");
	});

	it("keeps an answer that asks the user something as the full reply", () => {
		renderOrchestrator([
			report("t1", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?"),
			command("t1", 1, "ao session logs proj-286"),
			reply("t1", 2, "Checked its logs.\n\nShould it load over the tunnel?"),
		]);

		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
		expect(screen.getByText("Checked its logs.")).toBeInTheDocument();
		expect(screen.getByText("Should it load over the tunnel?")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Worked for/ })).toBeInTheDocument();
	});

	it("still shows the work of a turn that answered with tools only", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render above the browser"),
			command("t1", 1, "gh pr merge 7054"),
		]);

		expect(screen.getByTestId("worker-update")).toHaveTextContent("Menus");
		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Worked for/ })).toBeInTheDocument();
	});
});
