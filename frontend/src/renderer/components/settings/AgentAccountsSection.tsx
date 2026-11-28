import { Info, LoaderCircle, Plus, RefreshCw, RotateCcw, Trash2, TriangleAlert, X } from "@/components/app-icons";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { getAgentUILogin, type AgentLoginSnapshot } from "../../hooks/useAgentAuth";
import { useRefreshAfterEndpointChange, type AgentEndpointStatus } from "../../hooks/useAgentEndpoint";
import { agentAccountLabel, agentAccountPerToken as perToken, agentAccountProviderFor, agentAccountProviderNames, agentAccountUsable, describeAccountProblem, useAgentAccountActions, useAgentAccounts, useAgentAccountUsage, withoutSameLogins, type AgentAccount, type AgentAccountLimitWindow, type AgentAccountLogin, type AgentAccountLoginRequest, type AgentAccountProvider } from "../../hooks/useAgentAccounts";
import type { TerminalSessionState } from "../../hooks/useTerminalSession";
import { apiErrorCode, apiErrorDetails } from "../../lib/api-client";
import { baseUrlForHost } from "../../lib/host-clients";
import { useShellMaybe } from "../../lib/shell-context";
import { formatRelativeTime } from "../../lib/usage-format";
import { agentLabel, type AgentId } from "../../lib/agent-options";
import { cn } from "../../lib/utils";
import { useSettings, useUpdateAccountHandoff } from "../../hooks/useSettings";
import { createTerminalMux, muxUrlFromApiBase } from "../../lib/terminal-mux";
import { useResolvedTheme, useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { TerminalPane } from "../TerminalPane";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { endpointHost, endpointSaved } from "./harness/CustomEndpointMethod";
import { HarnessLoginPanel } from "./harness/HarnessLoginPanel";
import { HarnessDetailGroup, HarnessDetailRow } from "./harness/harness-ui";

const providerNames = agentAccountProviderNames;

type LoginMethod = components["schemas"]["AgentLoginMethod"];
type AccountKind = AgentAccount["kind"];

/** The harness whose CLI signs in an account of each provider. */
const providerHarness: Record<AgentAccountProvider, "claude-code" | "codex"> = { claude: "claude-code", codex: "codex" };

/** Claude Code's API key method, which AO saves as an account's own key. */
const API_KEY_METHOD = "env_ANTHROPIC_API_KEY";

/** The harness's sign-in methods that add an account, and those that sign a
 * saved account of each kind in again; a method always makes its own kind. */
const accountMethods: Record<AgentAccountProvider, { add: string[] } & Partial<Record<AccountKind, string[]>>> = {
	claude: {
		add: ["browser", "console", "sso", "setup_token", API_KEY_METHOD],
		subscription: ["browser", "sso"], metered: ["console"], setup_token: ["setup_token"], api_key: [API_KEY_METHOD],
	},
	codex: {
		add: ["browser", "device_code", "api_key", "access_token"],
		subscription: ["browser", "device_code", "access_token"], metered: ["api_key"],
	},
};

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

/** One CLI's accounts on its Harness Account tab: the CLI's own login and the
 * accounts added in AO, with their limits, default and sign-in. loginMethods
 * are the harness's own sign-in methods; adding an account or signing one in
 * again offers those that suit it in the same panel, instead of a terminal. */
export function AgentAccountsPanel({ provider, cliLogin: cliLoginControls, hostId, loginMethods, customEndpoint }: {
	provider: AgentAccountProvider;
	cliLogin?: CliLoginControls;
	hostId?: string;
	loginMethods?: LoginMethod[];
	/** The harness's custom base URL, offered as a sign-in option when it supports one. */
	customEndpoint?: AgentEndpointStatus;
}) {
	const { t } = useTranslation();
	const accounts = useAgentAccounts(true, hostId);
	const usage = useAgentAccountUsage(30, hostId);
	const actions = useAgentAccountActions(hostId);
	const loginKey = `${hostId ?? ""}|${provider}`;
	const [logins, setLogins] = useState(() => pendingLogins);
	const [loginNote, setLoginNote] = useState("");
	const [busy, setBusy] = useState<string | null>(null);
	const [error, setError] = useState("");
	// Windows refused the account folder's links because its own Developer Mode
	// is off; the alert says where to turn it on. Other link failures stay errors.
	const [needsDeveloperMode, setNeedsDeveloperMode] = useState(false);
	const [removing, setRemoving] = useState<AgentAccount | null>(null);
	// The in-app sign-in: adding an account, or signing one in again. The
	// account's own sign-in it started is kept to save or cancel it.
	// endpoint opens it on the custom base URL, as Edit on a saved one does.
	const [signIn, setSignIn] = useState<{ account?: AgentAccount; endpoint?: boolean } | null>(null);
	const refreshEndpoint = useRefreshAfterEndpointChange(hostId);
	// The computer's own sign-in waits while an account signs in: both run the
	// same harness's sign-in, which takes one at a time.
	const cliLogin = cliLoginControls && { ...cliLoginControls, disabled: cliLoginControls.disabled || signIn !== null };
	const accountOperation = useRef<string | undefined>(undefined);
	// The account a sign-in that finished at once saved, for the chat that sent the user here.
	const finishedAccount = useRef<string | undefined>(undefined);
	// Moves on whenever the in-app sign-in opens or closes, so a start that
	// resolves after the user moved on never writes the newer sign-in's refs.
	const signInGeneration = useRef(0);
	const harness = providerHarness[provider];
	const tokensByAccount = new Map((usage.data ?? []).map((row) => [row.accountId, row.inputTokens + row.outputTokens]));
	const providerAccounts = (accounts.data ?? []).filter((account) => account.provider === provider);
	// Only subscriptions take part in moving at a usage limit.
	const hasMultipleAccounts = withoutSameLogins(providerAccounts).filter((account) => !perToken(account) && (!account.device || account.signedIn)).length > 1;
	// The harness's methods that suit adding an account, or account's kind.
	// A daemon from before account kinds sends none and takes no sign-in method:
	// its accounts sign in only in a terminal.
	const takesMethods = providerAccounts.some((account) => (account as { kind?: string }).kind);
	const methodsFor = (account?: AgentAccount) => {
		if (!takesMethods) return [];
		const allowed = account ? accountMethods[provider][account.kind] ?? [] : accountMethods[provider].add;
		return (loginMethods ?? []).filter((method) => allowed.includes(method.id));
	};
	// A key, setup token or Console login signs in only through the panel: a
	// terminal sign-in could pick another kind of login.
	const panelOnly = (account?: AgentAccount) => account?.kind === "api_key" || account?.kind === "setup_token" || account?.kind === "metered";
	// Until its methods load, such an account has nothing to sign in with.
	const signInReady = (account: AgentAccount) => !panelOnly(account) || methodsFor(account).length > 0;
	const rows = withoutSameLogins(providerAccounts, providerAccounts.find((account) => account.default)?.id);
	const cliTwinOf = (account: AgentAccount) => account.device ? undefined : providerAccounts.find((other) => other.sameLoginAs === account.id && !rows.includes(other));
	const login = logins[loginKey];

	const run = useCallback(async (key: string, action: () => Promise<unknown>) => {
		setBusy(key);
		setError("");
		setNeedsDeveloperMode(false);
		try {
			await action();
		} catch (err) {
			const isDeveloperMode = err instanceof Error && apiErrorDetails(err.cause)?.windowsDeveloperMode === true;
			setNeedsDeveloperMode(isDeveloperMode);
			setError(isDeveloperMode ? t("settings.agentAccounts.unshared") : err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(null);
		}
	}, [t]);

	const setLogin = (next: AgentAccountLogin | undefined) => {
		pendingLogins = { ...pendingLogins, [loginKey]: next };
		setLogins(pendingLogins);
	};
	// accountId signs that saved account in again, in its own folder.
	const startLogin = (accountId?: string) => run(`login:${provider}`, async () => {
		if (login?.operationId) await actions.cancelLogin(login.operationId).catch(() => undefined);
		setLogin(undefined);
		setLoginNote("");
		setLogin(await (accountId ? actions.signInAgain(accountId) : actions.startLogin(provider)));
	});
	// One check at a time: the CLI exiting checks on its own, and a second check
	// racing a click would find the sign-in already finished and report an error.
	const verifying = useRef(false);
	const checkLogin = (current: TerminalLogin) => {
		// A closed sign-in's terminal still reports its exit; there is nothing left to check.
		if (pendingLogins[loginKey]?.operationId !== current.operationId || verifying.current) return;
		void verifyLogin(current);
	};
	const verifyLogin = (current: TerminalLogin) => run(`verify:${provider}`, async () => {
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
	const closeLogin = (current: TerminalLogin) => {
		setLogin(undefined);
		void actions.cancelLogin(current.operationId).catch(() => undefined);
	};
	// Opens the in-app sign-in for a new account, or for account again; with no
	// method that suits it, the CLI's own sign-in runs in a terminal instead.
	const openSignIn = (account?: AgentAccount, endpoint = false) => {
		// Adding a sign-in also offers the custom base URL, which needs no method.
		if (methodsFor(account).length === 0 && !(customEndpoint && !account)) {
			if (!panelOnly(account)) void startLogin(account?.id);
			return;
		}
		setError("");
		setNeedsDeveloperMode(false);
		if (hasTerminal(login)) closeLogin(login);
		// Another account's sign-in still pending is stopped, never verified as this one.
		signInGeneration.current += 1;
		const pending = accountOperation.current;
		accountOperation.current = undefined;
		finishedAccount.current = undefined;
		if (pending) void actions.cancelLogin(pending).catch(() => undefined);
		setSignIn({ account, endpoint });
	};
	// A sign-in that finished at once (a key or token) leaves nothing to follow.
	const finishedAtOnce = (method: string): AgentLoginSnapshot => ({ id: "", agentId: harness, method, status: "succeeded", expiresAt: new Date().toISOString() });
	const credentialError = (err: unknown) => {
		const code = err instanceof Error ? apiErrorCode(err.cause) : undefined;
		return new Error(code === "AGENT_ACCOUNT_KEY_REJECTED" ? t("settings.apiKeys.rejected")
			: code === "AGENT_ACCOUNT_KEY_UNCHECKED" ? t("settings.apiKeys.unreachable")
			: err instanceof Error ? err.message : String(err));
	};
	// Runs method for the account. An API key is checked and saved by the
	// daemon, a setup token saved at once; any other method signs the account's
	// own folder in in the background, which the panel follows like the
	// harness's own sign-in.
	const startAccountSignIn = async (method: string, secret: string, name: string): Promise<AgentLoginSnapshot> => {
		const target = signIn?.account;
		// A start that resolves after the user opened or closed another sign-in
		// belongs to a panel that is gone: it never writes the newer one's refs.
		const generation = signInGeneration.current;
		const current = () => generation === signInGeneration.current;
		setError("");
		setNeedsDeveloperMode(false);
		try {
			if (method === API_KEY_METHOD) {
				if (target) await actions.replaceApiKey(target.id, secret);
				else if (provider === "claude") await actions.addApiKey(provider, name, secret);
				if (current()) finishedAccount.current = target?.id;
				return finishedAtOnce(method);
			}
			const request = { method, ...(secret ? { secret } : {}) } as AgentAccountLoginRequest;
			const started = target
				? await actions.signInAgain(target.id, request)
				: await actions.startLogin(provider, { ...request, ...(name ? { name } : {}) });
			if (started.account || !started.authOperationId) {
				await actions.refresh();
				if (current()) finishedAccount.current = started.account?.id ?? target?.id;
				return finishedAtOnce(method);
			}
			if (!current()) {
				// Its panel is gone, so nothing will save or cancel it: stop it now.
				if (started.operationId) void actions.cancelLogin(started.operationId).catch(() => undefined);
			} else {
				accountOperation.current = started.operationId;
			}
			return await getAgentUILogin(harness, started.authOperationId, hostId);
		} catch (err) {
			// Windows refused the account folder's links: no method can sign it in
			// until Developer Mode is on, so the card says how, as for a terminal sign-in.
			if (err instanceof Error && apiErrorDetails(err.cause)?.windowsDeveloperMode === true) {
				const notice = t("settings.agentAccounts.unshared");
				if (current()) {
					closeSignIn();
					setNeedsDeveloperMode(true);
					setError(notice);
				}
				throw new Error(notice);
			}
			throw credentialError(err);
		}
	};
	// Saves the account once its sign-in finished; a sign-in that finished at
	// once is already saved.
	const verifyAccountSignIn = async (): Promise<boolean> => {
		// A chat turn that sent the user here to log in gets them back.
		const returnToChat = (accountId?: string) => {
			const ui = useUiStore.getState();
			const waiting = ui.loginReturn;
			if (waiting && agentAccountProviderFor(waiting.agentId) === provider) ui.completeLoginReturn(waiting.agentId, hostId, accountId);
		};
		const operation = accountOperation.current;
		if (!operation) {
			returnToChat(finishedAccount.current);
			finishedAccount.current = undefined;
			return true;
		}
		const result = await actions.verifyLogin(operation);
		if (result.status === "waiting") return false;
		if (result.status === "not_subscription") {
			setError(t("settings.agentAccounts.loginNotSubscription"));
			return false;
		}
		// Another account's sign-in may have taken the ref meanwhile; leave it be.
		if (accountOperation.current === operation) accountOperation.current = undefined;
		if (result.status === "duplicate") setError(t("settings.agentAccounts.loginDuplicate", { email: result.account?.email ?? "" }));
		if (result.status === "mismatch") setError(t("settings.agentAccounts.loginMismatch", { email: [result.account?.email, result.account?.orgName].filter(Boolean).join(" · ") }));
		if (result.status !== "complete") return false;
		returnToChat(result.account?.id);
		return true;
	};
	// Closing before the sign-in was saved stops it and removes its new folder.
	const closeSignIn = () => {
		signInGeneration.current += 1;
		setSignIn(null);
		finishedAccount.current = undefined;
		const operation = accountOperation.current;
		accountOperation.current = undefined;
		if (operation) void actions.cancelLogin(operation).catch(() => undefined);
	};
	// Leaving Settings mid sign-in ends it: the daemon keeps a pending account
	// sign-in, and its new folder, until it is saved or cancelled.
	const actionsRef = useRef(actions);
	actionsRef.current = actions;
	useEffect(() => () => {
		// A start still in flight sees a newer generation and cancels itself.
		signInGeneration.current += 1;
		const operation = accountOperation.current;
		accountOperation.current = undefined;
		if (operation) void actionsRef.current.cancelLogin(operation).catch(() => undefined);
	}, []);
	const requestedLogin = useUiStore((state) => state.accountLogin);
	useEffect(() => {
		if (hostId) return;
		const account = rows.find((row) => row.id === requestedLogin);
		if (!account || (account.device && (!cliLogin || cliLogin.disabled)) || (!account.device && !signInReady(account))) return;
		useUiStore.setState({ accountLogin: null });
		if (account.device) cliLogin?.onSignIn();
		else openSignIn(account);
	}, [requestedLogin, accounts.data, cliLogin?.disabled, hostId, loginMethods]);
	const signInTarget = signIn?.account;

	return <>
		<HarnessDetailGroup
			title={t("settings.codexAccounts.title")}
			action={<div className="flex items-center gap-1">
				<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="ghost" aria-label={t("settings.agentAccounts.refresh")} disabled={busy !== null} onClick={() => void run("refresh", actions.refresh)}>{busy === "refresh" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}</Button></TooltipTrigger><TooltipContent>{t("settings.agentAccounts.refresh")}</TooltipContent></Tooltip>
				<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="secondary" aria-label={t("settings.codexAccounts.add")} aria-expanded={signIn !== null && !signInTarget} data-terminal-focus-handoff="true" disabled={busy !== null || signIn !== null} onClick={() => openSignIn()}><Plus aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.codexAccounts.add")}</TooltipContent></Tooltip>
			</div>}
		>
			{/* A setting the user can change is a warning, like an account problem that needs them. */}
			{error ? <p role="alert" className={cn("flex items-start gap-1.5 px-3 py-2.5 text-xs", needsDeveloperMode ? "text-warning" : "text-error")}>
				{needsDeveloperMode ? <TriangleAlert aria-hidden="true" className="mt-px size-icon-sm shrink-0" /> : null}
				<span className="min-w-0">
					{error}
					{needsDeveloperMode ? <Tooltip>
						<TooltipTrigger asChild>
							<Button type="button" size="icon-xs" variant="quiet" className="ml-1 align-middle" aria-label={t("settings.agentAccounts.unsharedHelpLabel")}>
								<Info className="size-icon-sm" aria-hidden="true" />
							</Button>
						</TooltipTrigger>
						<TooltipContent className="max-w-sm">{t("settings.agentAccounts.unsharedHelp")}</TooltipContent>
					</Tooltip> : null}
				</span>
			</p> : null}
			{/* The harness's own sign-in panel, for this account's folder. A new
			    panel for each account, so a typed key never carries over. Keys and
			    tokens are saved by AO itself, so they have no terminal sign-in. */}
			{signIn ? <HarnessLoginPanel
				key={signInTarget?.id ?? "new"}
				agentId={harness}
				methods={methodsFor(signInTarget)}
				hostId={hostId}
				account={{ start: startAccountSignIn, named: !signInTarget }}
				onVerify={verifyAccountSignIn}
				onClose={closeSignIn}
				onTerminal={panelOnly(signInTarget) ? undefined : async () => {
					closeSignIn();
					await startLogin(signInTarget?.id);
				}}
				endpoint={customEndpoint && !signInTarget ? {
					status: customEndpoint,
					initiallyOpen: signIn.endpoint,
					onChanged: async () => {
						closeSignIn();
						await refreshEndpoint(harness);
					},
				} : undefined}
			/> : null}
			{hasTerminal(login) ? <div className="px-3 py-2.5"><LoginTerminal login={login} hostId={hostId} note={loginNote} pending={busy === `verify:${provider}`} onCheck={checkLogin} onClose={closeLogin} /></div> : null}
			{accounts.isError ? <p role="alert" className="px-3 py-2.5 text-xs text-error">{accounts.error.message}</p> : null}
			{accounts.isLoading ? <p className="px-3 py-2.5 text-xs text-muted-foreground">{t("settings.agentAccounts.loading")}</p> : null}
			{/* The CLI login does not wait on the list: a cold read can take a while, and a failed one has no row. */}
			{cliLogin && !providerAccounts.some((account) => account.device) ? <div className="flex items-center justify-between gap-3 px-3 py-2.5">
				<p className="truncate text-sm font-medium text-foreground">{t("settings.agentAccounts.cliLoginOf", { provider: providerNames[provider] })}</p>
				<Button type="button" size="sm" variant="ghost" data-terminal-focus-handoff="true" disabled={busy !== null || cliLogin.disabled} onClick={cliLogin.onSignIn}>{t("settings.harness.login")}</Button>
			</div> : null}
			{customEndpoint && endpointSaved(customEndpoint) ? <EndpointRow endpoint={customEndpoint} agent={harness} disabled={busy !== null || signIn !== null} onEdit={() => openSignIn(undefined, true)} /> : null}
			{rows.map((account) => { const cliTwin = cliTwinOf(account); return <AccountRow
				key={account.id}
				account={account}
				name={agentAccountLabel(account, rows, t)}
				aoTokens={(tokensByAccount.get(account.id) ?? 0) + (cliTwin ? tokensByAccount.get(cliTwin.id) ?? 0 : 0) || undefined}
				busy={busy}
				cliLogin={account.device ? cliLogin : undefined}
				onMakeDefault={() => void run(`default:${account.id}`, () => actions.makeDefault(account.id))}
				onUseReset={() => void run(`reset:${account.id}`, () => actions.useReset(account.id))}
				onSignInAgain={signInReady(account) ? () => openSignIn(account) : undefined}
				onReplaceKey={signInReady(account) ? () => openSignIn(account) : undefined}
				onRemove={() => { setError(""); setNeedsDeveloperMode(false); setRemoving(account); }}
			/>; })}
			{!hostId && hasMultipleAccounts ? <AccountHandoffRow /> : null}
		</HarnessDetailGroup>
		{removing ? <ConfirmDialog open destructive
			title={t("settings.agentAccounts.remove")}
			description={t(removing.kind === "api_key" ? "settings.agentAccounts.removeApiKeyDescription" : "settings.agentAccounts.removeDescription", { email: agentAccountLabel(removing, rows, t) })}
			confirmLabel={t(busy === `remove:${removing.id}` ? "settings.agentAccounts.removing" : "settings.agentAccounts.remove")}
			busy={busy === `remove:${removing.id}`}
			error={error || undefined}
			onConfirm={() => void run(`remove:${removing.id}`, async () => { await actions.remove(removing.id); setRemoving(null); })}
			onOpenChange={(open) => { if (!open) setRemoving(null); }}
		/> : null}
	</>;
}

/** The saved custom base URL. Every session of the harness sends its requests
 * there instead of an account's sign-in, so it leads the list; Edit reopens it
 * in the sign-in options, where it is also removed. */
function EndpointRow({ endpoint, agent, disabled, onEdit }: { endpoint: AgentEndpointStatus; agent: string; disabled: boolean; onEdit: () => void }) {
	const { t } = useTranslation();
	const unusable = Boolean(endpoint.error);
	return <div className="px-3 py-2.5" data-custom-endpoint-row="">
		<div className="flex items-start justify-between gap-3">
			<div className="min-w-0">
				<p className="flex min-w-0 items-center gap-1.5 text-sm font-medium text-foreground">
					<span className="truncate">{t("settings.harness.endpoint.method")}</span>
					<Badge>{endpointHost(endpoint.baseUrl) || t("settings.harness.endpoint.badge")}</Badge>
				</p>
				<p className={cn("mt-0.5 truncate text-xs", unusable ? "text-error" : "text-muted-foreground")}>
					{unusable ? t("settings.harness.endpoint.unusableSummary") : t("settings.harness.endpoint.inUse", { agent: agentLabel(agent as AgentId) })}
				</p>
			</div>
			<Button type="button" size="sm" variant="ghost" disabled={disabled} onClick={onEdit}>{t("settings.harness.endpoint.edit")}</Button>
		</div>
	</div>;
}

function AccountRow({ account, name, aoTokens, busy, cliLogin, onMakeDefault, onUseReset, onSignInAgain, onReplaceKey, onRemove }: {
	account: AgentAccount;
	name: string;
	aoTokens: number | undefined;
	busy: string | null;
	/** Only on the CLI's own login, which has no folder of its own to remove. */
	cliLogin?: CliLoginControls;
	onMakeDefault: () => void;
	onUseReset: () => void;
	/** Missing while the sign-in methods this account needs are loading. */
	onSignInAgain?: () => void;
	onReplaceKey?: () => void;
	onRemove: () => void;
}) {
	const { t, i18n } = useTranslation();
	const kind = account.kind;
	// A key or setup token is AO's to replace; any other sign-in signs in again.
	const replaces = kind === "api_key" || kind === "setup_token";
	const plan = formatPlanName(account.plan);
	const resets = account.limits?.resetCredits ?? 0;
	const badge = kind === "api_key" ? t("settings.agentAccounts.apiKeyBadge")
		: kind === "metered" ? t("settings.agentAccounts.meteredBadge")
		: kind === "setup_token" ? t("settings.agentAccounts.setupTokenBadge")
		: null;
	const meta = (kind === "api_key" ? [
		// An API key account bills per token and has no limits to show.
		account.keyHint ? t("settings.agentAccounts.apiKeyEnding", { hint: account.keyHint }) : null,
		account.loggedIn ? t("settings.agentAccounts.apiKeyBilled") : t("settings.agentAccounts.apiKeyMissing"),
	] : kind === "metered" ? [
		// So does a metered sign-in, such as Anthropic Console.
		account.loggedIn ? t("settings.agentAccounts.apiKeyBilled") : t("settings.agentAccounts.signedOut"),
	] : [
		plan ? t("settings.codexAccounts.planLabel", { name: plan }) : null,
		kind === "setup_token" && account.keyHint ? t("settings.agentAccounts.tokenEnding", { hint: account.keyHint }) : null,
		// An API key (or a cloud provider login) in the CLI's own folder launches
		// it but is not a subscription: no limits, and no account to move to.
		account.signedIn ? null : account.loggedIn ? t("settings.agentAccounts.noSubscription") : t("settings.agentAccounts.signedOut"),
	]).concat(aoTokens ? t("settings.agentAccounts.aoUsage", { tokens: formatCompactNumber(aoTokens, i18n.language) }) : null).filter(Boolean).join(" · ");
	return <div className="px-3 py-2.5" data-agent-account={account.id}>
		<div className="flex items-start justify-between gap-3">
			<div className="min-w-0">
				<p className="flex min-w-0 items-center gap-1.5 text-sm font-medium text-foreground">
					<span className="truncate">{name}</span>
					{badge ? <Badge>{badge}</Badge> : null}
				</p>
				<p className="mt-0.5 truncate text-xs text-muted-foreground">{meta}</p>
			</div>
			<div className="flex shrink-0 items-center gap-2">
				{cliLogin
					? <Button type="button" size="sm" variant="ghost" data-terminal-focus-handoff="true" disabled={busy !== null || cliLogin.disabled} onClick={cliLogin.onSignIn}>{t("settings.agentAccounts.signInAgain")}</Button>
					: !replaces && !agentAccountUsable(account) && !account.device
						? <Button type="button" size="sm" variant="ghost" data-terminal-focus-handoff="true" disabled={busy !== null || !onSignInAgain} onClick={onSignInAgain}>{t("settings.agentAccounts.signInAgain")}</Button>
						: null}
				{account.default
					? <span className="text-xs text-muted-foreground">{t("settings.agentAccounts.default")}</span>
					: agentAccountUsable(account) ? <Button type="button" size="sm" variant="ghost" disabled={busy !== null} onClick={onMakeDefault}>{t("settings.agentAccounts.makeDefault")}</Button> : null}
				{replaces ? <Button type="button" size="sm" variant="ghost" disabled={busy !== null || !onReplaceKey} onClick={onReplaceKey}>{t(kind === "api_key" ? "settings.agentAccounts.replaceKey" : "settings.agentAccounts.replaceToken")}</Button> : null}
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

/** A sign-in that runs in a terminal; a background one has none. */
type TerminalLogin = AgentAccountLogin & { operationId: string; shellTerminal: NonNullable<AgentAccountLogin["shellTerminal"]> };

function hasTerminal(login: AgentAccountLogin | undefined): login is TerminalLogin {
	return Boolean(login?.operationId && login.shellTerminal);
}

function LoginTerminal({ login, hostId, note, pending, onCheck, onClose }: {
	login: TerminalLogin;
	hostId?: string;
	note: string;
	pending: boolean;
	onCheck: (login: TerminalLogin) => void;
	onClose: (login: TerminalLogin) => void;
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
