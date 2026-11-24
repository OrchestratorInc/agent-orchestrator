import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { useMoveStore } from "../lib/move-runner";
import type { WorkspaceSession } from "../types/workspace";
import { usePendingMove } from "./useSessionMovedTo";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("./useMoveSession", () => ({ useMoveTargets: () => ({ targets: [], settled: true }), useMoveSession: () => undefined }));

afterEach(() => useMoveStore.setState({ moves: {}, shown: {} }));

it("leaves an unfinished move to the banner while the banner shows it", () => {
	const client = new QueryClient();
	client.setQueryData(["session-moved-to", "local", "web-3", false], { pending: { hostId: "host-b", transferId: "tr1", label: "MacBook B" } });
	const session = { id: "web-3", workspaceId: "web" } as WorkspaceSession;
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const { result } = renderHook(() => usePendingMove(session), { wrapper });
	expect(result.current?.label).toBe("MacBook B");

	act(() => useMoveStore.setState({ moves: { "local:web-3": { phase: "failed", targetHostId: "host-b", targetProjectId: "web", targetLabel: "MacBook B" } } }));
	expect(result.current).toBeUndefined();

	act(() => useMoveStore.setState({ moves: {} }));
	expect(result.current?.label).toBe("MacBook B");
});
