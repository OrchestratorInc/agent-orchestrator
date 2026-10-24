import { useEffect } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { codexAccountsPollPaused, noteCodexAccountsAvailable, noteCodexAccountsUnavailable } from "./codex-accounts-pause";
import { codexAccountsQueryKey, mergeCodexAccounts, writeCodexAccounts } from "./codex-accounts-state";

export { codexAccountsPollPaused, noteCodexAccountsAvailable } from "./codex-accounts-pause";

export { codexAccountsQueryKey } from "./codex-accounts-state";

export type CodexAccountsResponse = components["schemas"]["CodexAccountsResponse"];
export type CodexAccount = components["schemas"]["CodexAccountResponse"];
export type CodexAccountLoginOperation = components["schemas"]["CodexAccountLoginResponse"];
export type CodexAccountLoginTerminalStart = components["schemas"]["OpenCodexAccountLoginTerminalResponse"];
export type CodexActiveLogin = components["schemas"]["CodexActiveLoginResponse"];
export type CodexAccountSwitch = components["schemas"]["CodexAccountSwitchResponse"];

const CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE = "CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE";
const CODEX_ENSURE_BACKOFF_CODES = new Set([
	CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE,
	"CODEX_ACCOUNT_SETUP_INCOMPLETE",
]);

function readApiErrorCode(error: unknown): string | undefined {
	try {
		return apiErrorCode(error);
	} catch {
		return undefined;
	}
}

function codexAccountManagementUnavailable(error: unknown): boolean {
	return readApiErrorCode(error) === CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE;
}

function codexEnsureShouldBackOff(error: unknown): boolean {
	const code = readApiErrorCode(error);
	return code !== undefined && CODEX_ENSURE_BACKOFF_CODES.has(code);
}

function throwCodexRequest(error: unknown): never {
	const err = new Error(apiErrorMessage(error)) as Error & { code?: string };
	const code = readApiErrorCode(error);
	if (code) err.code = code;
	throw err;
}

export async function fetchCodexAccounts(): Promise<CodexAccountsResponse> {
	const { data, error } = await apiClient.GET("/api/v1/agents/codex/accounts");
	if (error) throwCodexRequest(error);
	noteCodexAccountsAvailable();
	return data as CodexAccountsResponse;
}

export type EnsureCodexAccountsOptions = {
	includeUsage?: boolean;
	forceAuthentication?: boolean;
	forceDeviceReconciliation?: boolean;
};

export async function ensureCodexAccounts(accountIds: string[] = [], options: EnsureCodexAccountsOptions = {}): Promise<CodexAccountsResponse> {
	const body = {
		accountIds,
		includeUsage: options.includeUsage ?? false,
		...(options.forceAuthentication ? { forceAuthentication: true } : {}),
		...(options.forceDeviceReconciliation ? { forceDeviceReconciliation: true } : {}),
	};
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/ensure", { body });
	if (error) throwCodexRequest(error);
	noteCodexAccountsAvailable();
	return data as CodexAccountsResponse;
}

export async function consumeCodexAccountResetCredit(accountId: string, idempotencyKey: string): Promise<CodexAccountsResponse> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/{accountId}/reset-credit/consume", {
		params: { path: { accountId } },
		body: { idempotencyKey },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsResponse;
}

export async function openCodexAccountLoginTerminal(): Promise<CodexAccountLoginTerminalStart> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/login-terminal");
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountLoginTerminalStart;
}

export async function openCodexAccountReauthenticationTerminal(accountId: string): Promise<CodexAccountLoginTerminalStart> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/{accountId}/login-terminal", {
		params: { path: { accountId } },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountLoginTerminalStart;
}

export async function logoutCodexAccount(accountId: string): Promise<CodexAccountsResponse> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/{accountId}/logout", {
		params: { path: { accountId } },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsResponse;
}

export async function deleteCodexAccount(accountId: string): Promise<CodexAccountsResponse> {
	const { data, error } = await apiClient.DELETE("/api/v1/agents/codex/accounts/{accountId}", {
		params: { path: { accountId } },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountsResponse;
}

export async function verifyCodexAccountLogin(operationId: string): Promise<CodexAccountLoginOperation> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/login-operations/{operationId}/verify", { params: { path: { operationId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountLoginOperation;
}

export async function cancelCodexAccountLogin(operationId: string): Promise<CodexAccountLoginOperation> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/accounts/login-operations/{operationId}/cancel", { params: { path: { operationId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountLoginOperation;
}

export async function startCodexAccountSwitch(targetAccountId: string, idempotencyKey: string): Promise<CodexAccountSwitch> {
	const { data, error } = await apiClient.POST("/api/v1/agents/codex/account-switches", { body: { targetAccountId, idempotencyKey } });
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountSwitch;
}

export async function fetchCodexAccountSwitch(switchId: string): Promise<CodexAccountSwitch> {
	const { data, error } = await apiClient.GET("/api/v1/agents/codex/account-switches/{switchId}", {
		params: { path: { switchId } },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data as CodexAccountSwitch;
}

export const codexAccountsQueryOptions = {
	queryKey: codexAccountsQueryKey,
	queryFn: async ({ client }: { client: QueryClient }) => {
		const incoming = await fetchCodexAccounts();
		return mergeCodexAccounts(client.getQueryData<CodexAccountsResponse>(codexAccountsQueryKey), incoming, "replace");
	},
	retry: (failureCount: number, error: unknown) => failureCount < 1 && !codexAccountManagementUnavailable(error),
	staleTime: Number.POSITIVE_INFINITY,
};
export function useCodexAccountsQuery(enabled = true) { return useQuery({ ...codexAccountsQueryOptions, enabled }); }

export function useEnsureCodexAccounts(): void {
	const queryClient = useQueryClient();
	useEffect(() => {
		let active = true;
		const ensure = () => {
			if (codexAccountsPollPaused()) return;
			const cached = queryClient.getQueryData(codexAccountsQueryKey);
			const ready = cached ? Promise.resolve() : queryClient.fetchQuery(codexAccountsQueryOptions).then(() => undefined).catch(() => undefined);
			void ready.then(() => ensureCodexAccounts()).then((next) => { if (active) writeCodexAccounts(queryClient, next, "replace"); }).catch((error: unknown) => {
				if (codexEnsureShouldBackOff(error)) noteCodexAccountsUnavailable();
			});
		};
		const stopBaseUrl = subscribeApiBaseUrl(() => {
			if (getApiBaseUrl()) noteCodexAccountsAvailable();
		});
		const onFocus = () => ensure();
		const onVisibility = () => { if (document.visibilityState === "visible") ensure(); };
		window.addEventListener("focus", onFocus); document.addEventListener("visibilitychange", onVisibility);
		return () => { active = false; stopBaseUrl(); window.removeEventListener("focus", onFocus); document.removeEventListener("visibilitychange", onVisibility); };
	}, [queryClient]);
}
