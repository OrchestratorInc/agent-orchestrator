import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, apiRequest } from "./api";
import type { ServerConfig } from "./config";
import type { PickerOption } from "./NativePickerList";

const API = "/api/v1/agents/accounts";
const READ_TIMEOUT_MS = 50_000;
const RETRY_MS = [2_000, 5_000, 15_000, 30_000];

export type AgentAccountProvider = "claude" | "codex";

export type AgentAccount = {
	id: string;
	provider: AgentAccountProvider;
	email?: string;
	signedIn: boolean;
	default: boolean;
};

export function agentAccountProvider(harness?: string): AgentAccountProvider | undefined {
	if (harness === "claude-code") return "claude";
	if (harness === "codex") return "codex";
	return undefined;
}

export function agentAccountName(account: Pick<AgentAccount, "email" | "provider">): string {
	if (account.email) return account.email;
	return account.provider === "claude" ? "OAuth token" : "Codex CLI login";
}

export async function getAgentAccounts(cfg: ServerConfig, provider: AgentAccountProvider): Promise<AgentAccount[]> {
	const res = await apiRequest(cfg, API, undefined, READ_TIMEOUT_MS);
	const accounts = ((await res.json())?.accounts ?? []) as AgentAccount[];
	return accounts.filter((account) => account.provider === provider);
}

export async function getSessionAgentAccount(cfg: ServerConfig, sessionId: string, provider: AgentAccountProvider): Promise<string | undefined> {
	const res = await apiRequest(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}`, undefined, READ_TIMEOUT_MS);
	return ((await res.json()) as Partial<Record<AgentAccountProvider, string>>)?.[provider];
}

/** Starts moving the session to another account; the interface transition reports its progress. */
export async function moveSessionAgentAccount(cfg: ServerConfig, sessionId: string, accountId: string, stopTurn: boolean): Promise<void> {
	await apiRequest(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}`, {
		method: "PUT",
		body: JSON.stringify(stopTurn ? { accountId, stopTurn: true } : { accountId }),
	});
}

export function agentAccountOptions(accounts: readonly AgentAccount[]): PickerOption[] {
	return accounts.map((account) => ({
		id: account.id,
		title: agentAccountName(account),
		detail: !account.signedIn ? "Signed out" : account.default ? "Default" : undefined,
		disabled: !account.signedIn,
	}));
}

function accountReadRetryable(error: unknown): boolean {
	return !(error instanceof ApiError) || error.status >= 500 || error.status === 408 || error.status === 429;
}

/** Reloads when `reloadKey` changes. An older machine that refuses the phone offers no accounts. */
export function useAgentAccounts(cfg: ServerConfig | null | undefined, harness: string | undefined, sessionId?: string, reloadKey?: unknown) {
	const provider = agentAccountProvider(harness);
	const [accounts, setAccounts] = useState<AgentAccount[]>([]);
	const [current, setCurrent] = useState<string>();
	const [attempt, setAttempt] = useState(0);
	const failures = useRef(0);
	const cfgRef = useRef(cfg);
	cfgRef.current = cfg;
	const cfgKey = cfg ? JSON.stringify(cfg) : "";
	useEffect(() => {
		setAccounts((list) => (list.length ? [] : list));
		setCurrent(undefined);
	}, [cfgKey, provider, sessionId]);
	useEffect(() => {
		const cfg = cfgRef.current;
		if (!cfg || !provider) return;
		let cancelled = false;
		let retry: ReturnType<typeof setTimeout> | undefined;
		Promise.all([getAgentAccounts(cfg, provider), sessionId ? getSessionAgentAccount(cfg, sessionId, provider) : undefined])
			.then(([list, id]) => {
				if (cancelled) return;
				failures.current = 0;
				setAccounts(list);
				setCurrent(id);
			})
			.catch((error) => {
				if (cancelled || !accountReadRetryable(error)) return;
				const delay = RETRY_MS[Math.min(failures.current++, RETRY_MS.length - 1)];
				retry = setTimeout(() => setAttempt((n) => n + 1), delay);
			});
		return () => { cancelled = true; clearTimeout(retry); };
	}, [attempt, cfgKey, provider, reloadKey, sessionId]);
	const reload = useCallback(async () => {
		const cfg = cfgRef.current;
		if (!cfg || !provider) return undefined;
		const [list, id] = await Promise.all([getAgentAccounts(cfg, provider), sessionId ? getSessionAgentAccount(cfg, sessionId, provider) : undefined]);
		setAccounts(list);
		setCurrent(id);
		return { accounts: list, current: id };
	}, [provider, sessionId]);
	return { provider, accounts, current, reload };
}
