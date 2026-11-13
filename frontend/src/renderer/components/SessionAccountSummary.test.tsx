import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionAccountSummary } from "./SessionAccountSummary";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));

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
	clientForSessionHost: () => ({ GET: getMock }),
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

const chatSession = session("s-1", { mode: "chat" } as Partial<WorkspaceSession>);

function stubDaemon(accountHandoff: boolean) {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/settings" ? { accountHandoff }
			: path === "/api/v1/agents/accounts" ? { accounts: [{ id: "acct-a", provider: "claude", device: false, default: true, signedIn: true, email: "a@example.com" }] }
			: path === "/api/v1/agents/accounts/sessions/{sessionId}" ? { claude: "acct-a", stayOnAccount: false }
			: {},
	}));
}

function renderSummary(target: WorkspaceSession = chatSession) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><SessionAccountSummary session={target} /></QueryClientProvider>);
}

beforeEach(() => {
	getMock.mockReset();
	putMock.mockReset().mockResolvedValue({ data: { claude: "acct-a", stayOnAccount: true } });
});

it("pins a Chat session to its account through the daemon", async () => {
	stubDaemon(true);
	const user = userEvent.setup();
	renderSummary();
	const stay = await screen.findByRole("switch", { name: "Stay on this account" });
	await waitFor(() => expect(stay).toBeEnabled());
	expect(stay).not.toBeChecked();

	await user.click(stay);

	expect(putMock).toHaveBeenCalledWith("/api/v1/agents/accounts/sessions/{sessionId}/stay", {
		params: { path: { sessionId: "s-1" } }, body: { stay: true },
	});
	await waitFor(() => expect(stay).toBeChecked());
});

it.each([
	{ name: "the handoff setting is off", accountHandoff: false, target: chatSession },
	{ name: "the session is not a Chat session", accountHandoff: true, target: session("s-1", { mode: "tui" } as Partial<WorkspaceSession>) },
])("offers no pin when $name", async ({ accountHandoff, target }) => {
	stubDaemon(accountHandoff);
	renderSummary(target);
	await screen.findByTestId("session-account-summary");
	await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/settings", { signal: expect.any(AbortSignal) }));
	expect(screen.queryByRole("switch", { name: "Stay on this account" })).not.toBeInTheDocument();
});
