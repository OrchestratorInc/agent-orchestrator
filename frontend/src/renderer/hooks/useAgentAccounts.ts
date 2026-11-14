import { apiErrorException } from "../lib/api-error-exception";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import type { AgentReadinessSnapshot } from "./useAgentReadinessQuery";
import { sessionInterfaceTransitionQueryKey } from "./useSessionInterfaceTransition";

export type AgentAccount = components["schemas"]["AgentAccountResponse"];
export type AgentAccountProvider = AgentAccount["provider"];
export type AgentAccountLimitWindow = components["schemas"]["AgentAccountLimitWindow"];
export type AgentAccountLogin = components["schemas"]["StartAgentAccountLoginResponse"];
export type AgentAccountUsageRow = components["schemas"]["AgentAccountUsageRow"];

export const agentAccountsQueryKey = ["agent-accounts"] as const;
const remoteHost = (hostId?: HostId) => (hostId && hostId !== LOCAL_HOST ? hostId : undefined);
export const agentAccountsQueryKeyFor = (hostId?: HostId) => {
	const host = remoteHost(hostId);
	return host ? [...agentAccountsQueryKey, "host", host] : agentAccountsQueryKey;
};
const usageQueryKey = (hostId?: HostId) => [...agentAccountsQueryKeyFor(hostId), "usage"];
const sessionAccountsKey = (sessionId: string, hostId?: HostId) => [...agentAccountsQueryKeyFor(hostId), "session", sessionId];
const client = (hostId?: HostId) => clientForSessionHost(remoteHost(hostId));

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
	if (error) throw apiErrorException(error);
	return data as T;
}

export async function fetchAgentAccounts(refresh = false, hostId?: HostId) {
	return unwrap(await client(hostId).GET("/api/v1/agents/accounts", { params: { query: refresh ? { refresh: true } : {} } }))?.accounts ?? [];
}

/** Every account with its limits. The daemon reuses a reading for five minutes. */
export function useAgentAccounts(enabled = true, hostId?: HostId) {
	return useQuery({ queryKey: agentAccountsQueryKeyFor(hostId), queryFn: () => fetchAgentAccounts(false, hostId), refetchInterval: 60_000, enabled });
}

export const agentAccountProviderNames: Record<AgentAccountProvider, string> = { claude: "Claude Code", codex: "Codex" };

type AccountNameT = (key: "settings.agentAccounts.cliLoginOf" | "settings.agentAccounts.oauthToken", options?: { provider: string }) => string;

/** An account's name: its email. A Claude login without one is a
 * CLAUDE_CODE_OAUTH_TOKEN login, which reports no identity. */
export function agentAccountName(account: Pick<AgentAccount, "email" | "provider"> & { signedIn?: boolean }, t: AccountNameT): string {
	if (account.email) return account.email;
	return account.provider === "claude" && account.signedIn
		? t("settings.agentAccounts.oauthToken")
		: t("settings.agentAccounts.cliLoginOf", { provider: agentAccountProviderNames[account.provider] });
}

export function withoutSameLogins<T extends Pick<AgentAccount, "id" | "sameLoginAs">>(accounts: T[], keepId?: string): T[] {
	const ids = new Set(accounts.map((account) => account.id));
	const twinOf = (account: T) => account.sameLoginAs && ids.has(account.sameLoginAs) ? account.sameLoginAs : undefined;
	return accounts.filter((account) => account.id === keepId || !twinOf(account));
}

export function agentAccountLabel(account: Pick<AgentAccount, "id" | "email" | "provider" | "device" | "orgId" | "orgName"> & { signedIn?: boolean }, peers: readonly Pick<AgentAccount, "id" | "email" | "orgId" | "orgName">[], t: AccountNameT): string {
	const name = agentAccountName(account, t);
	const email = account.email?.toLowerCase();
	const twins = email ? peers.filter((peer) => peer.id !== account.id && peer.email?.toLowerCase() === email) : [];
	if (twins.length === 0) return name;
	const org = (peer: Pick<AgentAccount, "orgId" | "orgName">) => peer.orgId ?? `name:${peer.orgName ?? ""}`;
	const otherOrgs = twins.filter((twin) => org(twin) !== org(account));
	const parts = [name];
	if (otherOrgs.length > 0 && account.orgName) parts.push(account.orgName);
	if (account.orgId && otherOrgs.some((twin) => (twin.orgName ?? "") === (account.orgName ?? ""))) parts.push(account.orgId.slice(0, 8));
	if (account.device && twins.some((twin) => org(twin) === org(account))) parts.push(t("settings.agentAccounts.cliLoginOf", { provider: agentAccountProviderNames[account.provider] }));
	return parts.join(" · ");
}

/** Names an account by id in status text, such as "Moved from … to …". */
export function useAgentAccountLabel(enabled = true, hostId?: HostId) {
	const { t } = useTranslation();
	const accounts = useAgentAccounts(enabled, hostId);
	return (id: string | undefined) => {
		const account = accounts.data?.find((candidate) => candidate.id === id);
		return account ? agentAccountName(account, t) : t("settings.agentAccounts.removedAccount");
	};
}

export function useAgentAccountUsage(days = 30, hostId?: HostId) {
	return useQuery({
		queryKey: [...usageQueryKey(hostId), days],
		queryFn: async () => unwrap(await client(hostId).GET("/api/v1/agents/accounts/usage", { params: { query: { days } } }))?.usage ?? [],
		refetchInterval: 60_000,
	});
}

export function useSessionAgentAccounts(sessionId: string | undefined, hostId?: HostId) {
	return useQuery({
		queryKey: sessionAccountsKey(sessionId ?? "", hostId),
		enabled: Boolean(sessionId),
		queryFn: async () => unwrap(await client(hostId).GET("/api/v1/agents/accounts/sessions/{sessionId}", { params: { path: { sessionId: sessionId ?? "" } } })) ?? { stayOnAccount: false },
	});
}

export function useAgentAccountActions(hostId?: HostId) {
	const queryClient = useQueryClient();
	const setAccounts = (accounts: AgentAccount[]) => queryClient.setQueryData(agentAccountsQueryKeyFor(hostId), accounts);
	return {
		refresh: async () => setAccounts(await fetchAgentAccounts(true, hostId)),
		startLogin: async (provider: AgentAccountProvider) =>
			unwrap(await client(hostId).POST("/api/v1/agents/accounts/login", { body: { provider } })),
		/** Signs a saved account in again in its own folder; verify and cancel are the same as a new sign-in. */
		signInAgain: async (accountId: string) =>
			unwrap(await client(hostId).POST("/api/v1/agents/accounts/{accountId}/login", { params: { path: { accountId } } })),
		verifyLogin: async (operationId: string) => {
			const result = unwrap(await client(hostId).POST("/api/v1/agents/accounts/login/{operationId}/verify", { params: { path: { operationId } } }));
			if (result.status === "complete") await queryClient.invalidateQueries({ queryKey: agentAccountsQueryKeyFor(hostId) });
			return result;
		},
		cancelLogin: async (operationId: string) => {
			const { error } = await client(hostId).POST("/api/v1/agents/accounts/login/{operationId}/cancel", { params: { path: { operationId } } });
			if (error) throw apiErrorException(error);
		},
		makeDefault: async (accountId: string) =>
			setAccounts(unwrap(await client(hostId).POST("/api/v1/agents/accounts/{accountId}/default", { params: { path: { accountId } } })).accounts),
		remove: async (accountId: string) =>
			setAccounts(unwrap(await client(hostId).DELETE("/api/v1/agents/accounts/{accountId}", { params: { path: { accountId } } })).accounts),
		useReset: async (accountId: string) => {
			const result = unwrap(await client(hostId).POST("/api/v1/agents/accounts/{accountId}/reset-credit", {
				params: { path: { accountId } }, body: { idempotencyKey: crypto.randomUUID() },
			}));
			await queryClient.invalidateQueries({ queryKey: agentAccountsQueryKeyFor(hostId) });
			return result.outcome;
		},
		/** Starts the durable move; the shared interface-transition status shows its progress. */
		switchSession: async (sessionId: string, accountId: string, stopTurn = false) => {
			const result = unwrap(await client(hostId).PUT("/api/v1/agents/accounts/sessions/{sessionId}", {
				params: { path: { sessionId } }, body: { accountId, ...(stopTurn ? { stopTurn: true } : {}) },
			}));
			await queryClient.invalidateQueries({ queryKey: sessionInterfaceTransitionQueryKey(sessionId, remoteHost(hostId)) });
			return result.transition;
		},
		refreshSession: (sessionId: string) => queryClient.invalidateQueries({ queryKey: sessionAccountsKey(sessionId, hostId) }),
		setStayOnAccount: async (sessionId: string, stay: boolean) =>
			queryClient.setQueryData(sessionAccountsKey(sessionId, hostId), unwrap(await client(hostId).PUT("/api/v1/agents/accounts/sessions/{sessionId}/stay", {
				params: { path: { sessionId } }, body: { stay },
			}))),
	};
}
