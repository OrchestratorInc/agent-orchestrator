import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CLAUDE_CONNECTORS_URL, HarnessConnectorsTab } from "./HarnessConnectorsTab";

const h = vi.hoisted(() => ({
	accounts: [] as Array<Record<string, unknown>>,
	fetchConnectors: vi.fn(),
	openExternal: vi.fn(async () => undefined),
}));

vi.mock("../../../hooks/useAgentAccounts", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../../hooks/useAgentAccounts")>()),
	useAgentAccounts: () => ({ data: h.accounts, isPending: false }),
	fetchAgentAccountConnectors: h.fetchConnectors,
}));
vi.mock("../../../lib/bridge", () => ({ aoBridge: { app: { openExternal: h.openExternal } } }));

const claudeAccount = (overrides: Record<string, unknown>) => ({
	id: "device-claude", provider: "claude", device: true, default: true, kind: "subscription",
	signedIn: true, loggedIn: true, email: "me@example.com", ...overrides,
});

const renderTab = (onOpenAccount = vi.fn()) => {
	render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<HarnessConnectorsTab onOpenAccount={onOpenAccount} />
		</QueryClientProvider>,
	);
	return onOpenAccount;
};

afterEach(() => {
	h.accounts = [];
	h.fetchConnectors.mockReset();
	h.openExternal.mockClear();
});

describe("HarnessConnectorsTab", () => {
	it("lists the default account's claude.ai connectors with their status", async () => {
		h.accounts = [claudeAccount({})];
		h.fetchConnectors.mockResolvedValue({
			accountId: "device-claude",
			checkedAt: "2026-11-27T10:00:00Z",
			connectors: [
				{ name: "Gmail", status: "connected" },
				{ name: "AI Producer", status: "needs_auth" },
			],
		});
		renderTab();
		expect(await screen.findByText("Gmail")).toBeInTheDocument();
		expect(screen.getByText("Connected")).toBeInTheDocument();
		expect(screen.getByText("Needs sign-in")).toBeInTheDocument();
		expect(h.fetchConnectors).toHaveBeenCalledWith("device-claude", false, undefined);
	});

	it("explains that a setup token account cannot load connectors", async () => {
		h.accounts = [claudeAccount({ id: "token", device: false, kind: "setup_token" })];
		h.fetchConnectors.mockResolvedValue({ accountId: "token", unavailable: "setup_token", connectors: [] });
		const onOpenAccount = renderTab();
		expect(await screen.findByText("Not available with a setup token")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Check again/ })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Open Account" }));
		expect(onOpenAccount).toHaveBeenCalled();
	});

	it("reads the list again on Check again and opens claude.ai to manage connectors", async () => {
		h.accounts = [claudeAccount({})];
		h.fetchConnectors.mockResolvedValue({ accountId: "device-claude", connectors: [{ name: "Gmail", status: "connected" }] });
		renderTab();
		await userEvent.click(await screen.findByRole("button", { name: /Check again/ }));
		await waitFor(() => expect(h.fetchConnectors).toHaveBeenCalledWith("device-claude", true, undefined));
		await userEvent.click(screen.getByRole("button", { name: /Manage on claude\.ai/ }));
		expect(h.openExternal).toHaveBeenCalledWith(CLAUDE_CONNECTORS_URL);
	});

	it("says when an account has no connectors", async () => {
		h.accounts = [claudeAccount({})];
		h.fetchConnectors.mockResolvedValue({ accountId: "device-claude", connectors: [] });
		renderTab();
		expect(await screen.findByText("No connectors on this account")).toBeInTheDocument();
	});
});
