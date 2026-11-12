import { useEffect, useState } from "react";
import { apiRequest } from "./api";
import type { ServerConfig } from "./config";
import type { PickerOption } from "./NativePickerList";

const API = "/api/v1/agents/accounts";

export type AgentAccountProvider = "claude" | "codex";

export type AgentAccount = {
	id: string;
	provider: AgentAccountProvider;
	email?: string;
	signedIn: boolean;
	default: boolean;
};

/** The subscription a harness signs in with, or undefined when it has none. */
export function agentAccountProvider(harness?: string): AgentAccountProvider | undefined {
	if (harness === "claude-code") return "claude";
	if (harness === "codex") return "codex";
	return undefined;
}

/** An account's name: its email, else what kind of login it is. */
export function agentAccountName(account: Pick<AgentAccount, "email" | "provider">): string {
	if (account.email) return account.email;
	return account.provider === "claude" ? "OAuth token" : "Codex CLI login";
}

/** The machine's accounts for one provider. */
export async function getAgentAccounts(cfg: ServerConfig, provider: AgentAccountProvider): Promise<AgentAccount[]> {
	const res = await apiRequest(cfg, API);
	const accounts = ((await res.json())?.accounts ?? []) as AgentAccount[];
	return accounts.filter((account) => account.provider === provider);
}

/** The account id a session runs on, per provider. */
export async function getSessionAgentAccount(cfg: ServerConfig, sessionId: string, provider: AgentAccountProvider): Promise<string | undefined> {
	const res = await apiRequest(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}`);
	return ((await res.json()) as Partial<Record<AgentAccountProvider, string>>)?.[provider];
}

/** Starts moving the session to another account; the interface transition reports its progress. */
export async function moveSessionAgentAccount(cfg: ServerConfig, sessionId: string, accountId: string, stopTurn: boolean): Promise<void> {
	await apiRequest(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}`, {
		method: "PUT",
		body: JSON.stringify(stopTurn ? { accountId, stopTurn: true } : { accountId }),
	});
}

/** Accounts as sheet rows; a signed-out one shows but cannot take a session. */
export function agentAccountOptions(accounts: readonly AgentAccount[]): PickerOption[] {
	return accounts.map((account) => ({
		id: account.id,
		title: agentAccountName(account),
		detail: !account.signedIn ? "Signed out" : account.default ? "Default" : undefined,
		disabled: !account.signedIn,
	}));
}

/**
 * The machine's accounts for a harness and, given a session, the one it runs
 * on. Reloads when `reloadKey` changes, such as when a move settles. An older
 * machine that refuses the phone answers nothing, and no account is offered.
 */
export function useAgentAccounts(cfg: ServerConfig | null | undefined, harness: string | undefined, sessionId?: string, reloadKey?: unknown) {
	const provider = agentAccountProvider(harness);
	const [accounts, setAccounts] = useState<AgentAccount[]>([]);
	const [current, setCurrent] = useState<string>();
	useEffect(() => {
		setAccounts([]);
		setCurrent(undefined);
	}, [cfg, provider, sessionId]);
	useEffect(() => {
		if (!cfg || !provider) return;
		let cancelled = false;
		Promise.all([getAgentAccounts(cfg, provider), sessionId ? getSessionAgentAccount(cfg, sessionId, provider) : undefined])
			.then(([list, id]) => {
				if (cancelled) return;
				setAccounts(list);
				setCurrent(id);
			})
			.catch(() => {});
		return () => { cancelled = true; };
	}, [cfg, provider, reloadKey, sessionId]);
	return { provider, accounts, current };
}
