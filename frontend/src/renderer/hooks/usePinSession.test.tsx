import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const h = vi.hoisted(() => ({ setSessionPinned: vi.fn() }));
vi.mock("../lib/cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({ setSessionPinned: h.setSessionPinned }),
}));

import { usePinSession, useUnpinSession } from "./usePinSession";
import { toCloudWorkspaceSession } from "./useWorkspaceQuery";

const cloudKey = ["cloud-sessions", "https://cp.test", "org-1"];
const session = { id: "s-1", hostId: undefined, cloud: { orgId: "org-1" } } as unknown as WorkspaceSession;

function setup() {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	queryClient.setQueryData(["settings"], { cloudControlPlaneUrl: "https://cp.test" });
	queryClient.setQueryData(cloudKey, [{ id: "s-1", displayName: "One" }]);
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	return { queryClient, wrapper };
}

describe("Cloud session pins", () => {
	beforeEach(() => { h.setSessionPinned.mockReset(); });

	it("pins on the control plane and moves the row before it answers", async () => {
		const { queryClient, wrapper } = setup();
		let answer!: () => void;
		h.setSessionPinned.mockReturnValue(new Promise<void>((resolve) => { answer = resolve; }));
		const { result } = renderHook(() => usePinSession(), { wrapper });
		act(() => result.current.mutate(session));
		await waitFor(() => expect(queryClient.getQueryData<Array<{ pinned?: boolean }>>(cloudKey)?.[0].pinned).toBe(true));
		expect(h.setSessionPinned).toHaveBeenCalledWith("org-1", "s-1", true);
		await act(async () => answer());
	});

	it("puts the row back when the control plane refuses an unpin", async () => {
		const { queryClient, wrapper } = setup();
		queryClient.setQueryData(cloudKey, [{ id: "s-1", displayName: "One", pinned: true, pinnedAt: "2026-10-01T00:00:00Z" }]);
		h.setSessionPinned.mockRejectedValue(new Error("refused"));
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		const { result } = renderHook(() => useUnpinSession(), { wrapper });
		await act(async () => {
			await expect(result.current.mutateAsync(session)).rejects.toThrow("refused");
		});
		expect(queryClient.getQueryData(cloudKey)).toEqual([{ id: "s-1", displayName: "One", pinned: true, pinnedAt: "2026-10-01T00:00:00Z" }]);
	});

	it("maps the caller's pin onto the session", () => {
		const mapped = toCloudWorkspaceSession({
			id: "s-1", orgId: "org-1", projectId: "p-1", kind: "worker", harness: "codex", displayName: "One", branch: "", mode: "trusted",
			interfaceMode: "chat", deniedCommands: [], activityState: "idle", status: "running", runtimeConnected: true, isTerminated: false,
			prs: [], pinned: true, pinnedAt: "2026-10-01T00:00:00Z", createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
		}, { id: "p-1", displayName: "Project" }, "org-1");
		expect(mapped).toMatchObject({ isPinned: true, pinnedAt: "2026-10-01T00:00:00Z" });
	});
});
