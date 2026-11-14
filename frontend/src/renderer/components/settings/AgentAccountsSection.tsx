import { Info, LoaderCircle, Plus, RefreshCw, RotateCcw, Trash2, TriangleAlert, X } from "@/components/app-icons";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountLabel, agentAccountProviderFor, agentAccountProviderNames, describeAccountProblem, useAgentAccountActions, useAgentAccounts, useAgentAccountUsage, withoutSameLogins, type AgentAccount, type AgentAccountLimitWindow, type AgentAccountLogin, type AgentAccountProvider } from "../../hooks/useAgentAccounts";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { baseUrlForHost } from "../../lib/host-clients";
import { useShellMaybe } from "../../lib/shell-context";
import { formatRelativeTime } from "../../lib/usage-format";
import { cn } from "../../lib/utils";
import { useSettings, useUpdateAccountHandoff } from "../../hooks/useSettings";
import { createTerminalMux, muxUrlFromApiBase } from "../../lib/terminal-mux";
import { useResolvedTheme, useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { TerminalPane } from "../TerminalPane";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { HarnessDetailGroup, HarnessDetailRow } from "./harness/harness-ui";

const providerNames = agentAccountProviderNames;

// One sign-in per host and provider, kept outside the page so leaving Settings
// does not lose a browser sign-in in progress.
let pendingLogins: Partial<Record<string, AgentAccountLogin>> = {};

/** The CLI's own login signs in and out through the harness's login terminal. */
export type CliLoginControls = {
	/** A login or logout terminal is open, or the harness is busy. */
	disabled: boolean;
	canLogout: boolean;
	onSignIn: () => void;
	onLogout: () => void;
};

/** One CLI's subscription accounts on its Harness Account tab: the CLI's own
 * login and the accounts added in AO, with their limits, default and sign-in. */
export function AgentAccountsPanel({ provider, cliLogin, hostId }: { provider: AgentAccountProvider; cliLogin?: CliLoginControls; hostId?: string }) {
	const { t } = useTranslation();
	const accounts = useAgentAccounts(true, hostId);
	const usage = useAgentAccountUsage(30, hostId);
	const actions = useAgentAccountActions(hostId);
	const loginKey = `${hostId ?? ""}|${provider}`;
	const [logins, setLogins] = useState(() => pendingLogins);
	const [loginNote, setLoginNote] = useState("");
	const [busy, setBusy] = useState<string | null>(null);
	const [error, setError] = useState("");
	const [removing, setRemoving] = useState<AgentAccount | null>(null);
	const tokensByAccount = new Map((usage.data ?? []).map((row) => [row.accountId, row.inputTokens + row.outputTokens]));
	const providerAccounts = (accounts.data ?? []).filter((account) => account.provider === provider);
	const rows = withoutSameLogins(providerAccounts, providerAccounts.find((account) => account.default)?.id);
	const cliTwinOf = (account: AgentAccount) => account.device ? undefined : providerAccounts.find((other) => other.sameLoginAs === account.id && !rows.includes(other));
	const login = logins[loginKey];

	const run = useCallback(async (key: string, action: () => Promise<unknown>) => {
		setBusy(key);
		setError("");
		try {
			await action();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(null);
		}
	}, []);

	const setLogin = (next: AgentAccountLogin | undefined) => {
		pendingLogins = { ...pendingLogins, [loginKey]: next };
		setLogins(pendingLogins);
	};
	// accountId signs that saved account in again, in its own folder.
	const startLogin = (accountId?: string) => run(`login:${provider}`, async () => {
		if (login) await actions.cancelLogin(login.operationId).catch(() => undefined);
		setLogin(undefined);
		setLoginNote("");
		setLogin(await (accountId ? actions.signInAgain(accountId) : actions.startLogin(provider)));
	});
	// One check at a time: the CLI exiting checks on its own, and a second check
	// racing a click would find the sign-in already finished and report an error.
	const verifying = useRef(false);
	const checkLogin = (current: AgentAccountLogin) => {
		// A closed sign-in's terminal still reports its exit; there is nothing left to check.
		if (pendingLogins[loginKey]?.operationId !== current.operationId || verifying.current) return;
		void verifyLogin(current);
	};
	const verifyLogin = (current: AgentAccountLogin) => run(`verify:${provider}`, async () => {
		verifying.current = true;
		const result = await actions.verifyLogin(current.operationId).finally(() => { verifying.current = false; });
		if (result.status === "waiting") setLoginNote(t("settings.agentAccounts.loginWaiting"));
		else if (result.status === "not_subscription") setLoginNote(t("settings.agentAccounts.loginNotSubscription"));
		else {
			setLogin(undefined);
			if (result.status === "duplicate") setError(t("settings.agentAccounts.loginDuplicate", { email: result.account?.email ?? "" }));
			if (result.status === "mismatch") setError(t("settings.agentAccounts.loginMismatch", { email: [result.account?.email, result.account?.orgName].filter(Boolean).join(" · ") }));
			if (result.status === "complete") {
				// A chat turn that sent the user here to log in gets them back.
				const ui = useUiStore.getState();
				const waiting = ui.loginReturn;
				if (waiting && agentAccountProviderFor(waiting.agentId) === provider) {
					ui.completeLoginReturn(waiting.agentId, hostId, result.account?.id);
				}
			}
		}
	});
	const closeLogin = (current: AgentAccountLogin) => {
		setLogin(undefined);
		void actions.cancelLogin(current.operationId).catch(() => undefined);
	};
	const requestedLogin = useUiStore((state) => state.accountLogin);
	useEffect(() => {
		if (hostId) return;
		const account = rows.find((row) => row.id === requestedLogin);
		if (!account || (account.device && (!cliLogin || cliLogin.disabled))) return;
		useUiStore.setState({ accountLogin: null });
		if (account.device) cliLogin?.onSignIn();
		else void startLogin(account.id);
	}, [requestedLogin, accounts.data, cliLogin?.disabled, hostId]);

	return <>
		<HarnessDetailGroup
			title={t("settings.codexAccounts.title")}
			action={<div className="flex items-center gap-1">
				<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="ghost" aria-label={t("settings.agentAccounts.refresh")} disabled={busy !== null} onClick={() => void run("refresh", actions.refresh)}>{busy === "refresh" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}</Button></TooltipTrigger><TooltipContent>{t("settings.agentAccounts.refresh")}</TooltipContent></Tooltip>
				<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="secondary" aria-label={t("settings.codexAccounts.add")} data-terminal-focus-handoff="true" disabled={busy !== null} onClick={() => void startLogin()}><Plus aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.codexAccounts.add")}</TooltipContent></Tooltip>
			</div>}
		>
			{error ? <p role="alert" className="px-3 py-2.5 text-xs text-error">{error}</p> : null}
			{login ? <div className="px-3 py-2.5"><LoginTerminal login={login} hostId={hostId} note={loginNote} pending={busy === `verify:${provider}`} onCheck={checkLogin} onClose={closeLogin} /></div> : null}
			{accounts.isError ? <p role="alert" className="px-3 py-2.5 text-xs text-error">{accounts.error.message}</p> : null}
			{accounts.isLoading ? <p className="px-3 py-2.5 text-xs text-muted-foreground">{t("settings.agentAccounts.loading")}</p> : null}
			{/* The CLI login does not wait on the list: a cold read can take a while, and a failed one has no row. */}
			{cliLogin && !providerAccounts.some((account) => account.device) ? <div className="flex items-center justify-between gap-3 px-3 py-2.5">
				<p className="truncate text-sm font-medium text-foreground">{t("settings.agentAccounts.cliLoginOf", { provider: providerNames[provider] })}</p>
				<Button type="button" size="sm" variant="ghost" data-terminal-focus-handoff="true" disabled={busy !== null || cliLogin.disabled} onClick={cliLogin.onSignIn}>{t("settings.harness.login")}</Button>
			</div> : null}
			{rows.map((account) => { const cliTwin = cliTwinOf(account); return <AccountRow
				key={account.id}
				account={account}
				name={agentAccountLabel(account, rows, t)}
				aoTokens={(tokensByAccount.get(account.id) ?? 0) + (cliTwin ? tokensByAccount.get(cliTwin.id) ?? 0 : 0) || undefined}
				busy={busy}
				cliLogin={account.device ? cliLogin : undefined}
				onMakeDefault={() => void run(`default:${account.id}`, () => actions.makeDefault(account.id))}
				onUseReset={() => void run(`reset:${account.id}`, () => actions.useReset(account.id))}
				onSignInAgain={() => void startLogin(account.id)}
				onRemove={() => { setError(""); setRemoving(account); }}
			/>; })}
			{hostId ? null : <AccountHandoffRow />}
		</HarnessDetailGroup>
		{removing ? <ConfirmDialog open destructive
			title={t("settings.agentAccounts.remove")}
			description={t("settings.agentAccounts.removeDescription", { email: agentAccountLabel(removing, rows, t) })}
			confirmLabel={t(busy === `remove:${removing.id}` ? "settings.agentAccounts.removing" : "settings.agentAccounts.remove")}
			busy={busy === `remove:${removing.id}`}
			error={error || undefined}
			onConfirm={() => void run(`remove:${removing.id}`, async () => { await actions.remove(removing.id); setRemoving(null); })}
			onOpenChange={(open) => { if (!open) setRemoving(null); }}
		/> : null}
	</>;
}

function AccountRow({ account, name, aoTokens, busy, cliLogin, onMakeDefault, onUseReset, onSignInAgain, onRemove }: {
	account: AgentAccount;
	name: string;
	aoTokens: number | undefined;
	busy: string | null;
	/** Only on the CLI's own login, which has no folder of its own to remove. */
	cliLogin?: CliLoginControls;
	onMakeDefault: () => void;
	onUseReset: () => void;
	onSignInAgain: () => void;
	onRemove: () => void;
}) {
	const { t, i18n } = useTranslation();
	const plan = formatPlanName(account.plan);
	const resets = account.limits?.resetCredits ?? 0;
	const meta = [
		plan ? t("settings.codexAccounts.planLabel", { name: plan }) : null,
		// An API key (or a cloud provider login) launches the CLI but is not a
		// subscription: no limits, and no account to move a session to.
		account.signedIn ? null : account.loggedIn ? t("settings.agentAccounts.noSubscription") : t("settings.agentAccounts.signedOut"),
		aoTokens ? t("settings.agentAccounts.aoUsage", { tokens: formatCompactNumber(aoTokens, i18n.language) }) : null,
	].filter(Boolean).join(" · ");
	return <div className="px-3 py-2.5" data-agent-account={account.id}>
		<div className="flex items-start justify-between gap-3">
			<div className="min-w-0">
				<p className="truncate text-sm font-medium text-foreground">{name}</p>
				<p className="mt-0.5 truncate text-xs text-muted-foreground">{meta}</p>
			</div>
			<div className="flex shrink-0 items-center gap-2">
				{cliLogin
					? <Button type="button" size="sm" variant="ghost" data-terminal-focus-handoff="true" disabled={busy !== null || cliLogin.disabled} onClick={cliLogin.onSignIn}>{t("settings.agentAccounts.signInAgain")}</Button>
					: !account.signedIn && !account.device
						? <Button type="button" size="sm" variant="ghost" data-terminal-focus-handoff="true" disabled={busy !== null} onClick={onSignInAgain}>{t("settings.agentAccounts.signInAgain")}</Button>
						: null}
				{account.default
					? <span className="text-xs text-muted-foreground">{t("settings.agentAccounts.default")}</span>
					: account.signedIn ? <Button type="button" size="sm" variant="ghost" disabled={busy !== null} onClick={onMakeDefault}>{t("settings.agentAccounts.makeDefault")}</Button> : null}
				{resets > 0 ? <Tooltip><TooltipTrigger asChild><Button type="button" size="sm" variant="ghost" disabled={busy !== null} onClick={onUseReset}>{busy === `reset:${account.id}` ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RotateCcw aria-hidden="true" />}{t("settings.codexAccounts.useReset")}</Button></TooltipTrigger><TooltipContent>{t("settings.codexAccounts.resetCount", { count: resets })}</TooltipContent></Tooltip> : null}
				{account.device
					? cliLogin?.canLogout && account.loggedIn ? <Button type="button" size="sm" variant="ghost" disabled={busy !== null || cliLogin.disabled} onClick={cliLogin.onLogout}>{t("settings.harness.logout")}</Button> : null
					: <Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="destructive-ghost" aria-label={t("settings.agentAccounts.remove")} disabled={busy !== null} onClick={onRemove}><Trash2 aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.agentAccounts.remove")}</TooltipContent></Tooltip>}
			</div>
		</div>
		<AccountProblemNote account={account} />
		<AccountLimits account={account} />
	</div>;
}

/** Why the account's last check failed, in plain words. A failure that clears
 * on its own reads as a muted note beside the age of the numbers still shown;
 * one that needs the user is a warning. The CLI's own error is under Details. */
export function AccountProblemNote({ account }: { account: AgentAccount }) {
	const { t, i18n } = useTranslation();
	const problem = describeAccountProblem(account, t);
	if (!problem) return null;
	const fetchedAt = account.limits?.windows.length ? account.limits.fetchedAt : undefined;
	const age = !problem.actionable && fetchedAt ? t("settings.agentAccounts.updatedAt", { value: formatRelativeTime(fetchedAt, new Date(), i18n.language) }) : null;
	return <p className={cn("mt-2 flex items-start gap-1.5 text-xs", problem.actionable ? "text-warning" : "text-muted-foreground")} data-account-problem={account.problemKind ?? "unavailable"}>
		{problem.actionable ? <TriangleAlert aria-hidden="true" className="mt-px size-icon-sm shrink-0" /> : null}
		<span className="min-w-0">
			{age ? `${age} · ${problem.text}` : problem.text}
			{problem.detail ? <> <Tooltip><TooltipTrigger asChild><button type="button" className="text-muted-foreground underline decoration-dotted underline-offset-2 hover:text-foreground focus-visible:text-foreground">{t("settings.agentAccounts.problemDetails")}</button></TooltipTrigger><TooltipContent className="max-w-sm break-words font-mono">{problem.detail}</TooltipContent></Tooltip></> : null}
		</span>
	</p>;
}

function AccountHandoffRow() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateAccountHandoff();
	return <>
		<HarnessDetailRow label={<span className="inline-flex items-center gap-1.5">
			{t("settings.agentAccounts.handoff")}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button type="button" size="icon-xs" variant="quiet" aria-label={t("settings.agentAccounts.problemDetails")}>
						<Info className="size-icon-sm" aria-hidden="true" />
					</Button>
				</TooltipTrigger>
				<TooltipContent className="max-w-sm">{t("settings.agentAccounts.handoffDescription")}</TooltipContent>
			</Tooltip>
		</span>}>
			<Switch
				aria-label={t("settings.agentAccounts.handoff")}
				checked={settings?.accountHandoff ?? false}
				disabled={isLoading || saving || !settings}
				onCheckedChange={update}
			/>
		</HarnessDetailRow>
		{error ? <p role="alert" className="px-3 pb-2 text-xs text-error">{error}</p> : null}
	</>;
}

/** The account's limit meters, shared by the Harness Account tab and the session summary. */
export function AccountLimits({ account }: { account: AgentAccount }) {
	const { t, i18n } = useTranslation();
	const windows = account.limits?.windows ?? [];
	const extra = account.limits?.extraUsage;
	if (windows.length === 0 && !extra?.enabled) return null;
	// One grid for every row, so bars and figures line up. The label keeps its
	// width; the reset time gives way first, and the bar stays short.
	return <div className="mt-3 space-y-2 text-xs">
		{windows.length ? <div className="grid grid-cols-[auto_minmax(0,1fr)_3rem_auto] items-center gap-x-2 gap-y-2">
			{windows.map((window) => <LimitRow key={window.id} window={window} locale={i18n.language} />)}
		</div> : null}
		{extra?.enabled && extra.usedCredits != null && extra.monthlyLimit != null ? <p className="text-muted-foreground tabular-nums">{t("settings.agentAccounts.extraUsage", {
			used: formatMoney(extra.usedCredits, extra.currency, i18n.language),
			limit: formatMoney(extra.monthlyLimit, extra.currency, i18n.language),
		})}</p> : null}
	</div>;
}

export function formatPlanName(plan: string | null | undefined): string | null {
	const value = plan?.trim();
	if (!value) return null;
	const known: Record<string, string> = { free: "Free", plus: "Plus", pro: "Pro", max: "Max", team: "Team", business: "Business", enterprise: "Enterprise" };
	return known[value.toLowerCase()] ?? value;
}

function formatPercentage(value: number, locale?: string): string {
	return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value)}%`;
}

function formatCompactNumber(value: number, locale: string): string {
	return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

/** A reset time: the hour today, or day and hour later; `full` is the tooltip. */
function formatResetTime(value: string | null | undefined, locale: string): { visible: string; full: string } | null {
	if (!value) return null;
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return null;
	const now = new Date();
	const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
	return {
		visible: new Intl.DateTimeFormat(locale, sameDay ? { hour: "2-digit", minute: "2-digit" } : { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }).format(date),
		full: new Intl.DateTimeFormat(locale, { dateStyle: "full", timeStyle: "long" }).format(date),
	};
}

/** Formats an amount in the server's currency. A code Intl does not know
 * throws, so it falls back to the bare number and the code. */
function formatMoney(value: number, currency: string | undefined, locale: string): string {
	try {
		return new Intl.NumberFormat(locale, { style: "currency", currency: currency || "USD" }).format(value);
	} catch {
		return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value)} ${currency ?? ""}`.trim();
	}
}

function LimitRow({ window, locale }: { window: AgentAccountLimitWindow; locale: string }) {
	const { t } = useTranslation();
	// The provider reports consumed capacity; the meter fills as usage grows.
	const used = Math.max(0, Math.min(100, window.usedPercent));
	const value = formatPercentage(used, locale);
	const reset = formatResetTime(window.resetsAt, locale);
	const fill = used >= 100 ? "bg-error" : used >= 75 ? "bg-warning" : "bg-foreground/80";
	return <>
		<p className="whitespace-nowrap text-foreground">{window.label}</p>
		<p className="min-w-0 truncate text-muted-foreground" title={reset?.full}>{reset ? t("settings.codexAccounts.capacityResets", { value: reset.visible }) : null}</p>
		<div role="progressbar" aria-label={t("settings.codexAccounts.usedForLimit", { label: window.label, value })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={used} className="h-1.5 w-full overflow-hidden rounded-full bg-muted"><div className={`h-full rounded-full ${fill}`} style={{ width: `${used}%` }} /></div>
		<p className="whitespace-nowrap text-right tabular-nums text-muted-foreground">{t("settings.codexAccounts.percentUsed", { value })}</p>
	</>;
}

function LoginTerminal({ login, hostId, note, pending, onCheck, onClose }: {
	login: AgentAccountLogin;
	hostId?: string;
	note: string;
	pending: boolean;
	onCheck: (login: AgentAccountLogin) => void;
	onClose: (login: AgentAccountLogin) => void;
}) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	const [state, setState] = useState<TerminalSessionState>("connecting");
	const createMux = useCallback(() => {
		const base = hostId ? baseUrlForHost(hostId) : undefined;
		if (!base) throw new Error("Remote host disconnected");
		return createTerminalMux(muxUrlFromApiBase(base));
	}, [hostId]);
	const checkRef = useRef(onCheck);
	checkRef.current = onCheck;
	// The CLI exits when its sign-in finishes, so check once without a click.
	const handleState = useCallback((next: TerminalSessionState) => {
		setState(next);
		if (next === "exited") checkRef.current(login);
	}, [login]);
	return <div className="overflow-hidden rounded-md border border-border bg-terminal" data-settings-inline-edit="" data-testid="agent-account-login-terminal">
		<div className="flex min-h-10 items-center justify-between gap-3 border-b border-border px-3 py-2">
			<p className="truncate text-xs font-medium text-foreground">{t("settings.agentAccounts.loginTitle", { provider: providerNames[login.provider] })}</p>
			<Button type="button" size="icon-sm" variant="quiet" aria-label={t("settings.codexAccounts.loginClose")} className="shrink-0" onClick={() => onClose(login)}><X aria-hidden="true" /></Button>
		</div>
		<div className="h-[300px] min-h-0"><TerminalPane key={login.operationId} createMux={hostId ? createMux : undefined} daemonReady={hostId || !shell ? true : shell.daemonStatus.state === "ready"} focusRequested={state === "attached"} fontSize={12} onTerminalStateChange={handleState} terminalTarget={{ kind: "shell", handleId: login.shellTerminal.handleId, generation: login.shellTerminal.createdAt, title: login.shellTerminal.title }} theme={theme} /></div>
		<div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2">
			<p className="min-w-0 truncate text-xs text-muted-foreground" role="status" aria-live="polite">{note}</p>
			<Button type="button" size="sm" variant="secondary" disabled={pending} onClick={() => onCheck(login)}>{pending ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}{t("settings.agentAccounts.checkSignIn")}</Button>
		</div>
	</div>;
}
