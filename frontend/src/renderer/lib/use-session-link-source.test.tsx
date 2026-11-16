import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useSessionLinkSource } from "./use-session-link-navigation";

const mocks = vi.hoisted(() => ({ org: vi.fn(), projects: vi.fn(), sessions: vi.fn() }));

vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => mocks.org() }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	toCloudWorkspace: vi.fn(),
	useCloudProjectsQuery: () => mocks.projects(),
	useCloudSessionsQuery: () => mocks.sessions(),
	useWorkspaceQuery: vi.fn(),
}));
vi.mock("./navigate-to-session", () => ({ useNavigateToSession: vi.fn() }));

function wrapperFor(client: QueryClient) {
	return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useSessionLinkSource", () => {
	beforeEach(() => {
		mocks.projects.mockReturnValue({ data: undefined, isLoading: false, isSuccess: false, isError: false });
		mocks.sessions.mockReturnValue({ data: undefined, isLoading: false, isSuccess: false, isError: false });
	});

	it("does not stay loading for a remote host that has no query", () => {
		const { result } = renderHook(() => useSessionLinkSource("box-a"), { wrapper: wrapperFor(new QueryClient()) });
		expect(result.current).toMatchObject({ isLoading: false, ready: false, workspaces: [] });
	});

	it("reports loading only while a remote fetch is running", () => {
		const client = new QueryClient();
		const query = client.getQueryCache().build(client, { queryKey: remoteWorkspaceQueryKey("box-a") });
		const wrapper = wrapperFor(client);
		expect(renderHook(() => useSessionLinkSource("box-a"), { wrapper }).result.current.isLoading).toBe(false);
		query.setState({ fetchStatus: "fetching" });
		expect(renderHook(() => useSessionLinkSource("box-a"), { wrapper }).result.current.isLoading).toBe(true);
	});

	it("does not stay loading for a signed-out Cloud source", () => {
		mocks.org.mockReturnValue({ org: undefined, ready: false });
		const { result } = renderHook(() => useSessionLinkSource(undefined, "cloud"), { wrapper: wrapperFor(new QueryClient()) });
		expect(result.current).toMatchObject({ isLoading: false, ready: false });
	});
});
