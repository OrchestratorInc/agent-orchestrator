import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionAccountSummary } from "./SessionAccountSummary";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: vi.fn() },
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
	render(<QueryClientProvider client={queryClient}>
		<SessionAccountSummary session={session("s-cloud", { cloud: { orgId: "org-1" } })} />
		<SessionAccountSummary session={session("s-local")} />
	</QueryClientProvider>);

	await waitFor(() => {
		const paths = getMock.mock.calls.filter(([, options]) => sessionIdOf(options) === "s-local").map(([path]) => path);
		expect(paths).toEqual(expect.arrayContaining(["/api/v1/agents/accounts/sessions/{sessionId}", "/api/v1/sessions/{sessionId}/interface-transition"]));
	});
	expect(getMock.mock.calls.filter(([, options]) => sessionIdOf(options) === "s-cloud")).toEqual([]);
});

// A refresh the provider could not answer keeps the last reading; the summary
// says so beside the bars instead of showing old numbers as current.
it("warns beside the kept usage when the provider could not refresh it", async () => {
	const account = {
		id: "device-claude", provider: "claude", device: true, default: true, signedIn: true, email: "a@example.com",
		problem: "Claude usage is temporarily unavailable",
		limits: { fetchedAt: "2026-11-07T10:00:00Z", windows: [{ id: "five_hour", usedPercent: 42 }] },
	};
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts" ? { accounts: [account] }
			: path === "/api/v1/agents/accounts/sessions/{sessionId}" ? { claude: account.id } : {},
	}));
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={queryClient}><SessionAccountSummary session={session("s-warn")} /></QueryClientProvider>);

	expect(await screen.findByText("Claude usage is temporarily unavailable")).toBeInTheDocument();
	expect(screen.getByText(/58% left/)).toBeInTheDocument();
});
