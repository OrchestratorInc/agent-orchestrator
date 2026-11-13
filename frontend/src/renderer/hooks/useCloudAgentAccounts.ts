/**
 * The signed-in user's Cloud coding-agent accounts (GET /me/agent-accounts),
 * when the control plane reports agentAccounts: each login or key is an
 * account, the default one runs new sessions, and a live session can move to
 * another of its agent's accounts. The default account is also the agent's
 * /me/providers connection, so every change here refreshes that list too.
 */

import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { agentLabel } from "../lib/agent-options";
import type { CloudCpAgentAccount } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { providerConnectionsQueryKey } from "./useProviderConnections";
import { sessionInterfaceTransitionQueryKey } from "./useSessionInterfaceTransition";

export const cloudAgentAccountsQueryKey = ["cloud-agent-accounts"] as const;
const sessionAccountKey = (sessionId: string) => [...cloudAgentAccountsQueryKey, "session", sessionId] as const;

export function useCloudAgentAccounts(enabled = true) {
	const { client, ready } = useCloudCp(enabled);
	return useQuery({
		queryKey: cloudAgentAccountsQueryKey,
		enabled: ready && enabled,
		staleTime: 30_000,
		queryFn: async (): Promise<CloudCpAgentAccount[]> => (await client.listAgentAccounts()).agentAccounts,
	});
}

/** The account a Cloud session's agent runs on. */
export function useCloudSessionAgentAccount(orgId: string | undefined, sessionId: string | undefined) {
	const { client, ready } = useCloudCp(Boolean(orgId && sessionId));
	return useQuery({
		queryKey: sessionAccountKey(sessionId ?? ""),
		enabled: ready && Boolean(orgId && sessionId),
		queryFn: ({ signal }) => client.getSessionAgentAccount(orgId ?? "", sessionId ?? "", { signal }),
	});
}

/** The agents whose live session can move to another account (the control plane's accountSwitchHarnesses). */
export const CLOUD_ACCOUNT_SWITCH_HARNESSES: readonly string[] = ["claude-code", "codex", "cursor"];

/** An account's name: the one the user gave it, else its login's email, else
 * what it is (a setup token reports no identity, nor does an API key). */
export function cloudAgentAccountName(account: Pick<CloudCpAgentAccount, "agent" | "name" | "email" | "credentialType">, t: TFunction): string {
	if (account.name) return account.name;
	if (account.email) return account.email;
	const provider = agentLabel(account.agent);
	return isCloudSubscriptionAccount(account)
		? t("settings.agentAccounts.cloudLoginOf", { provider })
		: t("cloudCredential.method.apiKey", { provider });
}

/** A subscription login, as opposed to an API key: the plan's limits apply. */
export function isCloudSubscriptionAccount(account: Pick<CloudCpAgentAccount, "credentialType">): boolean {
	return account.credentialType === "oauth_token" || account.credentialType === "auth_json" || account.credentialType === "access_token";
}

export function useCloudAgentAccountActions() {
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const refresh = () => Promise.all([
		queryClient.invalidateQueries({ queryKey: cloudAgentAccountsQueryKey }),
		queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey }),
	]);
	return {
		refresh,
		makeDefault: async (accountId: string) => {
			await client.makeAgentAccountDefault(accountId);
			await refresh();
		},
		rename: async (accountId: string, name: string) => {
			await client.renameAgentAccount(accountId, name);
			await refresh();
		},
		remove: async (accountId: string) => {
			await client.removeAgentAccount(accountId);
			await refresh();
		},
		/**
		 * Starts the move; the session's interface-transition status shows its
		 * progress. A terminal cannot prove it is idle, so a drain would wait out
		 * its window and fail: as an agent switch does, a terminal whose agent's
		 * latest activity is idle is stopped directly, and a working one drains.
		 */
		switchSession: async (orgId: string, sessionId: string, accountId: string, stopTurn = false, terminal = false) => {
			let interrupt = stopTurn;
			if (!interrupt && terminal) {
				const { session: latest } = await client.getSession(orgId, sessionId);
				interrupt = !["active", "waiting_input", "blocked"].includes(latest.activityState) &&
					latest.status !== "working" && latest.status !== "needs_input";
			}
			const { transition } = await client.switchSessionAgentAccount(orgId, sessionId, {
				accountId,
				...(interrupt ? { policy: "interrupt" as const } : {}),
			});
			await queryClient.invalidateQueries({ queryKey: sessionInterfaceTransitionQueryKey(sessionId, { orgId }) });
			return transition;
		},
		refreshSession: (sessionId: string) => queryClient.invalidateQueries({ queryKey: sessionAccountKey(sessionId) }),
	};
}
