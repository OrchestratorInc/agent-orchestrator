import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SessionActionsMenu } from "./SessionActionsMenu";
import { SessionProviderAccountMenuItem } from "./SessionProviderAccountMenuItem";

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: api.get, PUT: api.put },
	apiErrorMessage: (error: { message: string }) => error.message,
}));

const inventory = {
	accounts: [
		{ id: "alice", provider: "codex", displayName: "Harbor Codex", email: "alice@example.test", signedIn: true, primary: true, sessions: ["session-1"], usage: { status: "available", windows: [{ durationSeconds: 18000, remainingFraction: 0.62 }] } },
		{ id: "bob", provider: "codex", displayName: "Summit Codex", email: "bob@example.test", signedIn: true, primary: false, sessions: [], usage: { status: "available", windows: [{ durationSeconds: 18000, remainingFraction: 0.14 }] } },
	],
	defaults: [{ provider: "codex", primaryId: "alice", managed: true }],
	recoveryRequired: false,
};

function renderMenu() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><SessionActionsMenu><SessionProviderAccountMenuItem sessionId="session-1" /></SessionActionsMenu></QueryClientProvider>);
}

beforeEach(() => {
	vi.clearAllMocks();
	api.get.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts" ? { data: inventory } : { data: { managed: true, provider: "codex", accountId: "alice", loginRequired: false } });
	api.put.mockResolvedValue({ data: { managed: true, provider: "codex", accountId: "bob", loginRequired: false } });
});
afterEach(() => vi.restoreAllMocks());

describe("session account actions", () => {
	it("places account choices beside the other session actions", async () => {
		const user = userEvent.setup();
		renderMenu();
		await user.click(await screen.findByRole("button", { name: "Session actions" }));
		// The item names the session's current account before it is opened.
		const switchItem = await screen.findByRole("menuitem", { name: /^Account.*Harbor Codex/ });
		await user.hover(switchItem);
		await waitFor(() => expect(switchItem).toHaveAttribute("aria-expanded", "true"));
		// Each choice shows what is left of its shortest usage window.
		expect(await screen.findByRole("menuitem", { name: /Harbor Codex.*Default.*62% left/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Summit Codex.*14% left/ })).toBeInTheDocument();
		// How a switch is applied inside is not explained to the user.
		expect(screen.queryByText(/next request/)).toBeNull();
		expect(screen.getByRole("menuitem", { name: "Manage accounts" })).toBeInTheDocument();
	});

	it("switches the current session from the submenu without a separate toolbar", async () => {
		const user = userEvent.setup();
		renderMenu();
		await user.click(await screen.findByRole("button", { name: "Session actions" }));
		const switchItem = await screen.findByRole("menuitem", { name: /^Account/ });
		await user.hover(switchItem);
		await waitFor(() => expect(switchItem).toHaveAttribute("aria-expanded", "true"));
		fireEvent.click(await screen.findByRole("menuitem", { name: /Summit Codex/ }));
		await waitFor(() => expect(api.put).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/provider-account",
			{ params: { path: { sessionId: "session-1" } }, body: { accountId: "bob" } },
		));
	});

});
