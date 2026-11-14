import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../main/cloud-cp-proxy";
import type { CloudCpSession, CloudCpSessionLive } from "../lib/cloud-cp";
import { subscribeCloudSessionEvents } from "../lib/cloud-cp/session-events-hub";
import type { CloudCpStreamBridge } from "../lib/cloud-cp/stream-bridge";
import { cloudOrchestratorModel, liveCloudOrchestrator, useCloudOrchestratorModel } from "./useCloudOrchestratorModel";
import { cloudSessionsQueryKey } from "./useWorkspaceQuery";

const BASE_URL = "https://cloud.example.test";

function session(overrides: Partial<CloudCpSession>): CloudCpSession {
	return {
		id: "orch-1",
		orgId: "org-1",
		projectId: "project-1",
		kind: "orchestrator",
		harness: "claude-code",
		model: "opus",
		status: "working",
		isTerminated: false,
		createdAt: "2026-11-01T00:00:00Z",
		...overrides,
	} as CloudCpSession;
}

describe("liveCloudOrchestrator", () => {
	it("picks the project's newest running orchestrator", () => {
		const sessions = [
			session({ id: "old", createdAt: "2026-10-01T00:00:00Z" }),
			session({ id: "new", createdAt: "2026-11-02T00:00:00Z" }),
			session({ id: "stopped", createdAt: "2026-11-03T00:00:00Z", isTerminated: true, status: "terminated" }),
			session({ id: "worker", kind: "worker", createdAt: "2026-11-04T00:00:00Z" }),
			session({ id: "elsewhere", projectId: "project-2", createdAt: "2026-11-05T00:00:00Z" }),
		];
		expect(liveCloudOrchestrator(sessions, "project-1")?.id).toBe("new");
		expect(liveCloudOrchestrator(sessions, "project-3")).toBeUndefined();
		expect(liveCloudOrchestrator(undefined, "project-1")).toBeUndefined();
	});
});

describe("cloudOrchestratorModel", () => {
	it("reads the model and effort the control plane says the session runs now", () => {
		expect(cloudOrchestratorModel(session({ currentModel: "sonnet", currentReasoningEffort: "high" }), undefined))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet", reasoningEffort: "high" });
	});

	it("reads an empty currentModel as the agent's default, without falling back to the model the session started with", () => {
		expect(cloudOrchestratorModel(session({ model: "opus", currentModel: "" }), undefined)).toBeUndefined();
		expect(cloudOrchestratorModel(session({ model: "opus", currentModel: "", currentReasoningEffort: "high" }), { reasoningEffort: "low" })).toBeUndefined();
	});

	it("falls back to the model the session started with on a control plane that predates currentModel", () => {
		const older = session({});
		expect(Object.prototype.hasOwnProperty.call(older, "currentModel")).toBe(false);
		expect(cloudOrchestratorModel(older, undefined)).toEqual({ sessionId: "orch-1", harness: "claude-code", model: "opus" });
		// The agent's own default names no model.
		expect(cloudOrchestratorModel(session({ model: "default" }), undefined)).toBeUndefined();
		expect(cloudOrchestratorModel(session({ model: "" }), {})).toBeUndefined();
	});

	it("lets the live Chat settings win, a field left unset keeping the session's", () => {
		const orchestrator = session({ currentModel: "sonnet", currentReasoningEffort: "high" });
		expect(cloudOrchestratorModel(orchestrator, { model: "haiku", reasoningEffort: "low" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "haiku", reasoningEffort: "low" });
		// A pick of only an effort keeps the model the session runs now.
		expect(cloudOrchestratorModel(orchestrator, { reasoningEffort: "low" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet", reasoningEffort: "low" });
		expect(cloudOrchestratorModel(orchestrator, { model: "sonnet" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet", reasoningEffort: "high" });
		expect(cloudOrchestratorModel(orchestrator, {}))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet", reasoningEffort: "high" });
	});

	it("keeps an effort with the model it was chosen for: a live model other than the session's takes none of its effort", () => {
		const orchestrator = session({ currentModel: "sonnet", currentReasoningEffort: "high" });
		expect(cloudOrchestratorModel(orchestrator, { model: "haiku" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "haiku" });
		expect(cloudOrchestratorModel(orchestrator, { model: "haiku", reasoningEffort: "low" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "haiku", reasoningEffort: "low" });
	});

	it("reads a live \"default\" as the agent's own: no model, or no effort", () => {
		const orchestrator = session({ currentModel: "sonnet", currentReasoningEffort: "high" });
		expect(cloudOrchestratorModel(orchestrator, { model: "default", reasoningEffort: "low" })).toBeUndefined();
		expect(cloudOrchestratorModel(orchestrator, { reasoningEffort: "default" }))
			.toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet" });
	});
});

function fakeBridge() {
	const listeners = new Map<string, (event: CloudCpStreamEvent) => void>();
	let next = 0;
	const bridge = {
		openStream: vi.fn(async (_init: { path: string }) => ({ streamId: `stream-${++next}` })),
		closeStream: vi.fn((streamId: string) => listeners.delete(streamId)),
		onStreamEvent: vi.fn((streamId: string, listener: (event: CloudCpStreamEvent) => void) => {
			listeners.set(streamId, listener);
			return () => listeners.delete(streamId);
		}),
		emit: (streamId: string, event: CloudCpStreamEvent) => listeners.get(streamId)?.(event),
	};
	return bridge satisfies CloudCpStreamBridge;
}

describe("useCloudOrchestratorModel", () => {
	const settingsKey = "cloud-chat-settings:org-1:orch-1:claude-code";
	const cleanups: Array<() => void> = [];
	afterEach(() => {
		for (const cleanup of cleanups.splice(0)) cleanup();
		window.localStorage.removeItem(settingsKey);
	});

	function renderWith(sessions: CloudCpSession[] | undefined, enabled = true) {
		const queryClient = new QueryClient();
		if (sessions) queryClient.setQueryData([...cloudSessionsQueryKey, BASE_URL, "org-1"], sessions);
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		const renders = { count: 0 };
		const rendered = renderHook(() => {
			renders.count += 1;
			return useCloudOrchestratorModel({ baseUrl: BASE_URL, orgId: "org-1", projectId: "project-1", enabled });
		}, { wrapper });
		return { ...rendered, queryClient, renders };
	}

	it("reads what the orchestrator runs from the cached session list, not this computer's saved Chat settings", () => {
		window.localStorage.setItem(settingsKey, JSON.stringify({ model: "haiku", reasoningEffort: "low" }));
		const { result } = renderWith([session({ currentModel: "sonnet", currentReasoningEffort: "high" })]);
		expect(result.current).toEqual({ sessionId: "orch-1", harness: "claude-code", model: "sonnet", reasoningEffort: "high" });
	});

	it("follows the session list as it changes", () => {
		const { result, queryClient } = renderWith([session({ currentModel: "sonnet" })]);
		expect(result.current?.model).toBe("sonnet");
		act(() => queryClient.setQueryData([...cloudSessionsQueryKey, BASE_URL, "org-1"], [session({ currentModel: "haiku" })]));
		expect(result.current?.model).toBe("haiku");
	});

	it("takes the orchestrator's live Chat settings while its Chat is open in this window", async () => {
		const bridge = fakeBridge();
		// The orchestrator's Chat, open elsewhere in the app, follows its live state.
		cleanups.push(subscribeCloudSessionEvents({ baseUrl: BASE_URL, orgId: "org-1", sessionId: "orch-1" }, { live: true }, { bridge }));
		const { result } = renderWith([session({ currentModel: "sonnet", currentReasoningEffort: "high" })]);
		await waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1));
		const live: CloudCpSessionLive = {
			type: "session.live", viewers: [], composer: { holder: null, epoch: 0, waitlist: [] },
			canTakeOver: false, you: "user-1", chatSettings: { model: "haiku", reasoningEffort: "low" }, canConfigure: true,
		};
		act(() => bridge.emit("stream-1", { type: "chunk", data: `event: session.live\ndata: ${JSON.stringify(live)}\n\n` }));
		await waitFor(() => expect(result.current).toEqual({ sessionId: "orch-1", harness: "claude-code", model: "haiku", reasoningEffort: "low" }));
		// The hook itself opened nothing.
		expect(bridge.openStream).toHaveBeenCalledTimes(1);
	});

	it("re-renders for nothing but a change to the model or effort it reports", async () => {
		const bridge = fakeBridge();
		cleanups.push(subscribeCloudSessionEvents({ baseUrl: BASE_URL, orgId: "org-1", sessionId: "orch-1" }, { live: true }, { bridge }));
		const { result, renders, queryClient } = renderWith([session({ currentModel: "sonnet", currentReasoningEffort: "high" })]);
		await waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1));
		const live = (overrides: Partial<CloudCpSessionLive>): CloudCpStreamEvent => ({
			type: "chunk",
			data: `event: session.live\ndata: ${JSON.stringify({
				type: "session.live", viewers: [], composer: { holder: null, epoch: 0, waitlist: [] },
				canTakeOver: false, you: "user-1", chatSettings: { model: "haiku" }, canConfigure: true, ...overrides,
			})}\n\n`,
		});
		act(() => bridge.emit("stream-1", live({})));
		await waitFor(() => expect(result.current?.model).toBe("haiku"));
		const answer = result.current;
		const rendered = renders.count;

		// Someone opens the Chat and takes the composer: the settings read here are unchanged.
		act(() => bridge.emit("stream-1", live({
			viewers: [{ userId: "user-2", name: "Ada", role: "member" }],
			composer: { holder: { userId: "user-2", name: "Ada" }, epoch: 1, waitlist: [] },
		})));
		await new Promise((resolve) => setTimeout(resolve, 5));
		expect(renders.count).toBe(rendered);
		expect(result.current).toBe(answer);

		// A poll that changes nothing it reports keeps the same answer.
		act(() => queryClient.setQueryData([...cloudSessionsQueryKey, BASE_URL, "org-1"], [session({ currentModel: "sonnet", currentReasoningEffort: "high", status: "idle" })]));
		expect(result.current).toBe(answer);
	});

	it("names none until the session list has loaded, and none outside Cloud", () => {
		expect(renderWith(undefined).result.current).toBeUndefined();
		expect(renderWith([session({ currentModel: "sonnet" })], false).result.current).toBeUndefined();
	});
});
