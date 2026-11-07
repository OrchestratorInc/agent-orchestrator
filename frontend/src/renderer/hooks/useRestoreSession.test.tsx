import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpSession } from "../lib/cloud-cp";
import { useTerminalResetStore } from "../stores/terminal-reset-store";
import { cloudSessionsQueryKey, workspaceQueryKey } from "./useWorkspaceQuery";
import { settingsQueryKey } from "./useSettings";
import { useRestoreSession } from "./useRestoreSession";

const { restoreSessionMock } = vi.hoisted(() => ({ restoreSessionMock: vi.fn() }));

vi.mock("./useCloudCp", () => ({
	createRendererCloudCpClient: () => ({ restoreSession: restoreSessionMock }),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: vi.fn() },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));
vi.mock("../lib/host-clients", () => ({ clientForHost: vi.fn() }));
vi.mock("../lib/bridge", () => ({
	aoBridge: { notifications: { show: vi.fn() } },
}));
vi.mock("../lib/session-management-telemetry", () => ({
	recordDirectWorkerInteraction: vi.fn(),
}));

function wrapper(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
}

function cloudSession(overrides: Partial<CloudCpSession> = {}): CloudCpSession {
	return {
		id: "sess-chat",
		orgId: "org-1",
		projectId: "project-1",
		kind: "worker",
		harness: "codex",
		displayName: "Cloud chat",
		branch: "ao/cloud-chat",
		mode: "standard",
		interfaceMode: "chat",
		deniedCommands: [],
		activityState: "idle",
		status: "terminated",
		runtimeConnected: true,
		isTerminated: true,
		prs: [],
		workerEpoch: 3762,
		createdAt: "2026-10-13T21:00:00Z",
		updatedAt: "2026-10-13T21:47:59Z",
		...overrides,
	};
}

function createClient() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	queryClient.setQueryData(
		[...cloudSessionsQueryKey, "https://cloud.example.test", "org-1"],
		[cloudSession()],
	);
	queryClient.setQueryData(settingsQueryKey, { cloudControlPlaneUrl: "https://cloud.example.test" });
	return queryClient;
}

describe("useRestoreSession", () => {
	beforeEach(() => {
		restoreSessionMock.mockReset();
		useTerminalResetStore.setState({ baselineEpoch: {}, nonces: {}, reconnecting: {} });
	});

	it("records the restore baseline and preserves a fresh terminal factory nonce for a later Chat-to-TUI switch", async () => {
		restoreSessionMock.mockResolvedValue({ session: cloudSession({ workerEpoch: 3763 }) });
		const queryClient = createClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue(undefined);
		const { result } = renderHook(() => useRestoreSession(), { wrapper: wrapper(queryClient) });

		const restoreResult = await act(async () => result.current("sess-chat"));

		expect(restoreResult).toEqual({ status: "success" });
		expect(restoreSessionMock).toHaveBeenCalledWith("org-1", "sess-chat");
		expect(invalidate).toHaveBeenCalledWith({ queryKey: cloudSessionsQueryKey });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
		expect(useTerminalResetStore.getState()).toMatchObject({
			baselineEpoch: { "sess-chat": 3762 },
			nonces: { "sess-chat": 1 },
			reconnecting: { "sess-chat": true },
		});
	});

	it("does not leave reconnect pending when the Cloud restore fails", async () => {
		restoreSessionMock.mockRejectedValue(new Error("restore rejected"));
		const queryClient = createClient();
		const { result } = renderHook(() => useRestoreSession(), { wrapper: wrapper(queryClient) });

		const restoreResult = await act(async () => result.current("sess-chat"));

		expect(restoreResult).toEqual({ status: "error", message: "restore rejected" });
		expect(useTerminalResetStore.getState()).toMatchObject({
			baselineEpoch: {},
			nonces: {},
			reconnecting: {},
		});
	});
});
