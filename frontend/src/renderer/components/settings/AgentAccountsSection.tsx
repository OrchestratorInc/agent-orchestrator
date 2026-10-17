import { LoaderCircle, Plus, RefreshCw, RotateCcw, Trash2, X } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountName, agentAccountProviderNames, useAgentAccountActions, useAgentAccounts, useAgentAccountUsage, type AgentAccount, type AgentAccountLimitWindow, type AgentAccountLogin, type AgentAccountProvider } from "../../hooks/useAgentAccounts";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { useShellMaybe } from "../../lib/shell-context";
import { useResolvedTheme } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { TerminalPane } from "../TerminalPane";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { AgentProviderGroup } from "./AgentProviderGroup";
import { formatCompactNumber, formatPercentage, formatPlanName, formatResetTime } from "./CodexAccountDetails";
import { SettingsSection } from "./SettingsSection";

const providerNames = agentAccountProviderNames;

// One sign-in per provider, kept outside the page so leaving Settings does not
// lose a browser sign-in in progress.
let pendingLogins: Partial<Record<AgentAccountProvider, AgentAccountLogin>> = {};

/** Every Claude Code and Codex account with all of its limits. Each account is
 * a config folder of the CLI itself; "This computer" is the CLI's own login. */
export function AgentAccountsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const accounts = useAgentAccounts();
	const usage = useAgentAccountUsage();
	const actions = useAgentAccountActions();
	const [logins, setLogins] = useState(() => pendingLogins);
	const [loginNotes, setLoginNotes] = useState<Partial<Record<AgentAccountProvider, string>>>({});
	const [busy, setBusy] = useState<string | null>(null);
	const [error, setError] = useState("");
	const [removing, setRemoving] = useState<AgentAccount | null>(null);
	const tokensByAccount = new Map((usage.data ?? []).map((row) => [row.accountId, row.inputTokens + row.outputTokens]));

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

	const setLogin = (provider: AgentAccountProvider, login: AgentAccountLogin | undefined) =>
		setLogins((current) => (pendingLogins = { ...current, [provider]: login }));
	const startLogin = (provider: AgentAccountProvider) => run(`login:${provider}`, async () => {
		const previous = logins[provider];
		setLogin(provider, undefined);
		if (previous) await actions.cancelLogin(previous.operationId).catch(() => undefined);
		setLoginNotes((current) => ({ ...current, [provider]: "" }));
		setLogin(provider, await actions.startLogin(provider));
	});
	const checkLogin = (login: AgentAccountLogin) => void run(`verify:${login.provider}`, async () => {
		const result = await actions.verifyLogin(login.operationId);
		if (result.status === "waiting") setLoginNotes((current) => ({ ...current, [login.provider]: t("settings.agentAccounts.loginWaiting") }));
		else {
			setLogin(login.provider, undefined);
			if (result.status === "duplicate") setError(t("settings.agentAccounts.loginDuplicate", { email: result.account?.email ?? "" }));
		}
	});
	const closeLogin = (login: AgentAccountLogin) => {
		setLogin(login.provider, undefined);
		void actions.cancelLogin(login.operationId).catch(() => undefined);
	};

	return <SettingsSection title={t("settings.codexAccounts.title")} sectionId="agent-accounts" titleHidden={titleHidden}>
		<div className="space-y-3">
			{error ? <p role="alert" className="text-xs text-error">{error}</p> : null}
			{(["claude", "codex"] as const).map((provider) => {
				const rows = (accounts.data ?? []).filter((account) => account.provider === provider);
				return <AgentProviderGroup
					key={provider}
					provider={provider === "claude" ? "claude-code" : "codex"}
					name={providerNames[provider]}
					expanded
					collapsible={false}
					onExpandedChange={() => undefined}
					action={<div className="flex items-center gap-1">
						<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="ghost" aria-label={t("settings.agentAccounts.refresh")} disabled={busy !== null} onClick={() => void run("refresh", actions.refresh)}>{busy === "refresh" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}</Button></TooltipTrigger><TooltipContent>{t("settings.agentAccounts.refresh")}</TooltipContent></Tooltip>
						<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="secondary" aria-label={t("settings.codexAccounts.add")} data-terminal-focus-handoff="true" disabled={busy !== null} onClick={() => void startLogin(provider)}><Plus aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.codexAccounts.add")}</TooltipContent></Tooltip>
					</div>}
				>
					{logins[provider] ? <div className="border-t border-border px-4 py-3"><LoginTerminal login={logins[provider]} note={loginNotes[provider] ?? ""} pending={busy === `verify:${provider}`} onCheck={checkLogin} onClose={closeLogin} /></div> : null}
					{accounts.isLoading ? <p className="border-t border-border px-4 py-3 text-xs text-muted-foreground">{t("settings.agentAccounts.loading")}</p> : null}
					<div className="divide-y divide-border border-t border-border">
						{rows.map((account) => <AccountRow
							key={account.id}
							account={account}
							aoTokens={tokensByAccount.get(account.id)}
							busy={busy}
							onMakeDefault={() => void run(`default:${account.id}`, () => actions.makeDefault(account.id))}
							onUseReset={() => void run(`reset:${account.id}`, () => actions.useReset(account.id))}
							onRemove={() => setRemoving(account)}
						/>)}
					</div>
				</AgentProviderGroup>;
			})}
		</div>
		{removing ? <ConfirmDialog open destructive
			title={t("settings.agentAccounts.remove")}
			description={t("settings.agentAccounts.removeDescription", { email: agentAccountName(removing, t) })}
			confirmLabel={t("settings.agentAccounts.remove")}
			busy={busy === `remove:${removing.id}`}
			error={error || undefined}
			onConfirm={() => void run(`remove:${removing.id}`, async () => { await actions.remove(removing.id); setRemoving(null); })}
			onOpenChange={(open) => { if (!open) setRemoving(null); }}
		/> : null}
	</SettingsSection>;
}


function AccountRow({ account, aoTokens, busy, onMakeDefault, onUseReset, onRemove }: {
	account: AgentAccount;
	aoTokens: number | undefined;
	busy: string | null;
	onMakeDefault: () => void;
	onUseReset: () => void;
	onRemove: () => void;
}) {
	const { t, i18n } = useTranslation();
	const plan = formatPlanName(account.plan);
	const resets = account.limits?.resetCredits ?? 0;
	const meta = [
		account.device ? t("settings.agentAccounts.cliLogin") : null,
		plan ? t("settings.codexAccounts.planLabel", { name: plan }) : null,
		account.signedIn ? null : t("settings.agentAccounts.signedOut"),
		aoTokens ? t("settings.agentAccounts.aoUsage", { tokens: formatCompactNumber(aoTokens, i18n.language) }) : null,
	].filter(Boolean).join(" · ");
	return <div className="px-4 py-3" data-agent-account={account.id}>
		<div className="flex items-start justify-between gap-3">
			<div className="min-w-0">
				<p className="truncate text-sm font-medium text-foreground">{agentAccountName(account, t)}</p>
				<p className="mt-0.5 truncate text-xs text-muted-foreground">{meta}</p>
			</div>
			<div className="flex shrink-0 items-center gap-2">
				{account.default
					? <span className="text-xs text-muted-foreground">{t("settings.agentAccounts.default")}</span>
					: <Button type="button" size="sm" variant="ghost" disabled={busy !== null || !account.signedIn} onClick={onMakeDefault}>{t("settings.agentAccounts.makeDefault")}</Button>}
				{resets > 0 ? <Tooltip><TooltipTrigger asChild><Button type="button" size="sm" variant="ghost" disabled={busy !== null} onClick={onUseReset}>{busy === `reset:${account.id}` ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RotateCcw aria-hidden="true" />}{t("settings.codexAccounts.useReset")}</Button></TooltipTrigger><TooltipContent>{t("settings.codexAccounts.resetCount", { count: resets })}</TooltipContent></Tooltip> : null}
				{account.device ? null : <Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="ghost" aria-label={t("settings.agentAccounts.remove")} disabled={busy !== null} onClick={onRemove}><Trash2 aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.agentAccounts.remove")}</TooltipContent></Tooltip>}
			</div>
		</div>
		{account.problem ? <p className="mt-2 text-xs text-warning">{account.problem}</p> : null}
		<AccountLimits account={account} />
	</div>;
}

/** The account's limit meters, shared by the Accounts page and the session summary. */
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
			used: new Intl.NumberFormat(i18n.language, { style: "currency", currency: extra.currency || "USD" }).format(extra.usedCredits),
			limit: new Intl.NumberFormat(i18n.language, { style: "currency", currency: extra.currency || "USD" }).format(extra.monthlyLimit),
		})}</p> : null}
	</div>;
}

function LimitRow({ window, locale }: { window: AgentAccountLimitWindow; locale: string }) {
	const { t } = useTranslation();
	const remaining = Math.max(0, Math.min(100, 100 - window.usedPercent));
	const value = formatPercentage(remaining, locale);
	const reset = formatResetTime(window.resetsAt, locale);
	const fill = remaining <= 0 ? "bg-error" : remaining <= 25 ? "bg-warning" : "bg-foreground/80";
	return <>
		<p className="whitespace-nowrap text-foreground">{window.label}</p>
		<p className="min-w-0 truncate text-muted-foreground" title={reset?.full}>{reset ? t("settings.codexAccounts.capacityResets", { value: reset.visible }) : null}</p>
		<div role="progressbar" aria-label={t("settings.codexAccounts.remainingForLimit", { label: window.label, value })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={remaining} className="h-1.5 w-full overflow-hidden rounded-full bg-muted"><div className={`h-full rounded-full ${fill}`} style={{ width: `${remaining}%` }} /></div>
		<p className="whitespace-nowrap text-right tabular-nums text-muted-foreground">{t("settings.codexAccounts.percentLeft", { value })}</p>
	</>;
}

function LoginTerminal({ login, note, pending, onCheck, onClose }: {
	login: AgentAccountLogin;
	note: string;
	pending: boolean;
	onCheck: (login: AgentAccountLogin) => void;
	onClose: (login: AgentAccountLogin) => void;
}) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	const [state, setState] = useState<TerminalSessionState>("connecting");
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
			<button type="button" aria-label={t("settings.codexAccounts.loginClose")} className="grid size-7 shrink-0 place-items-center rounded text-muted-foreground hover:bg-interactive-hover hover:text-foreground" onClick={() => onClose(login)}><X className="size-4" aria-hidden="true" /></button>
		</div>
		<div className="h-[300px] min-h-0"><TerminalPane key={login.operationId} daemonReady={shell ? shell.daemonStatus.state === "ready" : true} focusRequested={state === "attached"} fontSize={12} onTerminalStateChange={handleState} terminalTarget={{ kind: "shell", handleId: login.shellTerminal.handleId, generation: login.shellTerminal.createdAt, title: login.shellTerminal.title }} theme={theme} /></div>
		<div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2">
			<p className="min-w-0 truncate text-xs text-muted-foreground" role="status" aria-live="polite">{note}</p>
			<Button type="button" size="sm" variant="outline" disabled={pending} onClick={() => onCheck(login)}>{pending ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}{t("settings.agentAccounts.checkSignIn")}</Button>
		</div>
	</div>;
}
