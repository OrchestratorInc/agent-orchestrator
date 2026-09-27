import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type CodexAccountsManagerSnapshot = components["schemas"]["AccountsManagerAccountsResponse"];
export type CodexAccountsManagerAccount = components["schemas"]["AccountsManagerAccountResponse"];

export const codexAccountsManagerQueryKey = ["accounts-manager", "codex"] as const;

export async function fetchCodexAccountsManager(): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.GET("/api/v1/accounts-manager/accounts");
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function addCodexAPIKey(key: string, label?: string, baseUrl?: string): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.POST("/api/v1/accounts-manager/accounts/api-key", { body: { provider: "codex", key, ...(label ? { label } : {}), ...(baseUrl ? { baseUrl } : {}) } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function importCodexCredential(filename: string, credential: unknown): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.POST("/api/v1/accounts-manager/accounts/import", { body: { provider: "codex", filename, credential } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function setCodexRouting(enabled: boolean, accountIds: string[]): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.PUT("/api/v1/accounts-manager/routing/{provider}", { params: { path: { provider: "codex" } }, body: { enabled, accountIds } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function setCodexManagedAccountDisabled(accountId: string, disabled: boolean): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.PATCH("/api/v1/accounts-manager/accounts/{accountId}", { params: { path: { accountId } }, body: { disabled } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function removeCodexManagedAccount(accountId: string): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.DELETE("/api/v1/accounts-manager/accounts/{accountId}", { params: { path: { accountId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function refreshCodexManagedAccount(accountId: string): Promise<CodexAccountsManagerSnapshot> {
	const { data, error } = await apiClient.POST("/api/v1/accounts-manager/accounts/{accountId}/refresh", { params: { path: { accountId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsManagerSnapshot;
}

export async function fetchCodexManagedModels(accountId: string): Promise<components["schemas"]["AccountsManagerModelsResponse"]> {
	const { data, error } = await apiClient.GET("/api/v1/accounts-manager/accounts/{accountId}/models", { params: { path: { accountId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as components["schemas"]["AccountsManagerModelsResponse"];
}

export async function fetchCodexManagedQuota(accountId: string): Promise<components["schemas"]["AccountsManagerQuotaResponse"]> {
	const { data, error } = await apiClient.GET("/api/v1/accounts-manager/accounts/{accountId}/quota", { params: { path: { accountId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as components["schemas"]["AccountsManagerQuotaResponse"];
}

export async function resetCodexManagedQuota(accountId: string): Promise<void> {
	const { error } = await apiClient.POST("/api/v1/accounts-manager/accounts/{accountId}/quota/reset", { params: { path: { accountId } } });
	if (error) throw new Error(apiErrorMessage(error));
}

export function useCodexAccountsManagerQuery(enabled = true) {
	return useQuery({ queryKey: codexAccountsManagerQueryKey, queryFn: fetchCodexAccountsManager, enabled, staleTime: 5_000, retry: 1 });
}

export function useInvalidateCodexAccountsManager() {
	const queryClient = useQueryClient();
	return () => queryClient.invalidateQueries({ queryKey: codexAccountsManagerQueryKey });
}
