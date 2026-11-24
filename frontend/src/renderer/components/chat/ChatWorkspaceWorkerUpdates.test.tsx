/**
 * The orchestrator chat draws worker traffic as rows. These pin what a reader
 * keeps when it does: a quiet reply still copies and rolls back, a reply that asks
 * something stays a full reply, and a turn with no reply still shows its work.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { ChatLinkProvider, useChatLinkWorkspaces } from "./ChatMarkdown";
import { usePlainReferences } from "./WorkerUpdates";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import type { ConversationActivity, ConversationItem, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import type { WorkspaceSummary } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";

const at = (minute: number) => `2026-10-22T03:${String(minute).padStart(2, "0")}:00Z`;

function automation(turnId: string, minute: number, text: string): ConversationMessage {
	return {
		kind: "message", id: `${turnId}-report`, turnId, sequence: minute * 10, revision: 0, role: "user", origin: "automation",
		streaming: false, createdAt: at(minute), text,
	};
}

const report = (turnId: string, minute: number, entry: string) =>
	automation(turnId, minute, `Reports since your previous turn:\n\n${entry}`);

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

function snapshot(items: ConversationItem[], turnState: "completed" | "running" = "completed"): ConversationSnapshot {
	const turnIds = [...new Set(items.map((item) => item.turnId).filter((id): id is string => Boolean(id)))];
	return {
		...chatFixtureEmpty,
		latestSequence: Math.max(...items.map((item) => item.sequence)),
		turns: turnIds.map((id) => ({
			id, state: turnState, providerTurnId: `provider-${id}`,
			requestedAt: items.find((item) => item.turnId === id)!.createdAt,
			completedAt: turnState === "completed" ? items.filter((item) => item.turnId === id).at(-1)!.createdAt : undefined,
		})),
		items,
	};
}

function renderOrchestrator(items: ConversationItem[], onRollback = vi.fn(), workspaces: WorkspaceSummary[] = [], sessionRole: "orchestrator" | "worker" = "orchestrator", turnState: "completed" | "running" = "completed") {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, workspaces);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={snapshot(items, turnState)} sessionRole={sessionRole} onRollback={onRollback} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onRollback;
}

describe("orchestrator worker updates", () => {
	it("shows one collapsed spawn group and an idle worker when the daemon also recorded the spawn", async () => {
		const workspaces = [{
			id: "proj", name: "proj", path: "/tmp/proj",
			sessions: [{ id: "proj-288", workspaceId: "proj", title: "Audit usage", provider: "codex", kind: "worker", status: "idle", prs: [] }],
		}] as unknown as WorkspaceSummary[];
		const spawn = {
			...command("t1", 1, "ao spawn --project proj --name 'Audit usage'"),
			detail: { command: "ao spawn --project proj --name 'Audit usage'", output: 'spawned session proj-288 "Audit usage" (idle)' },
		};
		const action: ConversationActivity = {
			kind: "activity", id: "ao-action:session.spawned:proj-288", sequence: 13, revision: 0,
			activityKind: "ao_action", status: "completed", summary: "Spawned an agent", createdAt: at(1),
			detail: { action: "session.spawned", operationId: "proj-288", targetSessionId: "proj-288" },
		};
		renderOrchestrator([automation("t1", 1, "Check current usage."), spawn, reply("t1", 1, "Worker started."), action], vi.fn(), workspaces);

		// The agents a turn spawned fold with its work.
		await userEvent.click(screen.getByRole("button", { name: /^Worked for/ }));
		const group = screen.getByRole("button", { name: "Spawned 1 agent" });
		expect(group).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
		expect(screen.queryByText("Audit usage")).not.toBeInTheDocument();
		await userEvent.click(group);
		expect(group).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Audit usage")).toBeInTheDocument();
		const idle = screen.getByText("Idle");
		expect(idle.parentElement?.querySelector(".animate-spin")).toBeNull();
	});

	it("keeps a structured spawn action when no command group represents it", () => {
		const action: ConversationActivity = {
			kind: "activity", id: "action", sequence: 2, revision: 0, activityKind: "ao_action",
			status: "completed", summary: "Spawned an agent", createdAt: at(1),
			detail: { action: "session.spawned", operationId: "proj-288", targetSessionId: "proj-288" },
		};
		renderOrchestrator([automation("t1", 1, "Check current usage."), action]);
		expect(screen.getByText("Spawned an agent")).toBeInTheDocument();
	});

	it("keeps an action filed under a rolled-back turn on its own line", () => {
		const action: ConversationActivity = {
			kind: "activity", id: "ao-action:session.spawned:proj-9", turnId: "t2", sequence: 25, revision: 0, activityKind: "ao_action",
			status: "completed", summary: "Spawned an agent", createdAt: at(2),
			detail: { action: "session.spawned", operationId: "proj-9", targetSessionId: "proj-9" },
		};
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(workspaceQueryKey, []);
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace
						snapshot={{
							...chatFixtureEmpty, latestSequence: 25,
							items: [automation("t1", 1, "Check usage."), reply("t1", 1, "Checked."), action],
							turns: [
								{ id: "t1", state: "completed", providerTurnId: "provider-t1", requestedAt: at(1), completedAt: at(1) },
								{ id: "t2", state: "completed", providerTurnId: "provider-t2", requestedAt: at(2), completedAt: at(2), rolledBack: true },
							],
						}}
						sessionRole="orchestrator"
					/>
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(screen.getByText("Spawned an agent")).toBeInTheDocument();
	});

	it("hides the matching action as soon as the command completes during a running turn", () => {
		const spawn = {
			...command("t1", 1, "ao spawn --project proj"),
			detail: { command: "ao spawn --project proj", output: 'spawned session proj-288 "Audit" (idle)' },
		};
		const action: ConversationActivity = {
			kind: "activity", id: "ao-action:session.spawned:proj-288", sequence: 13, revision: 0,
			activityKind: "ao_action", status: "completed", summary: "Spawned an agent", createdAt: at(1),
			detail: { action: "session.spawned", operationId: "proj-288", targetSessionId: "proj-288" },
		};
		renderOrchestrator([automation("t1", 1, "Check usage."), spawn, action], vi.fn(), [], "orchestrator", "running");
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("keeps a distinct structured spawn next to the command spawn group", async () => {
		const spawn = {
			...command("t1", 1, "ao spawn --project proj"),
			detail: { command: "ao spawn --project proj", output: 'spawned session proj-288 "Audit" (idle)' },
		};
		const action: ConversationActivity = {
			kind: "activity", id: "ao-action:session.spawned:proj-289", sequence: 13, revision: 0,
			activityKind: "ao_action", status: "completed", summary: "Spawned an agent", createdAt: at(1),
			detail: { action: "session.spawned", operationId: "proj-289", targetSessionId: "proj-289" },
		};
		renderOrchestrator([automation("t1", 1, "Check usage."), spawn, reply("t1", 1, "Started."), action]);
		await userEvent.click(screen.getByRole("button", { name: /^Worked for/ }));
		expect(screen.getByRole("button", { name: "Spawned 1 agent" })).toBeInTheDocument();
		expect(screen.getByText("Spawned an agent")).toBeInTheDocument();
	});

	it.each([
		["provisioning", { provisionState: "provisioning" }, "Spawning", true],
		["active", { status: "working", activity: { state: "active" } }, "Working", true],
		["idle", { status: "idle", activity: { state: "idle" } }, "Idle", false],
		["exited", { status: "exited", activity: { state: "exited" } }, "Exited", false],
		["terminated", { status: "terminated", isTerminated: true }, "Terminated", false],
		["failed start", { provisionState: "failed" }, "Start failed", false],
		["status needs input", { status: "needs_input" }, "Needs input", false],
		["PR waiting input", { status: "needs_input", prs: [{ number: 94, state: "open", ci: "pending", review: "pending", mergeability: "unknown" }] }, "Needs input", false],
		["activity waiting input", { activity: { state: "waiting_input" } }, "Needs input", false],
		["activity blocked", { activity: { state: "blocked" } }, "Needs input", false],
		["checking", { statusReadiness: "checking" }, "Checking status", false],
		["unavailable", { statusReadiness: "unavailable" }, "Status unavailable", false],
		["missing", null, "Status unavailable", false],
	] as const)("shows %s child state without a false spinner", async (_case, child, label, spinning) => {
		const workspaces = [{
			id: "proj", name: "proj", path: "/tmp/proj",
			sessions: child ? [{ id: "proj-288", workspaceId: "proj", title: "Audit usage", provider: "codex", kind: "worker", status: "idle", prs: [], ...child }] : [],
		}] as unknown as WorkspaceSummary[];
		const spawn = {
			...command("t1", 1, "ao spawn --project proj"),
			detail: { command: "ao spawn --project proj", output: 'spawned session proj-288 "Audit" (idle)' },
		};
		renderOrchestrator([automation("t1", 1, "Check usage."), spawn, reply("t1", 1, "Started.")], vi.fn(), workspaces);
		await userEvent.click(screen.getByRole("button", { name: /^Worked for/ }));
		await userEvent.click(screen.getByRole("button", { name: "Spawned 1 agent" }));
		const line = screen.getByText(label).parentElement;
		expect(line?.querySelector(".animate-spin") !== null).toBe(spinning);
	});

	it("draws a report batch as one line that opens to its rows, and the orchestrator's answer as a normal reply", async () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Check regressions](ao://sessions/proj/proj-288)\nOne strong candidate: #7043\nScreened 58 PRs."),
			command("t1", 1, "gh pr merge 7054"),
			reply("t1", 2, "Merged [#7054](https://github.com/o/r/pull/7054). Close #7043 too?"),
		]);

		const line = screen.getByRole("button", { name: "1 Agent Report" });
		expect(line).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByTestId("worker-update")).not.toBeInTheDocument();
		await userEvent.click(line);
		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("Check regressions");
		expect(row).toHaveTextContent("One strong candidate: #7043");
		expect(screen.queryByText(/Reports since your previous turn/)).not.toBeInTheDocument();

		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Worked for/ })).toBeInTheDocument();
		expect(screen.getByText("#7054").closest("[data-chat-message-role]")).toHaveAttribute("data-chat-message-role", "assistant");
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

	it("draws an older worker message that names its sender only in its prefix as a row titled by the session", () => {
		const workspaces = [{
			id: "proj", name: "proj", path: "/tmp/proj",
			sessions: [{ id: "proj-187", workspaceId: "proj", title: "PR 5051 rebase", provider: "codex", kind: "worker", status: "idle", prs: [] }],
		}] as unknown as WorkspaceSummary[];
		renderOrchestrator([
			automation("t1", 1, "[from proj-187] Rebased #5051; disk full during validation, retrying."),
			reply("t1", 2, "Should I free some disk?"),
		], vi.fn(), workspaces);

		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("PR 5051 rebase");
		expect(row).toHaveTextContent("Rebased #5051");
		expect(row).not.toHaveTextContent("[from");
		expect(within(row).getByRole("link", { name: "PR 5051 rebase" })).toHaveAttribute("href", "ao://sessions/proj/proj-187");
	});

	it("folds AO's task-naming prompt into a row that names the task it produced", async () => {
		renderOrchestrator([
			automation("t1", 1, "AO TASK TITLE UPDATE\nChoose a concise task title from the brief and run:\nao session rename proj-150 \"<title>\""),
			command("t1", 1, 'ao session rename proj-150 "Chat Link Browser"'),
			reply("t1", 2, "Renamed the worker task to **Chat Link Browser**."),
		]);

		await userEvent.click(screen.getByRole("button", { name: /^1 background update/ }));
		expect(screen.getByTestId("background-turn")).toHaveTextContent("New task named Chat Link Browser");
		expect(screen.queryByText(/AO TASK TITLE UPDATE/)).not.toBeInTheDocument();
	});

	it("draws only the reports sent with a human message that still need the user", async () => {
		const envelope = [
			"Reports since your previous turn:", "",
			"[needs_input] [iOS pages](ao://sessions/proj/proj-286)", "Tunnel or loopback?", "",
			"[done] [Menus](ao://sessions/proj/proj-285)", "Menus render.",
		].join("\n");
		const human: ConversationMessage = {
			kind: "message", id: "t1-human", turnId: "t1", sequence: 10, revision: 0, role: "user", origin: "human",
			streaming: false, createdAt: at(1), text: `How is it going?\n\n<ao-worker-reports>\n${envelope}\n</ao-worker-reports>`,
		};
		renderOrchestrator([human, reply("t1", 2, "Both moved.")]);

		expect(screen.getByText("How is it going?")).toBeInTheDocument();
		const line = screen.getByTestId("worker-reports");
		expect(line).toHaveTextContent("1 needs you");
		await userEvent.click(within(line).getByRole("button"));
		expect(screen.getAllByTestId("worker-update")).toHaveLength(1);
		expect(screen.getByText("iOS pages")).toBeInTheDocument();
		expect(screen.queryByText(/Menus/)).not.toBeInTheDocument();
	});

	it("keeps reports a worker's steer carried under that steer after the turn settles", async () => {
		const prompt: ConversationMessage = {
			kind: "message", id: "run-human", turnId: "run", sequence: 10, revision: 0, role: "user", origin: "human",
			streaming: false, createdAt: at(1), text: "How are the workers doing?",
		};
		const steer: ConversationActivity = {
			kind: "activity", id: "steer-1", turnId: "run", sequence: 12, revision: 0, activityKind: "system", status: "completed",
			summary: "Steer", createdAt: at(1), detail: { event: "steer", senderSessionId: "proj-7", senderDisplayName: "Menus", text: "Rebased." },
		};
		const carried = report("t-report", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?");
		// Work before the steer, so the steer arrives mid-turn rather than with the prompt.
		const items: ConversationItem[] = [prompt, { ...command("run", 1, "ao status"), sequence: 11 }, steer, { ...carried, sequence: 13 }, reply("run", 2, "Both moved.")];
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(workspaceQueryKey, []);
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace
						snapshot={{
							...chatFixtureEmpty, latestSequence: 22, items,
							// The batched report turn is not among the snapshot's turns, as the daemon sends it.
							turns: [{ id: "run", state: "completed", providerTurnId: "provider-run", requestedAt: at(1), completedAt: at(2) }],
							batchedDeliveries: [{ turnId: "t-report", deliveredWithTurnId: "run", steerActivityId: "steer-1" }],
						}}
						sessionRole="orchestrator"
					/>
				</TooltipProvider>
			</QueryClientProvider>,
		);

		await userEvent.click(screen.getByRole("button", { name: /^1 Agent Report/ }));
		expect(screen.getByText("iOS pages")).toBeInTheDocument();
	});

	it("draws a report the user's steer carried while that worker still needs them", () => {
		const prompt: ConversationMessage = {
			kind: "message", id: "run-human", turnId: "run", sequence: 10, revision: 0, role: "user", origin: "human",
			streaming: false, createdAt: at(1), text: "How are the workers doing?",
		};
		const steer: ConversationActivity = {
			kind: "activity", id: "steer-1", turnId: "run", sequence: 12, revision: 0, activityKind: "system", status: "completed",
			summary: "Also check the docs.", createdAt: at(1), detail: { event: "steer", text: "Also check the docs." },
		};
		const carried = report("t-report", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?");
		const items: ConversationItem[] = [prompt, { ...command("run", 1, "ao status"), sequence: 11 }, steer, { ...carried, sequence: 13 }, reply("run", 2, "Both moved.")];
		renderSnapshot({
			...chatFixtureEmpty, latestSequence: 22, items,
			turns: [{ id: "run", state: "completed", providerTurnId: "provider-run", requestedAt: at(1), completedAt: at(2) }],
			batchedDeliveries: [{ turnId: "t-report", deliveredWithTurnId: "run", steerActivityId: "steer-1" }],
		});

		expect(screen.getByTestId("worker-reports")).toHaveTextContent("1 needs you");
	});

	it("draws one logo per sender on the reports line", () => {
		const entries = [1, 2, 3, 4].map((n) => `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${n}.`).join("\n\n");
		renderOrchestrator([report("t1", 1, entries), reply("t1", 2, "Which one first?")]);
		const line = screen.getByRole("button", { name: /^4 Agent Reports/ });
		expect(line.querySelectorAll("[data-report-logo]")).toHaveLength(1);
		expect(line).not.toHaveTextContent("...");
	});

	it("still shows the work of a turn that answered with tools only", async () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render above the browser"),
			command("t1", 1, "gh pr merge 7054"),
		]);

		await userEvent.click(screen.getByRole("button", { name: /^1 background update/ }));
		await userEvent.click(within(screen.getByTestId("background-turn")).getAllByRole("button")[0]!);
		expect(screen.getByTestId("worker-update")).toHaveTextContent("Menus");
		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Worked for/ })).toBeInTheDocument();
	});
});

function human(turnId: string, minute: number, text: string): ConversationMessage {
	return {
		kind: "message", id: `${turnId}-human`, turnId, sequence: minute * 10, revision: 0, role: "user", origin: "human",
		streaming: false, createdAt: at(minute), text,
	};
}

function orchestratorView() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, []);
	return (value: ConversationSnapshot, request?: { id: string; text: string; messageId: string }) => (
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={value} sessionRole="orchestrator" annotationNavigationRequest={request} />
			</TooltipProvider>
		</QueryClientProvider>
	);
}

function renderSnapshot(value: ConversationSnapshot) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, []);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={value} sessionRole="orchestrator" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("background turns in the orchestrator chat", () => {
	it("folds worker turns the orchestrator handled on its own into one line per run, around the user's turns", async () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render above the browser."),
			reply("t1", 2, "Noted."),
			automation("t2", 3, "[from proj-187] Rebased #5051 cleanly."),
			reply("t2", 4, "Merged the rebased #5051 after CI went green."),
			human("t3", 5, "How is the browser work going?"),
			reply("t3", 6, "Menus are done and #5051 is merged."),
			report("t4", 7, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages load over the tunnel."),
			reply("t4", 8, "Noted."),
		]);

		expect(screen.getByText("How is the browser work going?")).toBeInTheDocument();
		expect(screen.getByText("Menus are done and #5051 is merged.")).toBeInTheDocument();
		expect(screen.queryByText("Noted.")).not.toBeInTheDocument();
		const lines = screen.getAllByTestId("background-updates");
		expect(lines).toHaveLength(2);
		expect(lines[0]).toHaveTextContent(/^2 background updates · 1 done · /);
		expect(lines[1]).toHaveTextContent(/^1 background update · 1 done · /);

		await userEvent.click(within(lines[0]!).getByRole("button", { name: /^2 background updates/ }));
		const rows = within(lines[0]!).getAllByTestId("background-turn");
		expect(rows).toHaveLength(2);
		expect(rows[0]).toHaveTextContent("Menus");
		expect(rows[0]).toHaveTextContent("done");
		expect(rows[0]).not.toHaveTextContent("Noted.");
		expect(rows[1]).toHaveTextContent("Merged the rebased #5051 after CI went green");

		await userEvent.click(within(rows[0]!).getAllByRole("button")[0]!);
		expect(within(rows[0]!).getByText("Menus render above the browser.")).toBeInTheDocument();
		expect(within(rows[0]!).getByText("Noted.")).toBeInTheDocument();
	});

	it("keeps a report that still needs the user in view, and folds it once that worker moves on", () => {
		renderOrchestrator([
			report("t1", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?"),
			reply("t1", 2, "Noted."),
		]);
		expect(screen.getByTestId("worker-reports")).toHaveTextContent("1 needs you");
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("keeps a background turn in view while a subagent it launched still works", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render above the browser."),
			{
				kind: "activity", id: "t1-launch", turnId: "t1", sequence: 15, revision: 0, providerItemId: "tool-launch", activityKind: "reasoning", status: "completed", summary: "Audit pages",
				detail: { providerToolName: "Agent", nestedAgent: true, input: { description: "Audit pages", prompt: "Audit.", subagent_type: "Explore" } } as ConversationActivity["detail"], createdAt: at(1),
			},
			{
				kind: "activity", id: "t1-step", turnId: "t1", sequence: 16, revision: 0, providerItemId: "tool-step", activityKind: "command", status: "running", summary: "grep -rn menu src",
				detail: { parentProviderItemId: "tool-launch", command: "grep -rn menu src" }, createdAt: at(1),
			},
			reply("t1", 2, "Noted."),
		]);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("folds a needs-you report a newer update from the same worker overtook", () => {
		renderOrchestrator([
			report("t1", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?"),
			reply("t1", 2, "Noted."),
			report("t2", 3, "[done] [iOS pages](ao://sessions/proj/proj-286)\nWent with the tunnel."),
			reply("t2", 4, "Noted."),
		]);
		expect(screen.queryByTestId("worker-reports")).not.toBeInTheDocument();
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · 1 done · /);
	});

	it("names where each worker got to on the line, and opens to one row per worker", async () => {
		renderOrchestrator([
			report("t1", 1, "[checkpoint] [iOS pages](ao://sessions/proj/proj-286)\nPages render."),
			reply("t1", 2, "Noted."),
			report("t2", 3, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t2", 4, "Noted."),
			report("t3", 5, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages ship."),
			reply("t3", 6, "Noted."),
		]);
		const line = screen.getByTestId("background-updates");
		expect(line).toHaveTextContent(/^3 background updates · 2 done · /);

		await userEvent.click(within(line).getByRole("button", { name: /^3 background updates/ }));
		const rows = within(line).getAllByTestId(/^background-(turn|worker)$/);
		expect(rows).toHaveLength(2);
		expect(rows[0]).toHaveTextContent("Menus");
		expect(rows[1]).toHaveAttribute("data-testid", "background-worker");
		expect(rows[1]).toHaveTextContent(/iOS pagesdone · 2 updatesPages ship/);

		await userEvent.click(within(rows[1]!).getByRole("button", { name: /iOS pages/ }));
		expect(within(rows[1]!).getAllByTestId("background-turn")).toHaveLength(2);
	});

	it("folds account failures into the background line, with one notice for the newest", () => {
		const base = snapshot([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			report("t2", 3, "[checkpoint] [iOS pages](ao://sessions/proj/proj-286)\nPages render."),
			report("t3", 5, "[done] [Docs](ao://sessions/proj/proj-287)\nDocs build."),
			reply("t3", 6, "Noted."),
			report("t4", 7, "[checkpoint] [iOS pages](ao://sessions/proj/proj-286)\nPages ship."),
		]);
		renderSnapshot({
			...base,
			turns: base.turns.map((turn) => turn.id === "t2" || turn.id === "t4"
				? { ...turn, state: "failed", errorMessage: "You've hit your usage limit.", usageLimit: { autoResume: false } }
				: turn),
		});

		const lines = screen.getAllByTestId("background-updates");
		expect(lines).toHaveLength(1);
		expect(lines[0]).toHaveTextContent(/^4 background updates · 2 not handled · 2 done · /);
		expect(screen.getAllByTestId("usage-limit-marker")).toHaveLength(1);
		expect(screen.getAllByText("You've hit your usage limit.")).toHaveLength(1);
	});

	it("drops the account notice once a later background turn in the run went through", () => {
		const base = snapshot([
			report("t1", 1, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages render."),
			report("t2", 3, "[done] [Docs](ao://sessions/proj/proj-287)\nDocs build."),
			reply("t2", 4, "Noted."),
		]);
		renderSnapshot({
			...base,
			turns: base.turns.map((turn) => turn.id === "t1"
				? { ...turn, state: "failed", errorMessage: "Signed out.", failureCode: "auth_required" }
				: turn),
		});

		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · 1 not handled · 1 done · /);
		expect(screen.queryByText("Signed out.")).not.toBeInTheDocument();
	});

	it("folds an agent stopped between background turns into their line", () => {
		const stopped: ConversationActivity = {
			kind: "activity", id: "ao-action:session.terminated:proj-9", sequence: 25, revision: 0, activityKind: "ao_action",
			status: "completed", summary: "Terminated an agent", createdAt: at(2),
			detail: { action: "session.terminated", operationId: "proj-9", targetSessionId: "proj-9" },
		};
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			stopped,
			report("t3", 3, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages ship."),
			reply("t3", 4, "Noted."),
		]);

		const lines = screen.getAllByTestId("background-updates");
		expect(lines).toHaveLength(1);
		expect(lines[0]).toHaveTextContent(/^2 background updates · 2 done · 1 agent stopped · /);
		expect(screen.queryByText("Terminated an agent")).not.toBeInTheDocument();
	});

	it("counts every worker a batch of reports came from on the line", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render.\n\n[done] [Docs](ao://sessions/proj/proj-287)\nDocs build."),
			reply("t1", 2, "Noted."),
		]);
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · 2 done · /);
	});

	it("keeps a background turn that asked the user something in view when it then failed on the account", () => {
		const base = snapshot([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Should I close #8906?"),
			report("t2", 3, "[done] [Docs](ao://sessions/proj/proj-287)\nDocs build."),
			reply("t2", 4, "Noted."),
		]);
		renderSnapshot({
			...base,
			turns: base.turns.map((turn) => turn.id === "t1" ? { ...turn, state: "failed", errorMessage: "You've hit your usage limit.", usageLimit: { autoResume: false } } : turn),
		});
		expect(screen.getByText("Should I close #8906?")).toBeInTheDocument();
	});

	it("still says how many workers reported when the line only names agent notices", () => {
		const stopped: ConversationActivity = {
			kind: "activity", id: "ao-action:session.terminated:proj-9", sequence: 25, revision: 0, activityKind: "ao_action",
			status: "completed", summary: "Terminated an agent", createdAt: at(2),
			detail: { action: "session.terminated", operationId: "proj-9", targetSessionId: "proj-9" },
		};
		renderOrchestrator([
			report("t1", 1, "[checkpoint] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			stopped,
			report("t3", 3, "[checkpoint] [iOS pages](ao://sessions/proj/proj-286)\nPages render."),
			reply("t3", 4, "Noted."),
		]);
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · 2 workers · 1 agent stopped · /);
	});

	it("leaves an agent stopped after the last background turn in view", () => {
		const stopped: ConversationActivity = {
			kind: "activity", id: "ao-action:session.terminated:proj-9", sequence: 25, revision: 0, activityKind: "ao_action",
			status: "completed", summary: "Terminated an agent", createdAt: at(2),
			detail: { action: "session.terminated", operationId: "proj-9", targetSessionId: "proj-9" },
		};
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			stopped,
			human("t3", 3, "What next?"),
			reply("t3", 4, "The docs."),
		]);
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^1 background update · 1 done · /);
		expect(screen.getByText("Terminated an agent")).toBeInTheDocument();
	});

	it("keeps a turn the user opened open when a newer report from that worker joins it", async () => {
		const first = [report("t1", 1, "[checkpoint] [iOS pages](ao://sessions/proj/proj-286)\nPages render."), reply("t1", 2, "Noted.")];
		const view = orchestratorView();
		const { rerender } = render(view(snapshot(first)));
		await userEvent.click(screen.getByRole("button", { name: /^1 background update/ }));
		await userEvent.click(within(screen.getByTestId("background-turn")).getAllByRole("button")[0]!);
		expect(within(screen.getByTestId("background-turn")).getAllByRole("button")[0]).toHaveAttribute("aria-expanded", "true");

		rerender(view(snapshot([...first, report("t2", 3, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages ship."), reply("t2", 4, "Noted.")])));
		const worker = screen.getByTestId("background-worker");
		expect(worker).toHaveTextContent(/done · 2 updates/);
		const rows = within(worker).getAllByTestId("background-turn");
		expect(rows).toHaveLength(2);
		expect(within(rows[0]!).getAllByRole("button")[0]).toHaveAttribute("aria-expanded", "true");
	});

	it("leaves a failed stop between background turns in view", () => {
		const failed: ConversationActivity = {
			kind: "activity", id: "ao-action:session.terminated:proj-9", sequence: 25, revision: 0, activityKind: "ao_action",
			status: "failed", summary: "Terminated an agent", createdAt: at(2),
			detail: { action: "session.terminated", operationId: "proj-9", targetSessionId: "proj-9" },
		};
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			failed,
			report("t3", 3, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages ship."),
			reply("t3", 4, "Noted."),
		]);
		expect(screen.getAllByTestId("background-updates")).toHaveLength(2);
		expect(screen.queryByText(/agent stopped/)).not.toBeInTheDocument();
	});

	it("merges repeated failures with the same error into one line above the latest", async () => {
		const items = [1, 3, 5].flatMap((minute) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`)]);
		const base = snapshot(items);
		renderSnapshot({
			...base,
			turns: base.turns.map((turn, index) => ({ ...turn, state: "failed", errorMessage: `You've hit your usage limit. Try again at 9:2${index} AM.` })),
		});

		expect(screen.getByRole("button", { name: /^2 earlier updates failed the same way/ })).toBeInTheDocument();
		expect(screen.getAllByText(/You've hit your usage limit/)).toHaveLength(1);
		expect(screen.getByText(/Try again at 9:22 AM/)).toBeInTheDocument();
	});

	it("draws the orchestrator's replacement as one notice that opens to its goodbye", async () => {
		renderOrchestrator([
			automation("t1", 1, "AO is replacing this project orchestrator. Stop coordinating new work now and hand off."),
			reply("t1", 2, "Handing off to the next orchestrator."),
		]);
		const notice = screen.getByRole("button", { name: /The orchestrator agent is being replaced/ });
		expect(screen.queryByText("Handing off to the next orchestrator.")).not.toBeInTheDocument();
		await userEvent.click(notice);
		expect(screen.getByText("Handing off to the next orchestrator.")).toBeInTheDocument();
		expect(screen.getAllByText(/The orchestrator agent is being replaced/)).toHaveLength(1);
	});

	it("keeps a reply that ends in a full-width question mark in view", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "#5051をマージしますか？"),
		]);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("keeps a reply that ends in a question inside Japanese quotes in view", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "「マージしますか？」"),
		]);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("keeps a failed turn whose report still needs the user out of the merged failures", () => {
		const base = snapshot([
			report("t1", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?"),
			report("t3", 3, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
		]);
		renderSnapshot({ ...base, turns: base.turns.map((turn) => ({ ...turn, state: "failed", errorMessage: "You've hit your usage limit." })) });
		expect(screen.queryByRole("button", { name: /failed the same way/ })).not.toBeInTheDocument();
		expect(screen.getByText(/1 needs you/)).toBeInTheDocument();
	});

	it("keeps a failed turn whose reply asks the user out of the merged failures", () => {
		const base = snapshot([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."), reply("t1", 2, "Should I close the PR?"),
			report("t3", 3, "[done] [Menus](ao://sessions/proj/proj-285)\nStep 3."),
		]);
		renderSnapshot({ ...base, turns: base.turns.map((turn) => ({ ...turn, state: "failed", errorMessage: "Provider returned HTTP 500." })) });
		expect(screen.queryByRole("button", { name: /failed the same way/ })).not.toBeInTheDocument();
		expect(screen.getByText("Should I close the PR?")).toBeInTheDocument();
	});

	it("keeps failures whose errors differ apart", () => {
		const base = snapshot([1, 3].flatMap((minute) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`)]));
		renderSnapshot({ ...base, turns: base.turns.map((turn, index) => ({ ...turn, state: "failed", errorMessage: `Provider returned HTTP ${index ? 500 : 429}.` })) });
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
		expect(screen.getByText(/HTTP 429/)).toBeInTheDocument();
	});

	it("keeps failures apart when they differ only in a file position", () => {
		const base = snapshot([1, 3].flatMap((minute) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`)]));
		renderSnapshot({ ...base, turns: base.turns.map((turn, index) => ({ ...turn, state: "failed", errorMessage: `TypeError at worker.ts:${index ? "88:10" : "12:34"}` })) });
		expect(screen.queryByRole("button", { name: /failed the same way/ })).not.toBeInTheDocument();
	});

	it("merges failures that differ only in an ISO time", () => {
		const base = snapshot([1, 3].flatMap((minute) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`)]));
		renderSnapshot({ ...base, turns: base.turns.map((turn, index) => ({ ...turn, state: "failed", errorMessage: `Rate limited until 2026-11-22T10:0${index}:00.123+05:30.` })) });
		expect(screen.getByRole("button", { name: /^1 earlier update failed the same way/ })).toBeInTheDocument();
	});

	it("leaves a worker turn where the user answered an approval in view", () => {
		const approval: ConversationActivity = {
			kind: "activity", id: "t1-approval", turnId: "t1", sequence: 15, revision: 0, activityKind: "approval", status: "completed",
			summary: "gh pr merge 7054", createdAt: at(1), detail: { decision: "accept" },
		};
		renderOrchestrator([report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."), approval, reply("t1", 2, "Merged.")]);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("keeps an open background line open when an older background turn joins its front", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(workspaceQueryKey, []);
		const view = (items: ConversationItem[]) => (
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace snapshot={snapshot(items)} sessionRole="orchestrator" />
				</TooltipProvider>
			</QueryClientProvider>
		);
		const newer = [report("t3", 3, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."), reply("t3", 4, "Noted."), human("t5", 5, "Status?"), reply("t5", 6, "All good.")];
		const { rerender } = render(view(newer));
		await userEvent.click(screen.getByRole("button", { name: /^1 background update/ }));
		expect(screen.getAllByTestId("background-turn")).toHaveLength(1);
		rerender(view([report("t1", 1, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages load."), reply("t1", 2, "Noted."), ...newer]));
		expect(screen.getByRole("button", { name: /^2 background updates/ })).toBeInTheDocument();
		expect(screen.getAllByTestId("background-turn")).toHaveLength(2);
	});

	it("keeps a background line's key as turns join it at either end", () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(workspaceQueryKey, []);
		const view = (items: ConversationItem[]) => (
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace snapshot={snapshot(items)} sessionRole="orchestrator" />
				</TooltipProvider>
			</QueryClientProvider>
		);
		const turn = (id: string, minute: number) => [report(id, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`), reply(id, minute + 1, "Noted.")];
		const you = [human("t9", 9, "Status?"), reply("t9", 10, "All good.")];
		const lineKey = () => screen.getByTestId("background-updates").closest("[data-chat-turn]")!.getAttribute("data-chat-turn");
		const { rerender } = render(view([...turn("t3", 3), ...you]));
		const key = lineKey();
		rerender(view([...turn("t1", 1), ...turn("t3", 3), ...you]));
		expect(screen.getByRole("button", { name: /^2 background updates/ })).toBeInTheDocument();
		expect(lineKey()).toBe(key);
		rerender(view([...turn("t1", 1), ...turn("t3", 3), ...turn("t5", 5), ...you]));
		expect(screen.getByRole("button", { name: /^3 background updates/ })).toBeInTheDocument();
		expect(lineKey()).toBe(key);
	});

	it("leaves a background turn in view when a worker's steer carried in a report that still needs the user", () => {
		const steer: ConversationActivity = {
			kind: "activity", id: "steer-1", turnId: "run", sequence: 12, revision: 0, activityKind: "system", status: "completed",
			summary: "Steer", createdAt: at(1), detail: { event: "steer", senderSessionId: "proj-7", senderDisplayName: "Menus", text: "Rebased." },
		};
		const carried = report("t-report", 1, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?");
		const items: ConversationItem[] = [
			report("run", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			{ ...command("run", 1, "ao status"), sequence: 11 }, steer, { ...carried, sequence: 13 }, reply("run", 2, "Noted."),
		];
		renderSnapshot({
			...chatFixtureEmpty, latestSequence: 22, items,
			turns: [{ id: "run", state: "completed", providerTurnId: "provider-run", requestedAt: at(1), completedAt: at(2) }],
			batchedDeliveries: [{ turnId: "t-report", deliveredWithTurnId: "run", steerActivityId: "steer-1" }],
		});
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("counts a worker's steer and the reports it carried on the background line", () => {
		const steer: ConversationActivity = {
			kind: "activity", id: "steer-1", turnId: "run", sequence: 12, revision: 0, activityKind: "system", status: "completed",
			summary: "Steer", createdAt: at(1), detail: { event: "steer", senderSessionId: "proj-7", senderDisplayName: "Docs", text: "Rebased." },
		};
		const carried = report("t-report", 1, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages ship.");
		const items: ConversationItem[] = [
			report("run", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			{ ...command("run", 1, "ao status"), sequence: 11 }, steer, { ...carried, sequence: 13 }, reply("run", 2, "Noted."),
		];
		renderSnapshot({
			...chatFixtureEmpty, latestSequence: 22, items,
			turns: [{ id: "run", state: "completed", providerTurnId: "provider-run", requestedAt: at(1), completedAt: at(2) }],
			batchedDeliveries: [{ turnId: "t-report", deliveredWithTurnId: "run", steerActivityId: "steer-1" }],
		});
		expect(screen.getByRole("button", { name: /^3 background updates · 2 done/ })).toBeInTheDocument();
	});

	it("keeps failures apart when the field after a timestamp differs", () => {
		const base = snapshot([1, 3].flatMap((minute) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`)]));
		renderSnapshot({ ...base, turns: base.turns.map((turn, index) => ({ ...turn, state: "failed", errorMessage: `Provider error: {"time":"2026-11-22T10:0${index}:00Z","status":${index ? 500 : 429}}` })) });
		expect(screen.queryByRole("button", { name: /failed the same way/ })).not.toBeInTheDocument();
	});

	it("leaves a turn whose loaded part starts at a worker steer in view", () => {
		const steer: ConversationActivity = {
			kind: "activity", id: "t1-steer", turnId: "t1", sequence: 5, revision: 0, activityKind: "system", status: "completed",
			summary: "[from proj-187] Also rebase #5051.", createdAt: at(1), detail: { event: "steer", text: "[from proj-187] Also rebase #5051.", senderSessionId: "proj-187" },
		};
		renderOrchestrator([steer, reply("t1", 2, "Noted.")]);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("leaves worker turns that stopped short in view without calling them failed", () => {
		const base = snapshot([1, 3].flatMap((minute) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`)]));
		renderSnapshot({ ...base, turns: base.turns.map((turn) => ({ ...turn, state: "interrupted" })) });
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("leaves a replacement that failed in view", () => {
		const base = snapshot([automation("t1", 1, "AO is replacing this project orchestrator. Stop coordinating new work now and hand off.")]);
		renderSnapshot({ ...base, turns: base.turns.map((turn) => ({ ...turn, state: "failed", errorMessage: "Authentication required." })) });
		expect(screen.getByText(/Authentication required/)).toBeInTheDocument();
	});

	it("leaves a worker turn the user steered in view", () => {
		const steer: ConversationActivity = {
			kind: "activity", id: "t2-steer", turnId: "t2", sequence: 35, revision: 0, activityKind: "system", status: "completed",
			summary: "Also rebase #5051.", createdAt: at(3), detail: { event: "steer", text: "Also rebase #5051." },
		};
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			report("t2", 3, "[done] [iOS pages](ao://sessions/proj/proj-286)\nPages ship."),
			{ ...reply("t2", 3, "Checking the iOS pages."), id: "t2-early" },
			steer,
			reply("t2", 4, "Rebased #5051 too."),
		]);
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^1 background update · 1 done · /);
		expect(screen.getByText("Checking the iOS pages.")).toBeInTheDocument();
	});

	it("folds a worker message AO delivered inside another turn", () => {
		const base = snapshot([
			report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			reply("t1", 2, "Noted."),
			automation("t2", 3, "[from proj-187] Rebased #5051 cleanly."),
		]);
		renderSnapshot({ ...base, turns: base.turns.filter((turn) => turn.id !== "t2") });
		expect(screen.queryByTestId("worker-update")).not.toBeInTheDocument();
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · 1 done · /);
	});

	it("leaves a turn a tool sent for the user in view", () => {
		renderOrchestrator([
			automation("t1", 1, "[via Grok Bot #c1] What's the status?"),
			reply("t1", 2, "Noted."),
		]);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
		expect(screen.getByText("Noted.")).toBeInTheDocument();
	});
});

describe("agent messages in a worker chat", () => {
	const workspaces = [{
		id: "proj", name: "proj", path: "/tmp/proj",
		sessions: [
			{ id: "proj-1", workspaceId: "proj", title: "Orchestrator", provider: "codex", kind: "orchestrator", status: "working", prs: [] },
			{ id: "proj-7", workspaceId: "proj", title: "Sibling worker", provider: "codex", kind: "worker", status: "idle", prs: [] },
		],
	}] as unknown as WorkspaceSummary[];
	const sent = (text: string, sender: string): ConversationMessage => ({
		...automation("t1", 1, `[from ${sender}] ${text}`), senderSessionId: sender, senderProjectId: "proj",
	});

	it("draws the orchestrator's message under its name, linked to its session", () => {
		renderOrchestrator([sent("Keep the draft local until Dhruv approves.", "proj-1"), reply("t1", 2, "Kept it local.")], vi.fn(), workspaces, "worker");

		const message = screen.getByTestId("agent-message");
		expect(message).toHaveTextContent("Keep the draft local until Dhruv approves.");
		expect(message).not.toHaveTextContent("[from");
		expect(within(message).getByRole("link", { name: "Orchestrator" })).toHaveAttribute("href", "ao://sessions/proj/proj-1");
	});

	it("marks the paths in its plain prose as code and links the sessions it names", () => {
		renderOrchestrator([sent("Compare /tmp/proj/.ao/shot.png with proj-7, then report.", "proj-1"), reply("t1", 2, "On it.")], vi.fn(), workspaces, "worker");

		const message = screen.getByTestId("agent-message");
		expect(within(message).getByText("/tmp/proj/.ao/shot.png").tagName).toBe("CODE");
		expect(within(message).getByRole("link", { name: "proj-7" })).toHaveAttribute("href", "ao://sessions/proj/proj-7");
		expect(message).toHaveTextContent("Compare /tmp/proj/.ao/shot.png with proj-7, then report.");
	});

	it("keeps an authored reference link to its own target", () => {
		renderOrchestrator([sent("See [proj-7][target].\n\n[target]: ao://sessions/proj/proj-1", "proj-1"), reply("t1", 2, "Ok.")], vi.fn(), workspaces, "worker");

		const links = within(screen.getByTestId("agent-message")).getAllByRole("link", { name: "proj-7" });
		expect(links).toHaveLength(1);
		expect(links[0]).toHaveAttribute("href", "ao://sessions/proj/proj-1");
	});

	it("shares one plugin list until the set of sessions changes, so session events do not re-parse messages", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(workspaceQueryKey, workspaces);
		// A chat reads its link workspaces once, in its link provider.
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}><ChatLinkProvider>{children}</ChatLinkProvider></QueryClientProvider>;
		const { result } = renderHook(() => ({ plugins: usePlainReferences(), workspaces: useChatLinkWorkspaces() }), { wrapper });
		const first = result.current.plugins;
		expect(renderHook(usePlainReferences, { wrapper }).result.current).toBe(first);
		const [project] = workspaces;
		act(() => queryClient.setQueryData(workspaceQueryKey, [{ ...project, sessions: project!.sessions.map((entry) => ({ ...entry, status: "idle" })) }]));
		await waitFor(() => expect(result.current.workspaces).not.toBe(workspaces));
		expect(result.current.plugins).toBe(first);
		act(() => queryClient.setQueryData(workspaceQueryKey, [{ ...project, sessions: [...project!.sessions, { ...project!.sessions[1]!, id: "proj-8" }] }]));
		await waitFor(() => expect(result.current.plugins).not.toBe(first));
	});

	it("names a sibling worker by its task", () => {
		renderOrchestrator([sent("Rebased on main.", "proj-7"), reply("t1", 2, "Noted.")], vi.fn(), workspaces, "worker");

		const message = screen.getByTestId("agent-message");
		expect(message).toHaveTextContent("Rebased on main.");
		expect(within(message).getByRole("link", { name: "Sibling worker" })).toHaveAttribute("href", "ao://sessions/proj/proj-7");
	});

	it("leaves a message whose sender this chat cannot find as it was", () => {
		renderOrchestrator([sent("Rebased on main.", "proj-404"), reply("t1", 2, "Noted.")], vi.fn(), workspaces, "worker");

		expect(screen.queryByTestId("agent-message")).not.toBeInTheDocument();
		expect(screen.getByText("Rebased on main.")).toBeInTheDocument();
	});
});

describe("messages an outside tool relayed", () => {
	const ask = "Catch me up on what is done, what is blocked and what is next.";

	it("draws an automation message under the tool's name, without its prefix or tag", () => {
		renderOrchestrator([automation("t1", 1, `[via Grok Bot #191450] ${ask}`), reply("t1", 2, "Two PRs merged.")]);

		const message = screen.getByTestId("agent-message");
		expect(message).toHaveTextContent("Grok Bot");
		expect(message).toHaveTextContent(ask);
		expect(message).not.toHaveTextContent("[via");
		expect(message).not.toHaveTextContent("191450");
		expect(screen.queryByText("automation")).not.toBeInTheDocument();
		// The orchestrator's answer stays a full reply: the tool asked, not a worker.
		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
	});

	it("draws one AO stored as the human's the same way, instead of as the user's bubble", () => {
		renderOrchestrator([{ ...automation("t1", 1, `[via Grok Bot] ${ask}`), origin: "human" }, reply("t1", 2, "Two PRs merged.")]);

		expect(screen.getByTestId("agent-message")).toHaveTextContent(`Grok Bot${ask}`);
	});

	it("draws a steer it sent into a running turn the same way", () => {
		renderOrchestrator([
			{ ...automation("t1", 0, "List the open PRs."), origin: "human" },
			command("t1", 1, "gh pr list"),
			{
				kind: "activity", id: "t1-steer", turnId: "t1", sequence: 12, revision: 0, activityKind: "system", status: "completed",
				summary: "[via Grok Bot #200733] Stop that.", detail: { event: "steer", text: "[via Grok Bot #200733] Stop that." }, createdAt: at(1),
			},
			reply("t1", 2, "Stopped."),
		]);

		expect(screen.getByTestId("agent-message")).toHaveTextContent("Grok BotStop that.");
	});

	it("keeps a message an AO session sent as that session's, whatever its text says", () => {
		renderOrchestrator([{ ...automation("t1", 1, `[via Grok Bot] ${ask}`), senderSessionId: "proj-404" }, reply("t1", 2, "Ok.")], vi.fn(), [], "worker");

		expect(screen.queryByTestId("agent-message")).not.toBeInTheDocument();
	});
});
