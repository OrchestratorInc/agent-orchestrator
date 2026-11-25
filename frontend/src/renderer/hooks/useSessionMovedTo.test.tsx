import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useMoveStore } from "../lib/move-runner";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import type { WorkspaceSession } from "../types/workspace";
import { usePendingMove, useSessionMovedTo } from "./useSessionMovedTo";

const { navigate, start, state } = vi.hoisted(() => ({
	navigate: vi.fn(),
	start: vi.fn(),
	state: { local: [] as unknown[], remote: [] as unknown[], targets: [] as unknown[] },
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("../lib/host-clients", async (importOriginal) => ({ ...await importOriginal<typeof import("../lib/host-clients")>(), clientForSessionHost: () => ({}) }));
vi.mock("./useMoveSession", () => ({ useMoveTargets: () => ({ targets: state.targets, settled: true }), useMoveSession: () => ({ start }) }));
vi.mock("./useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: state.local }), useRemoteWorkspaces: () => ({ data: state.remote }) }));

const project = (id: string, hostId?: string) => ({ id, hostId, sessions: [{ id: "web-9" }] });
const movedToB = { movedTo: { hostId: "h_b", transferId: "tr1", label: "MacBook B", sessionId: "web-9" } };
const pendingToB = { pending: { hostId: "h_b", transferId: "tr1", label: "MacBook B", sessionId: "" } };

function render<T>(hook: (session: WorkspaceSession) => T, machineId: string, transfer: unknown, session: Partial<WorkspaceSession>) {
	const client = new QueryClient();
	client.setQueryData(["machine-id"], machineId);
	client.setQueryData(["session-moved-to", session.hostId ?? "local", "web-3", Boolean(session.isTerminated)], transfer);
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return renderHook(() => hook({ id: "web-3", workspaceId: "web", ...session } as WorkspaceSession), { wrapper }).result;
}

beforeEach(() => {
	vi.clearAllMocks();
	Object.assign(state, { local: [], remote: [], targets: [] });
});
afterEach(() => useMoveStore.setState({ moves: {}, shown: {} }));

it("leaves an unfinished move to the banner while the banner shows it", () => {
	const result = render(usePendingMove, "h_a", pendingToB, {});
	expect(result.current?.label).toBe("MacBook B");

	act(() => useMoveStore.setState({ moves: { "local:web-3": { phase: "failed", targetHostId: "h_b", targetProjectId: "web", targetLabel: "MacBook B" } } }));
	expect(result.current).toBeUndefined();

	act(() => useMoveStore.setState({ moves: {} }));
	expect(result.current?.label).toBe("MacBook B");
});

it("opens a moved session on the computer it moved to, not a local session with the same id", () => {
	state.local = [project("unrelated")];
	expect(render(useSessionMovedTo, "h_a", movedToB, { isTerminated: true }).current?.open).toBeUndefined();

	state.remote = [project("other", "h_c"), project("web", "h_b")];
	render(useSessionMovedTo, "h_a", movedToB, { isTerminated: true }).current?.open?.();
	expect(navigate).toHaveBeenCalledExactlyOnceWith(sessionNavigateTarget("web", "web-9", "h_b"));
});

it("opens a session that moved to this computer here", () => {
	state.local = [project("web")];
	state.remote = [project("unrelated", "h_a")];
	render(useSessionMovedTo, "h_b", movedToB, { hostId: "h_a", isTerminated: true }).current?.open?.();
	expect(navigate).toHaveBeenCalledExactlyOnceWith(sessionNavigateTarget("web", "web-9", "local"));
});

it("finishes a move to the recorded computer from either desktop", () => {
	const local = { hostId: "local", projectId: "web", label: "This computer" };
	const remote = { hostId: "h_b", projectId: "web", label: "MacBook B" };

	state.targets = [remote];
	render(usePendingMove, "h_a", pendingToB, {}).current?.finish?.();
	state.targets = [local];
	render(usePendingMove, "h_b", pendingToB, { hostId: "h_a" }).current?.finish?.();
	expect(start.mock.calls).toEqual([[remote], [local]]);

	// Another computer viewing A, with B not connected.
	expect(render(usePendingMove, "h_c", pendingToB, { hostId: "h_a" }).current?.finish).toBeUndefined();
});
