import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { MoveError } from "../lib/host-move";
import { useMoveStore } from "../lib/move-runner";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { useMoveSession } from "./useMoveSession";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/host-clients")>(),
	// No client for an offline host.
	clientForSessionHost: (hostId?: string) => {
		if (hostId) throw new Error(`Host ${hostId} is not connected`);
		return {};
	},
}));

afterEach(() => useMoveStore.setState({ moves: {}, shown: {} }));

it("says an offline destination is not reachable and leaves an unfinished move as it was", () => {
	const showGlobalToast = vi.fn();
	useUiStore.setState({ showGlobalToast });
	const failed = { phase: "failed" as const, targetHostId: "host-b", targetProjectId: "web", targetLabel: "MacBook B", error: new MoveError("MacBook B is not reachable", "receive", false, "RECEIVE_UNCONFIRMED", "tr1") };
	useMoveStore.setState({ moves: { "local:web-3": failed }, shown: {} });
	const session = { id: "web-3", title: "Osprey notes", workspaceId: "web" } as WorkspaceSession;
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
	const { result } = renderHook(() => useMoveSession(session), { wrapper });

	result.current?.retry();

	expect(showGlobalToast).toHaveBeenCalledWith("Couldn't move Osprey notes", "MacBook B is not reachable", "error");
	expect(useMoveStore.getState().moves["local:web-3"]).toEqual(failed);
});
