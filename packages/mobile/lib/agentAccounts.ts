import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, apiRequest } from "./api";
import type { ServerConfig } from "./config";
import { QUOTA_CRITICAL_PERCENT, QUOTA_WARN_PERCENT, resetLabel } from "./chat/conversationChrome";
import type { PickerOption } from "./NativePickerList";

const API = "/api/v1/agents/accounts";
const READ_TIMEOUT_MS = 50_000;
const RETRY_MS = [2_000, 5_000, 15_000, 30_000];

export type AgentAccountProvider = "claude" | "codex";

export type AgentAccount = {
	id: string;
	provider: AgentAccountProvider;
	email?: string;
	device?: boolean;
	orgId?: string;
	orgName?: string;
	signedIn: boolean;
	default: boolean;
	/** An API key account's own name. */
	name?: string;
	/** The subscription's usage meters, when the machine could read them. */
	limits?: { windows?: AgentAccountLimitWindow[] };
};

export type AgentAccountLimitWindow = {
	id: string;
	label: string;
	usedPercent: number;
	resetsAt?: string;
};

export type AgentAccountUsageSeverity = "normal" | "warn" | "critical";

export function agentAccountProvider(harness?: string): AgentAccountProvider | undefined {
	if (harness === "claude-code") return "claude";
	if (harness === "codex") return "codex";
	return undefined;
}

export function agentAccountName(account: Pick<AgentAccount, "email" | "provider">): string {
	if (account.email) return account.email;
	return account.provider === "claude" ? "OAuth token" : "Codex CLI login";
}

type LabelPeer = Pick<AgentAccount, "id" | "email" | "orgId" | "orgName">;

export function agentAccountLabel(account: Pick<AgentAccount, "id" | "email" | "provider" | "device" | "orgId" | "orgName">, peers: readonly LabelPeer[]): string {
	const name = agentAccountName(account);
	const email = account.email?.toLowerCase();
	const twins = email ? peers.filter((peer) => peer.id !== account.id && peer.email?.toLowerCase() === email) : [];
	if (twins.length === 0) return name;
	const org = (peer: Pick<AgentAccount, "orgId" | "orgName">) => peer.orgId ?? `name:${peer.orgName ?? ""}`;
	const otherOrgs = twins.filter((twin) => org(twin) !== org(account));
	const parts = [name];
	if (otherOrgs.length > 0 && account.orgName) parts.push(account.orgName);
	if (account.orgId && otherOrgs.some((twin) => (twin.orgName ?? "") === (account.orgName ?? ""))) parts.push(account.orgId.slice(0, 8));
	if (account.device && twins.some((twin) => org(twin) === org(account))) parts.push(account.provider === "claude" ? "Claude Code CLI login" : "Codex CLI login");
	return parts.join(" · ");
}

/** The letter an account's avatar shows: its email's or name's first, else the provider's. */
export function agentAccountInitial(account: Pick<AgentAccount, "email" | "name" | "provider">): string {
	const source = account.email?.trim() || account.name?.trim() || account.provider;
	// By code point: a name led by an emoji keeps both halves of its surrogate pair.
	return (Array.from(source)[0] ?? "").toUpperCase();
}

function reportedWindows(account: Pick<AgentAccount, "limits">): AgentAccountLimitWindow[] {
	return (account.limits?.windows ?? []).filter((window) => Number.isFinite(window.usedPercent));
}

/** The fullest meter's use, 0..100, or undefined when the machine reported none. */
export function agentAccountPeakUsage(account: Pick<AgentAccount, "limits">): number | undefined {
	const windows = reportedWindows(account);
	if (!windows.length) return undefined;
	return Math.min(100, Math.max(0, ...windows.map((window) => window.usedPercent)));
}

export function agentAccountUsageSeverity(percent: number | undefined): AgentAccountUsageSeverity {
	if (percent === undefined) return "normal";
	return percent >= QUOTA_CRITICAL_PERCENT ? "critical" : percent >= QUOTA_WARN_PERCENT ? "warn" : "normal";
}

/**
 * Each meter's use on one line, "Session (5h) 42% · Weekly 18%". A full meter
 * says when it frees up, since that is what decides whether to pick it.
 */
export function agentAccountUsageSummary(account: Pick<AgentAccount, "limits">, nowMs = Date.now()): string | undefined {
	const windows = reportedWindows(account);
	if (!windows.length) return undefined;
	return windows.map((window) => {
		const percent = Math.round(Math.min(100, Math.max(0, window.usedPercent)));
		const resetsAt = window.resetsAt ? Date.parse(window.resetsAt) : NaN;
		const reset = percent >= 100 && Number.isFinite(resetsAt) ? resetLabel(Math.max(0, (resetsAt - nowMs) / 1000)) : undefined;
		return reset ? `${window.label} full, resets in ${reset}` : `${window.label} ${percent}%`;
	}).join(" · ");
}

/** What a screen reader says for the avatar's button. */
export function accountUsageAccessibilityLabel(name: string | undefined, account: Pick<AgentAccount, "limits" | "signedIn"> | undefined): string {
	if (!account || !name) return "Account, choose an account";
	const percent = account.signedIn ? agentAccountPeakUsage(account) : undefined;
	return percent === undefined ? `Account, ${name}` : `Account, ${name}, ${Math.round(percent)}% of usage limit used`;
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

/** `mark` draws each row's avatar, given the account. */
export function agentAccountOptions(accounts: readonly AgentAccount[], mark?: (account: AgentAccount) => PickerOption["mark"], nowMs = Date.now()): PickerOption[] {
	return accounts.map((account) => {
		const usage = account.signedIn ? agentAccountUsageSummary(account, nowMs) : undefined;
		const detail = [!account.signedIn ? "Signed out" : account.default ? "Default" : undefined, usage].filter(Boolean).join(" · ");
		const option: PickerOption = {
			id: account.id,
			title: agentAccountLabel(account, accounts),
			detail: detail || undefined,
			disabled: !account.signedIn,
		};
		if (usage && agentAccountUsageSeverity(agentAccountPeakUsage(account)) !== "normal") option.warn = true;
		const drawn = mark?.(account);
		if (drawn) option.mark = drawn;
		return option;
	});
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
