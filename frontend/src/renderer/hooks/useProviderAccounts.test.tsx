import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { accountProvider, cancelProviderLogin, changeProviderAccount, fetchProviderAccounts, fetchProviderLogin, startProviderLogin, useProviderAccounts } from "./useProviderAccounts";

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), remove: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: api.get, POST: api.post, PUT: api.put, DELETE: api.remove }, apiErrorMessage: (error: { message: string }) => error.message }));
beforeEach(() => {
	vi.resetAllMocks();
});
afterEach(cleanup);
function queryWrapper() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
	function Wrapper({ children }: { children: ReactNode }) {
		return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	}
	return { client, Wrapper };
}
describe("provider identity and account API", () => {
	it("maps only the two supported native harnesses", () => {
		expect(accountProvider("codex")).toBe("codex");
		expect(accountProvider("claude-code")).toBe("claude");
		for (const harness of ["", "claude", "openai", "Codex", "fx", "cursor", "gemini", "claude-code-extra"]) {
			expect(accountProvider(harness)).toBe("");
		}
	});
	it("fetches the safe account catalogue from AO", async () => {
		const catalogue = { accounts: [], defaults: [], recoveryRequired: false };
		api.get.mockResolvedValue({ data: catalogue });
		expect(await fetchProviderAccounts()).toBe(catalogue);
		expect(api.get).toHaveBeenCalledWith("/api/v1/provider-accounts");
		expect(api.post).not.toHaveBeenCalled();
		expect(api.put).not.toHaveBeenCalled();
		expect(api.remove).not.toHaveBeenCalled();
	});
	it("reports the daemon catalogue error without manufacturing empty accounts", async () => {
		api.get.mockResolvedValue({ error: { message: "Account catalogue unavailable" } });
		await expect(fetchProviderAccounts()).rejects.toThrow("Account catalogue unavailable");
		expect(api.get).toHaveBeenCalledTimes(1);
	});
	it("can fetch the account catalogue without waiting for usage", async () => {
		const catalogue = { accounts: [], defaults: [], recoveryRequired: false };
		api.get.mockResolvedValue({ data: catalogue });
		expect(await fetchProviderAccounts(false)).toBe(catalogue);
		expect(api.get).toHaveBeenCalledWith("/api/v1/provider-accounts", { params: { query: { includeUsage: false } } });
	});
	it("changes a primary without any session reassignment body", async () => {
		const result = { accounts: [], defaults: [], recoveryRequired: false };
		api.put.mockResolvedValue({ data: result });
		expect(await changeProviderAccount("chosen-account", "primary")).toBe(result);
		expect(api.put).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/primary", { params: { path: { accountId: "chosen-account" } } });
		expect(api.post).not.toHaveBeenCalled();
		expect(api.remove).not.toHaveBeenCalled();
	});
	it("passes a chosen replacement only to sign-out", async () => {
		const result = { accounts: [], defaults: [], recoveryRequired: false };
		api.post.mockResolvedValue({ data: result });
		expect(await changeProviderAccount("old-primary", "sign-out", "new-primary")).toBe(result);
		expect(api.post).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/sign-out", { params: { path: { accountId: "old-primary" } }, body: { replacementPrimaryId: "new-primary" } });
		expect(api.put).not.toHaveBeenCalled();
		expect(api.remove).not.toHaveBeenCalled();
	});
	it("sends removal through DELETE with the exact replacement", async () => {
		const result = { accounts: [], defaults: [], recoveryRequired: true };
		api.remove.mockResolvedValue({ data: result });
		expect(await changeProviderAccount("old-primary", "remove", "replacement")).toBe(result);
		expect(api.remove).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}", { params: { path: { accountId: "old-primary" } }, body: { replacementPrimaryId: "replacement" } });
		expect(api.post).not.toHaveBeenCalled();
		expect(api.put).not.toHaveBeenCalled();
	});
	it("leaves replacement selection to AO when removing a secondary", async () => {
		api.remove.mockResolvedValue({ data: { accounts: [], defaults: [], recoveryRequired: false } });
		await changeProviderAccount("secondary", "remove");
		expect(api.remove).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}", { params: { path: { accountId: "secondary" } }, body: { replacementPrimaryId: undefined } });
	});
	for (const action of ["primary", "sign-out", "remove"] as const) {
		it(`preserves AO's refusal message for ${action}`, async () => {
			api.put.mockResolvedValue({ error: { message: "Wait until the affected sessions are idle" } });
			api.post.mockResolvedValue({ error: { message: "Wait until the affected sessions are idle" } });
			api.remove.mockResolvedValue({ error: { message: "Wait until the affected sessions are idle" } });
			await expect(changeProviderAccount("account", action)).rejects.toThrow("Wait until the affected sessions are idle");
			const totalCalls = api.put.mock.calls.length + api.post.mock.calls.length + api.remove.mock.calls.length;
			expect(totalCalls).toBe(1);
		});
	}
});
describe("browser login API", () => {
	it("starts a new login with no presumed native credential import", async () => {
		const login = { id: "attempt", provider: "codex", url: "https://provider.test/login", status: "waiting", accountId: "" };
		api.post.mockResolvedValue({ data: login });
		expect(await startProviderLogin({ provider: "codex" })).toBe(login);
		expect(api.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex" } });
		expect(api.get).not.toHaveBeenCalled();
	});
	it("re-signs into the chosen retained account identity", async () => {
		const login = { id: "attempt", provider: "claude", url: "https://provider.test/login", status: "waiting", accountId: "retained" };
		api.post.mockResolvedValue({ data: login });
		expect(await startProviderLogin({ provider: "claude", accountId: "retained" })).toBe(login);
		expect(api.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "claude", accountId: "retained" } });
	});
	it("polls the exact attempt without a raw OAuth state", async () => {
		const login = { id: "attempt", provider: "codex", url: "https://provider.test/login", status: "complete", accountId: "account" };
		api.get.mockResolvedValue({ data: login });
		expect(await fetchProviderLogin("attempt")).toBe(login);
		expect(api.get).toHaveBeenCalledWith("/api/v1/provider-accounts/login/{loginId}", { params: { path: { loginId: "attempt" } } });
		expect(api.post).not.toHaveBeenCalled();
	});
	it("cancels through AO without a provider password or callback code", async () => {
		api.remove.mockResolvedValue({});
		await expect(cancelProviderLogin("attempt")).resolves.toBeUndefined();
		expect(api.remove).toHaveBeenCalledWith("/api/v1/provider-accounts/login/{loginId}", { params: { path: { loginId: "attempt" } } });
		expect(api.get).not.toHaveBeenCalled();
		expect(api.post).not.toHaveBeenCalled();
	});
	it("preserves callback-port errors on start", async () => {
		api.post.mockResolvedValue({ error: { message: "Login callback port is in use" } });
		await expect(startProviderLogin({ provider: "codex" })).rejects.toThrow("Login callback port is in use");
		expect(api.post).toHaveBeenCalledTimes(1);
	});
	it("preserves an unknown-attempt error so the UI can ask for sign-in again", async () => {
		api.get.mockResolvedValue({ error: { message: "Login attempt not found; sign in again" } });
		await expect(fetchProviderLogin("gone-attempt")).rejects.toThrow("sign in again");
		expect(api.get).toHaveBeenCalledTimes(1);
	});
	it("does not pretend cancellation succeeded after an upstream refusal", async () => {
		api.remove.mockResolvedValue({ error: { message: "Unable to cancel login" } });
		await expect(cancelProviderLogin("attempt")).rejects.toThrow("Unable to cancel login");
		expect(api.remove).toHaveBeenCalledTimes(1);
	});
});
describe("catalogue query ownership", () => {
	it("does not call the local daemon when the caller disables account lookup", async () => {
		api.get.mockResolvedValue({ data: { accounts: [], defaults: [], recoveryRequired: false } });
		const { Wrapper } = queryWrapper();
		const { result } = renderHook(() => useProviderAccounts(false), { wrapper: Wrapper });
		expect(result.current.fetchStatus).toBe("idle");
		expect(api.get).not.toHaveBeenCalled();
		expect(result.current.data).toBeUndefined();
	});
	it("deduplicates the account inventory for multiple rendered controls", async () => {
		const catalogue = { accounts: [], defaults: [], recoveryRequired: false };
		api.get.mockResolvedValue({ data: catalogue });
		const { Wrapper, client } = queryWrapper();
		const { result } = renderHook(() => [useProviderAccounts(), useProviderAccounts()], { wrapper: Wrapper });
		await waitFor(() => expect(result.current.every(query => query.isSuccess)).toBe(true));
		expect(api.get).toHaveBeenCalledTimes(1);
		expect(result.current[0].data).toEqual(catalogue);
		expect(result.current[1].data).toEqual(catalogue);
		expect(client.getQueryData(["provider-accounts"])).toEqual(catalogue);
	});
});
