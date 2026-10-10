import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import type { ChangeEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Check, ChevronDown, ChevronRight, CirclePause, Copy, ExternalLink, Eye, EyeOff, FileUp, KeyRound, Link2, LoaderCircle, LogIn, MonitorSmartphone, Plus, RefreshCw, X } from "lucide-react";
import { accountHeadroom, cancelProviderLogin, changeProviderAccount, fetchProviderAccounts, fetchProviderLogin, providerAccountName, providerAccountsCatalogueKey, providerAccountsKey, renameProviderAccount, runAccountAction, spendAccountReset, startProviderLogin, switchSessionAccount, useProviderAccounts, type ProviderAccount, type ProviderAccounts, type ProviderLogin } from "../../hooks/useProviderAccounts";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { AccountChoiceLabel, AccountName } from "../AccountChoiceLabel";
import { AgentAvatar } from "../AgentAvatar";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { SettingsSection } from "./SettingsSection";

const loginKey = ["provider-account-login"] as const;

const DAY_SECONDS = 24 * 60 * 60;

type Provider = "codex" | "claude";
const PROVIDERS = [
	{ id: "codex", name: "Codex", agent: "codex", agentName: "Codex", key: "sk-…", baseUrl: "https://api.openai.com/v1" },
	{ id: "claude", name: "Claude", agent: "claude-code", agentName: "Claude Code", key: "sk-ant-…", baseUrl: "https://api.anthropic.com" },
] as const;
const providerOf = (account: Pick<ProviderAccount, "provider">): Provider => account.provider === "claude" ? "claude" : "codex";
const providerInfo = (provider: Provider) => PROVIDERS.find(entry => entry.id === provider)!;

type UsageWindow = NonNullable<NonNullable<ProviderAccount["usage"]>["windows"]>[number];

type Usage = NonNullable<ProviderAccount["usage"]>;

// How long a window runs, when the provider reports it.
function windowLength(window: UsageWindow, translate: TFunction): string {
	const seconds = window.durationSeconds ?? 0;
	if (seconds === 7 * DAY_SECONDS) return translate("providerAccounts.usageWindowWeekly");
	if (seconds >= DAY_SECONDS && seconds % DAY_SECONDS === 0) return translate("providerAccounts.usageWindowDays", { days: seconds / DAY_SECONDS });
	if (seconds >= 3600) return translate("providerAccounts.usageWindowHours", { hours: Math.round(seconds / 3600) });
	return "";
}

// What a scoped limit covers. The account's two general limits have no scope.
function windowScope(window: UsageWindow, translate: TFunction): string {
	if (window.scope === "code_review") return translate("providerAccounts.limitCodeReview");
	if (window.scope === "cowork") return translate("providerAccounts.limitCowork");
	if (window.scope === "oauth_apps") return translate("providerAccounts.limitApps");
	return window.name ?? "";
}

function formatResetTime(value: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	return new Intl.DateTimeFormat(undefined, {
		month: "short",
		day: "numeric",
		hour: "numeric",
		minute: "2-digit",
	}).format(date);
}

// A day, with its year only when that is not this year.
function formatDay(value: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: date.getFullYear() === new Date().getFullYear() ? undefined : "numeric" }).format(date);
}

// A moment soon: the time alone today, the day and time otherwise.
function formatSoon(value: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	if (date.toDateString() !== new Date().toDateString()) return formatResetTime(value);
	return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
}

function formatAgo(value: string, language: string, justNow: string): string {
	const minutes = Math.floor((Date.now() - new Date(value).getTime()) / 60_000);
	if (!Number.isFinite(minutes)) return value;
	if (minutes < 1) return justNow;
	const relative = new Intl.RelativeTimeFormat(language, { numeric: "always" });
	if (minutes < 60) return relative.format(-minutes, "minute");
	if (minutes < 24 * 60) return relative.format(-Math.floor(minutes / 60), "hour");
	return relative.format(-Math.floor(minutes / (24 * 60)), "day");
}

// A large count, shortened: 1.2M, 482M, 142B.
function formatCount(value: number, language: string): string {
	return new Intl.NumberFormat(language, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

// A span of time in the platform's own words, so plurals follow the language.
function formatSpan(value: number, unit: "day" | "hour" | "minute", language: string): string {
	return new Intl.NumberFormat(language, { style: "unit", unit, unitDisplay: unit === "day" ? "long" : "short" }).format(value);
}

function formatTurn(seconds: number, language: string): string {
	const minutes = Math.max(1, Math.round(seconds / 60));
	if (minutes < 60) return formatSpan(minutes, "minute", language);
	const hours = Math.floor(minutes / 60);
	return [formatSpan(hours, "hour", language), minutes % 60 ? formatSpan(minutes % 60, "minute", language) : ""].filter(Boolean).join(" ");
}

// The provider counts tokens by calendar day; "today" is its day or ours.
function isToday(day: string): boolean {
	const now = new Date();
	const local = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
	return day === local || day === now.toISOString().slice(0, 10);
}

function usagePercent(value: number): number {
	return typeof value === "number" && Number.isFinite(value) ? Math.max(0, Math.min(100, Math.round(value * 100))) : 0;
}

// While the account helper holds an account back after a provider refusal.
function pausedUntil(usage: Usage | undefined): string {
	const until = usage?.pausedUntil;
	return until && new Date(until).getTime() > Date.now() ? until : "";
}

// A group of settings rows: one softly filled surface with hairlines between
// its rows. The card around the page is the only outline.
function Group({ children }: { children: ReactNode }) {
	return <div className="divide-y divide-border rounded-xl bg-foreground/[0.035]">{children}</div>;
}

function Heading({ children }: { children: ReactNode }) {
	return <h4 className="mb-2 mt-6 text-sm font-normal text-muted-foreground">{children}</h4>;
}

// One settings row: what it is on the left, its one control on the right.
function Row({ title, hint, children, label, icon }: { title: ReactNode; hint?: ReactNode; children?: ReactNode; label?: string; icon?: ReactNode }) {
	return (
		<div role={label ? "group" : undefined} aria-label={label} className="flex min-h-15 flex-wrap items-center justify-between gap-x-5 gap-y-2 px-4 py-3">
			<div className="flex min-w-0 items-center gap-3">
				{icon}
				<div className="min-w-0">
					<p className="text-sm font-medium text-foreground">{title}</p>
					{hint ? <p className="mt-px text-xs text-muted-foreground">{hint}</p> : null}
				</div>
			</div>
			{children ? <div className="flex shrink-0 items-center gap-2">{children}</div> : null}
		</div>
	);
}

// A plain fact: its name on the left, its value on the right.
function Fact({ label, children }: { label: ReactNode; children: ReactNode }) {
	return (
		<div className="flex min-h-11 items-center justify-between gap-4 px-4 py-2 text-[13px]">
			<span className="min-w-0 truncate text-muted-foreground">{label}</span>
			<span className="flex shrink-0 items-center gap-1.5 tabular-nums text-foreground">{children}</span>
		</div>
	);
}

// What is left of an allowance, with room to read: its name on the left, a wide
// bar, and the figure. Colour appears only when it is nearly used (caution) or
// used up (needs attention).
function MeterRow({ testId, title, hint, percent, value, meterLabel }: { testId?: string; title: string; hint?: string; percent: number; value?: string; meterLabel: string }) {
	const { t } = useTranslation();
	const reached = percent === 0;
	const low = !reached && percent <= 20;
	return (
		<div data-testid={testId} className="flex min-h-15 items-center gap-5 px-4 py-3">
			<div className="w-44 shrink-0">
				<p className="truncate text-sm font-medium text-foreground" title={title}>{title}</p>
				{hint ? <p className="mt-px truncate text-xs text-muted-foreground">{hint}</p> : null}
			</div>
			<div
				role="progressbar"
				aria-label={meterLabel}
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={percent}
				className={cn("h-1.5 min-w-0 max-w-[420px] flex-1 overflow-hidden rounded-full", reached ? "bg-status-needs-you/40" : "bg-muted")}
			>
				<div className={cn("h-full rounded-full transition-[width]", low ? "bg-warning" : "bg-muted-foreground")} style={{ width: `${percent}%` }} />
			</div>
			<span className={cn("ml-auto min-w-20 shrink-0 text-right text-sm tabular-nums", reached ? "text-status-needs-you" : low ? "text-warning" : "text-foreground")}>
				{value ?? (reached ? t("providerAccounts.usageReached") : t("providerAccounts.usageRemaining", { percent }))}
			</span>
		</div>
	);
}

// One limit. A general limit is named by its length; a scoped one by what it
// covers, with its length beside the reset time.
function UsageRow({ account, window, index }: { account: ProviderAccount; window: UsageWindow; index: number }) {
	const { t } = useTranslation();
	const length = windowLength(window, t);
	const scope = windowScope(window, t);
	const title = scope || length || t("providerAccounts.usageLabel");
	const resets = window.resetTime ? t("providerAccounts.usageResets", { reset: formatResetTime(window.resetTime) }) : "";
	return (
		<MeterRow
			testId={`provider-account-usage-${account.id}${index ? `-${index}` : ""}`}
			title={title}
			hint={[scope ? length : "", resets].filter(Boolean).join(" · ") || undefined}
			percent={usagePercent(window.remainingFraction)}
			meterLabel={t("providerAccounts.usageMeterLabel", { account: account.email, window: [scope, length].filter(Boolean).join(" · ") || title })}
		/>
	);
}

type Removal = { account: ProviderAccount; action: "remove" | "sign-out" };
// After the default changes, the sessions still on the old default can follow.
type MoveOffer = { fromId: string; toId: string };
type Method = "browser" | "device" | "api_key" | "import";

// Settings is a full page, so Accounts is a list and a detail: every account on
// the left, and the one that is selected on the right with room for its usage
// and each of its actions as a labelled row.
export function ProviderAccountsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t, i18n } = useTranslation();
	const query = useProviderAccounts(true, false);
	const usageQuery = useProviderAccounts(true, true);
	const cache = useQueryClient();
	function setAccountData(next: ProviderAccounts) {
		cache.setQueryData(providerAccountsKey, next);
		cache.setQueryData(providerAccountsCatalogueKey, next);
	}
	// While this page is open, coming back to the window re-checks every
	// account's sign-in, as opening settings does.
	const [checking, setChecking] = useState(false);
	const checkNow = useCallback(async () => {
		setChecking(true);
		try {
			cache.setQueryData(providerAccountsKey, await fetchProviderAccounts(true, true));
		} catch {
			// The cached accounts stay; the next automatic check tries again.
		} finally {
			setChecking(false);
		}
	}, [cache]);
	useEffect(() => {
		const onVisibility = () => { if (document.visibilityState === "visible") void checkNow(); };
		const onFocus = () => void checkNow();
		window.addEventListener("focus", onFocus);
		document.addEventListener("visibilitychange", onVisibility);
		return () => { window.removeEventListener("focus", onFocus); document.removeEventListener("visibilitychange", onVisibility); };
	}, [checkNow]);
	// "Checked just now" ages while the page stays open.
	const [, setTick] = useState(0);
	useEffect(() => {
		const timer = setInterval(() => setTick(tick => tick + 1), 30_000);
		return () => clearInterval(timer);
	}, []);

	const [login, updateLogin] = useState<ProviderLogin | null>(() => cache.getQueryData<ProviderLogin>(loginKey) ?? null);
	function setLogin(next: ProviderLogin | null) {
		cache.setQueryData(loginKey, next);
		updateLogin(next);
	}
	const waiting = login?.status === "waiting";
	const loginProvider: Provider | null = login ? (login.provider === "claude" ? "claude" : "codex") : null;
	const [pending, setPending] = useState(false);
	const [message, setMessage] = useState("");
	const [selectedId, setSelectedId] = useState<string | null>(() => cache.getQueryData<ProviderLogin>(loginKey)?.accountId || null);
	// Which provider's "add an account" page is showing on the right, if any.
	const [adding, setAdding] = useState<Provider | null>(() => {
		const cached = cache.getQueryData<ProviderLogin>(loginKey);
		return cached?.status === "waiting" && !cached.accountId && (cached.provider === "codex" || cached.provider === "claude") ? cached.provider : null;
	});
	const [removal, setRemoval] = useState<Removal | null>(null);
	const [moveOffer, setMoveOffer] = useState<MoveOffer | null>(null);
	// The account whose "Use reset" is asking for confirmation.
	const [resetAsk, setResetAsk] = useState<string | null>(null);
	const [replacement, setReplacement] = useState("");
	const [apiKeyOpen, setApiKeyOpen] = useState(false);
	const [apiKey, setApiKey] = useState("");
	const [baseUrl, setBaseUrl] = useState("");
	const [label, setLabel] = useState("");
	const [apiKeyShown, setApiKeyShown] = useState(false);
	const [copiedLoginValue, setCopiedLoginValue] = useState<"link" | "code" | null>(null);
	const loginStatusFailures = useRef(0);
	// A copied icon shows its tick for a moment, then goes back.
	useEffect(() => {
		if (!copiedLoginValue) return;
		const timer = setTimeout(() => setCopiedLoginValue(null), 1500);
		return () => clearTimeout(timer);
	}, [copiedLoginValue]);
	// An account whose saved sign-in the provider no longer accepts is shown and
	// handled exactly like a signed-out one: "Sign in again" replaces it.
	const accounts = query.data?.accounts.map((account) => ({
		...account,
		signedIn: account.signedIn && !account.signInRequired,
		usage: usageQuery.data?.accounts.find((withUsage) => withUsage.id === account.id)?.usage,
	})) ?? [];
	useEffect(() => {
		if (!login || login.status !== "waiting") return;
		let mounted = true;
		let timer: ReturnType<typeof setTimeout>;
		loginStatusFailures.current = 0;
		const poll = async () => {
			try {
				const next = await fetchProviderLogin(login.id);
				if (!mounted) return;
				setLogin(next);
				if (next.status === "complete") {
					setMessage(t("providerAccounts.loginComplete"));
					setAdding(null);
					// The account that was just signed in becomes the one on show.
					if (next.accountId) setSelectedId(next.accountId);
					void cache.invalidateQueries({ queryKey: providerAccountsKey });
					void cache.invalidateQueries({ queryKey: providerAccountsCatalogueKey });
				} else if (next.status === "failed") {
					setAdding(null);
					setMessage(t("providerAccounts.loginFailed"));
				}
				else timer = setTimeout(poll, 1500);
			} catch (error) {
				if (!mounted) return;
				if (error instanceof Error && "code" in error && error.code === "PROVIDER_LOGIN_NOT_FOUND") {
					setLogin({ ...login, status: "failed" });
					setAdding(null);
					setMessage(t("providerAccounts.loginFailed"));
				} else if (++loginStatusFailures.current >= 5) {
					setLogin({ ...login, status: "failed" });
					setAdding(null);
					setMessage(t("providerAccounts.loginStatusFailed"));
				} else { setMessage(error instanceof Error ? error.message : t("providerAccounts.loginStatusFailed")); timer = setTimeout(poll, 3000); }
			}
		};
		timer = setTimeout(poll, 1000);
		return () => { mounted = false; clearTimeout(timer); };
	}, [login?.id, login?.status, cache, t]);
	async function run(action: () => Promise<void>) {
		setPending(true); setMessage("");
		try { await action(); } catch (error) { setMessage(error instanceof Error ? error.message : t("providerAccounts.operationFailed")); }
		finally { setPending(false); }
	}
	async function copyLoginValue(value: string, kind: "link" | "code") {
		try {
			await aoBridge.clipboard.writeText(value);
			setCopiedLoginValue(kind);
		} catch (error) {
			setMessage(error instanceof Error ? error.message : t("providerAccounts.operationFailed"));
		}
	}
	function closeApiKeyForm() {
		setApiKeyOpen(false);
		setApiKeyShown(false);
		setApiKey(""); setBaseUrl(""); setLabel("");
	}
	function beginLogin(provider: Provider, accountId?: string, mode: Method = "browser", credentialJson?: string) {
		void run(async () => {
			setCopiedLoginValue(null);
			const next = await startProviderLogin({ provider, accountId, mode, apiKey: mode === "api_key" ? apiKey : undefined, baseUrl: mode === "api_key" ? baseUrl : undefined, label: mode === "api_key" ? label : undefined, credentialJson });
			setLogin(next);
			if (!accountId) setAdding(provider);
			closeApiKeyForm();
		});
	}
	function chooseImport(provider: Provider) {
		return async (event: ChangeEvent<HTMLInputElement>) => {
			const file = event.target.files?.[0];
			if (!file) return;
			try {
				if (file.size > 1 << 20) throw new Error("Credential JSON exceeds 1 MiB.");
				beginLogin(provider, undefined, "import", await file.text());
			} catch (error) { setMessage(error instanceof Error ? error.message : t("providerAccounts.operationFailed")); }
			event.target.value = "";
		};
	}
	function select(accountId: string) {
		setSelectedId(accountId);
		setAdding(null);
		setRemoval(null);
		setResetAsk(null);
		setMessage("");
		closeApiKeyForm();
	}
	function openAdd(provider: Provider) {
		setAdding(provider);
		setRemoval(null);
		setMessage("");
		closeApiKeyForm();
	}
	function rename(account: ProviderAccount, value: string) {
		const nextName = value.trim();
		if (!nextName || nextName === providerAccountName(account)) return;
		void run(async () => {
			setAccountData(await renameProviderAccount(account.id, nextName));
			setMessage(t("providerAccounts.nameUpdated"));
		});
	}
	const usable = (provider: string, exceptId: string) => accounts.filter(a => a.provider === provider && a.signedIn && a.id !== exceptId);
	const alternatives = removal ? usable(removal.account.provider, removal.account.id) : [];
	const needsReplacement = Boolean(removal?.account.primary && alternatives.length);

	function askRemoval(account: ProviderAccount, action: Removal["action"]) {
		// The next signed-in account is offered as the new default straight away.
		setReplacement(account.primary ? usable(account.provider, account.id)[0]?.id ?? "" : "");
		setRemoval({ account, action });
	}
	// The default only decides where a new session starts, so it changes at
	// once. Sessions already running stay put unless they are moved.
	function makeDefault(account: ProviderAccount) {
		if (account.primary) return;
		const previous = accounts.find(a => a.provider === account.provider && a.primary);
		setRemoval(null);
		void run(async () => {
			setAccountData(await changeProviderAccount(account.id, "primary", undefined, false));
			setMoveOffer(previous?.sessions.length ? { fromId: previous.id, toId: account.id } : null);
		});
	}
	// The same switch a session's own menu makes, one session at a time.
	function moveSessions(from: ProviderAccount, to: ProviderAccount) {
		const count = from.sessions.length;
		void run(async () => {
			try {
				for (const sessionId of from.sessions) await switchSessionAccount(sessionId, to.id);
				setMoveOffer(null);
				setMessage(t("providerAccounts.sessionsMoved", { count, name: providerAccountName(to) }));
			} finally {
				void cache.invalidateQueries({ queryKey: providerAccountsKey });
				void cache.invalidateQueries({ queryKey: ["session-provider-account"] });
			}
		});
	}

	function resume(account: ProviderAccount) {
		void run(async () => {
			setAccountData(await runAccountAction(account.id, "resume"));
			setMessage(t("providerAccounts.accountResumed", { name: providerAccountName(account) }));
		});
	}
	function refreshSignIn(account: ProviderAccount) {
		void run(async () => {
			setAccountData(await runAccountAction(account.id, "refresh-sign-in"));
			setMessage(t("providerAccounts.signInRefreshed"));
		});
	}
	// A reset cannot be taken back, so it is one attempt per confirmation and is
	// never repeated here: the outcome is stated and usage is read again.
	function spendReset(account: ProviderAccount) {
		void run(async () => {
			try {
				const outcome = await spendAccountReset(account.id);
				const name = providerAccountName(account);
				setMessage(outcome === "reset" ? t("providerAccounts.resetDone", { name })
					: outcome === "nothing_to_reset" ? t("providerAccounts.resetNothing")
						: outcome === "none_available" ? t("providerAccounts.resetNone")
							: outcome === "wait" ? t("providerAccounts.resetWait")
								: outcome === "failed" ? t("providerAccounts.resetFailed") : t("providerAccounts.resetUnknown"));
			} finally {
				setResetAsk(null);
				await checkNow();
			}
		});
	}

	const iconAction = (name: string, icon: ReactNode, onClick: () => void, off = false) => (
		<Button type="button" size="icon-sm" variant="ghost" className="text-muted-foreground" aria-label={name} title={name} disabled={off} onClick={onClick}>{icon}</Button>
	);
	const copied = <Check aria-hidden="true" className="size-3.5 text-status-ready" />;
	// A sign-in in progress: one line, with small icon actions at its end.
	function loginProgress(signingIn: ProviderLogin, method: Method, providerName: string) {
		return (
			<>
				<div className="flex min-h-7 items-center gap-2.5 text-sm text-foreground">
					<LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-status-working" />
					<span className="min-w-0 flex-1">{method === "device" ? t("providerAccounts.deviceSignIn", { provider: providerName }) : method === "import" ? t("providerAccounts.importWaiting") : method === "api_key" ? t("providerAccounts.apiKeyWaiting") : t("providerAccounts.browserSignIn")}</span>
					<span className="flex shrink-0 items-center gap-0.5">
						{signingIn.url ? <>
							{iconAction(t("providerAccounts.openSignInPage"), <ExternalLink aria-hidden="true" className="size-3.5" />, () => void run(async () => { await aoBridge.app.openExternal(signingIn.url!); }))}
							{iconAction(copiedLoginValue === "link" ? t("startup.commandCopied") : t("link.copy"), copiedLoginValue === "link" ? copied : <Link2 aria-hidden="true" className="size-3.5" />, () => void copyLoginValue(signingIn.url!, "link"))}
						</> : null}
						{iconAction(t("providerAccounts.cancelSignIn"), <X aria-hidden="true" className="size-3.5" />, () => void run(async () => { await cancelProviderLogin(signingIn.id); setLogin(null); setCopiedLoginValue(null); }), pending)}
					</span>
				</div>
				{method === "device" && signingIn.code ? (
					<div className="mt-2 inline-flex h-8 items-center gap-2 rounded-md border border-input bg-background pl-3 pr-0.5 font-mono text-sm font-medium tracking-widest text-foreground">
						{signingIn.code}
						{iconAction(copiedLoginValue === "code" ? t("startup.commandCopied") : t("providerAccounts.copyCode"), copiedLoginValue === "code" ? copied : <Copy aria-hidden="true" className="size-3.5" />, () => void copyLoginValue(signingIn.code!, "code"))}
					</div>
				) : null}
			</>
		);
	}
	const methodOf = (signingIn: ProviderLogin): Method => signingIn.mode === "device" || signingIn.mode === "api_key" || signingIn.mode === "import" ? signingIn.mode : "browser";

	// Adding an account takes over the right side. Sign-in methods are adjacent
	// choices in one group; the chosen one opens under its own row.
	function addView(provider: Provider) {
		const info = providerInfo(provider);
		const signingIn = waiting && login && loginProvider === provider && !login.accountId ? login : null;
		const active = signingIn ? methodOf(signingIn) : null;
		const disabled = pending || waiting;
		const head = "grid w-full grid-cols-[18px_minmax(0,1fr)_14px] items-center gap-3.5 px-4 py-3.5 text-left text-muted-foreground transition-colors hover:bg-interactive-hover disabled:pointer-events-none";
		const row = (method: Method) => cn(signingIn && active !== method ? "opacity-40" : "");
		const describe = (name: string, hint: string, open: boolean) => <span className="min-w-0"><span className="block text-sm font-medium text-foreground">{name}</span>{open ? null : <span className="block truncate text-xs">{hint}</span>}</span>;
		const chevron = (open: boolean) => open ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />;
		const progress = (method: Method) => signingIn && active === method ? <div className="pb-4 pl-12 pr-3">{loginProgress(signingIn, method, info.name)}</div> : null;
		const field = "h-8 min-w-0 rounded-md border border-input bg-background px-2.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring";
		const formOpen = apiKeyOpen && !signingIn;
		return (
			<div role="group" aria-label={t("providerAccounts.signInMethods", { provider })}>
				<div className="flex min-h-11 items-center gap-3.5">
					<span aria-hidden="true" className="grid size-10 shrink-0 place-items-center rounded-xl border border-dashed border-border text-muted-foreground"><Plus className="size-4" /></span>
					<div className="min-w-0 flex-1">
						<h3 className="truncate text-lg font-semibold tracking-tight text-foreground">{t("providerAccounts.addAccountTitle", { provider: info.name })}</h3>
						<p className="text-sm text-muted-foreground">{t("providerAccounts.chooseSignIn")}</p>
					</div>
					{waiting ? null : <Button size="icon-sm" variant="ghost" className="text-muted-foreground" aria-label={t("common.close")} title={t("common.close")} onClick={() => { setAdding(null); closeApiKeyForm(); }}><X aria-hidden="true" className="size-4" /></Button>}
				</div>
				<Heading>{t("providerAccounts.signInMethod")}</Heading>
				<div className="divide-y divide-border overflow-hidden rounded-xl bg-foreground/[0.035]">
					<div className={row("browser")}>
						<button type="button" className={head} aria-label={t("settings.browserProfiles")} disabled={disabled} onClick={() => beginLogin(provider)}>
							<LogIn aria-hidden="true" className="size-4" />{describe(t("settings.browserProfiles"), t("providerAccounts.browserMethodHint"), active === "browser")}{chevron(active === "browser")}
						</button>
						{progress("browser")}
					</div>
					{provider === "codex" ? (
						<div className={row("device")}>
							<button type="button" className={head} aria-label={t("providerAccounts.deviceMethod")} disabled={disabled} onClick={() => beginLogin(provider, undefined, "device")}>
								<MonitorSmartphone aria-hidden="true" className="size-4" />{describe(t("providerAccounts.deviceMethod"), t("providerAccounts.deviceMethodHint"), active === "device")}{chevron(active === "device")}
							</button>
							{progress("device")}
						</div>
					) : null}
					<div className={row("api_key")}>
						<button type="button" className={head} aria-label={t("providerAccounts.apiKeyLabel")} aria-expanded={formOpen} disabled={disabled} onClick={() => { if (apiKeyOpen) closeApiKeyForm(); else setApiKeyOpen(true); }}>
							<KeyRound aria-hidden="true" className="size-4" />{describe(t("providerAccounts.apiKeyLabel"), t("providerAccounts.apiKeyMethodHint"), formOpen || active === "api_key")}{chevron(formOpen || active === "api_key")}
						</button>
						{progress("api_key")}
						{formOpen ? (
							<div className="grid max-w-[620px] gap-2 pb-4 pl-12 pr-4 sm:grid-cols-2">
								<div className="relative min-w-0">
									<input className={cn(field, "w-full pr-8")} aria-label={t("providerAccounts.apiKeyLabel")} value={apiKey} onChange={event => setApiKey(event.target.value)} placeholder={t("providerAccounts.apiKeyPlaceholder", { example: info.key })} type={apiKeyShown ? "text" : "password"} autoComplete="off" spellCheck={false} />
									<Button type="button" size="icon-sm" variant="ghost" className="absolute right-0.5 top-0.5 text-muted-foreground" aria-label={t(apiKeyShown ? "providerAccounts.hideApiKey" : "providerAccounts.showApiKey")} title={t(apiKeyShown ? "providerAccounts.hideApiKey" : "providerAccounts.showApiKey")} aria-pressed={apiKeyShown} onClick={() => setApiKeyShown(shown => !shown)}>
										{apiKeyShown ? <EyeOff aria-hidden="true" className="size-3.5" /> : <Eye aria-hidden="true" className="size-3.5" />}
									</Button>
								</div>
								<input className={field} aria-label={t("providerAccounts.baseUrlLabel")} value={baseUrl} onChange={event => setBaseUrl(event.target.value)} placeholder={t("providerAccounts.baseUrlPlaceholder", { example: info.baseUrl })} spellCheck={false} />
								<input className={field} aria-label={t("providerAccounts.displayLabel")} value={label} onChange={event => setLabel(event.target.value)} placeholder={t("providerAccounts.displayLabel")} />
								<div><Button className="h-8" size="sm" disabled={pending || !apiKey.trim() || !baseUrl.trim()} onClick={() => beginLogin(provider, undefined, "api_key")}>{t("providerAccounts.addApiKey")}</Button></div>
							</div>
						) : null}
					</div>
					<div className={row("import")}>
						<label className={cn(head, "cursor-pointer", disabled ? "pointer-events-none" : "")}>
							<FileUp aria-hidden="true" className="size-4" />{describe(t("providerAccounts.importMethod"), t("providerAccounts.importMethodHint"), active === "import")}{chevron(active === "import")}
							<input className="sr-only" aria-label={t("providerAccounts.importMethod")} type="file" disabled={disabled} accept=".json,application/json" onChange={event => void chooseImport(provider)(event)} />
						</label>
						{progress("import")}
					</div>
				</div>
			</div>
		);
	}

	const dangerButton = "bg-destructive/15 text-destructive hover:bg-destructive/25 dark:hover:bg-destructive/25";
	// Signing out or removing asks once, in the same row, with at most one fact.
	function removalRow(account: ProviderAccount, action: Removal["action"]) {
		const name = providerAccountName(account);
		const signOut = action === "sign-out";
		if (removal?.account.id !== account.id) {
			return (
				<Row title={signOut ? t("shell.signOut") : t("providerAccounts.removeAccount")} hint={t(signOut ? "providerAccounts.signOutHint" : "providerAccounts.removeHint")}>
					<Button type="button" variant="ghost" className={dangerButton} disabled={pending} onClick={() => askRemoval(account, action)}>{t(signOut ? "shell.signOut" : "shell.remove")}</Button>
				</Row>
			);
		}
		const count = account.sessions.length;
		const fallback = alternatives.find(a => a.primary);
		const chosen = alternatives.find(a => a.id === replacement);
		// The one fact worth stating: where this account's sessions go.
		const fact = needsReplacement ? t("providerAccounts.chooseNewDefault")
			: !count ? ""
				: !alternatives.length ? t("providerAccounts.sessionsWaitForSignIn", { count })
					: fallback ? t("providerAccounts.sessionsMoveTo", { count, name: providerAccountName(fallback) }) : "";
		return (
			<Row label={t("providerAccounts.confirmChange")} title={t(signOut ? "providerAccounts.confirmSignOut" : "providerAccounts.confirmRemove", { email: name })} hint={fact || undefined}>
				{needsReplacement ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button type="button" variant="secondary" className="max-w-56 gap-1.5 font-normal" aria-label={t("providerAccounts.replacementPrimary")} disabled={pending}>
								<span className="min-w-0 truncate">{chosen ? <AccountName account={chosen} /> : t("providerAccounts.chooseAccount")}</span>
								<ChevronDown aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-60 text-xs">
							{alternatives.map(a => (
								<DropdownMenuItem key={a.id} onSelect={() => setReplacement(a.id)}>
									<Check aria-hidden="true" className={a.id === replacement ? "size-3.5" : "invisible size-3.5"} />
									<AccountChoiceLabel account={a} isDefault={false} />
								</DropdownMenuItem>
							))}
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
				<Button type="button" variant="ghost" className="text-muted-foreground" disabled={pending} onClick={() => setRemoval(null)}>{t("confirm.cancel")}</Button>
				<Button
					type="button"
					variant="ghost"
					className={dangerButton}
					disabled={pending || (needsReplacement && !replacement)}
					onClick={() => void run(async () => {
						setAccountData(await changeProviderAccount(account.id, action, replacement || undefined));
						setRemoval(null);
						setMessage(t(signOut ? "providerAccounts.signedOutOf" : "providerAccounts.removedAccount", { name }));
					})}
				>
					{t(signOut ? "shell.signOut" : "shell.remove")}
				</Button>
			</Row>
		);
	}

	// The limits of one account: a pause notice when the helper is holding it
	// back, every limit the provider reports, then what is spent beyond the plan.
	function limitsGroup(account: ProviderAccount, usage: Usage | undefined) {
		const windows = usage?.windows ?? [];
		const paused = pausedUntil(usage);
		const extra = usage?.extraUsage;
		const money = new Intl.NumberFormat(i18n.language, { style: "currency", currency: "USD" });
		return (
			<Group>
				{paused ? (
					<Row icon={<CirclePause aria-hidden="true" className="size-4 shrink-0 text-warning" />} title={t("providerAccounts.pausedUntil", { time: formatSoon(paused) })} hint={t(usage?.pausedReason === "quota" || usage?.pausedReason === "credential_quota" ? "providerAccounts.pausedRateLimited" : "providerAccounts.pausedRefused")}>
						<Button type="button" variant="secondary" disabled={pending} onClick={() => resume(account)}>{t("providerAccounts.resumeNow")}</Button>
					</Row>
				) : null}
				{windows.length ? windows.map((window, index) => <UsageRow key={index} account={account} window={window} index={index} />) : (
					<div data-testid={`provider-account-usage-${account.id}`}><Row title={!account.usage ? t("providerAccounts.loading") : account.kind === "api_key" ? t("providerAccounts.usageNotReported") : t("providerAccounts.usageUnavailable")} /></div>
				)}
				{extra ? (extra.limitCents > 0 ? (
					<MeterRow
						title={t("providerAccounts.extraUsage")}
						hint={t("providerAccounts.extraUsageSpent", { used: money.format(extra.usedCents / 100), limit: money.format(extra.limitCents / 100) })}
						percent={usagePercent(1 - extra.usedCents / extra.limitCents)}
						value={t("providerAccounts.amountLeft", { amount: money.format(Math.max(0, extra.limitCents - extra.usedCents) / 100) })}
						meterLabel={t("providerAccounts.extraUsage")}
					/>
				) : <Row title={t("providerAccounts.extraUsage")} hint={t("providerAccounts.extraUsageSpentNoCap", { used: money.format(extra.usedCents / 100) })} />) : null}
				{usage?.credits ? (
					<Row title={t("providerAccounts.credits")} hint={t("providerAccounts.creditsHint")}>
						<span className="text-sm tabular-nums text-foreground">{usage.credits.unlimited ? t("providerAccounts.creditsUnlimited") : t("providerAccounts.creditsAmount", { amount: new Intl.NumberFormat(i18n.language, { maximumFractionDigits: 0 }).format(Number(usage.credits.balance)) })}</span>
					</Row>
				) : null}
			</Group>
		);
	}

	// Resets: how many are left, when each expires, and the one action that
	// spends one. It asks once, in place, because it cannot be taken back.
	function resetsGroup(account: ProviderAccount, usage: Usage | undefined) {
		const count = usage?.resetCredits;
		if (typeof count !== "number") return null;
		if (count <= 0) return <Group><Row title={t("providerAccounts.noResets")} /></Group>;
		const usable = Boolean(usage?.resetUsable);
		const blocked = usage?.resetBlockedUntil && new Date(usage.resetBlockedUntil).getTime() > Date.now() ? usage.resetBlockedUntil : "";
		return (
			<Group>
				{resetAsk === account.id ? (
					<Row label={t("providerAccounts.useReset")} title={t("providerAccounts.confirmUseReset", { name: providerAccountName(account) })} hint={t("providerAccounts.confirmUseResetHint")}>
						<Button type="button" variant="ghost" className="text-muted-foreground" disabled={pending} onClick={() => setResetAsk(null)}>{t("confirm.cancel")}</Button>
						<Button type="button" disabled={pending} onClick={() => spendReset(account)}>{t("providerAccounts.useReset")}</Button>
					</Row>
				) : (
					<Row title={t("providerAccounts.limitResets")} hint={usable ? t("providerAccounts.resetUsableHint") : blocked ? t("providerAccounts.resetBlockedHint", { time: formatSoon(blocked) }) : t("providerAccounts.resetIdleHint")}>
						<span className="text-sm tabular-nums text-foreground">{t("providerAccounts.resetsAvailableShort", { count })}</span>
						<Button type="button" variant={usable ? "primary" : "secondary"} disabled={pending || !usable} onClick={() => setResetAsk(account.id)}>{t("providerAccounts.useReset")}</Button>
					</Row>
				)}
				{(usage?.resets ?? []).map((reset, index) => (
					<Fact key={index} label={[reset.label || t("providerAccounts.resetItem", { number: index + 1 }), reset.total > 1 ? t("providerAccounts.resetLeftOf", { left: reset.left, total: reset.total }) : ""].filter(Boolean).join(" · ")}>
						{reset.expiresAt ? t("providerAccounts.resetExpires", { date: formatDay(reset.expiresAt) }) : null}
					</Fact>
				))}
			</Group>
		);
	}

	// The narrow column: what the plan is, then what the account has been doing.
	function planGroup(account: ProviderAccount, usage: Usage | undefined, plan: string) {
		const facts = [
			plan ? <Fact key="plan" label={t("providerAccounts.planHeading")}>{plan}</Fact> : null,
			usage?.renewsAt ? <Fact key="renews" label={t("providerAccounts.factRenews")}>{formatDay(usage.renewsAt)}</Fact> : null,
			usage?.organization ? <Fact key="organization" label={t("providerAccounts.factOrganization")}><span className="max-w-36 truncate" title={usage.organization}>{usage.organization}</span></Fact> : null,
			usage?.addedAt ? <Fact key="added" label={t("providerAccounts.factAdded")}>{formatDay(usage.addedAt)}</Fact> : null,
			// Shown whenever the helper reports on the account, so the refresh is
			// always within reach even when the time of the last one is not known.
			account.kind !== "api_key" && (usage?.refreshedAt || usage?.addedAt || usage?.requests) ? (
				<Fact key="refreshed" label={t("providerAccounts.factSignInRefreshed")}>
					{usage?.refreshedAt ? formatAgo(usage.refreshedAt, i18n.language, t("providerAccounts.justNow")) : t("providerAccounts.factUnknown")}
					<Button type="button" size="icon-sm" variant="ghost" className="-mr-1.5 text-muted-foreground" aria-label={t("providerAccounts.refreshSignIn")} title={t("providerAccounts.refreshSignIn")} disabled={pending} onClick={() => refreshSignIn(account)}><RefreshCw aria-hidden="true" className="size-3.5" /></Button>
				</Fact>
			) : null,
		].filter(Boolean);
		return facts.length ? <><Heading>{account.kind === "api_key" ? t("providerAccounts.apiKeyLabel") : t("providerAccounts.planHeading")}</Heading><Group>{facts}</Group></> : null;
	}
	// Activity: the requests AO's helper has carried, then the provider's own
	// tally of tokens.
	function activityGroup(usage: Usage | undefined) {
		const buckets = usage?.requests ?? [];
		const tokens = usage?.tokens;
		const language = i18n.language;
		const tokenFacts = tokens ? [
			typeof tokens.latestDayTokens === "number" && tokens.latestDay ? <Fact key="latest" label={isToday(tokens.latestDay) ? t("providerAccounts.tokensToday") : t("providerAccounts.tokensOnDay", { date: formatDay(`${tokens.latestDay}T12:00:00`) })}>{formatCount(tokens.latestDayTokens, language)}</Fact> : null,
			typeof tokens.lifetime === "number" ? <Fact key="lifetime" label={t("providerAccounts.tokensLifetime")}>{formatCount(tokens.lifetime, language)}</Fact> : null,
			typeof tokens.peakDaily === "number" ? <Fact key="peak" label={t("providerAccounts.tokensPeakDay")}>{formatCount(tokens.peakDaily, language)}</Fact> : null,
			typeof tokens.longestTurnSeconds === "number" && tokens.longestTurnSeconds > 0 ? <Fact key="turn" label={t("providerAccounts.longestTurn")}>{formatTurn(tokens.longestTurnSeconds, language)}</Fact> : null,
			typeof tokens.currentStreakDays === "number" ? <Fact key="streak" label={t("providerAccounts.currentStreak")}>{formatSpan(tokens.currentStreakDays, "day", language)}</Fact> : null,
			typeof tokens.longestStreakDays === "number" ? <Fact key="longest" label={t("providerAccounts.longestStreak")}>{formatSpan(tokens.longestStreakDays, "day", language)}</Fact> : null,
		].filter(Boolean) : [];
		if (!buckets.length && !tokenFacts.length) return null;
		const failed = buckets.reduce((sum, bucket) => sum + bucket.failed, 0);
		const total = buckets.reduce((sum, bucket) => sum + bucket.succeeded, failed);
		return (
			<>
				<Heading>{t("providerAccounts.activityHeading")}</Heading>
				<Group>
					{buckets.length ? (
						<div className="flex min-h-15 items-center justify-between gap-4 px-4 py-3">
							<div className="min-w-0">
								<p className="text-sm font-medium text-foreground">{t("providerAccounts.requests")}</p>
								<p className="mt-px text-xs text-muted-foreground">{t("providerAccounts.requestsWindow")}</p>
							</div>
							<div className="shrink-0 text-right">
								<p className="text-sm tabular-nums text-foreground">{new Intl.NumberFormat(language).format(total)}</p>
								<p className="mt-px text-xs tabular-nums text-muted-foreground">{failed ? t("providerAccounts.requestsFailed", { failed }) : t("providerAccounts.requestsNoneFailed")}</p>
							</div>
						</div>
					) : null}
					{tokenFacts}
				</Group>
			</>
		);
	}

	// The selected account: who it is, how much room it has, where sessions go,
	// and its own settings. Every action is a row with one control. A signed-in
	// account reads in two columns: what you act on beside what you only read.
	function accountView(account: ProviderAccount) {
		const provider = providerOf(account);
		const info = providerInfo(provider);
		const name = providerAccountName(account);
		// An API key has no limits to report, but the helper still knows its activity.
		const usage = account.usage?.status === "available" || account.kind === "api_key" ? account.usage : undefined;
		const planName = usage?.plan?.trim() ?? "";
		const plan = planName ? [planName.charAt(0).toUpperCase() + planName.slice(1), usage?.planTier].filter(Boolean).join(" ") : "";
		const others = usable(account.provider, account.id);
		const currentDefault = accounts.find(a => a.provider === account.provider && a.primary);
		const offerFrom = moveOffer?.toId === account.id && account.primary ? accounts.find(a => a.id === moveOffer.fromId) : undefined;
		const count = account.sessions.length;
		const signingIn = waiting && login && login.accountId === account.id ? login : null;
		const accountSettings = (
			<>
				<Heading>{t("providerAccounts.accountLabel")}</Heading>
				<Group>
					<Row title={t("providerAccounts.displayNameLabel")} hint={t("providerAccounts.displayNameHint")}>
						<input
							// Remounts with the saved name, so a refused rename shows the old one again.
							key={`${account.id}:${name}`}
							// Escape belongs to this field; it must not also close the settings page.
							data-settings-inline-edit=""
							className="h-8 w-[280px] max-w-full rounded-md border border-input bg-background px-2.5 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
							aria-label={t("providerAccounts.accountName")}
							defaultValue={name}
							disabled={pending}
							onBlur={event => rename(account, event.target.value)}
							onKeyDown={event => {
								if (event.key === "Enter") event.currentTarget.blur();
								if (event.key === "Escape") { event.currentTarget.value = name; event.currentTarget.blur(); }
							}}
						/>
					</Row>
					<Row title={t(account.kind === "api_key" ? "providerAccounts.keyLabel" : "providerAccounts.signedInAs")} hint={t(account.kind === "api_key" ? "providerAccounts.keyLabelHint" : "providerAccounts.emailHint")}>
						{/* Blurred until pointed at or focused, so a shared screen does not show it. */}
						<span tabIndex={0} title={account.email} className="max-w-[280px] truncate rounded-sm text-sm text-foreground blur-sm outline-none transition-[filter] hover:blur-none focus:blur-none">{account.email}</span>
					</Row>
					{removalRow(account, account.signedIn ? "sign-out" : "remove")}
				</Group>
			</>
		);
		const aside = account.signedIn ? [planGroup(account, usage, plan), activityGroup(usage)].filter(Boolean) : [];
		const resets = account.signedIn ? resetsGroup(account, usage) : null;
		return (
			<div data-testid="provider-account-detail">
				<div className="flex min-h-11 items-center gap-3.5">
					<AgentAvatar className="size-10 shrink-0" decorative provider={info.agent} />
					<div className="min-w-0 flex-1">
						<h3 className="truncate text-lg font-semibold tracking-tight text-foreground">{name}</h3>
						<p className="flex flex-wrap items-center gap-x-1.5 text-sm text-muted-foreground">
							<span>{info.name}</span>
							{account.kind === "api_key" ? <><span aria-hidden="true" className="text-passive">·</span><span>{t("providerAccounts.apiKeyLabel")}</span></> : plan ? <><span aria-hidden="true" className="text-passive">·</span><span>{plan}</span></> : null}
							{account.signedIn ? null : <><span aria-hidden="true" className="text-passive">·</span><span className="text-status-needs-you">{t("providerAccounts.signedOut")}</span></>}
						</p>
					</div>
					{account.global ? <span data-testid="provider-account-global" title={t(account.kind === "api_key" ? "providerAccounts.globalKeyHint" : "providerAccounts.globalHint")} className="shrink-0 rounded-full bg-interactive-active px-2.5 py-0.5 text-xs text-muted-foreground">{t("providerAccounts.global")}</span> : null}
					{account.primary && account.signedIn ? <span className="shrink-0 rounded-full bg-interactive-active px-2.5 py-0.5 text-xs text-muted-foreground">{t("providerAccounts.default")}</span> : null}
				</div>

				{account.signedIn ? (
					<div className={cn("grid items-start gap-x-7", aside.length ? "grid-cols-[minmax(0,1fr)_288px] @max-5xl:grid-cols-1" : "")}>
						<div className="min-w-0">
							{/* A sign-in that still works but has stopped renewing: say so while there is time to replace it. */}
							{account.usage?.signInEnding || signingIn ? (
								<div data-testid={`provider-account-sign-in-ending-${account.id}`} className="mt-4">
									<Group>
										{signingIn ? <div className="px-4 py-3.5">{loginProgress(signingIn, methodOf(signingIn), info.name)}</div> : (
											<Row
												icon={<AlertCircle aria-hidden="true" className="size-4 shrink-0 text-warning" />}
												title={t("providerAccounts.signInEnding")}
												hint={account.usage?.signInEndsAt ? t("providerAccounts.signInEndsAt", { time: formatSoon(account.usage.signInEndsAt) }) : t("providerAccounts.signInEndsSoon")}
											>
												<Button type="button" variant="secondary" disabled={pending || waiting} onClick={() => beginLogin(provider, account.id)}>{t("providerAccounts.signInAgain")}</Button>
											</Row>
										)}
									</Group>
								</div>
							) : null}
							<Heading>{t("providerAccounts.limitsHeading")}</Heading>
							{limitsGroup(account, usage)}
							{resets ? <><Heading>{t("providerAccounts.resetsHeading")}</Heading>{resets}</> : null}

							<Heading>{t("providerAccounts.sessionsHeading")}</Heading>
							<Group>
								<Row title={t("providerAccounts.newSessionsTitle", { agent: info.agentName })} hint={account.primary ? t("providerAccounts.startHere") : currentDefault ? t("providerAccounts.startOn", { name: providerAccountName(currentDefault) }) : undefined}>
									{account.primary
										? <span className="flex items-center gap-1.5 text-sm text-muted-foreground"><Check aria-hidden="true" className="size-3.5 text-status-ready" />{t("providerAccounts.default")}</span>
										: <Button type="button" variant="secondary" disabled={pending} onClick={() => makeDefault(account)}>{t("providerAccounts.makeDefault")}</Button>}
								</Row>
								{offerFrom?.sessions.length ? (
									<Row label={t("providerAccounts.moveSessions")} title={t("providerAccounts.sessionsStillUse", { count: offerFrom.sessions.length, name: providerAccountName(offerFrom) })} hint={t("providerAccounts.offerHint")}>
										<Button type="button" variant="secondary" disabled={pending} onClick={() => moveSessions(offerFrom, account)}>{t("providerAccounts.moveSessionsHere", { count: offerFrom.sessions.length })}</Button>
										<Button type="button" size="icon-sm" variant="ghost" className="text-muted-foreground" aria-label={t("providerAccounts.leaveSessions", { count: offerFrom.sessions.length })} title={t("providerAccounts.leaveSessions", { count: offerFrom.sessions.length })} disabled={pending} onClick={() => setMoveOffer(null)}><X aria-hidden="true" className="size-3.5" /></Button>
									</Row>
								) : null}
								<Row title={t("providerAccounts.runningSessions")} hint={count ? t("providerAccounts.sessionsUsing", { count }) : t("providerAccounts.noSessionsUsing")}>
									{count && others.length ? (
										<DropdownMenu>
											<DropdownMenuTrigger asChild>
												<Button type="button" variant="secondary" className="gap-1.5" disabled={pending}>{t("providerAccounts.moveSessions")}<ChevronDown aria-hidden="true" className="size-3.5 text-muted-foreground" /></Button>
											</DropdownMenuTrigger>
											<DropdownMenuContent align="end" className="min-w-60 text-xs">
												{others.map(target => (
													<DropdownMenuItem key={target.id} onSelect={() => moveSessions(account, target)}>
														<AccountChoiceLabel account={target} isDefault={target.primary} />
													</DropdownMenuItem>
												))}
											</DropdownMenuContent>
										</DropdownMenu>
									) : null}
								</Row>
							</Group>
							{accountSettings}
						</div>
						{aside.length ? <aside className="min-w-0">{aside}</aside> : null}
					</div>
				) : (
					<>
						<Heading>{t("providerAccounts.signIn")}</Heading>
						<Group>
							{signingIn ? <div className="px-4 py-3.5">{loginProgress(signingIn, methodOf(signingIn), info.name)}</div> : (
								<Row title={t("providerAccounts.signedOutTitle")} hint={count ? t("providerAccounts.sessionsWaitingFor", { count }) : t("providerAccounts.signInToUse")}>
									<Button type="button" disabled={pending || waiting} onClick={() => beginLogin(provider, account.id)}>{t("providerAccounts.signInAgain")}</Button>
								</Row>
							)}
						</Group>
						{accountSettings}
					</>
				)}
			</div>
		);
	}

	// What the list says about an account: whether it is the default, and how
	// much room its shortest window has left.
	function listItem(account: ProviderAccount, current: boolean) {
		const headroom = accountHeadroom(account);
		const paused = Boolean(pausedUntil(account.usage));
		// Global marks an account that is this computer's own sign-in; more than
		// one account can carry it.
		const global = account.global ? <span title={t(account.kind === "api_key" ? "providerAccounts.globalKeyHint" : "providerAccounts.globalHint")}>{t("providerAccounts.global")}</span> : null;
		const marks = (account.signedIn ? [
			account.primary ? <span>{t("providerAccounts.default")}</span> : null,
			global,
			paused ? <span className="text-warning">{t("providerAccounts.paused")}</span> : null,
			account.usage?.signInEnding ? <span className="text-warning">{t("providerAccounts.signInEndingMark")}</span> : null,
			account.kind === "api_key" ? <span>{t("providerAccounts.apiKeyLabel")}</span>
				: headroom === null ? null
					: headroom === 0 ? <span className="text-status-needs-you">{t("providerAccounts.limitReached")}</span>
						: <span className={cn("tabular-nums", headroom <= 20 ? "text-warning" : "")}>{t("providerAccounts.usageRemaining", { percent: headroom })}</span>,
		] : [global, <span className="text-status-needs-you">{t("providerAccounts.signedOut")}</span>]).filter(Boolean);
		return (
			<button
				key={account.id}
				type="button"
				data-testid={`provider-account-${account.id}`}
				aria-current={current ? "true" : undefined}
				className={cn("flex w-full items-center gap-3 rounded-[10px] px-3 py-2.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring", current ? "bg-interactive-active" : "hover:bg-interactive-hover")}
				onClick={() => select(account.id)}
			>
				<AgentAvatar className="size-7 shrink-0" decorative provider={providerInfo(providerOf(account)).agent} />
				<span className="min-w-0 flex-1">
					<span className="block truncate text-sm font-medium text-foreground">{providerAccountName(account)}</span>
					<span className="mt-px flex items-center gap-1.5 truncate text-xs text-muted-foreground">
						{marks.map((mark, index) => <Fragment key={index}>{index ? <span aria-hidden="true" className="text-passive">·</span> : null}{mark}</Fragment>)}
					</span>
				</span>
				{account.signedIn ? null : <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-status-needs-you" />}
			</button>
		);
	}

	const selected = adding ? null : accounts.find(account => account.id === selectedId) ?? accounts[0] ?? null;
	// A new sign-in that is still waiting keeps its place in the list, even while
	// another account is on show.
	const newLoginProvider = waiting && login && !login.accountId ? loginProvider : null;
	const minutes = usageQuery.dataUpdatedAt ? Math.floor((Date.now() - usageQuery.dataUpdatedAt) / 60_000) : null;
	const checkedWhen = minutes === null ? "" : minutes < 1 ? t("providerAccounts.justNow") : new Intl.RelativeTimeFormat(i18n.language, { numeric: "always" }).format(-minutes, "minute");
	const notices = query.isLoading || query.error || query.data?.recoveryRequired;
	return (
		<SettingsSection title={t("providerAccounts.title")} sectionId="accountManager" titleHidden={titleHidden}>
			<div className="@container mx-auto w-full max-w-[1280px]">
				<div className="mb-3 flex min-h-8 items-center gap-2">
					<h3 className="text-[15px] font-medium text-foreground">{t("providerAccounts.title")}</h3>
					<span className="flex-1" />
					{checkedWhen ? <span className="text-xs text-muted-foreground">{t("providerAccounts.checkedAt", { when: checkedWhen })}</span> : null}
					<Button type="button" size="icon-sm" variant="ghost" className="text-muted-foreground" aria-label={t("providerAccounts.checkAgain")} title={t("providerAccounts.checkAgain")} disabled={checking} onClick={() => void checkNow()}>
						<RefreshCw aria-hidden="true" className={cn("size-3.5", checking ? "animate-spin" : "")} />
					</Button>
				</div>

				{/* Only what concerns every account goes above the card. */}
				{notices ? (
					<div className="mb-3 flex flex-col gap-2">
						{query.isLoading ? <p className="text-xs text-muted-foreground">{t("providerAccounts.loading")}</p> : null}
						{query.error ? <p role="alert" className="flex items-center gap-2 text-xs text-destructive"><AlertCircle aria-hidden="true" className="size-3.5 shrink-0" />{query.error.message}</p> : null}
						{query.data?.recoveryRequired ? <p role="alert" className="flex items-center gap-2 rounded-xl bg-status-needs-you/10 px-3 py-2.5 text-sm text-foreground"><AlertCircle aria-hidden="true" className="size-4 shrink-0 text-status-needs-you" />{t("providerAccounts.recovery")}</p> : null}
					</div>
				) : null}

				<div className="grid min-h-[640px] grid-cols-[288px_minmax(0,1fr)] overflow-hidden rounded-2xl border border-border bg-card/45 @max-3xl:min-h-0 @max-3xl:grid-cols-1">
					{/* The list is for finding an account. Everything you can do to one is on the right. */}
					<nav aria-label={t("providerAccounts.title")} className="border-r border-border p-2 @max-3xl:border-b @max-3xl:border-r-0">
						{PROVIDERS.map(({ id: provider, name: providerName }) => {
							const providerAccounts = accounts.filter(account => account.provider === provider);
							return (
								<section key={provider} data-testid={`provider-section-${provider}`}>
									<div className="flex items-center justify-between pb-1 pl-3 pr-1.5 pt-3">
										<h4 className="text-xs font-normal text-muted-foreground">{providerName}</h4>
										<Button type="button" size="icon-sm" variant="ghost" className="text-muted-foreground" aria-label={t("providerAccounts.addAccount")} title={t("providerAccounts.addAccountTitle", { provider: providerName })} aria-expanded={adding === provider} disabled={pending || waiting} onClick={() => openAdd(provider)}>
											<Plus aria-hidden="true" className="size-3.5" />
										</Button>
									</div>
									<div className="flex flex-col gap-0.5">
										{providerAccounts.map(account => listItem(account, selected?.id === account.id))}
										{adding === provider || newLoginProvider === provider ? (
											<button type="button" aria-current={adding === provider ? "true" : undefined} className={cn("flex w-full items-center gap-3 rounded-[10px] px-3 py-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring", adding === provider ? "bg-interactive-active" : "hover:bg-interactive-hover")} onClick={() => openAdd(provider)}>
												<span aria-hidden="true" className="grid size-7 shrink-0 place-items-center rounded-lg border border-dashed border-border text-muted-foreground"><Plus className="size-3.5" /></span>
												<span className="min-w-0 flex-1">
													<span className="block truncate text-sm font-medium text-foreground">{t("providerAccounts.newAccount", { provider: providerName })}</span>
													<span className="mt-px block text-xs text-muted-foreground">{newLoginProvider === provider ? t("providerAccounts.signingIn") : t("providerAccounts.chooseSignIn")}</span>
												</span>
											</button>
										) : null}
									</div>
									{query.data && !providerAccounts.some(account => account.signedIn) ? (
										<div className="px-3 pb-2 pt-1.5">
											<p className="text-xs text-foreground">{t("providerAccounts.emptyAccounts", { provider: providerName })}</p>
											<p className="mt-0.5 text-xs text-muted-foreground">{query.data.defaults.find(defaultAccount => defaultAccount.provider === provider)?.managed ? t("providerAccounts.managedNeedsLogin") : t("providerAccounts.deviceSessionsContinue")}</p>
											{adding === provider ? null : <Button size="sm" className="mt-2" disabled={pending || waiting} onClick={() => openAdd(provider)}>{t("providerAccounts.signIn")}</Button>}
										</div>
									) : null}
								</section>
							);
						})}
					</nav>

					<div className="min-w-0 px-7 pb-7 pt-6 @max-3xl:px-4">
						{adding ? addView(adding) : selected ? accountView(selected) : query.data ? (
							<div className="grid min-h-[420px] place-content-center gap-1.5 text-center">
								<p className="text-[15px] font-medium text-foreground">{t("providerAccounts.noAccountsYet")}</p>
								<p className="text-sm text-muted-foreground">{t("providerAccounts.addHint")}</p>
							</div>
						) : null}
						{message ? <p role="status" className="mt-3.5 text-xs text-muted-foreground">{message}</p> : null}
					</div>
				</div>
			</div>
		</SettingsSection>
	);
}
