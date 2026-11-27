import { useEffect, useId, useRef, useState } from "react";
import { ChevronDown, ExternalLink, KeyRound, LoaderCircle, UserRound } from "@/components/app-icons";
import { Trans, useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Pressable } from "../ui/pressable";
import { agentLabel } from "../../lib/agent-options";
import type { CLOUD_AGENT_PROVIDERS } from "../../lib/cloud-agents";
import { cn } from "../../lib/utils";
import { useCloudCp } from "../../hooks/useCloudCp";
import { cloudAgentAccountsQueryKey } from "../../hooks/useCloudAgentAccounts";
import { providerConnectionsQueryKey } from "../../hooks/useProviderConnections";
import { aoBridge } from "../../lib/bridge";
import type { CloudCpAddAgentAccountRequest } from "../../lib/cloud-cp";
import { KEY_FIELD_CLASS } from "./providerKeyUi";

const BROWSER_LOGIN = "browser_login";
const SETUP_TOKEN_STEPS = [
	"cloudCredential.setupTokenStep1",
	"cloudCredential.setupTokenStep2",
	"cloudCredential.setupTokenStep3",
] as const;

// The cloud harnesses and the ways each can log in. Browser login is the
// default where the harness has one: the desktop app runs the agent CLI's own
// login locally and securely sends the resulting credential to the control
// plane, so no token is pasted or displayed. Pasting a Claude setup token or a
// provider API key remains available as a fallback; each API key links to the
// page where it is created. OpenCode is multi-provider, so its credential type
// doubles as the provider the worker injects the key for. Method names and
// placeholders are translated whole; only the provider brand is inserted.
export type CloudHarness = (typeof CLOUD_AGENT_PROVIDERS)[number];

type LoginMethod =
	| { value: string; kind: "browser"; provider: "Anthropic" | "ChatGPT" }
	| { value: string; kind: "setupToken" }
	| { value: string; kind: "apiKey"; provider: string; help: { site: string; url: string } };

const ANTHROPIC_KEY_HELP = { site: "console.anthropic.com", url: "https://console.anthropic.com/settings/keys" };
const OPENAI_KEY_HELP = { site: "platform.openai.com", url: "https://platform.openai.com/api-keys" };

const CLOUD_LOGIN_METHODS: Record<CloudHarness, readonly LoginMethod[]> = {
	"claude-code": [
		{ value: BROWSER_LOGIN, kind: "browser", provider: "Anthropic" },
		{ value: "oauth_token", kind: "setupToken" },
		{ value: "api_key", kind: "apiKey", provider: "Anthropic", help: ANTHROPIC_KEY_HELP },
	],
	codex: [
		{ value: BROWSER_LOGIN, kind: "browser", provider: "ChatGPT" },
		{ value: "api_key", kind: "apiKey", provider: "OpenAI", help: OPENAI_KEY_HELP },
	],
	cursor: [
		{ value: "api_key", kind: "apiKey", provider: "Cursor", help: { site: "cursor.com/dashboard", url: "https://cursor.com/dashboard?tab=integrations" } },
	],
	opencode: [
		{ value: "opencode_api_key", kind: "apiKey", provider: "OpenCode", help: { site: "opencode.ai/auth", url: "https://opencode.ai/auth" } },
		{ value: "anthropic_api_key", kind: "apiKey", provider: "Anthropic", help: ANTHROPIC_KEY_HELP },
		{ value: "openai_api_key", kind: "apiKey", provider: "OpenAI", help: OPENAI_KEY_HELP },
		{ value: "openrouter_api_key", kind: "apiKey", provider: "OpenRouter", help: { site: "openrouter.ai/keys", url: "https://openrouter.ai/keys" } },
	],
};

/** Where a login goes when the harness can hold several accounts: "new" saves
 * it as another account, an account id signs that account in again. Signing
 * in again takes only the account's kind of login (credentialType): the
 * control plane refuses another kind, since its sessions were set up for it. */
export type CloudHarnessLoginAccount = { id: "new" | string; credentialType?: string };

// The credential a browser login produces, per harness.
const BROWSER_LOGIN_CREDENTIAL: Partial<Record<CloudHarness, string>> = { "claude-code": "oauth_token", codex: "auth_json" };

// The harness's login methods that produce credentialType, or all of them
// when none does (a kind the desktop cannot log in with, such as a pasted
// Codex access token).
function loginMethodsFor(agent: CloudHarness, credentialType: string | undefined): readonly LoginMethod[] {
	const methods = CLOUD_LOGIN_METHODS[agent];
	if (!credentialType) return methods;
	const matching = methods.filter((method) =>
		(method.kind === "browser" ? BROWSER_LOGIN_CREDENTIAL[agent] : method.value) === credentialType);
	return matching.length > 0 ? matching : methods;
}

// Logs one harness in for cloud sessions, in its Account card on the
// Harness settings page (the only place cloud agent logins are made). Every
// credential is personal: it runs the caller's cloud sessions in every org they
// belong to. Without `account` the login replaces the harness's one login
// (PUT /me/providers/{agent}); with it, it adds or signs in one of its accounts.
export function CloudHarnessLoginPanel({ agent, account, onClose, onPendingChange }: {
	agent: CloudHarness;
	account?: CloudHarnessLoginAccount;
	onClose: () => void;
	/** Whether a browser login is waiting, so the page keeps this harness open until it ends. */
	onPendingChange?: (pending: boolean) => void;
}) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const titleId = useId();
	const panelRef = useRef<HTMLElement>(null);
	const methods = loginMethodsFor(agent, account?.id === "new" ? undefined : account?.credentialType);
	// Like the local panel: a sign-in starts on click, a key or token opens its
	// field under its row. With no sign-in to offer, the first key opens at once.
	const [openMethod, setOpenMethod] = useState<string | null>(methods.some((method) => method.kind === "browser") ? null : methods[0].value);
	const [secret, setSecret] = useState("");
	const [busy, setBusy] = useState(false);
	const [browserPending, setBrowserPending] = useState(false);
	const [error, setError] = useState<string | null>(null);
	// Set when the user cancels the browser login, so the resulting rejection is
	// treated as a cancellation rather than shown as an error.
	const loginCancelledRef = useRef(false);

	const methodLabel = (method: LoginMethod) =>
		method.kind === "browser"
			? method.provider === "Anthropic" ? t("cloudCredential.loginWithAnthropic") : t("cloudCredential.loginWithChatGPT")
			: method.kind === "setupToken" ? t("cloudCredential.method.setupToken") : t("cloudCredential.method.apiKey", { provider: method.provider });
	const placeholder = (method: LoginMethod) => method.kind === "apiKey"
		? t("cloudCredential.pasteApiKey", { provider: method.provider })
		: t("cloudCredential.pasteSetupToken");
	const browserHint = agent === "claude-code" ? t("cloudCredential.anthropicLoginDescription") : t("cloudCredential.chatgptLoginDescription");
	const canSubmit = !busy && openMethod !== null && secret.trim() !== "";

	useEffect(() => {
		panelRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
	}, []);

	// Whether a browser login is waiting, and whether the panel has left the page,
	// read by the unmount cleanup and by a login that ends after it.
	const browserPendingRef = useRef(false);
	const unmountedRef = useRef(false);
	const onPendingChangeRef = useRef(onPendingChange);
	onPendingChangeRef.current = onPendingChange;
	// Reports a waiting browser login, and its end however it ends (done,
	// failed, cancelled, or the panel leaving).
	useEffect(() => {
		if (!browserPending) return;
		onPendingChangeRef.current?.(true);
		return () => onPendingChangeRef.current?.(false);
	}, [browserPending]);

	const cancelBrowserLogin = () => {
		loginCancelledRef.current = true;
		void aoBridge.cloud.cancelProviderAuth();
	};

	// Leaving the page (another tab, harness or search) abandons a waiting
	// browser login too, as Cancel does.
	useEffect(() => () => {
		unmountedRef.current = true;
		if (browserPendingRef.current) cancelBrowserLogin();
	}, []);

	// Closing mid-login abandons the login rather than leaving the agent CLI
	// waiting in the background.
	const close = () => {
		if (browserPending) cancelBrowserLogin();
		onClose();
	};

	// On success the panel closes; the row's status line shows the connection.
	// A panel that already left closes nothing, so a newer panel stays open.
	const finish = async () => {
		await Promise.all([
			queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey }),
			queryClient.invalidateQueries({ queryKey: cloudAgentAccountsQueryKey }),
		]);
		if (!unmountedRef.current) onClose();
	};

	const submit = async () => {
		if (!canSubmit || openMethod === null) return;
		setBusy(true);
		setError(null);
		try {
			const input = { credentialType: openMethod, secret: secret.trim() };
			const { validationState } = !account
				? (await client.putUserAgentConnection(agent, input)).providerConnection
				: account.id === "new"
					? (await client.addAgentAccount({ agent, ...input, credentialType: openMethod as CloudCpAddAgentAccountRequest["credentialType"] })).agentAccount
					: (await client.replaceAgentAccountCredential(account.id, input)).agentAccount;
			if (validationState !== "valid") {
				setError(t("cloudCredential.invalid", { state: validationState }));
				return;
			}
			setSecret("");
			await finish();
		} catch (err) {
			setError(err instanceof Error ? err.message : t("cloudCredential.failed"));
		} finally {
			setBusy(false);
		}
	};

	const loginWithBrowser = async () => {
		if (busy) return;
		setBusy(true);
		setBrowserPending(true);
		browserPendingRef.current = true;
		setOpenMethod(null);
		setError(null);
		loginCancelledRef.current = false;
		try {
			// One login for local and cloud: the credential becomes the caller's
			// personal cloud connection and, for Claude Code, is also persisted
			// locally so local sessions use the same login. Only the default
			// account's (the main process reads which account the control plane
			// saved it as): another account must not change what local sessions use.
			await aoBridge.cloud.connectProviderAuth({
				baseUrl,
				provider: agent,
				persistLocalClaudeToken: agent === "claude-code",
				...(account ? { account: account.id } : {}),
			});
			await finish();
		} catch (err) {
			if (!loginCancelledRef.current) setError(err instanceof Error ? err.message : t("cloudCredential.failed"));
		} finally {
			setBusy(false);
			setBrowserPending(false);
			browserPendingRef.current = false;
		}
	};

	const choose = (method: LoginMethod) => {
		if (method.kind === "browser") {
			void loginWithBrowser();
			return;
		}
		setOpenMethod(openMethod === method.value ? null : method.value);
		setSecret("");
		setError(null);
	};

	// Account sign-ins and keys are separate lists, as on the local panel.
	const methodGroups = [
		{ id: "signIn", title: t("settings.harness.loginUi.groupSignIn"), items: methods.filter((method) => method.kind === "browser") },
		{ id: "keys", title: t("settings.harness.loginUi.groupKeys"), items: methods.filter((method) => method.kind !== "browser") },
	].filter((group) => group.items.length > 0);
	const hintClass = "text-xs leading-4 text-settings-muted";
	const fieldLabelClass = "block text-xs leading-4 text-settings-muted";
	// While a key form is open its errors show inside it, next to the field.
	const errorInForm = openMethod !== null && !browserPending;

	return <section ref={panelRef} aria-labelledby={titleId} className="@container min-w-0 scroll-my-3 space-y-3 px-3 py-3" data-settings-inline-edit="" data-testid="cloud-harness-login">
		<div className={browserPending ? "space-y-1" : "sr-only"}>
			<h4 id={titleId} className="text-sm leading-5 font-medium text-settings-label">{t("settings.harness.loginUi.title", { agent: agentLabel(agent) })}</h4>
		</div>
		{browserPending ? <div className="space-y-1">
			<p role="status" aria-live="polite" className="flex items-center gap-2 text-[13px] leading-5 text-settings-label">
				<LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
				{t("cloudCredential.waitingForBrowser")}
			</p>
			<p className={cn(hintClass, "pl-5.5")}>{browserHint}</p>
		</div> : <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
			<fieldset disabled={busy} className="space-y-3">
				<legend className="sr-only">{t("settings.harness.loginUi.method")}</legend>
				{methodGroups.map((group) => <div key={group.id} className="space-y-1.5">
					{methodGroups.length > 1 && <p className={hintClass}>{group.title}</p>}
					{/* Rows run flush to the card's edges with hairlines between them, like the local panel's. */}
					<div className={cn("-mx-3 flex flex-col divide-y divide-border border-y border-border", methodGroups.length === 1 && "-mt-3 border-t-0")}>
						{group.items.map((method) => {
							const label = methodLabel(method);
							const open = openMethod === method.value;
							const MethodIcon = method.kind === "browser" ? UserRound : KeyRound;
							return <div key={method.value}>
								<Pressable data-login-method="" aria-describedby={method.kind === "browser" ? titleId + "-" + method.value : undefined} aria-expanded={method.kind === "browser" ? undefined : open} onClick={() => choose(method)} className="group/method relative flex min-h-9 w-full cursor-pointer items-center gap-2.5 px-3 py-2 transition-colors hover:bg-(--color-interactive-hover) aria-expanded:hover:bg-transparent focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-default disabled:opacity-60">
									<MethodIcon className="size-3.5 shrink-0 text-settings-muted group-aria-expanded/method:text-settings-label" aria-hidden="true" />
									<span className="min-w-0 flex-1 truncate text-control leading-5 text-settings-label">{label}</span>
									{method.kind === "browser"
										? <ExternalLink className="size-3.5 shrink-0 text-settings-muted" aria-hidden="true" />
										: <ChevronDown className="size-3.5 shrink-0 text-settings-muted transition-transform group-aria-expanded/method:rotate-180" aria-hidden="true" />}
								</Pressable>
								{method.kind === "browser" ? <span id={titleId + "-" + method.value} className="sr-only">{browserHint}</span> : null}
								{open && method.kind !== "browser" ? <div className="grid grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2.5 pt-0.5 pr-3 pb-3.5 pl-9" onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
									<label htmlFor={titleId + "-secret"} className={fieldLabelClass}>{t(method.kind === "setupToken" ? "settings.harness.loginUi.setupToken" : "settings.harness.loginUi.apiKey")}</label>
									<div className="flex min-w-0 items-center gap-2">
										<Input id={titleId + "-secret"} className={cn(KEY_FIELD_CLASS, "min-w-0 flex-1")} type="password" autoComplete="off" spellCheck={false} autoFocus aria-invalid={error ? true : undefined} placeholder={placeholder(method)} value={secret} onChange={(event) => { setSecret(event.target.value); setError(null); }} />
										<Button type="submit" disabled={!canSubmit}>
											{busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
											{t("settings.harness.loginUi.save")}
										</Button>
									</div>
									{method.kind === "apiKey" ? <p className={cn(hintClass, "col-start-2")}>
										<Trans
											i18nKey="cloudCredential.apiKeyHelp"
											components={{
												link: <Button type="button" variant="link" className="text-settings-label" onClick={() => void aoBridge.app.openExternal(method.help.url)}>{method.help.site}</Button>,
											}}
										/>
									</p> : null}
									{method.kind === "setupToken" ? <ol className={cn(hintClass, "col-start-2 list-decimal space-y-0.5 pl-4")}>
										{SETUP_TOKEN_STEPS.map((key) => <li key={key}>
											<Trans
												i18nKey={key}
												values={{ command: "claude setup-token", prefix: "sk-ant-oat" }}
												components={{ code: <code className="rounded bg-muted px-1 py-px font-mono text-[11px] text-settings-label" /> }}
											/>
										</li>)}
									</ol> : null}
									{error ? <p role="alert" className="col-start-2 text-xs leading-4 text-error">{error}</p> : null}
								</div> : null}
							</div>;
						})}
					</div>
				</div>)}
			</fieldset>
		</form>}
		{error && !errorInForm ? <p role="alert" className="text-[13px] leading-5 text-error">{error}</p> : null}
		{/* The panel's own actions sit together at the trailing edge, as on the local panel. */}
		<div className="flex flex-wrap items-center justify-end gap-2">
			<Button type="button" variant="outline" size="sm" disabled={busy && !browserPending} onClick={browserPending ? cancelBrowserLogin : close}>{t("settings.harness.loginUi.cancel")}</Button>
		</div>
	</section>;
}
