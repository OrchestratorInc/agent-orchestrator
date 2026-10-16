import { Fragment, useCallback, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, ChevronDown, ChevronRight, Eye, EyeOff, FileUp, KeyRound, LogIn, MonitorSmartphone, Plus, X } from "lucide-react";
import { accountAction, accountHeadroom, cancelProviderLogin, fetchProviderAccounts, fetchProviderLogin, PROVIDERS, providerAccountsCatalogueKey, providerAccountsKey, startProviderLogin, useProviderAccounts, type AccountAction, type ProviderAccount, type ProviderLogin, type ProviderLoginRequest } from "../../hooks/useProviderAccounts";
import { apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { AccountDetail, DeviceTag, Dot, IconAction, LoginProgress, Rows, upcoming, type AccountTab } from "./ProviderAccountDetail";

type Provider = ProviderAccount["provider"];
export type AccountsPage = ReturnType<typeof useAccountsPage>;
const loginKey = ["provider-account-login"] as const;
const METHODS = [
	{ id: "browser", icon: LogIn, name: "settings.browserProfiles", hint: "providerAccounts.browserMethodHint" },
	{ id: "device", icon: MonitorSmartphone, name: "providerAccounts.deviceMethod", hint: "providerAccounts.deviceMethodHint" },
	{ id: "api_key", icon: KeyRound, name: "providerAccounts.apiKeyLabel", hint: "providerAccounts.apiKeyMethodHint" },
	{ id: "import", icon: FileUp, name: "providerAccounts.importMethod", hint: "providerAccounts.importMethodHint" },
] as const;
// The one sign-in attempt, kept in the query cache so it survives leaving settings, and polled while it waits.
// A sign-in again names its account from the start; a new account gets its id when it completes.
function useProviderLogin(onSettled: (login: ProviderLogin, unreachable: boolean, again: boolean) => void) {
	const cache = useQueryClient();
	const login = useQuery<ProviderLogin | null>({ queryKey: loginKey, queryFn: skipToken }).data ?? null;
	const waiting = login?.status === "waiting";
	const poll = useQuery<ProviderLogin, Error & { code?: string }>({
		queryKey: [...loginKey, login?.id],
		queryFn: () => fetchProviderLogin(login!.id),
		enabled: waiting,
		refetchInterval: 1500,
		refetchIntervalInBackground: true,
		retry: (failures, error) => failures < 4 && error.code !== "PROVIDER_LOGIN_NOT_FOUND",
		gcTime: 0,
	});
	useEffect(() => {
		const next = poll.error && login ? { ...login, status: "failed" as const } : poll.data;
		if (!waiting || !next || next.status === "waiting") return;
		cache.setQueryData(loginKey, next);
		if (next.status !== "cancelled") onSettled(next, Boolean(poll.error && poll.error.code !== "PROVIDER_LOGIN_NOT_FOUND"), Boolean(login?.accountId));
	}, [poll.data, poll.error]);
	// A browser sign-in opens the provider's page at once; its link stays in the panel for another browser.
	const start = async (body: ProviderLoginRequest) => {
		const next = await startProviderLogin(body);
		cache.setQueryData(loginKey, next);
		if (next.mode === "browser" && next.url) await aoBridge.app.openExternal(next.url);
	};
	const cancel = () => cancelProviderLogin(login!.id).then(() => cache.setQueryData(loginKey, null));
	return { login, waiting, start, cancel };
}
// Everything the page's views share: the accounts, what is on show, and the actions, which report in one status line.
export function useAccountsPage() {
	const { t } = useTranslation();
	const cache = useQueryClient();
	const query = useProviderAccounts(true, false);
	const usageQuery = useProviderAccounts(true, true);
	const accounts = query.data?.accounts?.map((account) => ({ ...account, usage: usageQuery.data?.accounts.find((entry) => entry.id === account.id)?.usage })) ?? [];
	const [pending, setPending] = useState(false);
	const [message, setMessage] = useState("");
	// After the default changes, the sessions still on the old default can follow.
	const [moveOffer, setMoveOffer] = useState<{ fromId: string; toId: string } | null>(null);
	const signIn = useProviderLogin((login, unreachable, again) => {
		setAdding(null);
		// A provider's first account is its default, so the sessions on this computer's own sign-in move to it.
		const first = !accounts.some((other) => other.provider === login.provider && other.id !== login.accountId);
		const done = again ? "providerAccounts.loginComplete" : first ? "providerAccounts.loginCompleteFirst" : "providerAccounts.loginCompleteNew";
		setMessage(t(login.status === "complete" ? done : unreachable ? "providerAccounts.loginStatusFailed" : "providerAccounts.loginFailed"));
		if (login.status !== "complete") return;
		if (login.accountId) setSelectedId(login.accountId);
		void cache.invalidateQueries({ queryKey: providerAccountsKey });
	});
	const [selectedId, setSelectedId] = useState(signIn.login?.accountId || null);
	const [adding, setAdding] = useState<Provider | null>(signIn.waiting && !signIn.login?.accountId ? signIn.login!.provider : null);
	// Re-reads every account's sign-in state on coming back to the window; opening settings already did.
	const recheck = useCallback(() => fetchProviderAccounts(true, true).then((next) => void cache.setQueryData(providerAccountsKey, next), () => undefined), [cache]);
	useEffect(() => {
		window.addEventListener("focus", recheck);
		return () => window.removeEventListener("focus", recheck);
	}, [recheck]);
	// run reports a failure in the status line. Only a change that must not overlap another holds the page while it runs.
	async function run<T>(action: () => Promise<T>, hold = true): Promise<T | undefined> {
		if (hold) setPending(true);
		setMessage("");
		try {
			return await action();
		} catch (error) {
			setMessage(apiErrorMessage(error));
			return undefined;
		} finally {
			if (hold) setPending(false);
		}
	}
	// An empty id goes back to the provider's default account.
	function show(accountId: string | null, provider: Provider | null = null) {
		if (accountId !== null) setSelectedId(accountId);
		setAdding(provider);
		setMessage("");
	}
	const act = (accountId: string, body: AccountAction, done = "", hold = true) => run(async () => {
		const next = await accountAction(accountId, body);
		cache.setQueryData(providerAccountsCatalogueKey, next);
		void cache.invalidateQueries({ queryKey: providerAccountsKey, exact: true });
		setMessage(done);
		return next;
	}, hold);
	// A light change shows at once and is put back if the daemon refuses it. Nothing else on the page waits for it.
	const actNow = (accountId: string, body: AccountAction, change: (account: ProviderAccount) => Partial<ProviderAccount>) => {
		type List = Awaited<ReturnType<typeof accountAction>>;
		const before = cache.getQueryData<List>(providerAccountsCatalogueKey);
		cache.setQueryData<List>(providerAccountsCatalogueKey, (list) => list && { ...list, accounts: list.accounts.map((account) => ({ ...account, ...change(account) })) });
		setMessage("");
		return accountAction(accountId, body).then((next) => {
			cache.setQueryData(providerAccountsCatalogueKey, next);
			return true;
		}, (error) => {
			cache.setQueryData(providerAccountsCatalogueKey, before);
			setMessage(apiErrorMessage(error));
			return false;
		});
	};
	// The same switch a session's own menu makes, one session at a time.
	// Sessions show under their new account at once; the list is read again afterwards in case one was refused.
	const moveSessions = async (ids: string[], to: ProviderAccount, done: string) => {
		type List = Awaited<ReturnType<typeof accountAction>>;
		const sessions = (account: ProviderAccount) => (account.id === to.id ? [...account.sessions, ...ids.filter((id) => !account.sessions.includes(id))] : account.sessions.filter((id) => !ids.includes(id)));
		cache.setQueryData<List>(providerAccountsCatalogueKey, (list) => list && { ...list, accounts: list.accounts.map((account) => ({ ...account, sessions: sessions(account) })) });
		await run(async () => {
			try {
				for (const sessionId of ids) await accountAction(to.id, { action: "assign-session", sessionId });
				setMoveOffer(null);
				setMessage(done);
			} finally {
				void cache.invalidateQueries({ queryKey: providerAccountsCatalogueKey });
				void cache.invalidateQueries({ queryKey: ["session-provider-account"] });
			}
		}, false);
	};
	return {
		query, accounts, pending, message, moveOffer, setMoveOffer, signIn, selectedId, adding,
		run, say: setMessage, show, act, actNow, moveSessions, recheck,
	};
}
function ApiKeyForm({ info, disabled, onAdd }: { info: (typeof PROVIDERS)[number]; disabled: boolean; onAdd: (fields: { apiKey: string; baseUrl: string; label?: string }) => void }) {
	const { t } = useTranslation();
	const [apiKey, setApiKey] = useState("");
	const [baseUrl, setBaseUrl] = useState("");
	const [label, setLabel] = useState("");
	const [shown, setShown] = useState(false);
	const field = "h-8 min-w-0 rounded-md border border-input bg-background px-2.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring";
	return (
		<div className="grid max-w-[620px] gap-2 pb-4 pl-12 pr-4 sm:grid-cols-2">
			<div className="relative min-w-0">
				<input className={cn(field, "w-full pr-8")} aria-label={t("providerAccounts.apiKeyLabel")} value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder={`${t("providerAccounts.apiKeyLabel")} (${info.key})`} type={shown ? "text" : "password"} autoComplete="off" spellCheck={false} />
				<IconAction name={t(shown ? "providerAccounts.hideApiKey" : "providerAccounts.showApiKey")} icon={shown ? EyeOff : Eye} className="absolute right-0.5 top-0.5" aria-pressed={shown} onClick={() => setShown(!shown)} />
			</div>
			<input className={field} aria-label={t("providerAccounts.baseUrlLabel")} value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder={`${t("providerAccounts.baseUrlLabel")} (${info.baseUrl})`} spellCheck={false} />
			<input className={field} aria-label={t("providerAccounts.displayLabel")} value={label} onChange={(event) => setLabel(event.target.value)} placeholder={t("providerAccounts.displayLabel")} />
			<div><Button className="h-8" size="sm" disabled={disabled || !apiKey.trim() || !baseUrl.trim()} onClick={() => onAdd({ apiKey, baseUrl, label: label || undefined })}>{t("providerAccounts.addApiKey")}</Button></div>
		</div>
	);
}
// Adding an account takes over the right side. Sign-in methods are adjacent choices; the chosen one opens under its row.
function AddAccountView({ provider, page }: { provider: Provider; page: AccountsPage }) {
	const { t } = useTranslation();
	const [formOpen, setFormOpen] = useState(false);
	const info = PROVIDERS.find((entry) => entry.id === provider)!;
	const { login, waiting } = page.signIn;
	const signingIn = waiting && login?.provider === provider && !login.accountId ? login : null;
	const disabled = page.pending || waiting;
	const head = "grid w-full grid-cols-[18px_minmax(0,1fr)_14px] items-center gap-3.5 px-4 py-3.5 text-left text-muted-foreground transition-colors hover:bg-interactive-hover disabled:pointer-events-none";
	const begin = (body: Omit<ProviderLoginRequest, "provider">) => page.run(() => page.signIn.start({ provider, ...body }));
	async function importFile(input: HTMLInputElement) {
		const file = input.files?.[0];
		if (file) await page.run(async () => {
			if (file.size > 1 << 20) throw new Error(t("providerAccounts.importTooLarge"));
			await page.signIn.start({ provider, mode: "import", credentialJson: await file.text() });
		});
		input.value = "";
	}
	return (
		<div role="group" aria-label={t("providerAccounts.signInMethods", { provider })}>
			<div className="flex min-h-11 items-center gap-3.5">
				<span aria-hidden="true" className="grid size-10 shrink-0 place-items-center rounded-xl border border-dashed border-border text-muted-foreground"><Plus className="size-4" /></span>
				<div className="min-w-0 flex-1">
					<h3 className="truncate text-lg font-semibold tracking-tight text-foreground">{t("providerAccounts.addAccountTitle", { provider: info.name })}</h3>
					<p className="text-sm text-muted-foreground">{t("providerAccounts.chooseSignIn")}</p>
				</div>
				{waiting ? null : <IconAction name={t("common.close")} onClick={() => page.show(null)}><X aria-hidden="true" className="size-4" /></IconAction>}
			</div>
			<Rows title={t("providerAccounts.signInMethod")} className="overflow-hidden">
				{METHODS.filter((method) => method.id !== "device" || provider === "codex").map(({ id, icon: Icon, name, hint }) => {
					const active = signingIn?.mode === id;
					const form = id === "api_key" && formOpen && !signingIn;
					const body = (
						<>
							<Icon aria-hidden="true" className="size-4" />
							<span className="min-w-0">
								<span className="block text-sm font-medium text-foreground">{t(name)}</span>
								{active || form ? null : <span className="block truncate text-xs">{t(hint)}</span>}
							</span>
							{active || form ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />}
						</>
					);
					return (
						<div key={id} className={signingIn && !active ? "opacity-40" : ""}>
							{id === "import" ? (
								<label className={cn(head, "cursor-pointer", disabled ? "pointer-events-none" : "")}>
									{body}
									<input className="sr-only" aria-label={t(name)} type="file" disabled={disabled} accept=".json,application/json" onChange={(event) => void importFile(event.target)} />
								</label>
							) : (
								<button type="button" className={head} aria-label={t(name)} aria-expanded={id === "api_key" ? form : undefined} disabled={disabled} onClick={() => (id === "api_key" ? setFormOpen(!formOpen) : void begin({ mode: id }))}>{body}</button>
							)}
							{signingIn && active ? <div className="pb-4 pl-12 pr-3"><LoginProgress login={signingIn} page={page} /></div> : null}
							{form ? <ApiKeyForm info={info} disabled={page.pending} onAdd={(fields) => void begin({ mode: "api_key", ...fields }).then((started) => started && setFormOpen(false))} /> : null}
						</div>
					);
				})}
			</Rows>
		</div>
	);
}
// What the list says about an account: whether it is the default, and how much room its tightest general limit has left.
function AccountListItem({ account, current, onSelect }: { account: ProviderAccount; current: boolean; onSelect: () => void }) {
	const { t } = useTranslation();
	const headroom = accountHeadroom(account);
	const marks = (account.signedIn ? [
		account.primary ? <span>{t("providerAccounts.default")}</span> : null,
		account.reserved ? <span>{t("providerAccounts.inReserve")}</span> : null,
		upcoming(account.usage?.pausedUntil) ? <span className="text-warning">{t("automations.paused")}</span> : null,
		account.usage?.signInEnding ? <span className="text-warning">{t("providerAccounts.signInEndingMark")}</span> : null,
		account.kind === "api_key" ? <span>{t("providerAccounts.apiKeyLabel")}</span>
			: headroom === null ? null
				: headroom === 0 ? <span className="text-status-needs-you">{t("providerAccounts.limitReached")}</span>
					: <span className={cn("tabular-nums", headroom <= 20 ? "text-warning" : "")}>{t("providerAccounts.usageRemaining", { percent: headroom })}</span>,
	] : [<span className="text-status-needs-you">{t("settings.harness.notLoggedIn")}</span>]).filter(Boolean);
	return (
		<button type="button" data-testid={`provider-account-${account.id}`} aria-current={current ? "true" : undefined} className={cn("flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring", current ? "bg-interactive-active" : "hover:bg-interactive-hover")} onClick={onSelect}>
			<span className="min-w-0 flex-1">
				<span className="flex items-center gap-2">
					<span className="truncate text-sm font-medium text-foreground">{account.displayName}</span>
					{account.global ? <DeviceTag apiKey={account.kind === "api_key"} /> : null}
				</span>
				<span className="mt-px flex items-center gap-1.5 truncate text-xs text-muted-foreground">
					{marks.map((mark, index) => <Fragment key={index}>{index ? <Dot /> : null}{mark}</Fragment>)}
				</span>
			</span>
			{account.signedIn ? null : <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-status-needs-you" />}
		</button>
	);
}
// The accounts of one agent, nested under its row on the Agents page, and the row that adds one.
export function AgentAccountRows({ provider, page, selectedId, onOpen }: { provider: Provider; page: AccountsPage; selectedId: string | null; onOpen: () => void }) {
	const { t } = useTranslation();
	const { accounts, adding, signIn, query } = page;
	const busy = page.pending || signIn.waiting;
	const name = PROVIDERS.find((entry) => entry.id === provider)!.name;
	const own = accounts.filter((account) => account.provider === provider);
	// A new sign-in that is still waiting keeps its place in the list, even while another account is on show.
	const signingIn = signIn.waiting && !signIn.login?.accountId && signIn.login?.provider === provider;
	return (
		<section data-testid={`provider-section-${provider}`} className="flex flex-col gap-0.5 py-1 pl-7 pr-1">
			{own.map((account) => <AccountListItem key={account.id} account={account} current={adding !== provider && selectedId === account.id} onSelect={() => { onOpen(); page.show(account.id); }} />)}
			<button type="button" aria-current={adding === provider ? "true" : undefined} disabled={busy && adding !== provider && !signingIn} className={cn("flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50", adding === provider ? "bg-interactive-active text-foreground" : "enabled:hover:bg-interactive-hover")} onClick={() => { onOpen(); page.show(null, provider); }}>
				<Plus aria-hidden="true" className="size-3.5 shrink-0" />
				<span className="min-w-0 flex-1 truncate">{signingIn ? t("providerAccounts.signingIn") : t("providerAccounts.newAccount", { provider: name })}</span>
			</button>
			{/* Accounts that are all signed out still route the agent's sessions, so say what that means. */}
			{query.data && own.length && !own.some((account) => account.signedIn) ? <p className="px-3 pb-1 pt-1 text-xs text-muted-foreground">{t("providerAccounts.managedNeedsLogin")}</p> : null}
		</section>
	);
}
// The open account of one agent, or its sign-in methods while one is being added, then the page's status line.
export function AgentAccountPane({ provider, page, tab, onTab, models }: { provider: Provider; page: AccountsPage; tab: AccountTab; onTab: (tab: AccountTab) => void; models: ReactNode }) {
	const { t } = useTranslation();
	const { query, adding } = page;
	const selected = adding === provider ? null : selectedAccount(page, provider);
	return (
		<div className="@container max-w-[1040px]">
			{query.isLoading ? <p className="mb-3 text-xs text-muted-foreground">{t("providerAccounts.loading")}</p> : null}
			{query.error ? <p role="alert" className="mb-3 flex items-center gap-2 text-xs text-destructive"><AlertCircle aria-hidden="true" className="size-3.5 shrink-0" />{query.error.message}</p> : null}
			{adding === provider ? <AddAccountView key={adding} provider={provider} page={page} /> : null}
			{selected ? <AccountDetail key={selected.id} account={selected} page={page} tab={tab} onTab={onTab} models={models} /> : null}
			{page.message ? <p role="status" className="mt-3.5 text-xs text-muted-foreground">{page.message}</p> : null}
		</div>
	);
}
/** The account on show for a provider: the one picked, else its default, else its first. */
export function selectedAccount(page: AccountsPage, provider: Provider): ProviderAccount | null {
	const own = page.accounts.filter((account) => account.provider === provider);
	return own.find((account) => account.id === page.selectedId) ?? own.find((account) => account.primary) ?? own[0] ?? null;
}
