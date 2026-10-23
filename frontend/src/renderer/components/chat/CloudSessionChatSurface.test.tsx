import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentModelsQueryKey } from "../../hooks/useAgentModelsQuery";
import type { CloudCpClientEvent } from "../../lib/cloud-cp";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import type { WorkspaceSession } from "../../types/workspace";
import { appendCloudEvents, CloudSessionChatSurface, loadCloudChatEvents, toSnapshot } from "./CloudSessionChatSurface";

const cloudMocks = vi.hoisted(() => ({
	listChatEvents: vi.fn(),
	sendSessionMessage: vi.fn(),
	cancelTurn: vi.fn(),
	steerTurn: vi.fn(),
	decideChatApproval: vi.fn(),
	listChatModels: vi.fn(),
	resumeSession: vi.fn(),
	requestWorkspaceCheckout: vi.fn(),
	chatProps: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: cloudMocks }),
}));
vi.mock("./ChatWorkspace", () => ({
	ChatWorkspace: (props: unknown) => {
		cloudMocks.chatProps(props);
		return <div data-testid="cloud-chat" />;
	},
}));

const session = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "project",
	title: "Cloud session",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	runtimeConnected: true,
	updatedAt: "2026-09-22T00:00:00Z",
	prs: [],
} satisfies WorkspaceSession;

describe("CloudSessionChatSurface", () => {
	beforeEach(() => localStorage.clear());
	it("attributes delivered worker reports as automation without changing the agent prompt", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: { text: '[from worker a1b2c3d4 "Builder"] Full prompt', origin: "automation", senderLabel: "Worker · Builder", displayText: "Full prompt" },
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			role: "user", origin: "automation", senderLabel: "Worker · Builder", text: "Full prompt",
		});
	});
	it("shows a project setup outcome as a system row outside any turn", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "workspace.setup", payload: { status: "succeeded", exitCode: 0, durationMs: 12_400, output: "stack up" }, createdAt: "2026-10-01T00:00:00Z" },
			{ sessionId: session.id, sequence: 2, type: "workspace.setup", payload: { status: "failed", step: 3, exitCode: 2, durationMs: 800 }, createdAt: "2026-10-01T00:01:00Z" },
		];
		const [succeeded, failed] = toSnapshot(session, events).items;
		expect(succeeded).toMatchObject({
			kind: "activity", activityKind: "system", status: "completed",
			detail: { event: "workspace.setup", setupStatus: "succeeded", exitCode: 0, durationMs: 12_400, output: "stack up" },
		});
		expect(failed).toMatchObject({
			status: "failed", detail: { event: "workspace.setup", setupStatus: "failed", setupStep: 3, exitCode: 2 },
		});
		expect(succeeded?.turnId).toBeUndefined();
		expect(failed?.kind === "activity" ? failed.detail?.output : "unexpected").toBeUndefined();
	});
	it("draws one live row per script run and drops the summary it supersedes", () => {
		const step = (sequence: number, payload: Record<string, unknown>): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-01T00:00:00Z",
			payload: { phase: "setup", runId: "run-1", total: 2, command: "npm ci", repository: "", ...payload },
		});
		const running = [
			step(1, { step: 1, status: "running" }),
			step(2, { step: 1, status: "running", output: "added 10" }),
		];
		const [live] = toSnapshot(session, running).items;
		expect(live).toMatchObject({
			kind: "activity", id: "cloud-script-run-run-1", activityKind: "system", status: "running", revision: 2,
			summary: "Setup: Running step 1 of 2",
			detail: { event: "workspace.script.run", scriptRun: { runId: "run-1", status: "running", runningStep: 1 } },
		});
		const finished = [
			// An older worker's summary for an earlier boot has no step events and keeps its row.
			{ sessionId: session.id, sequence: 0, type: "workspace.setup", payload: { status: "succeeded" }, createdAt: "2026-10-01T00:00:00Z" },
			...running,
			step(3, { step: 1, status: "succeeded", exitCode: 0, durationMs: 1000 }),
			step(4, { step: 2, status: "succeeded", exitCode: 0, durationMs: 1000, command: "npm run build" }),
			{ sessionId: session.id, sequence: 5, type: "workspace.setup", payload: { status: "succeeded", durationMs: 2000 }, createdAt: "2026-10-01T00:00:05Z" },
			step(6, { phase: "cleanup", runId: "cleanup-1", total: 1, step: 1, status: "succeeded", exitCode: 0, durationMs: 10 }),
		] satisfies CloudCpClientEvent[];
		const items = toSnapshot(session, finished).items;
		expect(items.map((item) => item.kind === "activity" ? item.detail?.event : item.kind)).toEqual([
			"workspace.setup", "workspace.script.run", "workspace.script.run",
		]);
		expect(items[1]).toMatchObject({ status: "completed", summary: "Setup: Succeeded", detail: { scriptRun: { status: "succeeded" } } });
		expect(items[2]).toMatchObject({ summary: "Cleanup: Succeeded", detail: { scriptRun: { phase: "cleanup" } } });
	});
	it("places the session's start Setup above the first prompt, and a later Setup where it ran", () => {
		const at = "2026-10-01T00:00:00Z";
		const step = (sequence: number, runId: string, status: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: at,
			payload: { phase: "setup", runId, total: 1, step: 1, command: "npm ci", repository: "", status },
		});
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Reply OK", turnId: "t1" }, createdAt: at },
			step(2, "start", "running"),
			step(3, "start", "succeeded"),
			{ sessionId: session.id, sequence: 4, type: "chat.turn_started", payload: { turnId: "t1" }, createdAt: at },
			{ sessionId: session.id, sequence: 5, type: "chat.turn_completed", payload: { turnId: "t1" }, createdAt: at },
			step(6, "retry", "succeeded"),
		];
		const runs = toSnapshot(session, events).items.filter((item) => item.kind === "activity" && item.detail?.event === "workspace.script.run");
		expect(runs.map((run) => [run.id, run.sequence])).toEqual([["cloud-script-run-start", 0.5], ["cloud-script-run-retry", 6]]);
		// A first prompt without a turn id shares Setup's turn-less group, so Setup leads it in item order too.
		const untracked = events.map((event) => event.type === "chat.user_message" ? { ...event, payload: { text: "Reply OK" } } : event);
		expect(toSnapshot(session, untracked).items.slice(0, 2).map((item) => item.id)).toEqual(["cloud-script-run-start", "cloud-event-1"]);
	});
	it("carries a failed setup step's command, user, directory, log and hint", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "workspace.setup",
			payload: {
				status: "failed", step: 2, exitCode: 1, output: "denied", command: "docker compose up -d", dir: "/w/lib",
				user: "ao-worker", groups: ["ao-worker", "users"], logPath: "/d/setup-logs/step-2.log", hint: "No Docker.",
			},
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			detail: {
				setupHint: "No Docker.",
				setupFailure: {
					command: "docker compose up -d", user: "ao-worker", groups: ["ao-worker", "users"],
					dir: "/w/lib", logPath: "/d/setup-logs/step-2.log",
				},
			},
		});
	});
	it("preserves structured sender metadata for linked automation rendering", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: {
				text: '[from worker-1] Full prompt', origin: "automation", displayText: "Full prompt",
				senderSessionId: "worker-1", senderProjectId: "project-1", senderDisplayName: "Backend worker",
			},
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			origin: "automation", text: "Full prompt", senderSessionId: "worker-1",
			senderProjectId: "project-1", senderDisplayName: "Backend worker",
		});
	});
	it("keeps human messages human even when their text resembles a worker report", () => {
		const events: CloudCpClientEvent[] = [{
			sessionId: session.id, sequence: 1, type: "chat.user_message",
			payload: { text: '[from worker a1b2c3d4 "Builder"] Please help' },
			createdAt: "2026-10-01T00:00:00Z",
		}];
		expect(toSnapshot(session, events).items[0]).toMatchObject({
			origin: "human", text: '[from worker a1b2c3d4 "Builder"] Please help',
		});
	});

	it("offers native Claude Plan mode and sends read-only turns without changing the permission ceiling", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({
			models: [{ id: "opus", displayName: "Opus", default: true }],
			modes: ["default", "plan"],
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface
					session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }}
				/>
			</QueryClientProvider>,
		);
		await waitFor(() =>
			expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].choices).toContainEqual({
				value: "plan",
				name: "Plan",
			}),
		);
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseConfigOption("mode", { value: "plan" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("outline only", [], "plan-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			{ text: "outline only", mode: "read-only" },
			{ idempotencyKey: "plan-1" },
		);
		view.unmount();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface
					session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }}
				/>
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].currentValue).toBe("plan"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseConfigOption("mode", { value: "agent" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("implement", [], "agent-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			expect.objectContaining({ mode: "trusted" }),
			{ idempotencyKey: "agent-1" },
		);
	});

	it("offers Claude models the way local Chat does: catalog list, model and effort options", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", description: "Opus 5.5 · Best for complex tasks", default: true, efforts: ["low", "high"], defaultEffort: "high" },
			{ id: "claude-opus-5", displayName: "Claude Opus 5", default: false },
		] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5", label: "Claude Opus 5" },
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
		] });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("opus"));
		expect(props().models.map((model: { id: string }) => model.id)).toEqual(["claude-opus-5", "claude-opus-5-5"]);
		expect(props().configOptions.find((option: { id: string }) => option.id === "effort")).toMatchObject({ currentValue: "high", choices: [{ value: "low" }, { value: "high" }] });
		act(() => props().onChooseConfigOption("model", { value: "claude-opus-5" }));
		await props().onSend("go", [], "model-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ model: "claude-opus-5" }), { idempotencyKey: "model-1" });
	});

	it("shows a failed Setup the way a failed local Chat start does, and Retry asks the worker again", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.requestWorkspaceCheckout.mockResolvedValue({});
		const startupError = { code: "project_setup_failed", message: "Setup step 1 failed: exit status 1.", detail: "could not reach registry", at: "2026-10-20T10:00:00Z" };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1", startupError } }} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props().session).toMatchObject({
			provisionState: "failed",
			provisionSteps: [{ id: "worktree", status: "done" }, { id: "setup", status: "running" }, { id: "agent", status: "pending" }],
			provisionError: "Setup step 1 failed: exit status 1. Output: could not reach registry",
		});
		// No agent runs until a retry prepares the workspace, so the composer waits as local's does.
		expect(props().snapshot.controller.state).toBe("stopped");
		expect(props().settingsReady).toBe(false);
		act(() => props().onResumeAgent());
		await waitFor(() => expect(cloudMocks.requestWorkspaceCheckout).toHaveBeenCalledWith("org-1", session.id));
		await waitFor(() => expect(props().session.provisionState).toBe("provisioning"));
		expect(props().snapshot.controller.state).not.toBe("stopped");
	});

	it("keeps the first prompt queued under a failed Setup's checklist, and Retry waits for a waking worker", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			const prompt: CloudCpClientEvent = { sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: "2026-10-20T16:01:57Z" };
			const failedRun: CloudCpClientEvent = {
				sessionId: session.id, sequence: 2, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
				payload: { phase: "setup", runId: "run-1", step: 1, total: 1, status: "failed", exitCode: 1, command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
			};
			const startupError = { code: "project_setup_failed", message: "The project's Setup script failed at step 1 (exit status 1).", at: "2026-10-20T16:02:02Z" };
			const failedSession = { ...session, runtimeConnected: true, cloud: { orgId: "org-1", startupError } };
			// Nothing works on the prompt until a retry passes, as while Setup runs.
			expect(toSnapshot(failedSession, [prompt, failedRun]).turns).toEqual([expect.objectContaining({ id: "turn-1", state: "queued" })]);
			cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [prompt, failedRun], hasMore: false, nextAfter: 2 });
			cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
			// An idle-paused workspace refuses the request until its worker is back.
			cloudMocks.requestWorkspaceCheckout.mockReset()
				.mockRejectedValueOnce(new CloudCpError("The session worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" }))
				.mockResolvedValueOnce({});
			cloudMocks.chatProps.mockClear();
			render(
				<QueryClientProvider client={new QueryClient()}>
					<CloudSessionChatSurface session={failedSession} />
				</QueryClientProvider>,
			);
			const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
			await waitFor(() => expect(props().snapshot.turns).toEqual([expect.objectContaining({ id: "turn-1", state: "queued" })]));
			// The checklist reports the failure, so the failed run is not also a row.
			expect(props().snapshot.items.filter((item: { detail?: { event?: string } }) => item.detail?.event === "workspace.script.run")).toEqual([]);
			expect(props().session).toMatchObject({ provisionState: "failed", provisionSteps: [{ id: "worktree", status: "done" }, { id: "setup", status: "running" }, { id: "agent", status: "pending" }] });
			act(() => props().onResumeAgent());
			await act(async () => { await vi.advanceTimersByTimeAsync(1_500); });
			await waitFor(() => expect(cloudMocks.requestWorkspaceCheckout).toHaveBeenCalledTimes(2));
			await waitFor(() => expect(props().session.provisionState).toBe("provisioning"));
			expect(props().resumeError).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps only the Setup run that passed once a retry recovers, from step events too", async () => {
		const run = (sequence: number, runId: string, status: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
			payload: { phase: "setup", runId, step: 1, total: 1, status, ...(status === "running" ? {} : { exitCode: status === "succeeded" ? 0 : 1, durationMs: 10 }), command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
		});
		const prompt: CloudCpClientEvent = { sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: "2026-10-20T16:01:57Z" };
		const runs = (items: { kind: string; detail?: { event?: string; scriptRun?: { runId: string } } }[]) =>
			items.filter((item) => item.detail?.event === "workspace.script.run").map((item) => item.detail?.scriptRun?.runId);
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [prompt, run(2, "run-1", "failed"), run(3, "run-2", "succeeded")], hasMore: false, nextAfter: 3 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(runs(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items ?? [])).toEqual(["run-2"]));
		// The start Setup that passed still leads the first prompt.
		const items = cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items as { kind: string; role?: string; detail?: { event?: string } }[];
		expect(items.findIndex((item) => item.detail?.event === "workspace.script.run")).toBeLessThan(items.findIndex((item) => item.kind === "message" && item.role === "user"));
	});

	it("shows a terminal agent that could not start after Setup as the checklist's agent step", () => {
		const startupError = { code: "agent_start_failed", message: "The coding agent did not start.", at: "2026-10-20T16:02:02Z" };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1", startupError } }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].session).toMatchObject({
			provisionState: "failed",
			provisionSteps: [{ id: "worktree", status: "done" }, { id: "setup", status: "done" }, { id: "agent", status: "running" }],
		});
	});

	it("keeps only the Setup that passed once a retry recovers, as a local start keeps no failed attempts", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "workspace.setup", payload: { status: "failed", step: 1, exitCode: 1 }, createdAt: "2026-10-01T00:00:00Z" },
			{ sessionId: session.id, sequence: 2, type: "workspace.setup", payload: { status: "succeeded", exitCode: 0, durationMs: 400 }, createdAt: "2026-10-01T00:01:00Z" },
		], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		const setupRows = () => cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items
			.filter((item: { detail?: { event?: string } }) => item.detail?.event === "workspace.setup");
		await waitFor(() => expect(setupRows()).toHaveLength(1));
		expect(setupRows()[0]).toMatchObject({ detail: { setupStatus: "succeeded" } });
	});

	it("keeps a failed Setup row when no startup failure reports it", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "workspace.setup", payload: { status: "failed", step: 1, exitCode: 1 }, createdAt: "2026-10-01T00:00:00Z" },
		], hasMore: false, nextAfter: 1 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: true, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toEqual([
			expect.objectContaining({ detail: expect.objectContaining({ event: "workspace.setup", setupStatus: "failed" }) }),
		]));
	});

	it("keeps a read-only Claude session in Plan mode and cannot elevate it to Agent", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [], modes: ["default", "plan"] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface
					session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "read-only" } }}
				/>
			</QueryClientProvider>,
		);
		await waitFor(() =>
			expect(cloudMocks.chatProps.mock.lastCall?.[0].configOptions?.[0].choices).toEqual([
				{ value: "plan", name: "Plan" },
			]),
		);
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseConfigOption("mode", { value: "agent" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("hello", [], "readonly-1");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith(
			"org-1",
			session.id,
			{ text: "hello", mode: "read-only" },
			{ idempotencyKey: "readonly-1" },
		);
	});

	it("waits for the worker to connect before loading model choices, without an error", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// A sandbox still starting (or an idle-paused one waking) has no worker
		// yet, for as long as that takes.
		const starting = { ...session, runtimeConnected: false, cloud: { orgId: "org-1" } };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={starting} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.resumeSession).toHaveBeenCalledWith("org-1", session.id, { signal: expect.any(AbortSignal) }));
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		// Still not connected on the next session poll: nothing is asked again.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, updatedAt: "2026-09-22T00:00:05Z" }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.resumeSession).toHaveBeenCalledTimes(1);
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();

		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, runtimeConnected: true, terminalGeneration: "1" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "codex-test", displayName: "Codex Test", default: true },
		]));
		expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1);
		expect(cloudMocks.resumeSession).toHaveBeenCalledTimes(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
	});

	it("keeps a model chosen while the worker was still starting over the worker's native model", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "opus", models: [
			{ id: "opus", displayName: "Opus", default: true },
			{ id: "claude-opus-5", displayName: "Claude Opus 5", default: false },
		] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-opus-5", label: "Claude Opus 5" },
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
		] });
		const starting = { ...session, provider: "claude-code" as const, runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" as const } };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={starting} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		// Before the catalog, the composer offers the local model list.
		await waitFor(() => expect(props().models.map((model: { id: string }) => model.id)).toContain("claude-opus-5"));
		act(() => props().onChooseSettings({ model: "claude-opus-5" }, "model"));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, runtimeConnected: true, terminalGeneration: "1" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("claude-opus-5"));
		await props().onSend("go", [], "model-boot");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ model: "claude-opus-5" }), { idempotencyKey: "model-boot" });
	});

	it("replaces a model picked while the worker was starting when the worker cannot run it", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ model: "opus", reasoningEffort: "medium", models: [
			{ id: "opus", displayName: "Opus", default: true, efforts: ["low", "medium"] },
		] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentModelsQueryKey("claude-code", ""), { models: [
			{ id: "claude-haiku-local", label: "Haiku (local)" },
		] });
		const starting = { ...session, provider: "claude-code" as const, runtimeConnected: false, cloud: { orgId: "org-1", permissionMode: "trusted" as const } };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={starting} />
			</QueryClientProvider>,
		);
		const props = () => cloudMocks.chatProps.mock.lastCall?.[0];
		await waitFor(() => expect(props().models.map((model: { id: string }) => model.id)).toContain("claude-haiku-local"));
		act(() => props().onChooseSettings({ model: "claude-haiku-local", reasoningEffort: "high" }, "model"));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...starting, runtimeConnected: true }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(props().configOptions?.find((option: { id: string }) => option.id === "model")?.currentValue).toBe("opus"));
		await props().onSend("go", [], "model-unrunnable");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, expect.objectContaining({ model: "opus", reasoningEffort: "medium" }), { idempotencyKey: "model-unrunnable" });
	});

	it("does not wake a connected session, nor one that pauses while Chat is open", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const connected = { ...session, cloud: { orgId: "org-1" }, terminalGeneration: "3" };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={connected} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
		// Idle pause takes the worker away; the open tab keeps the choices it has.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...connected, runtimeConnected: false }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.resumeSession).not.toHaveBeenCalled();
		expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		// A message wakes it; the new worker's catalog is read once it connects.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...connected, runtimeConnected: true, terminalGeneration: "4" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(2));
		expect(cloudMocks.resumeSession).not.toHaveBeenCalled();
	});

	it("asks again while a worker that left mid-request is still reported connected, and stops once it is not", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			const unavailable = () => new CloudCpError("The session worker is not connected.", { status: 409, code: "WORKER_UNAVAILABLE" });
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			// The session list lags a quick pause and resume by a poll.
			cloudMocks.listChatModels.mockReset()
				.mockRejectedValueOnce(unavailable())
				.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
			cloudMocks.resumeSession.mockReset().mockResolvedValue({});
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(
				<QueryClientProvider client={queryClient}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			await act(async () => { await vi.advanceTimersByTimeAsync(1_500); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(2);
			view.unmount();

			// A worker that is really gone: the list reports it, and asking stops.
			cloudMocks.listChatModels.mockReset().mockRejectedValue(unavailable());
			const goneClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const gone = render(
				<QueryClientProvider client={goneClient}>
					<CloudSessionChatSurface session={{ ...session, id: "session-2", cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			gone.rerender(
				<QueryClientProvider client={goneClient}>
					<CloudSessionChatSurface session={{ ...session, id: "session-2", runtimeConnected: false, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			// The retry already scheduled when the list caught up runs once; the
			// next failure sees the worker gone and stops.
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(2);
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("asks again when a worker's first catalog read outlasts the request", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
			cloudMocks.listChatModels.mockReset()
				.mockRejectedValueOnce(new CloudCpError("The worker did not answer in time.", { status: 504, code: "TRANSPORT_TIMEOUT" }))
				.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
			const first = render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			await act(async () => { await vi.advanceTimersByTimeAsync(1_500); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toHaveLength(1));
			expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
			first.unmount();

			// A worker that never answers is reported once its retries run out.
			cloudMocks.listChatModels.mockReset().mockRejectedValue(new CloudCpError("The worker did not answer in time.", { status: 504, code: "TRANSPORT_TIMEOUT" }));
			cloudMocks.chatProps.mockClear();
			render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<CloudSessionChatSurface session={{ ...session, id: "session-2", cloud: { orgId: "org-1" } }} />
				</QueryClientProvider>,
			);
			await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
			await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBe("Model choices unavailable: The worker did not answer in time."));
			expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(4);
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not show a model error kept from a worker that is gone", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockRejectedValue(new CloudCpError("The worker could not complete the operation.", { status: 422, code: "WORKER_OPERATION_FAILED" }));
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toMatch(/^Model choices unavailable/));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: false, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined());
	});

	it("does not ask a deleted session for models and loads them once its restored worker connects", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.resumeSession.mockReset().mockResolvedValue({});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const deleted = { ...session, cloud: { orgId: "org-1" }, isTerminated: true, runtimeConnected: false, terminalGeneration: "15702" };
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={deleted} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps).toHaveBeenCalled());
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
		expect(cloudMocks.resumeSession).not.toHaveBeenCalled();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();

		// Restore clears the terminated flag before the new worker connects:
		// nothing is asked, and nothing reads as an error, until it does.
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...deleted, isTerminated: false }} />
			</QueryClientProvider>,
		);
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...deleted, isTerminated: false, runtimeConnected: true, terminalGeneration: "15704" }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "codex-test", displayName: "Codex Test", default: true },
		]));
		expect(cloudMocks.listChatModels).toHaveBeenCalledTimes(1);
		expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBeUndefined();
	});

	it("says a session deleted under the composer must be restored", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset();
		cloudMocks.resumeSession.mockReset().mockRejectedValue(
			new CloudCpError("This session was deleted. Restore it to continue.", { status: 409, code: "SESSION_TERMINATED" }),
		);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// The session list has not caught up with the delete yet; its worker is gone.
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, runtimeConnected: false, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError)
			.toBe("This session was deleted. Restore it to continue."));
		expect(cloudMocks.listChatModels).not.toHaveBeenCalled();
	});

	it("shows provider models and sends the selected model and effort with the next turn", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true, efforts: ["low", "high"] }] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "codex-test", displayName: "Codex Test", default: true, efforts: ["low", "high"] },
		]));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings).toBeTypeOf("function");
		cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "codex-test", reasoningEffort: "high" });
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("hello", [], "message-2");
		expect(cloudMocks.sendSessionMessage).toHaveBeenCalledWith("org-1", session.id, {
			text: "hello", model: "codex-test", reasoningEffort: "high",
		}, { idempotencyKey: "message-2" });
	});

	it("uses the Codex model and effort last selected in the TUI", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({
			models: [{ id: "tui-model", displayName: "TUI Model", default: false, efforts: ["low", "high"] }],
			model: "tui-model", reasoningEffort: "high",
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		localStorage.setItem("cloud-chat-settings:org-1:session-1:codex", JSON.stringify({ model: "old-chat-model", reasoningEffort: "low" }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "tui-model", reasoningEffort: "high" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("continue", [], "native-settings");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "continue", model: "tui-model", reasoningEffort: "high",
		}, { idempotencyKey: "native-settings" });
	});

	it("keeps a Chat selector change made while the native model request is in flight", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		let resolveModels!: (value: { models: { id: string; displayName: string; default: boolean; efforts: string[] }[]; model: string; reasoningEffort: string }) => void;
		cloudMocks.listChatModels.mockReturnValue(new Promise((resolve) => { resolveModels = resolve; }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalled());
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "new-chat-model", reasoningEffort: "xhigh" }));
		await act(async () => resolveModels({
			models: [{ id: "old-tui-model", displayName: "Old TUI Model", default: false, efforts: ["low"] }],
			model: "old-tui-model", reasoningEffort: "low",
		}));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "new-chat-model", reasoningEffort: "xhigh" });
	});

	it.each(["worker", "orchestrator"] as const)("shows Claude models for a %s and restores its native selection", async (kind) => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({
			models: [{ id: "claude-sonnet", displayName: "Claude Sonnet", default: true, efforts: ["medium", "high"] }],
			model: "claude-sonnet", reasoningEffort: "high",
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, kind, provider: "claude-code", cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].models).toEqual([
			{ id: "claude-sonnet", displayName: "Claude Sonnet", default: true, efforts: ["medium", "high"] },
		]));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings).toMatchObject({ model: "claude-sonnet", reasoningEffort: "high" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("continue", [], `claude-${kind}`);
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "continue", model: "claude-sonnet", reasoningEffort: "high",
		}, { idempotencyKey: `claude-${kind}` });
	});

	it.each([
		["claude-code", "trusted", ["default", "accept-edits", "auto", "bypass-permissions"], "accept-edits"],
		["cursor", "trusted", ["default", "accept-edits", "auto", "bypass-permissions"], "auto"],
		["codex", "trusted", ["default", "accept-edits", "auto", "bypass-permissions"], "accept-edits"],
	] as const)("offers Cloud %s approval modes and sends the selected policy", async (provider, ceiling, modes, chosen) => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider, cloud: { orgId: "org-1", permissionMode: ceiling } }} />
			</QueryClientProvider>,
		);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.configOptions).toBeUndefined();
		expect(props.approvalModes).toEqual(modes);
		act(() => props.onChooseSettings({ approvalMode: chosen }, "approvalMode"));
		await props.onSend("hello", [], `mode-${provider}`);
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "hello", mode: ceiling, approvalMode: chosen,
		}, { idempotencyKey: `mode-${provider}` });
	});

	it("does not offer a mode above the Cloud session ceiling", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1", permissionMode: "standard" } }} />
			</QueryClientProvider>,
		);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.approvalModes).toEqual(["accept-edits", "auto"]);
		expect(props.snapshot.settings?.approvalMode).toBe("auto");
		act(() => props.onChooseSettings({ approvalMode: "bypass-permissions" }, "approvalMode"));
		await props.onSend("hello", [], "capped-message");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "hello", mode: "standard",
		}, { idempotencyKey: "capped-message" });
	});

	it("does not carry one Cloud session's model selection into another session", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings).toBeTypeOf("function"));
		cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "codex-test" });
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, id: "session-2", cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("next", [], "message-3");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", "session-2", { text: "next" }, { idempotencyKey: "message-3" });
	});

	it("does not carry Codex selections into another harness in the same session", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [{ id: "codex-test", displayName: "Codex Test", default: true }] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings).toBeTypeOf("function"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ model: "codex-test" }));
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("next", [], "after-handoff");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "next", mode: "trusted",
		}, { idempotencyKey: "after-handoff" });
	});

	it("shows a trusted Chat's default without sending it and keeps the user's own choice", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("bypass-permissions");
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("first", [], "trusted-1");
		// The worker derives the mode from the session's, stepping down when
		// the adapter does not offer it.
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "first", mode: "trusted",
		}, { idempotencyKey: "trusted-1" });
		// The settings bar sends the shown settings back with an effort change.
		const shown = cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings;
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ ...shown, reasoningEffort: "high" }));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("effort", [], "trusted-effort");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "effort", reasoningEffort: "high", mode: "trusted",
		}, { idempotencyKey: "trusted-effort" });
		expect(JSON.parse(localStorage.getItem("cloud-chat-settings:org-1:session-1:claude-code") ?? "{}")).not.toHaveProperty("approvalMode");
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ approvalMode: "default" }, "approvalMode"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("second", [], "trusted-2");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "second", reasoningEffort: "high", mode: "trusted", approvalMode: "default",
		}, { idempotencyKey: "trusted-2" });
		// The pick is saved as one and survives a remount.
		view.unmount();
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("default"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("third", [], "trusted-3");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "third", reasoningEffort: "high", mode: "trusted", approvalMode: "default",
		}, { idempotencyKey: "trusted-3" });
	});

	it("sends the shown default once the user picks it from the approval menu", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		const shown = cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings;
		expect(shown?.approvalMode).toBe("bypass-permissions");
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ ...shown, approvalMode: "bypass-permissions" }, "approvalMode"));
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("go", [], "picked-default");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", mode: "trusted", approvalMode: "bypass-permissions",
		}, { idempotencyKey: "picked-default" });
		expect(JSON.parse(localStorage.getItem("cloud-chat-settings:org-1:session-1:claude-code") ?? "{}")).toMatchObject({ approvalMode: "bypass-permissions" });
	});

	it("stops showing a pick the session's mode no longer allows", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onSend).toBeTypeOf("function"));
		act(() => cloudMocks.chatProps.mock.lastCall?.[0].onChooseSettings({ approvalMode: "default" }, "approvalMode"));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("default");
		view.rerender(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "codex", cloud: { orgId: "org-1", permissionMode: "standard" } }} />
			</QueryClientProvider>,
		);
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.approvalModes).not.toContain("default");
		expect(props.snapshot.settings?.approvalMode).toBe("auto");
		await props.onSend("go", [], "dropped-mode");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", mode: "standard",
		}, { idempotencyKey: "dropped-mode" });
	});

	it("ignores an approval mode an older build saved with the model catalog", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [], hasMore: false, nextAfter: 0 });
		cloudMocks.listChatModels.mockResolvedValue({
			models: [{ id: "claude-sonnet", displayName: "Claude Sonnet", default: true, efforts: ["medium", "high"] }],
			model: "claude-sonnet", reasoningEffort: "medium",
		});
		cloudMocks.sendSessionMessage.mockResolvedValue({ event: {} });
		const key = "cloud-chat-settings:org-1:session-1:claude-code";
		localStorage.setItem(key, JSON.stringify({ model: "claude-sonnet", reasoningEffort: "medium", approvalMode: "default" }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, provider: "claude-code", cloud: { orgId: "org-1", permissionMode: "trusted" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.listChatModels).toHaveBeenCalled());
		await waitFor(() => expect(localStorage.getItem(key)).not.toContain("approvalMode"));
		expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.settings?.approvalMode).toBe("bypass-permissions");
		await cloudMocks.chatProps.mock.lastCall?.[0].onSend("go", [], "legacy-default");
		expect(cloudMocks.sendSessionMessage).toHaveBeenLastCalledWith("org-1", session.id, {
			text: "go", model: "claude-sonnet", reasoningEffort: "medium", mode: "trusted",
		}, { idempotencyKey: "legacy-default" });
	});

	it("surfaces send errors and sends cancellation to the active turn", async () => {
		cloudMocks.listChatEvents.mockReset().mockResolvedValue({
			events: [
				{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: session.updatedAt },
				{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			],
			hasMore: false,
			nextAfter: 2,
		});
		cloudMocks.sendSessionMessage.mockReset().mockRejectedValue(new Error("send failed"));
		cloudMocks.cancelTurn.mockReset().mockRejectedValue(new Error("cancel failed"));
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].onInterrupt).toBeTypeOf("function"));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		expect(props.onSteer).toBeUndefined();
		await expect(props.onSend("hello", [], "message-1")).rejects.toThrow("send failed");
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBe("send failed"));
		cloudMocks.chatProps.mock.lastCall?.[0].onInterrupt();
		await waitFor(() => expect(cloudMocks.cancelTurn).toHaveBeenCalledWith("org-1", session.id, "turn-1"));
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].commandError).toBe("cancel failed"));
	});

	it("appends only newer events without duplicating replayed pages", () => {
		const event = (sequence: number): CloudCpClientEvent => ({
			sessionId: session.id,
			sequence,
			type: "chat.assistant_delta",
			payload: { text: String(sequence) },
			createdAt: session.updatedAt,
		});
		const existing = [event(1), event(500)];
		const merged = appendCloudEvents(existing, [event(500), event(501)]);
		expect(merged.map((item) => item.sequence)).toEqual([1, 500, 501]);
		expect(merged.at(-1)?.sequence).toBe(501);
	});

	it("continues a long history from its last sequence", async () => {
		const event = (sequence: number): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "chat.assistant_delta",
			payload: { text: String(sequence) }, createdAt: session.updatedAt,
		});
		const listChatEvents = vi.fn().mockResolvedValue({ events: [event(500), event(501)], hasMore: false, nextAfter: 501 });
		const events = await loadCloudChatEvents({ listChatEvents }, "org-1", session.id, [event(1), event(500)]);
		expect(listChatEvents).toHaveBeenCalledWith("org-1", session.id, { after: 500, limit: 500 });
		expect(events.map((item) => item.sequence)).toEqual([1, 500, 501]);
	});

	it("projects completed and interrupted turns without leaving output streaming", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "First", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.assistant_delta", payload: { text: "Done", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 4, type: "chat.turn_completed", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 5, type: "chat.user_message", payload: { text: "Second", turnId: "turn-2" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 6, type: "chat.turn_started", payload: { turnId: "turn-2" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 7, type: "chat.turn_interrupted", payload: { turnId: "turn-2" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns.map((turn) => turn.state)).toEqual(["completed", "interrupted"]);
		expect(snapshot.items).toContainEqual(expect.objectContaining({ role: "assistant", text: "Done", streaming: false }));
		expect(snapshot.controller.state).toBe("ready");
	});

	it("projects Codex tool activity and turn diffs beside the assistant reply", () => {
		const event = (sequence: number, type: string, payload: unknown): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type, payload, createdAt: session.updatedAt,
		});
		const snapshot = toSnapshot(session, [
			event(1, "chat.turn_started", { turnId: "turn-1" }),
			event(2, "chat.activity", { turnId: "turn-1", activity: { id: "cmd-1", kind: "command", status: "running", summary: "go test", detail: { command: "go test" } } }),
			event(3, "chat.activity", { turnId: "turn-1", activity: { id: "cmd-1", kind: "command", status: "completed", summary: "go test", detail: { command: "go test", output: "ok" } } }),
			event(4, "chat.activity", { turnId: "turn-1", activity: { id: "edit-1", kind: "file_change", status: "completed", summary: "Edited files", detail: { files: [{ path: "main.go", status: "modified", additions: 0, deletions: 0 }] } } }),
			event(5, "chat.activity", { turnId: "turn-1", activity: { id: "turn-diff", kind: "turn_diff", status: "completed", summary: "Changed files", detail: { diff: "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n" } } }),
			event(6, "chat.assistant_delta", { turnId: "turn-1", text: "Done" }),
			event(7, "chat.turn_completed", { turnId: "turn-1" }),
		]);
		expect(snapshot.items).toContainEqual(expect.objectContaining({ activityKind: "command", status: "completed", detail: expect.objectContaining({ output: "ok" }) }));
		expect(snapshot.items).toContainEqual(expect.objectContaining({ role: "assistant", text: "Done" }));
		expect(snapshot.items).toContainEqual(expect.objectContaining({ activityKind: "file_change", detail: expect.objectContaining({ files: [expect.objectContaining({ path: "main.go", patch: expect.stringContaining("+new") })] }) }));
		expect(snapshot.turns[0].diff?.files).toContainEqual(expect.objectContaining({ path: "main.go", additions: 1, deletions: 1 }));
	});

	it("omits Codex's already-stored stdin status from the visible reply", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Hello", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.assistant_delta", payload: { text: "Reading additional input from stdin...\n", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.assistant_delta", payload: { text: "Hello!", turnId: "turn-1" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.items.filter((item) => item.kind === "message" && item.role === "assistant"))
			.toEqual([expect.objectContaining({ text: "Hello!" })]);
	});

	it("shows the first Cloud send as active while the worker claims it", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Next", turnId: "turn-next" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns).toEqual([expect.objectContaining({ id: "turn-next", state: "running" })]);
		expect(snapshot.controller.state).toBe("busy");
	});

	it("shows the session starting, not working, while its Setup runs ahead of the first turn", async () => {
		const setupStep = (sequence: number, status: string): CloudCpClientEvent => ({
			sessionId: session.id, sequence, type: "workspace.script.step", createdAt: "2026-10-20T16:02:01Z",
			payload: { phase: "setup", runId: "run-1", step: 1, total: 1, status, command: "./setup.sh", repository: "", startedAt: "2026-10-20T16:02:01Z" },
		});
		const prompt: CloudCpClientEvent = { sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Run", turnId: "turn-1" }, createdAt: "2026-10-20T16:01:57Z" };
		// Setup running: the first prompt waits on it rather than being worked on.
		const during = toSnapshot(session, [prompt, setupStep(2, "running")]);
		expect(during.turns).toEqual([expect.objectContaining({ id: "turn-1", state: "queued" })]);
		expect(during.controller.state).toBe("ready");
		// Setup done: the turn is the active request again while the worker claims it.
		const after = toSnapshot(session, [prompt, setupStep(2, "running"), { ...setupStep(3, "succeeded"), createdAt: "2026-10-20T16:03:01Z" }]);
		expect(after.turns[0].state).toBe("running");
		// Its working time counts from Setup's end, not from the prompt.
		expect(after.turns[0].startedAt).toBe("2026-10-20T16:03:01Z");

		cloudMocks.listChatEvents.mockReset().mockResolvedValue({ events: [prompt, setupStep(2, "running")], hasMore: false, nextAfter: 2 });
		cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
		cloudMocks.chatProps.mockClear();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<CloudSessionChatSurface session={{ ...session, cloud: { orgId: "org-1" } }} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].session.provisionState).toBe("provisioning"));
		const props = cloudMocks.chatProps.mock.lastCall?.[0];
		// The startup checklist in place of a working timer, and nothing to stop. It is
		// the checklist a failed start shows, with Setup running.
		expect(props.session.provisionSteps).toEqual([
			{ id: "worktree", status: "done" },
			{ id: "setup", status: "running", startedAt: expect.any(String) },
			{ id: "agent", status: "pending" },
		]);
		expect(props.onInterrupt).toBeUndefined();
		expect(props.onSteer).toBeUndefined();
	});

	it("queues only later Cloud sends while the first awaits its worker", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "First", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.user_message", payload: { text: "Second", turnId: "turn-2" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns.map((turn) => turn.state)).toEqual(["running", "queued"]);
	});

	it("shows the checkout error when a queued turn fails", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Build", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_aborted", payload: { turnId: "turn-1", error: "Repository checkout failed. Reconnect GitHub." }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns).toEqual([expect.objectContaining({ state: "failed", errorMessage: "Repository checkout failed. Reconnect GitHub." })]);
		expect(snapshot.controller.state).toBe("ready");
	});

	it("keeps a second Cloud message queued behind an active turn", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "First", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.user_message", payload: { text: "Second", turnId: "turn-2" }, createdAt: session.updatedAt },
		];
		const snapshot = toSnapshot(session, events);
		expect(snapshot.turns.map((turn) => turn.state)).toEqual(["running", "queued"]);
	});

	it("projects a durable steer as activity on the active turn", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Build it", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_steered", payload: { turnId: "turn-1", text: "Prefer tests first", clientMessageId: "message-1" }, createdAt: session.updatedAt },
		];

		const snapshot = toSnapshot(session, events);

		expect(snapshot.items).toContainEqual(expect.objectContaining({
			kind: "activity",
			turnId: "turn-1",
			summary: "Steered: Prefer tests first",
			detail: { event: "steer", text: "Prefer tests first", origin: "human", clientMessageId: "message-1" },
		}));
	});

	it("shows the provider's exact pending approval and resolves it through Cloud", async () => {
		cloudMocks.listChatEvents.mockResolvedValue({ events: [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Edit", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.approval_requested", payload: {
				turnId: "turn-1", requestId: "approval-1", summary: "Apply file changes",
				decisions: [{ id: "allow-once", label: "Allow once", kind: "allow_once" }, { id: "reject", label: "Reject", kind: "reject_once" }],
			}, createdAt: session.updatedAt },
		], hasMore: false, nextAfter: 3 });
		cloudMocks.decideChatApproval.mockResolvedValue({ ok: true });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		render(<QueryClientProvider client={queryClient}>
			<CloudSessionChatSurface session={{ ...session, provider: "cursor", cloud: { orgId: "org-1", permissionMode: "standard" } }} />
		</QueryClientProvider>);
		await waitFor(() => expect(cloudMocks.chatProps.mock.lastCall?.[0].snapshot.items).toContainEqual(expect.objectContaining({
			activityKind: "approval", requestId: "approval-1", status: "pending",
			decisions: [
				{ id: "allow-once", label: "Allow once", kind: "allow_once" },
				{ id: "reject", label: "Reject", kind: "reject_once" },
			],
		})));
		cloudMocks.chatProps.mock.lastCall?.[0].onDecide("approval-1", "allow-once");
		await waitFor(() => expect(cloudMocks.decideChatApproval).toHaveBeenCalledWith("org-1", session.id, "approval-1", "allow-once"));
	});

	it("closes an unresolved approval when its turn ends", () => {
		const events: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.approval_requested", payload: {
				turnId: "turn-1", requestId: "approval-1", summary: "Run command", decisions: [{ id: "accept", label: "Accept" }],
			}, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 3, type: "chat.turn_aborted", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
		];
		expect(toSnapshot(session, events).items).toContainEqual(expect.objectContaining({
			requestId: "approval-1", status: "cancelled",
		}));
	});

	it("offers steering only after the active provider advertises it", () => {
		const base: CloudCpClientEvent[] = [
			{ sessionId: session.id, sequence: 1, type: "chat.user_message", payload: { text: "Build", turnId: "turn-1" }, createdAt: session.updatedAt },
			{ sessionId: session.id, sequence: 2, type: "chat.turn_started", payload: { turnId: "turn-1" }, createdAt: session.updatedAt },
		];
		expect(toSnapshot(session, base).capabilities).toEqual([]);
		expect(toSnapshot(session, [...base, {
			sessionId: session.id, sequence: 3, type: "chat.turn_capabilities", payload: { turnId: "turn-1", steering: true }, createdAt: session.updatedAt,
		}]).capabilities).toEqual(["steer"]);
	});
});
