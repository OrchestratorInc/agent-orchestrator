import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { AgentReadinessSnapshot } from "./useAgentReadinessQuery";
import { sessionInterfaceTransitionQueryKey } from "./useSessionInterfaceTransition";

export type AgentAccount = components["schemas"]["AgentAccountResponse"];
export type AgentAccountProvider = AgentAccount["provider"];
export type AgentAccountLimitWindow = components["schemas"]["AgentAccountLimitWindow"];
export type AgentAccountLogin = components["schemas"]["StartAgentAccountLoginResponse"];
export type AgentAccountUsageRow = components["schemas"]["AgentAccountUsageRow"];

export const agentAccountsQueryKey = ["agent-accounts"] as const;
const usageQueryKey = ["agent-accounts", "usage"] as const;
const sessionAccountsKey = (sessionId: string) => ["agent-accounts", "session", sessionId] as const;

/** The account a harness signs in with, or null when it has none. */
export function agentAccountProviderFor(harness: string | undefined): AgentAccountProvider | null {
	if (harness === "claude-code") return "claude";
	if (harness === "codex") return "codex";
	return null;
}

/** New sessions start on a saved account, so readiness is that account's
 * sign-in; the CLI's own login is read from its row in the accounts list. */
export function followsSavedAccount(agent: AgentReadinessSnapshot | undefined): boolean {
	return Boolean(agent?.authentication.diagnostics?.some((step) => step.code === "saved_default_account"));
}

function unwrap<T>({ data, error }: { data?: T; error?: unknown }): T {
	if (error) throw new Error(apiErrorMessage(error));
	return data as T;
}

export async function fetchAgentAccounts(refresh = false) {
	return unwrap(await apiClient.GET("/api/v1/agents/accounts", { params: { query: refresh ? { refresh: true } : {} } }))?.accounts ?? [];
}

/** Every account with its limits. The daemon reuses a reading for five minutes. */
export function useAgentAccounts(enabled = true) {
	return useQuery({ queryKey: agentAccountsQueryKey, queryFn: () => fetchAgentAccounts(), refetchInterval: 60_000, enabled });
}

export const agentAccountProviderNames: Record<AgentAccountProvider, string> = { claude: "Claude Code", codex: "Codex" };

type AccountNameT = (key: "settings.agentAccounts.cliLoginOf" | "settings.agentAccounts.oauthToken", options?: { provider: string }) => string;

/** An account's name: its email. A Claude login without one is a
 * CLAUDE_CODE_OAUTH_TOKEN login, which reports no identity. */
export function agentAccountName(account: Pick<AgentAccount, "email" | "provider">, t: AccountNameT): string {
	if (account.email) return account.email;
	return account.provider === "claude"
		? t("settings.agentAccounts.oauthToken")
		: t("settings.agentAccounts.cliLoginOf", { provider: agentAccountProviderNames[account.provider] });
}

/** Names an account by id in status text, such as "Moved from … to …". */
export function useAgentAccountLabel(enabled = true) {
	const { t } = useTranslation();
	const accounts = useAgentAccounts(enabled);
	return (id: string | undefined) => {
		const account = accounts.data?.find((candidate) => candidate.id === id);
		return account ? agentAccountName(account, t) : t("settings.agentAccounts.removedAccount");
	};
}

export function useAgentAccountUsage(days = 30) {
	return useQuery({
		queryKey: [...usageQueryKey, days],
		queryFn: async () => unwrap(await apiClient.GET("/api/v1/agents/accounts/usage", { params: { query: { days } } }))?.usage ?? [],
		refetchInterval: 60_000,
	});
}

export function useSessionAgentAccounts(sessionId: string | undefined) {
	return useQuery({
		queryKey: sessionAccountsKey(sessionId ?? ""),
		enabled: Boolean(sessionId),
		queryFn: async () => unwrap(await apiClient.GET("/api/v1/agents/accounts/sessions/{sessionId}", { params: { path: { sessionId: sessionId ?? "" } } })) ?? {},
	});
}

export function useAgentAccountActions() {
	const queryClient = useQueryClient();
	const setAccounts = (accounts: AgentAccount[]) => queryClient.setQueryData(agentAccountsQueryKey, accounts);
	return {
		refresh: async () => setAccounts(await fetchAgentAccounts(true)),
		startLogin: async (provider: AgentAccountProvider) =>
			unwrap(await apiClient.POST("/api/v1/agents/accounts/login", { body: { provider } })),
		/** Signs a saved account in again in its own folder; verify and cancel are the same as a new sign-in. */
		signInAgain: async (accountId: string) =>
			unwrap(await apiClient.POST("/api/v1/agents/accounts/{accountId}/login", { params: { path: { accountId } } })),
		verifyLogin: async (operationId: string) => {
			const result = unwrap(await apiClient.POST("/api/v1/agents/accounts/login/{operationId}/verify", { params: { path: { operationId } } }));
			if (result.status === "complete") await queryClient.invalidateQueries({ queryKey: agentAccountsQueryKey });
			return result;
		},
		cancelLogin: async (operationId: string) => {
			const { error } = await apiClient.POST("/api/v1/agents/accounts/login/{operationId}/cancel", { params: { path: { operationId } } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		makeDefault: async (accountId: string) =>
			setAccounts(unwrap(await apiClient.POST("/api/v1/agents/accounts/{accountId}/default", { params: { path: { accountId } } })).accounts),
		remove: async (accountId: string) =>
			setAccounts(unwrap(await apiClient.DELETE("/api/v1/agents/accounts/{accountId}", { params: { path: { accountId } } })).accounts),
		useReset: async (accountId: string) => {
			const result = unwrap(await apiClient.POST("/api/v1/agents/accounts/{accountId}/reset-credit", {
				params: { path: { accountId } }, body: { idempotencyKey: crypto.randomUUID() },
			}));
			await queryClient.invalidateQueries({ queryKey: agentAccountsQueryKey });
			return result.outcome;
		},
		/** Starts the durable move; the shared interface-transition status shows its progress. */
		switchSession: async (sessionId: string, accountId: string, stopTurn = false) => {
			const result = unwrap(await apiClient.PUT("/api/v1/agents/accounts/sessions/{sessionId}", {
				params: { path: { sessionId } }, body: { accountId, ...(stopTurn ? { stopTurn: true } : {}) },
			}));
			await queryClient.invalidateQueries({ queryKey: sessionInterfaceTransitionQueryKey(sessionId) });
			return result.transition;
		},
		refreshSession: (sessionId: string) => queryClient.invalidateQueries({ queryKey: sessionAccountsKey(sessionId) }),
	};
}
