import { Building2, Check, ChevronDown, Copy, ExternalLink, KeyRound, LoaderCircle, Smartphone, Terminal, UserRound } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../api/schema";
import { actOnAgentUILogin, cancelAgentUILogin, fetchCatalogProviders, getAgentUILogin, startAgentUILogin, startCatalogKeyLogin, submitAgentUILoginCode, type AgentLoginSnapshot, type CatalogProvider } from "../../../hooks/useAgentAuth";
import { checkProviderKey, providerKeyName, type ProviderKey } from "../../../hooks/useProviderKeys";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiErrorCode } from "../../../lib/api-client";
import { runAgentLoginTask } from "../../../lib/agent-login-lifecycle";
import { aoBridge } from "../../../lib/bridge";
import { LOCAL_HOST } from "../../../lib/hosts";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import { Input } from "../../ui/input";
import { SettingsOptionMenu } from "../SettingsOptionMenu";

const CATALOG_METHOD = "provider_key";

type MenuAction = "previous" | "next" | "select" | "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9";

export function HarnessLoginPanel({ agentId, methods, hostId, providerKeys, onProviderKeysChanged, onVerify, onClose, onTerminal }: {
	agentId: AgentId;
	methods: components["schemas"]["AgentLoginMethod"][];
	hostId?: string;
	/** Saved provider keys, without values, for this host. */
	providerKeys?: ProviderKey[];
	onProviderKeysChanged?: () => void;
	onVerify: (methodId: string) => Promise<boolean | "configured">;
	onClose: () => void;
	onTerminal: () => Promise<unknown>;
}) {
	const { t } = useTranslation();
	const titleId = useId();
	const panelRef = useRef<HTMLElement>(null);
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	const availableMethods = remote && agentId === "codex" ? methods.filter((item) => item.id !== "browser") : methods;
	const [methodId, setMethodId] = useState(availableMethods[0]?.id ?? "browser");
	const [secret, setSecret] = useState("");
	const [model, setModel] = useState("");
	const [device, setDevice] = useState(false);
	const [models, setModels] = useState<string[]>([]);
	const [loadingModels, setLoadingModels] = useState(false);
	// The harness's own provider catalog, for its provider_key method.
	const [catalog, setCatalog] = useState<CatalogProvider[]>();
	const [catalogError, setCatalogError] = useState("");
	const [catalogProvider, setCatalogProvider] = useState("");
	const [loginCode, setLoginCode] = useState("");
	const [operation, setOperation] = useState<AgentLoginSnapshot>();
	const [busy, setBusy] = useState(false);
	const [verifying, setVerifying] = useState(false);
	const [connected, setConnected] = useState(false);
	const [configured, setConfigured] = useState(false);
	const [error, setError] = useState("");
	const [copied, setCopied] = useState(false);
	const operationRef = useRef<AgentLoginSnapshot | undefined>(undefined);
	const mounted = useRef(true);
	const pending = useRef(false);
	const openedOperation = useRef<string | undefined>(undefined);
	const secretRowRef = useRef<HTMLDivElement>(null);
	const verifyRef = useRef(onVerify);
	verifyRef.current = onVerify;
	const method = availableMethods.find((item) => item.id === methodId) ?? (methodId ? availableMethods[0] : undefined);
	const selectedMethodId = method?.id ?? methodId;
	const providerKeyFor = (id?: string) => {
		const name = id ? providerKeyName(id) : undefined;
		return name ? providerKeys?.find((key) => key.name === name) : undefined;
	};
	const selectedKey = providerKeyFor(selectedMethodId);
	const savedKey = selectedKey?.configured ? selectedKey : undefined;
	// Start from the saved device setting so saving again never withdraws it silently.
	useEffect(() => { setDevice(selectedKey?.device ?? false); }, [selectedKey?.name, selectedKey?.device]);
	const needsModel = (agentId === "qwen" || agentId === "openhands" || agentId === "continue" && selectedMethodId !== "env_CONTINUE_API_KEY") && selectedMethodId.startsWith("env_");
	const running = operation?.status === "running";
	const locked = busy || running || verifying;
	const updateOperation = (next: AgentLoginSnapshot | undefined) => {
		operationRef.current = next;
		setOperation(next);
	};

	useEffect(() => {
		mounted.current = true;
		panelRef.current?.querySelector<HTMLButtonElement>("[data-login-method]")?.focus({ preventScroll: true });
		return () => {
			mounted.current = false;
			const current = operationRef.current;
			if (current) void runAgentLoginTask(agentId, hostId, () => cancelAgentUILogin(agentId, current.id, hostId)).catch(() => undefined);
		};
	}, [agentId, hostId]);

	const offersCatalog = availableMethods.some((item) => item.id === CATALOG_METHOD);
	useEffect(() => {
		if (!offersCatalog) return;
		let active = true;
		void fetchCatalogProviders(agentId, hostId).then((providers) => {
			if (active) setCatalog(providers);
		}).catch((err) => {
			if (active) setCatalogError(err instanceof Error ? err.message : t("settings.harness.loginUi.catalogUnavailable"));
		});
		return () => { active = false; };
	}, [agentId, hostId, offersCatalog, t]);

	// Bring the inline credential field into view and focus it without the
	// browser's instant focus jump.
	useEffect(() => {
		const row = secretRowRef.current;
		if (!row) return;
		row.querySelector("input")?.focus({ preventScroll: true });
		const reduceMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
		row.scrollIntoView?.({ block: "nearest", behavior: reduceMotion ? "auto" : "smooth" });
	}, [selectedMethodId, method?.secret]);

	useEffect(() => {
		if (!operation || operation.status !== "running" || verifying) return;
		let active = true;
		let timer: ReturnType<typeof setTimeout>;
		const poll = async () => {
			try {
				const next = await getAgentUILogin(agentId, operation.id, hostId);
				if (!active) return;
				operationRef.current = next;
				setOperation(next);
				setError("");
				if (next.status !== "running") return;
			} catch (err) {
				if (!active) return;
				if (apiErrorCode(err) === "AGENT_AUTH_OPERATION_NOT_FOUND" || (err instanceof Error && "status" in err && ((err as Error & { status?: number }).status === 404 || (err as Error & { status?: number }).status === 410))) {
					updateOperation({ ...operation, status: "timed_out", loginUrl: undefined, deviceCode: undefined, acceptsCode: false });
					setError("");
					return;
				}
				setError(err instanceof Error ? err.message : t("settings.harness.authFailed"));
			}
			if (active) timer = setTimeout(poll, 1_000);
		};
		timer = setTimeout(poll, 500);
		return () => { active = false; clearTimeout(timer); };
	}, [agentId, hostId, operation?.id, operation?.status, t, verifying]);

	useEffect(() => {
		if (operation?.status !== "succeeded") return;
		let active = true;
		setVerifying(true);
		void verifyRef.current(operation.method).then((verified) => {
			if (!active) return;
			setConnected(Boolean(verified));
			setConfigured(verified === "configured");
			if (!verified) setError(t("settings.harness.loginUi.unverified"));
		}).catch(() => {
			if (active) setError(t("settings.harness.loginUi.unverified"));
		}).finally(() => { if (active) setVerifying(false); });
		return () => { active = false; };
	}, [operation?.id, operation?.status, t]);

	// The CLI opens the browser itself for local browser logins. Device-code
	// and remote logins cannot reach this machine's browser, so open the link.
	useEffect(() => {
		const url = operation?.loginUrl;
		if (!url || operation.status !== "running" || openedOperation.current === operation.id) return;
		if (!remote && operation.method !== "device_code") return;
		if (agentId === "claude-code" && !operation.acceptsCode) return;
		openedOperation.current = operation.id;
		void aoBridge.app.openExternal(url).catch(() => undefined);
	}, [agentId, remote, operation?.id, operation?.status, operation?.loginUrl, operation?.method, operation?.acceptsCode]);

	const start = async (target = method) => {
		const targetNeedsModel = (agentId === "qwen" || agentId === "openhands" || agentId === "continue" && target?.id !== "env_CONTINUE_API_KEY") && target?.id.startsWith("env_");
		const targetKey = providerKeyFor(target?.id);
		const targetCatalog = target?.id === CATALOG_METHOD;
		if (pending.current || locked || !target || (target.secret && !secret.trim() && !targetKey?.configured) || (targetNeedsModel && !model.trim()) || (targetCatalog && !catalogProvider)) return;
		pending.current = true;
		setBusy(true);
		setError("");
		setCopied(false);
		const credential = target.secret ? secret.trim() : "";
		setSecret("");
		setLoginCode("");
		try {
			// A new provider key is checked first so a mistyped key is never saved.
			if (credential && targetKey?.checkable && (await checkProviderKey(targetKey.name, credential, hostId)).status === "invalid") {
				if (mounted.current) {
					setSecret(credential);
					setError(t("settings.apiKeys.rejected"));
				}
				return;
			}
			await runAgentLoginTask(agentId, hostId, async () => {
				if (!mounted.current) return;
				const next = targetCatalog
					? await startCatalogKeyLogin(agentId, catalogProvider, credential, hostId)
					: targetKey && device
					? await startAgentUILogin(agentId, target.id, credential, hostId, targetNeedsModel ? model.trim() : undefined, true)
					: targetNeedsModel
					? await startAgentUILogin(agentId, target.id, credential, hostId, model.trim())
					: await startAgentUILogin(agentId, target.id, credential, hostId);
				if (!mounted.current) {
					await cancelAgentUILogin(agentId, next.id, hostId);
					return;
				}
				setMethodId(next.method);
				updateOperation(next);
				if (targetKey) onProviderKeysChanged?.();
			});
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.harness.authFailed"));
		} finally {
			pending.current = false;
			if (mounted.current) setBusy(false);
		}
	};

	const submitCode = async () => {
		const current = operationRef.current;
		if (pending.current || !current?.acceptsCode || current.status !== "running" || !loginCode.trim()) return;
		pending.current = true;
		setBusy(true);
		setError("");
		const code = loginCode.trim();
		setLoginCode("");
		try {
			const next = await submitAgentUILoginCode(agentId, current.id, code, hostId);
			if (mounted.current) updateOperation(next);
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.harness.authFailed"));
		} finally {
			pending.current = false;
			if (mounted.current) setBusy(false);
		}
	};

	const leave = async (terminal = false) => {
		if (pending.current || verifying) return;
		pending.current = true;
		setBusy(true);
		setSecret("");
		setLoginCode("");
		try {
			const current = operationRef.current;
			if (current) await runAgentLoginTask(agentId, hostId, () => cancelAgentUILogin(agentId, current.id, hostId));
			if (!mounted.current) return;
			operationRef.current = undefined;
			if (terminal) await onTerminal(); else onClose();
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.harness.authFailed"));
		} finally {
			pending.current = false;
			if (mounted.current) setBusy(false);
		}
	};
	const choose = (item: components["schemas"]["AgentLoginMethod"]) => {
		if (locked) return;
		// Clicking an open credential row again collapses it.
		setMethodId(item.secret && selectedMethodId === item.id ? "" : item.id);
		setSecret("");
		setModel("");
		setModels([]);
		setCatalogProvider("");
		setDevice(providerKeyFor(item.id)?.device ?? false);
		setLoginCode("");
		setError("");
		updateOperation(undefined);
		if (!item.secret) void start(item);
	};
	const act = (action: () => Promise<unknown>) => void action().catch(() => setError(t("settings.harness.authFailed")));
	// Fills the model picker from the provider, using the typed or saved key.
	const loadModels = async () => {
		if (!selectedKey || loadingModels || (!secret.trim() && !savedKey)) return;
		setLoadingModels(true);
		setError("");
		try {
			const result = await checkProviderKey(selectedKey.name, secret.trim(), hostId);
			if (!mounted.current) return;
			if (result.status === "valid" && result.models?.length) {
				setModels(result.models);
				if (!model) setModel(result.models[0]);
			} else {
				setError(t(result.status === "invalid" ? "settings.apiKeys.rejected" : result.status === "unreachable" ? "settings.apiKeys.unreachable" : "settings.apiKeys.noModels"));
			}
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.apiKeys.checkFailed"));
		} finally { if (mounted.current) setLoadingModels(false); }
	};
	const menuAction = async (action: MenuAction) => {
		if (!operation || busy || !running) return;
		setBusy(true);
		setError("");
		try { updateOperation(await actOnAgentUILogin(agentId, operation.id, action, hostId)); }
		catch (err) { setError(err instanceof Error ? err.message : t("settings.harness.authFailed")); }
		finally { setBusy(false); }
	};
	const checkNativeSignIn = async () => {
		if (!operation || !running || operation.method !== "native" || verifying || busy) return;
		setVerifying(true);
		setError("");
		try {
			if (await verifyRef.current("native") !== true) {
				setError(t("settings.harness.loginUi.unverified"));
				return;
			}
			await cancelAgentUILogin(agentId, operation.id, hostId);
			if (!mounted.current) return;
			updateOperation(undefined);
			setConnected(true);
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.harness.authFailed"));
		} finally { if (mounted.current) setVerifying(false); }
	};
	const status = verifying ? t("settings.harness.checkingLogin")
		: connected ? configured ? t("settings.harness.models.configuredProvider") : t("settings.harness.loginUi.connected")
		: operation?.status === "failed" ? t("settings.harness.loginUi.failed")
		: operation?.status === "timed_out" ? t("settings.harness.loginUi.timedOut")
		: operation?.status === "cancelled" ? t("settings.harness.loginUi.cancelled")
		: running ? selectedMethodId === "native" ? t(operation?.nativePrompt === "terminal_required" ? "settings.harness.loginUi.nativeFallback" : operation?.nativePrompt === "menu" ? "settings.harness.loginUi.nativeMenu" : "settings.harness.loginUi.nativeWaiting") : t(method?.secret ? "settings.harness.loginUi.saving" : operation?.deviceCode ? "settings.harness.loginUi.waitingForCode" : "settings.harness.loginUi.waiting") : "";

	const savedHint = (key?: ProviderKey) => key?.configured
		? [key.hint ? t("settings.apiKeys.savedHint", { hint: key.hint }) : t("settings.apiKeys.saved"), key.device ? t("settings.apiKeys.outsideAo") : ""].filter(Boolean).join(" · ")
		: undefined;
	// Account sign-ins and API keys are separate lists; the note that applies
	// to every AO-managed key is said once, under its list.
	const methodGroups = [
		{ id: "signIn", title: t("settings.harness.loginUi.groupSignIn"), items: availableMethods.filter((item) => !item.secret), note: "" },
		{ id: "keys", title: t("settings.harness.loginUi.groupKeys"), items: availableMethods.filter((item) => item.secret), note: availableMethods.some((item) => item.id.startsWith("env_")) ? t("settings.harness.loginUi.envHint") : "" },
	].filter((group) => group.items.length > 0);
	const methodKey = (id: string) => id !== "browser" || agentId === "codex" ? id : agentId === "claude-code" ? "claude" : agentId === "cline" ? "cline" : `${agentId}_browser`;
	const selectedMethodKey = methodKey(selectedMethodId);
	const methodLabel = t(`settings.harness.loginUi.methods.${selectedMethodKey}`, { defaultValue: method?.label ?? selectedMethodId });
	const nativeOptions = operation?.nativeOptions ?? [];

	return <section ref={panelRef} aria-labelledby={titleId} className="@container min-w-0 space-y-4 p-4" data-settings-inline-edit="">
		<div className="space-y-1">
			<h4 id={titleId} className="text-[15px] leading-5 font-semibold tracking-tight text-settings-label">{t("settings.harness.loginUi.title", { agent: agentLabel(agentId) })}</h4>
			{!connected && !locked && <p className="text-xs leading-4 text-settings-muted">{availableMethods.length ? t("settings.harness.loginUi.method") : t("settings.harness.loginUi.nativeInteractive")}</p>}
			{remote && !connected && <p className="max-w-prose text-xs leading-4 text-settings-muted">{t(agentId === "codex" ? "settings.harness.loginUi.remoteCodex" : "settings.harness.loginUi.remoteClaude")}</p>}
		</div>
		{availableMethods.length > 0 && !connected && !running && !verifying && <form id={titleId + "-form"} className="space-y-4" onSubmit={(event) => { event.preventDefault(); void start(); }}>
			<fieldset disabled={locked} className="space-y-3">
				<legend className="sr-only">{t("settings.harness.loginUi.method")}</legend>
				{methodGroups.map((group) => <div key={group.id} className="space-y-1.5">
				{methodGroups.length > 1 && <p className="px-1 text-xs leading-4 font-medium text-settings-muted">{group.title}</p>}
				<div className="flex flex-col divide-y divide-border overflow-hidden rounded-lg bg-(--color-bg-primary)">
				{group.items.map((item) => {
					const key = methodKey(item.id);
					const label = t(`settings.harness.loginUi.methods.${key}`, { defaultValue: item.label });
					const MethodIcon = item.id === "native" ? Terminal : item.secret ? KeyRound : item.id.startsWith("device_code") ? Smartphone : item.id === "sso" || item.id === "console" ? Building2 : UserRound;
					const selected = selectedMethodId === item.id;
					const open = item.secret && selected;
					return <div key={item.id} className={open ? "bg-(--color-interactive-active)" : undefined}><button type="button" data-login-method="" aria-label={label} aria-expanded={item.secret ? selected : undefined} aria-describedby={titleId + "-" + item.id} onClick={() => choose(item)} className="group/method relative flex w-full cursor-pointer items-center gap-3 px-4 py-3 text-left outline-none transition-colors hover:bg-(--color-interactive-hover) aria-expanded:hover:bg-transparent focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-default disabled:opacity-60">
						<span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-(--color-bg-tertiary) text-settings-muted group-aria-expanded/method:text-settings-label"><MethodIcon className="size-4" aria-hidden="true" /></span>
						<span className="min-w-0 flex-1 space-y-0.5">
							<span className="block text-[13px] leading-5 font-medium text-settings-label">{label}</span>
							<span id={titleId + "-" + item.id} className="block text-xs leading-4 text-settings-muted">{item.id === "native" ? t("settings.harness.loginUi.nativeHint") : item.id === CATALOG_METHOD ? catalogError || (catalog ? t("settings.harness.loginUi.catalogHint", { total: catalog.length, agent: agentLabel(agentId) }) : t("settings.harness.loginUi.catalogLoading")) : item.id.startsWith("env_") ? savedHint(providerKeyFor(item.id)) ?? t("settings.apiKeys.notSet") : t(`settings.harness.loginUi.hints.${key}`, { defaultValue: key.endsWith("_browser") ? "Finish sign-in in your browser." : item.label })}</span>
						</span>
						{busy && selected ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
							: item.secret ? <ChevronDown className="size-4 shrink-0 text-settings-muted transition-transform group-aria-expanded/method:rotate-180" aria-hidden="true" />
							: <ExternalLink className="size-3.5 shrink-0 text-settings-muted" aria-hidden="true" />}
					</button>
					{open && <div ref={secretRowRef} className="flex scroll-mb-4 flex-wrap items-center gap-2 pr-4 pb-4 pl-15" onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); choose(item); } }}>
						{item.id === CATALOG_METHOD && <SettingsOptionMenu
							aria-label={t("settings.harness.loginUi.catalogProvider")}
							value={catalogProvider}
							placeholder={t("settings.harness.loginUi.catalogChoose")}
							options={(catalog ?? []).map((provider) => ({ value: provider.id, label: provider.name }))}
							onChange={setCatalogProvider}
							searchable
							searchPlaceholder={t("settings.harness.loginUi.catalogSearch")}
							emptyLabel={t("settings.harness.loginUi.catalogEmpty")}
							disabled={locked || !catalog}
							triggerClassName="h-8 basis-full justify-between"
						/>}
						<Input aria-label={t(item.id === "access_token" ? "settings.harness.loginUi.accessToken" : "settings.harness.loginUi.apiKey")} className="min-w-48 flex-1 border-border bg-(--color-bg-primary) font-mono text-[13px] focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30" type="password" value={secret} disabled={locked} autoComplete="off" spellCheck={false} maxLength={16384} placeholder={savedKey ? t("settings.apiKeys.keepSaved") : undefined} onChange={(event) => { setSecret(event.target.value); setModels([]); }} />
						{needsModel && <div className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-settings-label">
							<span id={titleId + "-model"}>{t("settings.apiKeys.model")}</span>
							<div className="flex items-center gap-1">
								{models.length > 0
									? <SettingsOptionMenu aria-label={t("settings.apiKeys.model")} value={model} options={models.map((id) => ({ value: id, label: id }))} onChange={setModel} searchable searchPlaceholder={t("settings.apiKeys.searchModels")} disabled={locked} triggerClassName="h-8 min-w-0 flex-1 font-mono" />
									: <Input aria-labelledby={titleId + "-model"} className="border-border bg-(--color-bg-primary) font-mono text-[13px]" value={model} disabled={locked} autoComplete="off" spellCheck={false} maxLength={256} placeholder={t("settings.models.custom")} onChange={(event) => setModel(event.target.value)} />}
								{selectedKey?.checkable && models.length === 0 && <Button type="button" size="sm" variant="ghost" disabled={locked || loadingModels || (!secret.trim() && !savedKey)} onClick={() => void loadModels()}>
									{loadingModels && <LoaderCircle className="animate-spin" aria-hidden="true" />}{t("settings.apiKeys.loadModels")}
								</Button>}
							</div>
						</div>}
						<Button type="submit" disabled={busy || (!secret.trim() && !savedKey) || (needsModel && !model.trim()) || (item.id === CATALOG_METHOD && !catalogProvider)}>
							{busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
							{t("settings.harness.loginUi.save")}
						</Button>
						{selectedKey && <label className="flex basis-full items-start gap-2">
							<Checkbox checked={device} className="mt-0.5" disabled={locked} onCheckedChange={(checked) => setDevice(checked === true)} />
							<span className="min-w-0 flex-1">
								<span className="block text-xs leading-4 text-settings-label">{t("settings.apiKeys.device")}</span>
								<span className="block text-xs leading-4 text-settings-muted">{t("settings.apiKeys.deviceHint")}</span>
							</span>
						</label>}
						{agentId === "claude-code" && item.id === "env_ANTHROPIC_API_KEY" && <p className="basis-full text-xs leading-4 text-settings-muted">{t("settings.apiKeys.claudeBilling")}</p>}
					</div>}
					</div>;
				})}
				</div>
				{group.note && <p className="px-1 text-xs leading-4 text-settings-muted">{group.note}</p>}
				</div>)}
			</fieldset>
		</form>}
		{(running || verifying || connected) && <div className="space-y-3">
			{!operation?.deviceCode && <p className="text-xs leading-4 text-settings-muted">{methodLabel}</p>}
			<div className="space-y-1">
				<p role="status" aria-live="polite" className={connected ? "flex items-center gap-2.5 text-[15px] leading-5 font-medium text-settings-label" : "flex items-center gap-2 text-[13px] leading-5 text-settings-label"}>
					{connected ? <span className={`flex size-6 shrink-0 items-center justify-center rounded-full ${configured ? "bg-(--color-bg-tertiary) text-settings-muted" : "bg-success/10 text-success"}`}>{configured ? <KeyRound className="size-4" aria-hidden="true" /> : <Check className="size-4" aria-hidden="true" />}</span> : <LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />}
					{status}
				</p>
				{connected && <p className="pl-8.5 text-xs leading-4 text-settings-muted">{configured ? t("settings.harness.models.configuredProviderHint") : t("settings.harness.loginUi.connectedDescription")}</p>}
			</div>
			{operation?.deviceCode && running && <div className="inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-(--color-bg-secondary) py-1 pr-1 pl-3">
				<code className="break-all font-mono text-[15px] leading-6 font-medium tracking-wide text-settings-label" aria-label={t("settings.harness.loginUi.deviceCode")}>{operation.deviceCode}</code>
				<Button size="sm" variant="ghost" onClick={() => act(async () => { await aoBridge.clipboard.writeText(operation.deviceCode!); setCopied(true); })}>{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copied ? "settings.harness.loginUi.copied" : "settings.harness.loginUi.copyCode")}</Button>
			</div>}
			{running && operation?.nativePrompt === "menu" && nativeOptions.length > 0 && <div className="space-y-2 rounded-md border border-border bg-(--color-bg-secondary) p-3">
				<p className="text-sm text-settings-label">{t("settings.harness.loginUi.nativeChoose")}</p>
				<div className="flex flex-wrap gap-2">
					{nativeOptions.map((option, index) => <Button key={`${index}-${option}`} type="button" size="sm" variant="outline" disabled={busy} onClick={() => void menuAction(String(index + 1) as MenuAction)}>{index + 1}. {option}</Button>)}
					<Button type="button" size="sm" disabled={busy} onClick={() => void menuAction("select")}>{t("settings.harness.loginUi.nativeSelect")}</Button>
				</div>
			</div>}
			{running && operation?.nativePrompt === "menu" && nativeOptions.length === 0 && <div className="space-y-2 rounded-md border border-border bg-(--color-bg-secondary) p-3">
				<p className="text-sm text-settings-label">{t("settings.harness.loginUi.nativeSelected", { option: operation.selectedOption })}</p>
				<div className="flex flex-wrap gap-2">
					<Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void menuAction("previous")}>{t("settings.harness.loginUi.nativePrevious")}</Button>
					<Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void menuAction("next")}>{t("settings.harness.loginUi.nativeNext")}</Button>
					<Button type="button" size="sm" disabled={busy} onClick={() => void menuAction("select")}>{t("settings.harness.loginUi.nativeSelect")}</Button>
				</div>
			</div>}
		</div>}
		{running && operation.acceptsCode && <form className="max-w-lg space-y-2" onSubmit={(event) => { event.preventDefault(); void submitCode(); }}>
			<label className="block space-y-1.5">
				<span className="block text-xs leading-4 font-medium text-settings-label">{t("settings.harness.loginUi.signInCode")}</span>
				<Input type="password" value={loginCode} disabled={busy} autoComplete="off" spellCheck={false} maxLength={4096} onChange={(event) => setLoginCode(event.target.value)} />
			</label>
			<p className="text-xs leading-4 text-settings-muted">{t("settings.harness.loginUi.codeHint")}</p>
			<Button type="submit" size="sm" disabled={busy || !loginCode.trim()}>{busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}{t("settings.harness.loginUi.submitCode")}</Button>
		</form>}
		{status && !running && !verifying && !connected && <p role="status" className="text-[13px] leading-5 text-settings-muted">{status}</p>}
		{error && <p role="alert" className="text-[13px] leading-5 text-error">{error}</p>}
		<div className="flex flex-wrap items-center gap-2">
			{operation?.loginUrl && running && (agentId !== "claude-code" || operation.acceptsCode) && !(remote && agentId === "codex" && operation.method === "browser") && <Button size="sm" onClick={() => act(() => aoBridge.app.openExternal(operation.loginUrl!))}><ExternalLink aria-hidden="true" />{t("settings.harness.loginUi.openBrowser")}</Button>}
			{running && operation?.method === "native" && <Button variant="outline" size="sm" disabled={busy || verifying} onClick={() => void checkNativeSignIn()}>{verifying ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}{t("settings.harness.loginUi.nativeCheck")}</Button>}
			{running && operation?.nativePrompt === "terminal_required" && <Button variant="outline" size="sm" disabled={busy} onClick={() => void leave(true)}><Terminal aria-hidden="true" />{t("settings.harness.loginUi.terminal")}</Button>}
			<Button variant={connected ? "primary" : "ghost"} size="sm" disabled={busy || verifying} onClick={() => void leave()}>{t(connected ? "settings.harness.loginUi.done" : "settings.harness.loginUi.cancel")}</Button>
		</div>
		{!connected && availableMethods.length === 0 && <Button variant="outline" size="sm" disabled={busy || verifying} onClick={() => void leave(true)}><Terminal aria-hidden="true" />{t("settings.harness.loginUi.terminal")}</Button>}
		{!connected && availableMethods.length > 0 && operation?.nativePrompt !== "terminal_required" && <details className="group border-t border-border pt-3">
			<summary className="flex w-fit cursor-pointer list-none items-center gap-1 text-xs text-settings-muted outline-none hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden"><ChevronDown className="size-3.5 transition-transform group-open:rotate-180" aria-hidden="true" />{t("settings.harness.loginUi.otherOptions")}</summary>
			<Button className="mt-2 -ml-2" variant="ghost" size="sm" disabled={busy || verifying} onClick={() => void leave(true)}><Terminal aria-hidden="true" />{t("settings.harness.loginUi.terminal")}</Button>
		</details>}
	</section>;
}
