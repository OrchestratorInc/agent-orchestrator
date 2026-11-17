import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { useCloudScriptRuns } from "../../hooks/useCloudScriptRuns";
import { useCloudSessionLive } from "../../hooks/useCloudSessionLive";
import { useSessionScmSummary } from "../../hooks/useSessionScmSummary";
import { aoBridge } from "../../lib/bridge";
import type { WorkspaceSession } from "../../types/workspace";
import { CHAT_EVENTS_SAFETY_POLL_MS, CloudSessionChatSurface } from "./CloudSessionChatSurface";

const cloudMocks = vi.hoisted(() => ({
	listChatEvents: vi.fn(),
	listChatModels: vi.fn(),
	listUserAgentModels: vi.fn(),
	listSessionPullRequests: vi.fn(),
	resumeSession: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: cloudMocks, baseUrl: "https://cp.test", userId: "user-1" }),
}));
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.test" } }),
}));
vi.mock("../../lib/cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => cloudMocks,
}));
vi.mock("./ChatWorkspace", () => ({ ChatWorkspace: () => <div data-testid="cloud-chat" /> }));

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
	updatedAt: "2026-11-01T00:00:00Z",
	prs: [],
	cloud: { orgId: "org-1" },
} as WorkspaceSession;

/** The inspector's and the header's share of an open session view. */
function SessionViewParts() {
	useCloudSessionLive("org-1", session.id, true);
	useSessionScmSummary(session.id, true, "org-1");
	useCloudScriptRuns("org-1", session.id, { lifecycle: { desiredState: "running", observedState: "running" } });
	return null;
}

let streams: Array<{ path: string; streamId: string; closed: boolean; emit?: (event: CloudCpStreamEvent) => void }>;
let openFails = false;

/** The stream that carries the chat's events (not one for live state only). */
function chatStream() {
	return streams.find((stream) => stream.path.includes("after="));
}

function renderView(ui: ReactNode) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return { ...render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>), queryClient };
}

beforeEach(() => {
	localStorage.clear();
	streams = [];
	openFails = false;
	cloudMocks.listChatEvents.mockReset().mockResolvedValue({
		events: [{ sessionId: session.id, sequence: 3, type: "chat.user_message", payload: { text: "hi" }, createdAt: session.updatedAt }],
		hasMore: false,
		nextAfter: 3,
	});
	cloudMocks.listChatModels.mockReset().mockResolvedValue({ models: [] });
	cloudMocks.listUserAgentModels.mockReset().mockResolvedValue({ models: [] });
	cloudMocks.listSessionPullRequests.mockReset().mockResolvedValue({ sessionId: session.id, pullRequests: [] });
	cloudMocks.resumeSession.mockReset().mockResolvedValue({});
	vi.spyOn(aoBridge.cloudCp, "openStream").mockImplementation(async (init) => {
		if (openFails) throw new Error("CLOUD_CP_STREAM_ERROR 503 unavailable");
		const stream = { path: init.path, streamId: `stream-${streams.length + 1}`, closed: false };
		streams.push(stream);
		return { streamId: stream.streamId };
	});
	vi.spyOn(aoBridge.cloudCp, "closeStream").mockImplementation((streamId) => {
		const stream = streams.find((candidate) => candidate.streamId === streamId);
		if (stream) stream.closed = true;
	});
	vi.spyOn(aoBridge.cloudCp, "onStreamEvent").mockImplementation((streamId, listener) => {
		const stream = streams.find((candidate) => candidate.streamId === streamId);
		if (stream) stream.emit = listener;
		return () => undefined;
	});
});

afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

describe("CloudSessionChatSurface event stream", () => {
	it("drops to the safety-net poll as soon as the stream opens, with no event yet", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const view = renderView(<CloudSessionChatSurface session={session} />);
		await waitFor(() => expect(chatStream()).toBeDefined());
		expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(1);
		// An idle chat: the stream is open and sends nothing.
		await act(async () => { await vi.advanceTimersByTimeAsync(CHAT_EVENTS_SAFETY_POLL_MS - 1_000); });
		expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(1);
		await act(async () => { await vi.advanceTimersByTimeAsync(1_500); });
		expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(2);
		view.unmount();
	});

	it("keeps polling every second while the stream cannot open", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		openFails = true;
		const view = renderView(<CloudSessionChatSurface session={session} />);
		await waitFor(() => expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(1));
		await act(async () => { await vi.advanceTimersByTimeAsync(3_100); });
		expect(cloudMocks.listChatEvents.mock.calls.length).toBeGreaterThanOrEqual(4);
		view.unmount();
	});

	it("opens exactly one stream for a session view, resumed after the loaded chat, and closes it on leaving", async () => {
		const view = renderView(
			<>
				<CloudSessionChatSurface session={session} />
				<SessionViewParts />
			</>,
		);
		await waitFor(() => expect(streams).toHaveLength(1));
		expect(streams[0].path).toBe("/api/cloud/v1/orgs/org-1/sessions/session-1/events?after=3&live=1");
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(streams).toHaveLength(1);

		view.unmount();
		expect(streams[0].closed).toBe(true);
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(streams).toHaveLength(1);
	});

	it("reads the session's events once when a session view opens", async () => {
		const view = renderView(
			<>
				<SessionViewParts />
				<CloudSessionChatSurface session={session} />
			</>,
		);
		await waitFor(() => expect(cloudMocks.listChatEvents).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 100));
		expect(cloudMocks.listChatEvents).toHaveBeenCalledTimes(1);
		// The newest page first.
		expect(cloudMocks.listChatEvents).toHaveBeenCalledWith("org-1", session.id, { before: 9007199254740991, limit: 150 });
		view.unmount();
	});

	it("streams events into the chat's list", async () => {
		const view = renderView(<CloudSessionChatSurface session={session} />);
		await waitFor(() => expect(chatStream()?.emit).toBeDefined());
		const event = { sessionId: session.id, sequence: 4, type: "chat.assistant_delta", payload: { text: "hello", turnId: "t", itemId: "i" }, createdAt: session.updatedAt };
		act(() => chatStream()?.emit?.({ type: "chunk", data: `id: 4\ndata: ${JSON.stringify(event)}\n\n` }));
		await waitFor(() => expect(view.queryClient.getQueryData<Array<{ sequence: number }>>(["cloud-chat-events", "org-1", session.id])?.map((item) => item.sequence)).toEqual([3, 4]));
		view.unmount();
	});
});
