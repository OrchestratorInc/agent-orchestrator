import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../../i18n";
import type { CloudCpAgentAccount } from "../../../lib/cloud-cp";
import { TooltipProvider } from "../../ui/tooltip";
import { CloudAgentAccountsPanel } from "./CloudAgentAccountsPanel";

const cloud = vi.hoisted(() => ({
	listAgentAccounts: vi.fn(),
	makeAgentAccountDefault: vi.fn(),
	removeAgentAccount: vi.fn(),
	listUserProviderConnections: vi.fn(),
}));

vi.mock("../../../lib/bridge", () => ({
	aoBridge: { app: { openExternal: vi.fn() }, cloud: { connectProviderAuth: vi.fn(), cancelProviderAuth: vi.fn() } },
}));

vi.mock("../../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: cloud, ready: true, baseUrl: "https://cloud.example.test" }),
}));

const account = (overrides: Partial<CloudCpAgentAccount>): CloudCpAgentAccount => ({
	id: "a-1", agent: "codex", name: "", credentialType: "auth_json", isDefault: false, validationState: "valid",
	createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z", ...overrides,
});

function renderPanel(addFirst = false) {
	render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider><CloudAgentAccountsPanel agent="codex" addFirst={addFirst} /></TooltipProvider>
		</QueryClientProvider>,
	);
}

const row = (id: string) => document.querySelector(`[data-agent-account="${id}"]`) as HTMLElement;

describe("CloudAgentAccountsPanel", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		for (const mock of Object.values(cloud)) mock.mockReset();
		cloud.listUserProviderConnections.mockResolvedValue({ providerConnections: [] });
		cloud.listAgentAccounts.mockResolvedValue({ agentAccounts: [
			account({ id: "a-1", email: "work@example.com", plan: "pro", isDefault: true }),
			account({ id: "a-2", name: "Personal", email: "me@example.com" }),
			account({ id: "a-3", credentialType: "api_key" }),
			account({ id: "a-4", email: "old@example.com", validationState: "invalid", invalidReason: "The login was revoked." }),
			account({ id: "c-1", agent: "claude-code", email: "claude@example.com" }),
		] });
	});

	it("lists the agent's accounts as the local Account tab does", async () => {
		renderPanel();
		await screen.findByText("work@example.com");
		expect(within(row("a-1")).getByText("Pro plan")).toBeInTheDocument();
		expect(within(row("a-1")).getByText("Default for new tasks")).toBeInTheDocument();
		expect(within(row("a-2")).getByText("Personal")).toBeInTheDocument();
		expect(within(row("a-2")).getByText("me@example.com")).toBeInTheDocument();
		expect(within(row("a-3")).getByText("Codex API key")).toBeInTheDocument();
		expect(within(row("a-3")).getByText("Logged in without a subscription")).toBeInTheDocument();
		expect(within(row("a-4")).getByText("The login was revoked.")).toBeInTheDocument();
		expect(within(row("a-4")).getByRole("button", { name: "Sign in again" })).toBeInTheDocument();
		expect(within(row("a-4")).queryByRole("button", { name: "Make default" })).toBeNull();
		expect(screen.queryByText("claude@example.com")).toBeNull();
	});

	it("makes an account the default and removes one after asking", async () => {
		cloud.makeAgentAccountDefault.mockResolvedValue({ agentAccount: account({ id: "a-2", isDefault: true }) });
		cloud.removeAgentAccount.mockRejectedValueOnce(new Error("A live session runs on this account."));
		const user = userEvent.setup();
		renderPanel();
		await screen.findByText("work@example.com");

		await user.click(within(row("a-2")).getByRole("button", { name: "Make default" }));
		expect(cloud.makeAgentAccountDefault).toHaveBeenCalledWith("a-2");
		await waitFor(() => expect(cloud.listAgentAccounts).toHaveBeenCalledTimes(2));

		await user.click(within(row("a-3")).getByRole("button", { name: "Remove account" }));
		const dialog = await screen.findByRole("dialog");
		await user.click(within(dialog).getByRole("button", { name: "Remove account" }));
		expect(cloud.removeAgentAccount).toHaveBeenCalledWith("a-3");
		expect(await within(dialog).findByText("A live session runs on this account.")).toBeInTheDocument();
	});

	it("opens on adding an account while the agent has none", async () => {
		cloud.listAgentAccounts.mockResolvedValue({ agentAccounts: [] });
		renderPanel(true);
		expect(await screen.findByTestId("cloud-harness-login")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Log in with ChatGPT" })).toBeInTheDocument();
	});
});
