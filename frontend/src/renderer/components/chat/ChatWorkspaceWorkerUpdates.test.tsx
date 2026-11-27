/**
 * The orchestrator chat draws a spawned worker as a row in a spawn tree under its
 * turn until it answers, and worker traffic as rows. These pin what a reader keeps when it does:
 * a quiet reply still copies and rolls back, a reply that asks something stays a
 * full reply, and a turn with no reply still shows its work.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { ChatLinkProvider, useChatLinkWorkspaces } from "./ChatMarkdown";
import { usePlainReferences } from "./WorkerUpdates";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import { clientForSessionHost } from "../../lib/host-clients";
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

function renderOrchestrator(items: ConversationItem[], onRollback = vi.fn(), workspaces: WorkspaceSummary[] = [], sessionRole: "orchestrator" | "worker" = "orchestrator", turnState: "completed" | "running" = "completed", onSessionLinkOpen?: (url: string) => void) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, workspaces);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={snapshot(items, turnState)} sessionRole={sessionRole} onRollback={onRollback} onSessionLinkOpen={onSessionLinkOpen} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onRollback;
}

describe("orchestrator worker updates", () => {
	const worker = (overrides: Record<string, unknown> = {}) => [{
		id: "proj", name: "proj", path: "/tmp/proj",
		sessions: [{ id: "proj-288", workspaceId: "proj", title: "Audit usage", provider: "codex", kind: "worker", status: "idle", prs: [], ...overrides }],
	}] as unknown as WorkspaceSummary[];
	const spawnCommand = (id = "proj-288", status: ConversationActivity["status"] = "completed"): ConversationActivity => ({
		...command("t1", 1, "ao spawn --project proj --name 'Audit usage'"),
		status,
		detail: { command: "ao spawn --project proj --name 'Audit usage'", ...(status === "completed" ? { output: `spawned session ${id} "Audit usage" (idle)` } : {}) },
	});
	// The daemon files the spawn under the turn whose command ran it, as soon as the session exists.
	const spawnRecord = (id = "proj-288", turnId: string | undefined = "t1", sequence = 12, status: ConversationActivity["status"] = "completed"): ConversationActivity => ({
		kind: "activity", id: `ao-action:session.spawned:${id}`, turnId, sequence, revision: 0, activityKind: "ao_action",
		status, summary: "Spawned an agent", createdAt: at(1),
		detail: { action: "session.spawned", operationId: id, targetSessionId: id },
	});
	const startedReply = { ...reply("t1", 1, "Worker started."), sequence: 13 };

	it("shows a recorded spawn as one spawn tree row under the turn's reply, outside Worked for", async () => {
		const openSession = vi.fn();
		renderOrchestrator([automation("t1", 1, "Check current usage."), spawnCommand(), spawnRecord(), startedReply], vi.fn(), worker(), "orchestrator", "completed", openSession);

		const row = screen.getByTestId("spawned-agent");
		expect(screen.getAllByTestId("spawned-agent")).toHaveLength(1);
		expect(screen.getByText("Worker started.").compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		// The reply's copy row closes the turn, under the tree.
		expect(row.compareDocumentPosition(screen.getByRole("button", { name: "Copy message as markdown" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Spawned \d agents?/ })).not.toBeInTheDocument();
		// A reply arrow in place of the fork icon, and the worker's agent logo.
		expect(row.querySelector("svg.icon-corner-down-right")).not.toBeNull();
		expect(row.querySelector("svg.icon-split")).toBeNull();
		expect(within(row).getByRole("img", { name: "codex" })).toBeInTheDocument();
		// Logo, name and state are the session's link; the arrow stays outside it.
		const link = within(row).getByRole("link", { name: /Audit usage/ });
		expect(link).toHaveAttribute("href", "ao://sessions/proj/proj-288");
		expect(link.querySelector("svg.icon-corner-down-right")).toBeNull();
		const idle = within(row).getByText("Idle");
		expect(idle.parentElement?.querySelector('[data-testid="lattice-loader"]')).toBeNull();
		await userEvent.click(link);
		expect(openSession).toHaveBeenCalledWith("ao://sessions/proj/proj-288");
	});

	it("names a worker the session list does not have from its spawn record", () => {
		const record = { ...spawnRecord(), detail: { action: "session.spawned", operationId: "proj-288", targetSessionId: "proj-288", displayName: "Audit usage", harness: "codex", href: "ao://sessions/proj/proj-288" } };
		renderOrchestrator([automation("t1", 1, "Check current usage."), record, startedReply]);
		const row = screen.getByTestId("spawned-agent");
		expect(within(row).getByRole("img", { name: "codex" })).toBeInTheDocument();
		expect(within(row).getByRole("link", { name: /Audit usage/ })).toHaveAttribute("href", "ao://sessions/proj/proj-288");
		expect(row).not.toHaveTextContent("proj-288");
	});

	it("marks a hovered row by underlining its name, with no fill behind it", () => {
		renderOrchestrator([automation("t1", 1, "Check current usage."), spawnRecord(), startedReply], vi.fn(), worker());
		const row = screen.getByTestId("spawned-agent");
		const link = within(row).getByRole("link", { name: /Audit usage/ });
		expect(within(link).getByText("Audit usage")).toHaveClass("group-hover:underline");
		expect(`${row.className} ${link.className}`).not.toMatch(/\bhover:bg-|\bbg-/);
		expect(row.querySelector("svg.icon-arrow-up-right")).toBeNull();
	});

	it("draws a recorded spawn outside any turn as the worker's row", () => {
		renderOrchestrator([automation("t1", 1, "Check current usage."), spawnRecord("proj-288", undefined, 2)]);
		expect(screen.getByTestId("spawned-agent")).toHaveTextContent("proj-288");
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("draws a spawn filed under a rolled-back turn as the worker's row on its own", () => {
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
		expect(screen.getByTestId("spawned-agent")).toHaveTextContent("proj-9");
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("shows the row as soon as the daemon records the spawn, while the turn and its command still run", () => {
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnCommand("proj-288", "running"), spawnRecord()], vi.fn(), [], "orchestrator", "running");
		expect(screen.getByTestId("spawned-agent")).toBeInTheDocument();
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("draws a row for a spawn only its command output names, beside a recorded one", () => {
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnCommand("proj-288"), spawnRecord("proj-289"), startedReply]);
		expect(screen.getAllByTestId("spawned-agent").map((row) => row.textContent)).toEqual([
			expect.stringContaining("proj-289"),
			expect.stringContaining("proj-288"),
		]);
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("folds a turn's failed spawn into Worked for, as one failed step", async () => {
		const reason = "The default account is signed out or now holds another login, and no other account is signed in.";
		const cmd = "ao spawn --project proj --name 'background config snapshot'";
		const approval: ConversationActivity = {
			kind: "activity", id: "t1-approval", turnId: "t1", sequence: 11, revision: 0, activityKind: "auto_review", status: "completed",
			summary: cmd, detail: { status: "approved", riskLevel: "medium", command: cmd }, createdAt: at(1),
		};
		const failedCall: ConversationActivity = {
			kind: "activity", id: "t1-spawn", turnId: "t1", sequence: 12, revision: 0, activityKind: "command", status: "failed",
			summary: cmd, detail: { command: cmd, exitCode: 1, output: reason }, createdAt: at(1),
		};
		const failedRecord: ConversationActivity = {
			kind: "activity", id: "ao-action:session.spawned:failed-1", turnId: "t1", sequence: 13, revision: 0, activityKind: "ao_action", status: "failed",
			summary: "Spawned an agent", createdAt: at(1), detail: { action: "session.spawned", operationId: "failed:req-1", error: reason },
		};
		renderOrchestrator([automation("t1", 1, "Check the config."), approval, failedCall, failedRecord, { ...reply("t1", 1, "I couldn't start the check."), sequence: 14 }]);

		// Folded, the turn shows only Worked for and the reply.
		expect(screen.queryByRole("button", { name: /Ran 2 tool calls/ })).not.toBeInTheDocument();
		expect(screen.queryByText(reason)).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: /^Worked for/ }));
		const run = await screen.findByRole("button", { name: /Ran 2 tool calls/ });
		expect(run).toHaveTextContent("1 failed");
		// The failed step stays inside its run until the run opens.
		expect(screen.queryByRole("button", { name: /Starting\s*background config snapshot\s*failed/ })).not.toBeInTheDocument();
		await userEvent.click(run);
		expect(await screen.findByRole("button", { name: /Starting\s*background config snapshot\s*failed/ })).toBeInTheDocument();
		expect(screen.getByText(reason)).toBeInTheDocument();
	});

	it("keeps a failed spawn the daemon recorded nothing for under the fold, like any failed call", () => {
		const cmd = "ao spawn --project proj --name 'background config snapshot'";
		const failedCall: ConversationActivity = {
			kind: "activity", id: "t1-spawn", turnId: "t1", sequence: 12, revision: 0, activityKind: "command", status: "failed",
			summary: cmd, detail: { command: cmd }, createdAt: at(1),
		};
		renderOrchestrator([automation("t1", 1, "Check the config."), failedCall, { ...reply("t1", 1, "I couldn't start the check."), sequence: 14 }]);
		// An older daemon, or a record that landed in another run: no step says why, so the
		// call that failed stays on screen.
		expect(screen.getByRole("button", { name: /background config snapshot/ })).toBeInTheDocument();
	});

	it("keeps a failed spawn as the action row that says so, inside Worked for", async () => {
		renderOrchestrator([automation("t1", 1, "Check current usage."), { ...spawnRecord("", undefined, 2, "failed"), detail: { action: "session.spawned", operationId: "failed:req-1", error: "project not found" } }]);
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: /^Worked for/ }));
		expect(await screen.findByText("Spawned an agent")).toBeInTheDocument();
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
	});

	it("drops the row once the worker reports", () => {
		renderOrchestrator([
			automation("t1", 1, "Check current usage."), spawnRecord(), startedReply,
			report("t2", 3, "[done] [Audit usage](ao://sessions/proj/proj-288)\nFound 9 issues across 6 files."),
			reply("t2", 4, "Noted."),
		], vi.fn(), worker());
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
	});

	it("leaves no empty space under the reply once every worker has answered", () => {
		renderOrchestrator([
			automation("t1", 1, "Check current usage."), spawnCommand(), spawnRecord(), startedReply,
			report("t2", 3, "[done] [Audit usage](ao://sessions/proj/proj-288)\nFound 9 issues across 6 files."),
			reply("t2", 4, "Noted."),
		], vi.fn(), worker());
		// The `ao spawn` output still names the worker, but nothing is left to wait on.
		expect(screen.queryByTestId("spawned-agents")).not.toBeInTheDocument();
		expect(document.querySelector("div.mt-2\\.5:empty")).toBeNull();
	});

	it("drops the termination of a worker its row already shows", () => {
		const terminated = { ...spawnRecord("proj-288", undefined, 20), id: "ao-action:session.terminated:proj-288", summary: "Terminated an agent", detail: { action: "session.terminated", operationId: "proj-288:generation:0", targetSessionId: "proj-288" } };
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnRecord(), startedReply, terminated], vi.fn(), worker({ status: "terminated", isTerminated: true }));
		expect(within(screen.getByTestId("spawned-agent")).getByText("Terminated")).toBeInTheDocument();
		expect(screen.queryByText("Terminated an agent")).not.toBeInTheDocument();
	});

	it("shows the termination of a worker that has already answered", async () => {
		const terminated = { ...spawnRecord("proj-288", "t3", 52), id: "ao-action:session.terminated:proj-288", summary: "Terminated an agent", detail: { action: "session.terminated", operationId: "proj-288:generation:0", targetSessionId: "proj-288" } };
		renderOrchestrator([
			automation("t1", 1, "Check usage."), spawnRecord(), startedReply,
			report("t2", 3, "[done] [Audit usage](ao://sessions/proj/proj-288)\nFound 9 issues across 6 files."),
			reply("t2", 4, "Noted."),
			automation("t3", 5, "Stop the audit."), command("t3", 5, "ao session kill proj-288"), terminated, { ...reply("t3", 5, "Stopped it."), sequence: 53 },
		], vi.fn(), worker({ status: "terminated", isTerminated: true }));
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		await userEvent.click(screen.getAllByRole("button", { name: /^Worked for/ }).at(-1)!);
		expect(await screen.findByRole("button", { name: /Terminated an agent/ })).toBeInTheDocument();
	});

	it("keeps a termination of a worker this chat did not spawn, and a failed one", () => {
		const terminated = (id: string, sequence: number, status: ConversationActivity["status"]): ConversationActivity => ({
			kind: "activity", id: `ao-action:session.terminated:${id}`, sequence, revision: 0, activityKind: "ao_action",
			status, summary: "Terminated an agent", createdAt: at(1),
			detail: { action: "session.terminated", operationId: `${id}:generation:0`, targetSessionId: id },
		});
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnRecord("proj-288", undefined, 2), terminated("proj-288", 3, "failed"), terminated("proj-300", 4, "completed")]);
		// The failed one says it failed; the other worker has no row to say it ended.
		expect(screen.getByText("Terminated 2 agents")).toBeInTheDocument();
	});

	it("keeps a working worker's row to its state, with the stage the daemon names on its hover card", async () => {
		const workspaces = worker({ status: "working", activity: { state: "active" }, displayStatus: "Fixing CI failures", createdAt: new Date(Date.now() - 90_000).toISOString() });
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnRecord(), startedReply], vi.fn(), workspaces);
		const row = screen.getByTestId("spawned-agent");
		expect(row).toHaveTextContent("Working");
		expect(row).not.toHaveTextContent("1m");
		// One short line per worker: the stage waits on the card.
		expect(row).not.toHaveTextContent("Fixing CI failures");
		await userEvent.hover(within(row).getByRole("link", { name: /Audit usage/ }));
		expect(await screen.findByRole("status", { name: "Audit usage, Fixing CI failures" })).toBeInTheDocument();
	});

	it("says on the hover card why a worker's start failed", async () => {
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnRecord(), startedReply], vi.fn(), worker({ provisionState: "failed", provisionError: "worktree add failed" }));
		const row = screen.getByTestId("spawned-agent");
		expect(row).toHaveTextContent("Start failed");
		expect(row).not.toHaveTextContent("worktree add failed");
		await userEvent.hover(within(row).getByRole("link", { name: /Audit usage/ }));
		expect(await screen.findByText("worktree add failed")).toBeInTheDocument();
	});

	it("does not repeat the state as the stage", () => {
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnRecord(), startedReply], vi.fn(), worker({ status: "working", activity: { state: "active" }, displayStatus: "Working" }));
		expect(within(screen.getByTestId("spawned-agent")).getAllByText(/Working/)).toHaveLength(1);
	});

	it.each([
		["provisioning", { provisionState: "provisioning" }, "Starting", true],
		["active", { status: "working", activity: { state: "active" } }, "Working", true],
		["idle", { status: "idle", activity: { state: "idle" } }, "Idle", false],
		["exited", { status: "exited", activity: { state: "exited" } }, "Exited", false],
		["terminated", { status: "terminated", isTerminated: true }, "Terminated", false],
		["terminated while waiting for input", { status: "terminated", isTerminated: true, activity: { state: "waiting_input" } }, "Terminated", false],
		["failed start", { provisionState: "failed" }, "Start failed", false],
		["status needs input", { status: "needs_input" }, "Needs input", false],
		["PR waiting input", { status: "needs_input", prs: [{ number: 94, state: "open", ci: "pending", review: "pending", mergeability: "unknown" }] }, "Needs input", false],
		["activity waiting input", { activity: { state: "waiting_input" } }, "Needs input", false],
		["activity blocked", { activity: { state: "blocked" } }, "Needs input", false],
		["checking", { statusReadiness: "checking" }, "Checking", false],
		["PR in review", { status: "idle", activity: { state: "idle" }, kanbanColumn: "needs_review", displayStatus: "Needs human review" }, "In review", false],
		["PR ready", { status: "idle", activity: { state: "idle" }, kanbanColumn: "ready", displayStatus: "Mergeable" }, "Ready to merge", false],
		["PR merged", { status: "idle", activity: { state: "idle" }, kanbanColumn: "ready", displayStatus: "Merged" }, "Merged", false],
		["PR with failing CI", { status: "idle", activity: { state: "idle" }, displayStatus: "CI failing" }, "CI failing", false],
		["working with a PR", { status: "working", activity: { state: "active" }, kanbanColumn: "needs_review", displayStatus: "Needs human review" }, "Working", true],
		["unavailable", { statusReadiness: "unavailable" }, "Status unavailable", false],
		["missing", null, "Status unavailable", false],
	] as const)("shows %s child state without a false spinner", (_case, child, label, spinning) => {
		const workspaces = child ? worker(child) : ([{ id: "proj", name: "proj", path: "/tmp/proj", sessions: [] }] as unknown as WorkspaceSummary[]);
		renderOrchestrator([automation("t1", 1, "Check usage."), spawnCommand(), startedReply], vi.fn(), workspaces);
		const line = within(screen.getByTestId("spawned-agent")).getByText(label).parentElement;
		expect(line?.querySelector('[data-testid="lattice-loader"]') !== null).toBe(spinning);
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

	// The orchestrator answers a report by spawning another worker. That worker's line
	// is under the turn until it answers, so the turn stays in view until then.
	const spawnedFixCi: ConversationActivity = {
		kind: "activity", id: "ao-action:session.spawned:proj-290", turnId: "t1", sequence: 15, revision: 0, activityKind: "ao_action",
		status: "completed", summary: "Spawned an agent", createdAt: at(1),
		detail: { action: "session.spawned", operationId: "proj-290", targetSessionId: "proj-290", displayName: "Fix CI" },
	};
	const spawnedInBackground = [
		report("t1", 1, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render above the browser."),
		spawnedFixCi,
		reply("t1", 2, "Started Fix CI."),
	];

	it("keeps a background turn in view while a worker it spawned has not answered", () => {
		renderOrchestrator(spawnedInBackground);
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
		expect(screen.getByTestId("spawned-agent")).toHaveTextContent("Fix CI");
	});

	it("folds that turn once the worker it spawned reports", () => {
		renderOrchestrator([
			...spawnedInBackground,
			report("t2", 3, "[done] [Fix CI](ao://sessions/proj/proj-290)\nCI is green."),
			reply("t2", 4, "Noted."),
		]);
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · 2 done · /);
	});

	// A worker that is killed, exits, never starts or is deleted sends no report, so nothing
	// would ever take its row down or let its turn fold.
	const fixCi = (overrides: Record<string, unknown> | null) => [{
		id: "proj", name: "proj", path: "/tmp/proj",
		sessions: overrides ? [{ id: "proj-290", workspaceId: "proj", title: "Fix CI", provider: "codex", kind: "worker", status: "idle", prs: [], ...overrides }] : [],
	}] as unknown as WorkspaceSummary[];
	const laterTurn = [
		report("t2", 3, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus also close on Escape."),
		reply("t2", 4, "Noted."),
	];

	it.each([
		["terminated", { status: "terminated", isTerminated: true }],
		["exited", { status: "exited", activity: { state: "exited" } }],
		["never started", { provisionState: "failed" }],
		["gone from the session list", null],
	] as const)("lets go of a worker that %s without reporting once a later turn starts", (_case, session) => {
		renderOrchestrator([...spawnedInBackground, ...laterTurn], vi.fn(), fixCi(session));
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		// Its turn folds with the one after it.
		expect(screen.getByTestId("background-updates")).toHaveTextContent(/^2 background updates · /);
	});

	it("puts the spawn of a worker that ended without reporting back into its turn's work", async () => {
		renderOrchestrator([
			automation("t1", 1, "Fix CI."), spawnedFixCi, { ...reply("t1", 1, "Started Fix CI."), sequence: 16 },
			automation("t2", 3, "Thanks."), reply("t2", 4, "Anytime."),
		], vi.fn(), fixCi({ status: "terminated", isTerminated: true }));
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		expect(screen.queryByText("Spawned an agent")).not.toBeInTheDocument();
		await userEvent.click(screen.getAllByRole("button", { name: /^Worked for/ })[0]!);
		expect(await screen.findByText("Spawned an agent")).toBeInTheDocument();
	});

	it("keeps an ended worker's row while its turn is still the last", () => {
		renderOrchestrator(spawnedInBackground, vi.fn(), fixCi({ status: "terminated", isTerminated: true }));
		expect(within(screen.getByTestId("spawned-agent")).getByText("Terminated")).toBeInTheDocument();
		expect(screen.queryByTestId("background-updates")).not.toBeInTheDocument();
	});

	it("keeps a worker that is still running after a later turn starts", () => {
		renderOrchestrator([...spawnedInBackground, ...laterTurn], vi.fn(), fixCi({ status: "working", activity: { state: "active" } }));
		expect(screen.getByTestId("spawned-agent")).toHaveTextContent("Fix CI");
	});

	it("shows the termination of a worker whose row has gone", async () => {
		const terminated: ConversationActivity = {
			kind: "activity", id: "ao-action:session.terminated:proj-290", turnId: "t2", sequence: 31, revision: 0, activityKind: "ao_action",
			status: "completed", summary: "Terminated an agent", createdAt: at(3),
			detail: { action: "session.terminated", operationId: "proj-290:generation:0", targetSessionId: "proj-290" },
		};
		renderOrchestrator([...spawnedInBackground, automation("t2", 3, "Stop Fix CI."), terminated, reply("t2", 4, "Stopped it.")], vi.fn(), fixCi({ status: "terminated", isTerminated: true }));
		expect(screen.queryByTestId("spawned-agent")).not.toBeInTheDocument();
		await userEvent.click(screen.getAllByRole("button", { name: /^Worked for/ }).at(-1)!);
		expect(await screen.findByText("Terminated an agent")).toBeInTheDocument();
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

	it("keeps paging back to a quote while an older page folds into the first line", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(workspaceQueryKey, []);
		const onLoadOlder = vi.fn();
		const request = { id: "quote:1", text: "Ship it?", messageId: "t1-human" };
		const turn = (minute: number) => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`), reply(`t${minute}`, minute, "Noted.")];
		const page = (minutes: number[]): ConversationSnapshot => ({ ...snapshot(minutes.flatMap(turn)), hasMoreBefore: true, oldestSequence: minutes[0]! * 10 });
		const view = (value: ConversationSnapshot) => (
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ChatWorkspace snapshot={value} sessionRole="orchestrator" hasOlder onLoadOlder={onLoadOlder} annotationNavigationRequest={request} />
				</TooltipProvider>
			</QueryClientProvider>
		);
		const { rerender } = render(view(page([30, 31])));
		await waitFor(() => expect(onLoadOlder).toHaveBeenCalledTimes(1));
		rerender(view(page([20, 21, 30, 31])));
		await waitFor(() => expect(onLoadOlder).toHaveBeenCalledTimes(2));
		expect(screen.queryByText(/no longer available/)).not.toBeInTheDocument();
	});

	it("ends a background line at the gap before a distant find result", async () => {
		const turn = (minute: number, text = "Noted.") => [report(`t${minute}`, minute, `[done] [Menus](ao://sessions/proj/proj-285)\nStep ${minute}.`), reply(`t${minute}`, minute, text)];
		const old = snapshot(turn(1, "Found the needle."));
		const recent: ConversationSnapshot = { ...snapshot([...turn(5), ...turn(6)]), hasMoreBefore: true, oldestSequence: 50 };
		const hit = { messageId: "t1-reply", revision: 0, sequence: 12, block: 0, start: 10, end: 16 };
		const get = vi.fn(async (path: string) => ({ data: path.endsWith("find-window")
			? { ...old, controller: "ready", messages: old.items.filter((item) => item.kind === "message"), activities: old.items.filter((item) => item.kind === "activity") }
			: { conversationId: recent.conversationId, transcriptRevision: "gap", occurrences: [hit], total: 1 }, response: new Response() }));
		try {
			vi.spyOn(clientForSessionHost(), "GET").mockImplementation(get as never);
			vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{ top: 0, bottom: 100 }] as unknown as DOMRectList);
			vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue({ top: 20, bottom: 30, height: 10 } as DOMRect);
			renderSnapshot(recent);
			const log = screen.getByRole("log");
			log.focus();
			fireEvent.keyDown(log, { key: "f", ctrlKey: true });
			fireEvent.change(await screen.findByRole("searchbox"), { target: { value: "needle" } });
			await screen.findByText("Messages in between aren't loaded");
			expect(screen.getAllByTestId("background-updates")).toHaveLength(2);
		} finally { vi.restoreAllMocks(); }
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

describe("agent messages around the user's turns", () => {
	const steer = (id: string, sequence: number, text: string, sender?: string): ConversationActivity => ({
		kind: "activity", id, turnId: "h", sequence, revision: 0, activityKind: "system", status: "completed", summary: text, createdAt: at(Math.floor(sequence / 10)),
		detail: { event: "steer", text: sender ? `[from ${sender}] ${text}` : text, ...(sender ? { senderSessionId: sender } : {}) },
	});
	const withStates = (items: ConversationItem[], states: Record<string, "queued" | "running">, extra: Partial<ConversationSnapshot> = {}): ConversationSnapshot => {
		const base = snapshot(items);
		return {
			...base, ...extra,
			turns: base.turns.map((turn) => states[turn.id] === "queued"
				? { ...turn, state: "queued" as const, providerTurnId: undefined, completedAt: undefined }
				: states[turn.id] === "running" ? { ...turn, state: "running" as const, completedAt: undefined } : turn),
		};
	};
	const lines = () => screen.queryAllByTestId("background-updates").map((line) => line.textContent?.replace(/ · Oct.*$/, ""));
	const turnItems = (): ConversationItem[] => [
		human("h", 1, "Ship the shop changes."),
		{ ...command("h", 1, "ao status"), sequence: 11 },
		steer("s1", 20, "Design checkpoint verified.", "proj-7"),
		steer("s2", 21, "Draft PR created.", "proj-8"),
		steer("u1", 22, "Also check the logo link."),
		{ ...command("h", 2, "gh pr view 53"), id: "h-command-2", sequence: 23 },
		steer("s3", 24, "Fixes pushed.", "proj-7"),
		{
			kind: "activity", id: "ask", turnId: "h", sequence: 25, revision: 0, activityKind: "approval", status: "completed", summary: "Run a command",
			detail: { command: "npm test", decision: "approved" }, createdAt: at(2),
		} as ConversationActivity,
		steer("s4", 26, "Integration passed.", "proj-7"),
		steer("s5", 27, "Final head pushed.", "proj-7"),
	];

	it("folds agent messages that reach the user's running turn into one line between anything the user acts on", async () => {
		renderSnapshot(withStates(turnItems(), { h: "running" }));
		expect(lines()).toEqual(["2 background updates · 2 workers", "2 background updates · 1 worker"]);
		expect(screen.getByText("Also check the logo link.")).toBeInTheDocument();
		expect(screen.getByText("npm test")).toBeInTheDocument();
		expect(screen.getAllByTestId("worker-update").filter((row) => !row.closest("[data-testid=background-updates]"))).toHaveLength(1);

		await userEvent.click(within(screen.getAllByTestId("background-updates")[0]!).getAllByRole("button")[0]!);
		expect(screen.getByText(/Draft PR created/)).toBeInTheDocument();
	});

	it("keeps those lines once the user's turn settles, joining what an answered approval no longer splits", () => {
		renderSnapshot(snapshot([...turnItems(), reply("h", 3, "Shipped.")]));
		expect(lines()).toEqual(["2 background updates · 2 workers", "3 background updates · 1 worker"]);
		expect(screen.getByText("Also check the logo link.")).toBeInTheDocument();
		expect(screen.getByText("Shipped.")).toBeInTheDocument();
	});

	it("spans a line from its earliest to its latest message when delivery order differs", () => {
		const today = (hour: number) => new Date(new Date().setHours(hour, 0, 0, 0)).toISOString();
		renderSnapshot(withStates([
			human("h", 1, "Ship the shop changes."),
			{ ...steer("s1", 20, "Fixes pushed.", "proj-7"), createdAt: today(11) },
			{ ...steer("s2", 21, "Final head pushed.", "proj-8"), createdAt: today(10) },
		], { h: "running" }));
		expect(screen.getByTestId("background-updates").querySelector("button")).toHaveTextContent(/10:00.*–.*11:00/);
	});

	it("folds a batch of agent messages into one line while the orchestrator handles it", () => {
		const items = [
			automation("b", 1, "[from proj-7] Checkpoint one."),
			automation("c1", 2, "[from proj-8] Checkpoint two."),
			automation("c2", 3, "[from proj-7] Checkpoint three."),
		];
		const value = withStates(items, { b: "running" }, { batchedDeliveries: [{ turnId: "c1", deliveredWithTurnId: "b" }, { turnId: "c2", deliveredWithTurnId: "b" }] });
		renderSnapshot({ ...value, turns: value.turns.filter((turn) => turn.id === "b") });
		expect(lines()).toEqual(["3 background updates · 2 workers"]);
		expect(screen.queryAllByTestId("worker-update")).toHaveLength(0);
	});

	it("folds agent messages waiting behind the user's turn into a line of their own, apart from handled ones", () => {
		renderSnapshot(withStates([
			automation("d1", 1, "[from proj-7] Earlier checkpoint."),
			reply("d1", 2, "Noted."),
			human("h", 3, "Ship the shop changes."),
			automation("a1", 4, "[from proj-7] Checkpoint one."),
			automation("a2", 5, "[from proj-8] Checkpoint two."),
			automation("v", 6, "[via Slack #c1] Is the shop live?"),
			automation("a3", 7, "[from proj-7] Checkpoint three."),
		], { h: "running", a1: "queued", a2: "queued", v: "queued", a3: "queued" }));
		expect(lines()).toEqual(["1 background update · 1 worker", "2 updates waiting · 2 workers", "1 update waiting · 1 worker"]);
		expect(screen.getByText("Is the shop live?")).toBeInTheDocument();
	});

	it("keeps a waiting report that needs the user out of the waiting line", () => {
		renderSnapshot(withStates([
			human("h", 1, "Ship the shop changes."),
			report("a1", 2, "[done] [Menus](ao://sessions/proj/proj-285)\nMenus render."),
			report("a2", 3, "[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?"),
		], { h: "running", a1: "queued", a2: "queued" }));
		expect(lines()).toEqual(["1 update waiting · 1 done"]);
		expect(screen.getByTestId("worker-reports")).toHaveTextContent("1 needs you");
	});

	it("keeps a worker turn that carried a review result in view", () => {
		renderOrchestrator([
			automation("t1", 1, "[from proj-7] Checkpoint one."),
			{ ...automation("t1", 1, "AO review done: approved\nPR: https://github.com/acme/shop/pull/53"), id: "t1-review", sequence: 11 },
			reply("t1", 2, "Recorded."),
		]);
		expect(lines()).toEqual([]);
		expect(screen.getByText("Recorded.")).toBeInTheDocument();
	});

	it("shows the user's steer without the reports it carried", () => {
		renderSnapshot(withStates([
			human("h", 1, "Ship the shop changes."),
			{ ...command("h", 1, "ao status"), sequence: 11 },
			steer("s1", 20, "Please continue\n\n<ao-worker-reports>\nReports since your previous turn:\n\n[checkpoint] [Menus](ao://sessions/proj/proj-285)\nMenus render.\n</ao-worker-reports>\n"),
		], { h: "running" }));
		expect(screen.getByText("Please continue")).toBeInTheDocument();
		expect(screen.queryByText(/ao-worker-reports/)).not.toBeInTheDocument();
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
