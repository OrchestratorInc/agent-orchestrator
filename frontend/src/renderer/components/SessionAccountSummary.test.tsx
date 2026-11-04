import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionAccountSummary } from "./SessionAccountSummary";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	clientForHost: () => ({ GET: getMock }),
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "request failed",
}));

vi.mock("../hooks/useSessionInterfaceTransition", () => ({
	interfaceTransitionIsActive: () => false,
	useSessionInterfaceTransitionStatus: () => ({}),
}));

const chatSession = { id: "s-1", provider: "claude-code", mode: "chat" } as unknown as WorkspaceSession;

function stubDaemon(accountHandoff: boolean) {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/settings" ? { accountHandoff }
			: path === "/api/v1/agents/accounts" ? { accounts: [{ id: "acct-a", provider: "claude", device: false, default: true, signedIn: true, email: "a@example.com" }] }
			: { claude: "acct-a", stayOnAccount: false },
	}));
}

function renderSummary(session: WorkspaceSession = chatSession) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><SessionAccountSummary session={session} /></QueryClientProvider>);
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
	{ name: "the handoff setting is off", accountHandoff: false, session: chatSession },
	{ name: "the session is not a Chat session", accountHandoff: true, session: { ...chatSession, mode: "tui" } as WorkspaceSession },
])("offers no pin when $name", async ({ accountHandoff, session }) => {
	stubDaemon(accountHandoff);
	renderSummary(session);
	await screen.findByTestId("session-account-summary");
	await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/settings"));
	expect(screen.queryByRole("switch", { name: "Stay on this account" })).not.toBeInTheDocument();
});
