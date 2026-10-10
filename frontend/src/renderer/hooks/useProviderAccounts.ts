import { queryOptions, useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type ProviderAccount = components["schemas"]["ProviderAccountView"];
export type ProviderAccounts = components["schemas"]["ProviderAccountsResponse"];
export type ProviderLogin = components["schemas"]["ProviderLoginResponse"];
export const providerAccountsKey = ["provider-accounts"] as const;
export const providerAccountsCatalogueKey = ["provider-accounts", "catalogue"] as const;
export function accountProvider(harness: string): string {
	return harness === "codex" ? "codex" : harness === "claude-code" ? "claude" : "";
}
// refresh asks the daemon to re-read native logins and every account's sign-in
// state now; use it when settings opens or the window regains focus.
export function providerAccountName(account: Pick<ProviderAccount, "displayName" | "provider">): string {
	return account.displayName || `${account.provider === "codex" ? "Codex" : "Claude"} account`;
}
// What is left of an account's shortest usage window, as a whole percentage:
// the one figure shown wherever an account is chosen. Null when not reported.
export function accountHeadroom(account: ProviderAccount): number | null {
	const window = account.usage?.status === "available" ? account.usage.windows?.[0] : undefined;
	return window ? Math.max(0, Math.min(100, Math.round(window.remainingFraction * 100))) : null;
}
// The model-catalogue scope for one explicitly chosen account. Keep in sync
// with ports.ModelCatalogAccountScope in the daemon.
export function accountModelScope(accountId: string): string {
	return `@account:${accountId}`;
}
export async function fetchProviderAccounts(includeUsage = true, refresh = false): Promise<ProviderAccounts> {
	const response = !includeUsage
		? await apiClient.GET("/api/v1/provider-accounts", { params: { query: { includeUsage: false } } })
		: refresh
			? await apiClient.GET("/api/v1/provider-accounts", { params: { query: { refresh: true } } })
			: await apiClient.GET("/api/v1/provider-accounts");
	const { data, error } = response;
	if (error) throw new Error(apiErrorMessage(error));
	return data!;
}
export function useProviderAccounts(enabled = true, includeUsage = true) {
	return useQuery({
		queryKey: includeUsage ? providerAccountsKey : providerAccountsCatalogueKey,
		queryFn: () => fetchProviderAccounts(includeUsage),
		enabled,
		refetchInterval: 30000,
		retry: 1,
	});
}
export async function changeProviderAccount(accountId: string, action: "primary" | "sign-out" | "remove", replacementPrimaryId?: string, moveExisting?: boolean): Promise<ProviderAccounts> {
	const params = { path: { accountId } };
	const body = { replacementPrimaryId };
	const primaryOptions = moveExisting === undefined ? { params } : { params, body: { moveExisting } };
	const result = action === "primary"
		? await apiClient.PUT("/api/v1/provider-accounts/{accountId}/primary", primaryOptions)
		: action === "sign-out"
			? await apiClient.POST("/api/v1/provider-accounts/{accountId}/sign-out", { params, body })
			: await apiClient.DELETE("/api/v1/provider-accounts/{accountId}", { params, body });
	if (result.error) throw new Error(apiErrorMessage(result.error));
	return result.data!;
}
// Lifts the helper's hold on an account, or renews its saved sign-in now.
export async function runAccountAction(accountId: string, action: "resume" | "refresh-sign-in"): Promise<ProviderAccounts> {
	const params = { path: { accountId } };
	const result = action === "resume"
		? await apiClient.POST("/api/v1/provider-accounts/{accountId}/resume", { params })
		: await apiClient.POST("/api/v1/provider-accounts/{accountId}/refresh-sign-in", { params });
	if (result.error) throw new Error(apiErrorMessage(result.error));
	return result.data!;
}
export type AccountResetOutcome = components["schemas"]["ProviderAccountResetResponse"]["outcome"];
// Spends one usage-limit reset. Only "reset" means one was spent; "unknown"
// means the provider never confirmed, so the caller must not try again blindly.
export async function spendAccountReset(accountId: string): Promise<AccountResetOutcome> {
	const { data, error } = await apiClient.POST("/api/v1/provider-accounts/{accountId}/reset", { params: { path: { accountId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data!.outcome;
}
export const sessionAccountKey = (sessionId: string) => ["session-provider-account", sessionId] as const;
// Which managed account a session runs on, if any.
export function sessionAccountQueryOptions(sessionId: string) {
	return queryOptions({
		queryKey: sessionAccountKey(sessionId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/provider-account", { params: { path: { sessionId } } });
			if (error) throw new Error(apiErrorMessage(error));
			return data!;
		},
	});
}
// Moves one session to another account; it takes effect on the session's next request.
export async function switchSessionAccount(sessionId: string, accountId: string): Promise<components["schemas"]["SessionProviderAccountResponse"]> {
	const { data, error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/provider-account", { params: { path: { sessionId } }, body: { accountId } });
	if (error) throw new Error(apiErrorMessage(error));
	return data!;
}
export async function renameProviderAccount(accountId: string, displayName: string): Promise<ProviderAccounts> {
	const result = await apiClient.PATCH("/api/v1/provider-accounts/{accountId}", { params: { path: { accountId } }, body: { displayName } });
	if (result.error) throw new Error(apiErrorMessage(result.error));
	return result.data!;
}
export type ProviderLoginInput = { provider: "codex" | "claude"; accountId?: string; mode?: "browser" | "device" | "import" | "api_key"; apiKey?: string; baseUrl?: string; label?: string; credentialJson?: string };
export async function startProviderLogin(input: ProviderLoginInput): Promise<ProviderLogin> {
	const body = Object.fromEntries(Object.entries(input).filter(([, value]) => value !== undefined && value !== ""));
	if (body.mode === "browser") delete body.mode;
	const { data, error } = await apiClient.POST("/api/v1/provider-accounts/login", { body: body as ProviderLoginInput });
	if (error) throw new Error(apiErrorMessage(error));
	return data!;
}
export async function fetchProviderLogin(loginId: string): Promise<ProviderLogin> {
	const { data, error } = await apiClient.GET("/api/v1/provider-accounts/login/{loginId}", { params: { path: { loginId } } });
	if (error) throw Object.assign(new Error(apiErrorMessage(error)), { code: error.code });
	return data!;
}
export async function cancelProviderLogin(loginId: string): Promise<void> {
	const { error } = await apiClient.DELETE("/api/v1/provider-accounts/login/{loginId}", { params: { path: { loginId } } });
	if (error) throw new Error(apiErrorMessage(error));
}
