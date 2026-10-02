import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountsManagerQueryKey } from "../../hooks/useAccountsManagerQuery";
import { codexAccountsQueryKey } from "../../hooks/useCodexAccountsQuery";
import { GlobalSettingsForm } from "../GlobalSettingsForm";
import { TooltipProvider } from "../ui/tooltip";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() }));
vi.mock("../../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../../lib/api-client")>(),
	apiClient: api,
	getApiBaseUrl: () => null,
}));

const capability = { state: "supported", reasonCode: "supported", reason: "Available" };
const deviceAccount = {
	id: "device-account", label: "Device account", source: "managed", status: "valid", active: true, authMethod: "chatgpt",
	authentication: { state: "authorized", freshness: "fresh", reasonCode: "authorized", reason: "Signed in" },
	capacity: { state: "available", freshness: "fresh", plan: "pro", remainingPercent: 80, usedPercent: 20, reasonCode: "capacity_available", additionalBuckets: [], overall: { primary: { usedPercent: 20, windowDurationMinutes: 300 }, reached: "not_reached" }, resetCredits: { availableCount: 1 } },
};
const native = { accountRevision: 1, activeAccountId: deviceAccount.id, accounts: [deviceAccount], capabilities: { nativeLogin: capability, globalSwitch: capability, resetCreditConsume: capability }, deviceReconciliation: { status: "verified", activeAccountVerified: true, retryable: false } };
const managed = { revision: 1, availability: "ready", stale: false, accounts: [{ id: "session-account", provider: "codex", label: "Session account", kind: "oauth", generation: 1, status: "active", verification: "verified", disabled: false, unavailable: false, quotaSupported: true, cooldowns: [] }], oauthSessions: [], routing: [{ provider: "codex", enabled: false, accountIds: [] }] };
const quota = { observedAt: "2026-10-02T00:00:00Z", groups: [{ buckets: [{ window: "five_hour", remainingFraction: 0.6 }] }] };

beforeEach(() => {
	window.localStorage.clear();
	api.GET.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents/codex/accounts") return { data: native };
		if (path === "/api/v1/accounts-manager/accounts") return { data: managed };
		if (path.endsWith("/quota")) return { data: quota };
		return { data: {} };
	});
	api.POST.mockReset().mockResolvedValue({ data: native });
	api.PUT.mockReset().mockResolvedValue({ data: { ...managed, revision: 2 } });
	api.DELETE.mockReset();
});

function renderAccounts() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><TooltipProvider><GlobalSettingsForm section="accounts" /></TooltipProvider></QueryClientProvider>);
	return client;
}

describe("the single Accounts settings surface", () => {

	it("disables permanent removal through Accounts while retaining impact and recovery access", async () => {
		api.GET.mockImplementation(async (path: string) => {
			if (path.endsWith("removal-impact")) return { data: { accountId: "session-account", revision: 7, sessions: [] }, response: new Response(null, { status: 200 }) };
			return { data: path === "/api/v1/agents/codex/accounts" ? native : managed };
		});
		const client = renderAccounts();
		await screen.findByText("Session account");
		await userEvent.click(screen.getByRole("button", { name: "More account actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Remove account" }));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		const submit = within(screen.getByRole("dialog")).getByRole("button", { name: "Remove account" });
		expect(submit).toBeDisabled();
		expect(screen.getByText("No sessions are using this account.")).toBeInTheDocument();
		fireEvent.click(submit);
		expect(api.POST).not.toHaveBeenCalled();
		expect(api.DELETE).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		await userEvent.click(screen.getByText("Removal recovery (0 saved)"));
		expect(screen.getByRole("button", { name: "Inspect removal operation" })).toBeDisabled();
		expect(screen.getByText("Device account")).toBeInTheDocument();
		expect(screen.getByText("Session account")).toBeInTheDocument();
	});
	it("retains device plan, quota and confirmed reset actions without changing managed account state", async () => {
		const client = renderAccounts();
		await screen.findByText("Device account");
		expect(await screen.findByText("Session account")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: /^Device account/ }));
		expect(await screen.findByText("Pro plan")).toBeInTheDocument();
		expect(screen.getByRole("progressbar", { name: /80%/ })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Use reset" }));
		expect(api.POST.mock.calls.some(([path]) => path.endsWith("reset-credit/consume"))).toBe(false);
		await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Use reset" }));
		await waitFor(() => expect(api.POST).toHaveBeenCalledWith("/api/v1/agents/codex/accounts/{accountId}/reset-credit/consume", { params: { path: { accountId: "device-account" } }, body: { idempotencyKey: expect.any(String) } }));
		expect(client.getQueryData(accountsManagerQueryKey)).toEqual(managed);
	});

	it("keeps managed quota refresh, defaults and native inventory on the same page", async () => {
		const client = renderAccounts();
		await screen.findByText("Device account");
		await userEvent.click(await screen.findByRole("button", { name: /Session account/ }));
		expect(await screen.findByRole("progressbar", { name: "60% remaining" })).toBeInTheDocument();
		const reads = api.GET.mock.calls.filter(([path]) => path.endsWith("/quota")).length;
		await userEvent.click(screen.getByRole("button", { name: "Refresh usage" }));
		await waitFor(() => expect(api.GET.mock.calls.filter(([path]) => path.endsWith("/quota")).length).toBeGreaterThan(reads));
		await userEvent.click(screen.getByRole("button", { name: "Use as default" }));
		await waitFor(() => expect(api.PUT).toHaveBeenCalled());
		expect(client.getQueryData(codexAccountsQueryKey)).toEqual(native);
		expect(api.DELETE).not.toHaveBeenCalled();
	});

	it("does not hide device subscriptions when routed inventory is unavailable", async () => {
		api.GET.mockImplementation(async (path: string) => ({ data: path === "/api/v1/agents/codex/accounts" ? native : { ...managed, availability: "unavailable", stale: true } }));
		renderAccounts();
		expect(await screen.findByText("Device account")).toBeInTheDocument();
		expect(await screen.findByRole("button", { name: "Use as default" })).toBeDisabled();
	});
});
