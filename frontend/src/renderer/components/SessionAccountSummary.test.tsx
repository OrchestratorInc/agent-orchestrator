import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionAccountSummary, useSessionAccountSwitch } from "./SessionAccountSummary";

const { getMock, putMock, hostsAsked } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), hostsAsked: [] as Array<string | undefined> }));

vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: vi.fn(), PUT: putMock },
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	// Trusted, so a local session's move status is read; the cloud one must not be.
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => {},
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "Request failed",
}));

vi.mock("../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/host-clients")>(),
	clientForSessionHost: (hostId?: string) => {
		hostsAsked.push(hostId);
		return { GET: getMock, PUT: putMock };
	},
}));

vi.mock("../hooks/useCloudCp", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useCloudCp")>(),
	useCloudCp: () => ({ client: {}, ready: false, baseUrl: "" }),
}));

const session = (id: string, overrides: Partial<WorkspaceSession> = {}) =>
	({ id, provider: "claude-code", isTerminated: false, ...overrides }) as WorkspaceSession;

const sessionIdOf = (options: unknown) => (options as { params?: { path?: { sessionId?: string } } } | undefined)?.params?.path?.sessionId;

// A cloud session has no row in the local daemon: its summary is hidden and
// asks the local daemon for neither its account nor a move.
it("asks the local daemon about a local session's account, never a cloud session's", async () => {
	getMock.mockImplementation((path: string) => Promise.resolve({ data: path === "/api/v1/agents/accounts" ? { accounts: [] } : {} }));
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const { unmount } = render(<QueryClientProvider client={queryClient}>
		<SessionAccountSummary session={session("s-cloud", { cloud: { orgId: "org-1" } })} />
		<SessionAccountSummary session={session("s-local")} />
	</QueryClientProvider>);

	await waitFor(() => {
		const paths = getMock.mock.calls.filter(([, options]) => sessionIdOf(options) === "s-local").map(([path]) => path);
		expect(paths).toEqual(expect.arrayContaining(["/api/v1/agents/accounts/sessions/{sessionId}", "/api/v1/sessions/{sessionId}/interface-transition"]));
	});
	expect(getMock.mock.calls.filter(([, options]) => sessionIdOf(options) === "s-cloud")).toEqual([]);
	// Let every query settle and React Query deliver its batched updates (on a
	// timer) while the DOM still exists: one landing after the environment is
	// torn down throws "window is not defined".
	await waitFor(() => expect(queryClient.isFetching()).toBe(0));
	unmount();
	queryClient.clear();
	await new Promise((resolve) => setTimeout(resolve, 0));
});

it("asks a remote session's own host about its account", async () => {
	hostsAsked.length = 0;
	getMock.mockImplementation((path: string) => Promise.resolve({ data: path === "/api/v1/agents/accounts" ? { accounts: [] } : {} }));
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={queryClient}>
		<SessionAccountSummary session={session("s-remote", { hostId: "box-a" })} />
	</QueryClientProvider>);

	await waitFor(() => expect(hostsAsked.filter((host) => host === "box-a").length).toBeGreaterThanOrEqual(3));
	expect(hostsAsked.every((host) => host === "box-a")).toBe(true);
});

it("keeps a rejected move's reason so the composer can show it", async () => {
	getMock.mockImplementation((path: string) => Promise.resolve({ data:
		path === "/api/v1/agents/accounts" ? { accounts: [
			{ id: "acct-a", provider: "claude", email: "a@example.com", signedIn: true },
			{ id: "acct-b", provider: "claude", email: "b@example.com", signedIn: true },
		] }
		: path === "/api/v1/agents/accounts/sessions/{sessionId}" ? { claude: "acct-a" }
		: {} }));
	putMock.mockResolvedValue({ error: new Error("The account is signed out; sign it in again first") });
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const { result, unmount } = renderHook(() => useSessionAccountSwitch(session("s-move")), {
		wrapper: ({ children }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
	});

	await waitFor(() => expect(result.current.choice?.value).toBe("acct-a"));
	act(() => result.current.choice?.onPick("acct-b"));
	await waitFor(() => expect(result.current.error).toBe("The account is signed out; sign it in again first"));
	expect(putMock).toHaveBeenCalledWith("/api/v1/agents/accounts/sessions/{sessionId}", expect.objectContaining({ body: { accountId: "acct-b" } }));
	act(() => result.current.clearError());
	expect(result.current.error).toBe("");

	await waitFor(() => expect(queryClient.isFetching()).toBe(0));
	unmount();
	queryClient.clear();
	await new Promise((resolve) => setTimeout(resolve, 0));
});
