import { Building2, Check, ChevronDown, ChevronRight, ChevronUp, Copy, ExternalLink, KeyRound, LoaderCircle, Smartphone, Terminal, UserRound } from "@/components/app-icons";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../api/schema";
import { actOnAgentUILogin, cancelAgentUILogin, fetchCatalogProviders, getAgentUILogin, startAgentUILogin, startCatalogKeyLogin, submitAgentUILoginCode, type AgentLoginSnapshot, type CatalogProvider } from "../../../hooks/useAgentAuth";
import { checkProviderKey, providerKeyName, removeProviderKey, type ProviderKey } from "../../../hooks/useProviderKeys";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiErrorCode } from "../../../lib/api-client";
import { disposeAgentLogin, retainAgentLoginDisposal, runAgentLoginTask } from "../../../lib/agent-login-lifecycle";
import { aoBridge } from "../../../lib/bridge";
import { LOCAL_HOST } from "../../../lib/hosts";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { SettingsOptionMenu } from "../SettingsOptionMenu";
import { CatalogProviderPickerSkeleton } from "./CatalogProviderPicker";
import { DeviceEnvToggle, KEY_FIELD_CLASS, KeyWarning, providerKeyNeedsModel, REMOVE_CONFIRM_MS, savedKeySummary } from "../providerKeyUi";
import { cn } from "../../../lib/utils";

const CATALOG_METHOD = "provider_key";
// A native screen AO cannot read stays "waiting"; after this long the panel
// offers the terminal instead of a silent spinner.
const NATIVE_STALL_MS = 12_000;
// Crush providers whose stored sign-in Crush itself removes when a key is
// saved for them (backend crushSignInFields).
const CRUSH_SIGN_IN_PROVIDERS = new Set(["openai", "xai", "anthropic"]);

type MenuAction = "previous" | "next" | "select" | "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9";

export function HarnessLoginPanel({ agentId, methods, hostId, providerKeys, providerKeysError, onRetryProviderKeys, onProviderKeysChanged, onVerify, onClose, onTerminal }: {
	agentId: AgentId;
	methods: components["schemas"]["AgentLoginMethod"][];
	hostId?: string;
	/** Saved provider keys, without values, for this host. Undefined while loading or after a failed load. */
	providerKeys?: ProviderKey[];
	/** Why the saved provider keys could not be loaded. */
	providerKeysError?: string;
	onRetryProviderKeys?: () => void;
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
	// Numbers each model-list request; changing the method or the typed key
	// moves it on, so a late answer for an earlier provider is dropped.
	const modelRequest = useRef(0);
	const discardModelRequest = () => {
		modelRequest.current += 1;
		setLoadingModels(false);
	};
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
	// Removing a saved key takes a second click, which disarms itself after
	// a few seconds; removed says how it went once the key is gone.
	const [confirmRemove, setConfirmRemove] = useState(false);
	const [removed, setRemoved] = useState<{ warn: boolean; text: string }>();
	const operationRef = useRef<AgentLoginSnapshot | undefined>(undefined);
	const mounted = useRef(true);
	const pending = useRef(false);
	const openedOperation = useRef<string | undefined>(undefined);
	const secretRowRef = useRef<HTMLDivElement>(null);
	// Set once the user answers a native menu; until then an unrecognized
	// screen hands straight off to the terminal instead of asking.
	const nativeAnswered = useRef(false);
	const [stalled, setStalled] = useState(false);
	const verifyRef = useRef(onVerify);
	verifyRef.current = onVerify;
	const method = availableMethods.find((item) => item.id === methodId) ?? (methodId ? availableMethods[0] : undefined);
	const selectedMethodId = method?.id ?? methodId;
	const providerKeyFor = (id?: string) => {
		const name = id ? providerKeyName(id) : undefined;
		return name ? providerKeys?.find((key) => key.name === name) : undefined;
	};
	// Until the saved keys load, an AO-managed key can't be saved: AO would not
	// know whether to check it first or keep its device setting.
	const keyStateUnknown = (id?: string) => Boolean(id && providerKeyName(id) && !providerKeys);
	const selectedKey = providerKeyFor(selectedMethodId);
	const savedKey = selectedKey?.configured ? selectedKey : undefined;
	// The AO-stored key this method can remove. Gemini's own key method also
	// saves GEMINI_API_KEY in AO's store for its sessions, so it can be removed
	// here too, though that method takes no device setting and no empty save.
	const removableKey = savedKey ?? (selectedMethodId === "gemini_api_key"
		? providerKeys?.find((key) => key.name === "GEMINI_API_KEY" && key.configured)
		: undefined);
	// Start from the saved device setting so saving again never withdraws it silently.
	useEffect(() => { setDevice(selectedKey?.device ?? false); }, [selectedKey?.name, selectedKey?.device]);
	const needsModel = providerKeyNeedsModel(agentId, selectedMethodId);
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
			// A failed cancellation (for example on a disconnected host) is retried
			// and keeps the harness pending, so updates stay blocked meanwhile.
			if (current) void disposeAgentLogin(agentId, hostId, () => cancelAgentUILogin(agentId, current.id, hostId)).catch(() => undefined);
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
		// A recovered remote Codex browser login redirects to a localhost callback
		// on the remote host, which this computer's browser cannot reach.
		if (remote && agentId === "codex" && operation.method === "browser") return;
		openedOperation.current = operation.id;
		void aoBridge.app.openExternal(url).catch(() => undefined);
	}, [agentId, remote, operation?.id, operation?.status, operation?.loginUrl, operation?.method, operation?.acceptsCode]);

	const start = async (target = method) => {
		const targetNeedsModel = providerKeyNeedsModel(agentId, target?.id);
		const targetKey = providerKeyFor(target?.id);
		const targetCatalog = target?.id === CATALOG_METHOD;
		if (pending.current || locked || !target || keyStateUnknown(target.id) || (target.secret && !secret.trim() && !targetKey?.configured) || (targetNeedsModel && !model.trim()) || (targetCatalog && !catalogProvider)) return;
		pending.current = true;
		setBusy(true);
		setError("");
		setCopied(false);
		const credential = target.secret ? secret.trim() : "";
		setSecret("");
		setLoginCode("");
		try {
			// A new provider key is checked first so a mistyped key is never saved.
			if (credential && targetKey?.checkable) {
				let rejected = false;
				try {
					rejected = (await checkProviderKey(targetKey.name, credential, hostId)).status === "invalid";
				} catch (err) {
					// The check itself failed: keep the typed key so the user can retry.
					if (mounted.current) {
						setSecret(credential);
						setError(err instanceof Error ? err.message : t("settings.apiKeys.checkFailed"));
					}
					return;
				}
				if (rejected) {
					if (mounted.current) {
						setSecret(credential);
						setError(t("settings.apiKeys.rejected"));
					}
					return;
				}
			}
			await runAgentLoginTask(agentId, hostId, async () => {
				if (!mounted.current) return;
				// The device choice is sent only where it is shown; otherwise the
				// key keeps its current setting.
				const next = targetCatalog
					? await startCatalogKeyLogin(agentId, catalogProvider, credential, hostId)
					: targetKey?.deviceAvailable
					? await startAgentUILogin(agentId, target.id, credential, hostId, targetNeedsModel ? model.trim() : undefined, device)
					: targetNeedsModel
					? await startAgentUILogin(agentId, target.id, credential, hostId, model.trim())
					: await startAgentUILogin(agentId, target.id, credential, hostId);
				if (!mounted.current) {
					const dispose = () => cancelAgentUILogin(agentId, next.id, hostId);
					try {
						await dispose();
					} catch (err) {
						retainAgentLoginDisposal(agentId, hostId, dispose);
						throw err;
					}
					return;
				}
				setMethodId(next.method);
				updateOperation(next);
				if (targetKey || target?.id === "gemini_api_key") onProviderKeysChanged?.();
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
	// With native sign-in as the only method there is nothing to choose: start
	// it straight away. The timer keeps a double mount from starting twice.
	const onlyNative = availableMethods.length === 1 && availableMethods[0].id === "native";
	useEffect(() => {
		if (!onlyNative) return;
		const timer = window.setTimeout(() => void start(availableMethods[0]), 0);
		return () => window.clearTimeout(timer);
	// Start once per panel; start reads the latest state through refs.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [onlyNative]);
	// A first screen AO cannot drive goes to the terminal without an extra
	// click; mid-flow, the panel offers the terminal as its main action.
	useEffect(() => {
		// Wait until the start that produced this screen has settled; leave()
		// ignores calls while it is still pending.
		if (busy || operation?.status !== "running" || operation.nativePrompt !== "terminal_required" || nativeAnswered.current) return;
		void leave(true);
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [operation?.id, operation?.status, operation?.nativePrompt, busy]);
	// A screen AO does not recognise reads as "waiting" indefinitely. After a
	// while, say so and make the terminal login the main action.
	useEffect(() => {
		setStalled(false);
		if (operation?.status !== "running" || operation.method !== "native" || operation.nativePrompt !== "waiting") return;
		const timer = window.setTimeout(() => setStalled(true), NATIVE_STALL_MS);
		return () => window.clearTimeout(timer);
	}, [operation?.id, operation?.status, operation?.method, operation?.nativePrompt]);
	const needsTerminal = running && (operation?.nativePrompt === "terminal_required" || stalled);
	const choose =(item: components["schemas"]["AgentLoginMethod"]) => {
		if (locked) return;
		// Clicking an open credential row again collapses it.
		setMethodId(item.secret && selectedMethodId === item.id ? "" : item.id);
		setSecret("");
		setModel("");
		setModels([]);
		discardModelRequest();
		setCatalogProvider("");
		setDevice(providerKeyFor(item.id)?.device ?? false);
		setLoginCode("");
		setError("");
		setConfirmRemove(false);
		setRemoved(undefined);
		updateOperation(undefined);
		if (!item.secret) void start(item);
	};
	useEffect(() => {
		if (!confirmRemove || busy) return;
		const timer = window.setTimeout(() => setConfirmRemove(false), REMOVE_CONFIRM_MS);
		return () => window.clearTimeout(timer);
	}, [confirmRemove, busy]);
	// Removes the key AO saved for this method, and AO's copy in the user
	// environment, so it no longer reaches sessions or other apps. A copy a
	// harness wrote into its own settings is left alone.
	const removeSavedKey = async () => {
		if (!removableKey || pending.current || locked) return;
		if (!confirmRemove) {
			setConfirmRemove(true);
			return;
		}
		pending.current = true;
		setBusy(true);
		setError("");
		try {
			const result = await removeProviderKey(removableKey.name, hostId);
			if (!mounted.current) return;
			setSecret("");
			setDevice(false);
			setRemoved(result.withdrawFailed
				? { warn: true, text: t("settings.apiKeys.removedWithdrawFailed") }
				: { warn: false, text: t("settings.apiKeys.removed") });
			onProviderKeysChanged?.();
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.apiKeys.removeFailed"));
		} finally {
			pending.current = false;
			if (mounted.current) {
				setConfirmRemove(false);
				setBusy(false);
			}
		}
	};
	// A key typed for one catalog provider never goes to another: choosing a
	// different provider clears it with the rest of the form.
	const chooseCatalogProvider = (id: string) => {
		if (id === catalogProvider) return;
		setCatalogProvider(id);
		setSecret("");
		setModel("");
		setModels([]);
		discardModelRequest();
		setError("");
	};
	const act = (action: () => Promise<unknown>) => void action().catch(() => setError(t("settings.harness.authFailed")));
	// Fills the model picker from the provider, using the typed or saved key.
	const loadModels = async () => {
		if (!selectedKey || loadingModels || (!secret.trim() && !savedKey)) return;
		const request = ++modelRequest.current;
		const current = () => mounted.current && modelRequest.current === request;
		setLoadingModels(true);
		setError("");
		try {
			const result = await checkProviderKey(selectedKey.name, secret.trim(), hostId);
			if (!current()) return;
			if (result.status === "valid" && result.models?.length) {
				setModels(result.models);
				if (!model) setModel(result.models[0]);
			} else {
				setError(t(result.status === "invalid" ? "settings.apiKeys.rejected" : result.status === "unreachable" ? "settings.apiKeys.unreachable" : "settings.apiKeys.noModels"));
			}
		} catch (err) {
			if (current()) setError(err instanceof Error ? err.message : t("settings.apiKeys.checkFailed"));
		} finally { if (current()) setLoadingModels(false); }
	};
	// A numbered choice carries the label the user saw, so the daemon can refuse
	// it when the menu on screen changed between render and click.
	const menuAction = async (action: MenuAction, option?: string) => {
		if (!operation || busy || !running) return;
		nativeAnswered.current = true;
		setBusy(true);
		setError("");
		try { updateOperation(await (option ? actOnAgentUILogin(agentId, operation.id, action, hostId, option) : actOnAgentUILogin(agentId, operation.id, action, hostId))); }
		catch (err) { setError(err instanceof Error ? err.message : t("settings.harness.authFailed")); }
		finally { setBusy(false); }
	};
	const checkNativeSignIn = async () => {
		if (!operation || !running || operation.method !== "native" || verifying || busy) return;
		setVerifying(true);
		setError("");
		try {
			const verified = await verifyRef.current("native");
			if (!verified) {
				setError(t("settings.harness.loginUi.unverified"));
				return;
			}
			await cancelAgentUILogin(agentId, operation.id, hostId);
			if (!mounted.current) return;
			updateOperation(undefined);
			setConnected(true);
			// Present, not verified: shown as configured.
			setConfigured(verified === "configured");
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.harness.authFailed"));
		} finally { if (mounted.current) setVerifying(false); }
	};
	const status = verifying ? t("settings.harness.checkingLogin")
		: connected ? configured ? t("settings.harness.models.configuredProvider") : t("settings.harness.loginUi.connected")
		: operation?.status === "failed" ? t("settings.harness.loginUi.failed")
		: operation?.status === "timed_out" ? t("settings.harness.loginUi.timedOut")
		: operation?.status === "cancelled" ? t("settings.harness.loginUi.cancelled")
		: running ? selectedMethodId === "native" ? t(operation?.nativePrompt === "terminal_required" ? "settings.harness.loginUi.nativeFallback" : operation?.nativePrompt === "menu" ? "settings.harness.loginUi.nativeMenu" : stalled ? "settings.harness.loginUi.nativeStalled" : "settings.harness.loginUi.nativeWaiting") : t(method?.secret ? "settings.harness.loginUi.saving" : operation?.deviceCode ? "settings.harness.loginUi.waitingForCode" : "settings.harness.loginUi.waiting") : "";

	const secretPlaceholder = (id: string) => id === "setup_token" ? t("settings.harness.loginUi.setupTokenPlaceholder")
		: id === "api_key" ? t(agentId === "claude-code" ? "settings.harness.loginUi.claudeKeyPlaceholder" : "settings.harness.loginUi.openaiKeyPlaceholder")
		: undefined;
	// Account sign-ins and API keys are separate lists; the note that applies
	// to every AO-managed key is said once, under its list.
	const signInGroup = { id: "signIn", title: t("settings.harness.loginUi.groupSignIn"), items: availableMethods.filter((item) => !item.secret), note: "" };
	const keysGroup = { id: "keys", title: t("settings.harness.loginUi.groupKeys"), items: availableMethods.filter((item) => item.secret), note: availableMethods.some((item) => item.id.startsWith("env_")) ? t("settings.harness.loginUi.envHint") : "" };
	// Native sign-in is the fallback, not the headline: when it is the only
	// sign-in, the API keys come first. A real account sign-in (Claude,
	// ChatGPT, GitHub, Kimi) keeps its place at the top.
	const nativeOnlySignIn = signInGroup.items.length > 0 && signInGroup.items.every((item) => item.id === "native");
	const methodGroups = (nativeOnlySignIn ? [keysGroup, signInGroup] : [signInGroup, keysGroup]).filter((group) => group.items.length > 0);
	// Beside other methods, native sign-in takes a lighter, compact row.
	const secondaryMethod = (id: string) => id === "native" && availableMethods.length > 1;
	const methodKey = (id: string) => id !== "browser" || agentId === "codex" ? id : agentId === "claude-code" ? "claude" : agentId === "cline" ? "cline" : `${agentId}_browser`;
	const selectedMethodKey = methodKey(selectedMethodId);
	const methodLabel = t(`settings.harness.loginUi.methods.${selectedMethodKey}`, { defaultValue: method?.label ?? selectedMethodId });
	const nativeOptions = operation?.nativeOptions ?? [];
	// The second line under each method: what choosing it does, or for an
	// AO-managed key whether one is saved.
	const methodHint = (item: components["schemas"]["AgentLoginMethod"], key: string) => {
		if (item.id === "native") return t("settings.harness.loginUi.nativeHint");
		// Loading and load errors show once, in the form, not here as well.
		if (item.id === CATALOG_METHOD) return catalog ? t("settings.harness.loginUi.catalogHint", { total: catalog.length, agent: agentLabel(agentId) }) : t("settings.harness.loginUi.catalogHintPending", { agent: agentLabel(agentId) });
		if (item.id.startsWith("env_")) return savedKeySummary(t, providerKeyFor(item.id)) ?? t("settings.apiKeys.notSet");
		return t(`settings.harness.loginUi.hints.${key}`, { defaultValue: key.endsWith("_browser") ? t("settings.harness.loginUi.browserHint") : item.label });
	};
	const secretLabel = (id: string) => t(id === "setup_token" ? "settings.harness.loginUi.setupToken" : id === "access_token" ? "settings.harness.loginUi.accessToken" : "settings.harness.loginUi.apiKey");
	const fieldLabelClass = "block text-xs leading-4 text-settings-muted";
	// Pickers take the same surface, height and inset as the shared Input so
	// the provider, key and model fields read as one form.
	const fieldTriggerClass = "h-control-form w-full justify-between rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 text-control text-settings-label";
	// The chosen catalog provider, so its key variable can show beside the key field.
	const chosenProvider = catalog?.find((provider) => provider.id === catalogProvider);
	// Gemini's own key method also saves GEMINI_API_KEY in AO's store, which
	// AO passes to every agent that reads it.
	const geminiSharedWith = agentId === "gemini" ? (providerKeys?.find((key) => key.name === "GEMINI_API_KEY")?.agents ?? []).filter((id) => id !== "gemini").map((id) => agentLabel(id)) : [];
	const geminiSharedNote = geminiSharedWith.length > 3
		? t("settings.harness.loginUi.geminiSharedMore", { names: geminiSharedWith.slice(0, 3).join(", "), extra: geminiSharedWith.length - 3 })
		: geminiSharedWith.length > 0 ? t("settings.harness.loginUi.geminiShared", { names: geminiSharedWith.join(", ") }) : "";
	// While a key form is open its errors show inside it, next to the field.
	const errorInForm = Boolean(method?.secret) && !connected && !running && !verifying;

	return <section ref={panelRef} aria-labelledby={titleId} className="@container min-w-0 space-y-3 px-3 py-3" data-settings-inline-edit="">
		{/* While the method list shows, the Account heading and group captions already say what this is. */}
		<div className={availableMethods.length > 0 && !connected && !running && !verifying && !remote ? "sr-only" : "space-y-1"}>
			<h4 id={titleId} className="text-sm leading-5 font-medium text-settings-label">{t("settings.harness.loginUi.title", { agent: agentLabel(agentId) })}</h4>
			{!connected && !locked && (availableMethods.length === 0 || methodGroups.length < 2) && <p className="text-xs leading-4 text-settings-muted">{availableMethods.length ? t("settings.harness.loginUi.method") : t("settings.harness.loginUi.nativeInteractive")}</p>}
			{remote && !connected && <p className="max-w-prose text-xs leading-4 text-settings-muted">{t(agentId === "codex" ? "settings.harness.loginUi.remoteCodex" : "settings.harness.loginUi.remoteClaude")}</p>}
		</div>
		{availableMethods.length > 0 && !connected && !running && !verifying && <form id={titleId + "-form"} className="space-y-3" onSubmit={(event) => { event.preventDefault(); void start(); }}>
			<fieldset disabled={locked} className="space-y-3">
				<legend className="sr-only">{t("settings.harness.loginUi.method")}</legend>
				{methodGroups.map((group) => <div key={group.id} className="space-y-1.5">
				{methodGroups.length > 1 && <p className="text-xs leading-4 text-settings-muted">{group.title}</p>}
				{/* Rows run flush to the card's edges with hairlines between them: no second outlined card inside the Account card.
				    With no group caption above them they start at the card's top edge, not under an empty strip. */}
				<div className={cn("-mx-3 flex flex-col divide-y divide-border border-y border-border", methodGroups.length === 1 && !remote && "-mt-3 border-t-0")}>
				{group.items.map((item) => {
					const key = methodKey(item.id);
					const label = t(`settings.harness.loginUi.methods.${key}`, { defaultValue: item.label });
					const MethodIcon = item.id === "native" ? Terminal : item.secret ? KeyRound : item.id.startsWith("device_code") ? Smartphone : item.id === "sso" || item.id === "console" ? Building2 : UserRound;
					const selected = selectedMethodId === item.id;
					const open = item.secret && selected;
					// Save sits beside the last field: the key, or the model when one follows it.
					const saveButton = <Button type="submit" disabled={busy || keyStateUnknown(item.id) || (!secret.trim() && !savedKey) || (needsModel && !model.trim()) || (item.id === CATALOG_METHOD && !catalogProvider)}>
						{busy && !confirmRemove && <LoaderCircle className="animate-spin" aria-hidden="true" />}
						{t("settings.harness.loginUi.save")}
					</Button>;
					// One line per method. A sign-in's hint only restates its label, so it is
					// read to screen readers; a key row shows its saved state instead.
					const trailing = item.id === CATALOG_METHOD ? (catalog ? t("settings.harness.loginUi.catalogCount", { total: catalog.length }) : "")
						: item.id.startsWith("env_") ? savedKeySummary(t, providerKeyFor(item.id)) ?? t("settings.apiKeys.notSet")
						: "";
					return <div key={item.id}><button type="button" data-login-method="" aria-label={label} aria-expanded={item.secret ? selected : undefined} aria-describedby={titleId + "-" + item.id} onClick={() => choose(item)} className="group/method relative flex min-h-9 w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left outline-none transition-colors hover:bg-(--color-interactive-hover) aria-expanded:hover:bg-transparent focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-default disabled:opacity-60">
						<MethodIcon className="size-3.5 shrink-0 text-settings-muted group-aria-expanded/method:text-settings-label" aria-hidden="true" />
						<span className={`min-w-0 flex-1 truncate text-control leading-5 ${secondaryMethod(item.id) ? "text-settings-muted group-hover/method:text-settings-label" : "text-settings-label"}`}>{label}</span>
						<span id={titleId + "-" + item.id} className={trailing ? "shrink-0 truncate text-xs leading-4 text-settings-muted" : "sr-only"}>{trailing || methodHint(item, key)}</span>
						{busy && selected ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
							: item.secret ? <ChevronDown className="size-3.5 shrink-0 text-settings-muted transition-transform group-aria-expanded/method:rotate-180" aria-hidden="true" />
							: item.id === "native" ? <ChevronRight className="size-3.5 shrink-0 text-settings-muted" aria-hidden="true" />
							: <ExternalLink className="size-3.5 shrink-0 text-settings-muted" aria-hidden="true" />}
					</button>
					{open && <div ref={secretRowRef} className="grid scroll-mb-4 grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2.5 pt-0.5 pr-3 pb-3.5 pl-9" onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); choose(item); } }}>
						{item.id === CATALOG_METHOD && <>
							<span id={titleId + "-provider"} className={fieldLabelClass}>{t("settings.harness.loginUi.catalogProvider")}</span>
							{catalog
								? <SettingsOptionMenu aria-label={t("settings.harness.loginUi.catalogProvider")} value={catalogProvider} options={catalog.map((provider) => ({ value: provider.id, label: provider.name }))} onChange={chooseCatalogProvider} placeholder={t("settings.harness.loginUi.catalogChoose")} searchable searchPlaceholder={t("settings.harness.loginUi.catalogSearch")} disabled={locked} triggerClassName={fieldTriggerClass} menuAlign="start" menuClassName="w-(--radix-dropdown-menu-trigger-width) max-h-72" />
								: catalogError ? <p role="alert" className="text-xs leading-4 text-error">{catalogError}</p>
								: <CatalogProviderPickerSkeleton label={t("settings.harness.loginUi.catalogLoading", { agent: agentLabel(agentId) })} />}
						</>}
						<>
							<label htmlFor={titleId + "-secret"} className={fieldLabelClass}>{secretLabel(item.id)}</label>
							<div className="flex min-w-0 items-center gap-2">
							<Input id={titleId + "-secret"} className={`${KEY_FIELD_CLASS} min-w-0 flex-1`} type="password" value={secret} disabled={locked} autoComplete="off" spellCheck={false} maxLength={16384} aria-invalid={error ? true : undefined} placeholder={savedKey ? t("settings.apiKeys.keepSaved") : secretPlaceholder(item.id) ?? (item.id === CATALOG_METHOD ? chosenProvider?.env : selectedKey?.name)} onChange={(event) => { setSecret(event.target.value); setModels([]); discardModelRequest(); setError(""); }} />
							{needsModel ? null : saveButton}
							</div>
						</>
						{needsModel && <>
							<span id={titleId + "-model"} className={fieldLabelClass}>{t("settings.apiKeys.model")}</span>
							<div className="flex items-center gap-2">
								{models.length > 0
									? <SettingsOptionMenu aria-label={t("settings.apiKeys.model")} value={model} options={models.map((id) => ({ value: id, label: id }))} onChange={setModel} searchable searchPlaceholder={t("settings.apiKeys.searchModels")} disabled={locked} triggerClassName={`${fieldTriggerClass} min-w-0 flex-1 font-mono`} menuAlign="start" menuClassName="w-(--radix-dropdown-menu-trigger-width)" />
									: <Input aria-labelledby={titleId + "-model"} className={`${KEY_FIELD_CLASS} min-w-0 flex-1 placeholder:font-sans`} value={model} disabled={locked} autoComplete="off" spellCheck={false} maxLength={256} placeholder={t("settings.models.custom")} onChange={(event) => setModel(event.target.value)} />}
								{selectedKey?.checkable && models.length === 0 && <Button type="button" variant="outline" disabled={locked || loadingModels || (!secret.trim() && !savedKey)} onClick={() => void loadModels()}>
									{loadingModels && <LoaderCircle className="animate-spin" aria-hidden="true" />}{t("settings.apiKeys.loadModels")}
								</Button>}
								{saveButton}
							</div>
						</>}
						{agentId === "claude-code" && item.id === "env_ANTHROPIC_API_KEY" && <KeyWarning className="col-start-2">{t("settings.apiKeys.claudeBilling")}</KeyWarning>}
						{agentId === "crush" && item.id === CATALOG_METHOD && CRUSH_SIGN_IN_PROVIDERS.has(catalogProvider) && <KeyWarning className="col-start-2">{t("settings.harness.loginUi.crushSignOut", { provider: chosenProvider?.name ?? catalogProvider })}</KeyWarning>}
						{item.id === "gemini_api_key" && geminiSharedNote && <p className="col-start-2 text-xs leading-4 text-settings-muted">{geminiSharedNote}</p>}
						{error && <p role="alert" className="col-start-2 text-xs leading-4 text-error">{error}</p>}
						{keyStateUnknown(item.id) && (providerKeysError
							? <div role="alert" className="col-start-2 flex flex-wrap items-center gap-x-2 text-xs leading-4 text-error">
								<span>{providerKeysError}</span>
								{onRetryProviderKeys && <Button type="button" size="sm" variant="ghost" className="-my-1" onClick={onRetryProviderKeys}>{t("settings.harness.retry")}</Button>}
							</div>
							: <p role="status" className="col-start-2 flex items-center gap-2 text-xs leading-4 text-settings-muted"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{t("settings.apiKeys.loading")}</p>)}
						{selectedKey?.deviceAvailable ? <DeviceEnvToggle className="col-start-2" checked={device} disabled={locked} onChange={setDevice} /> : null}
						{removableKey ? <div className="col-start-2 flex justify-end">
							<Button type="button" size="sm" variant="destructive-ghost" disabled={locked} onClick={() => void removeSavedKey()}>
								{busy && confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
								{t(confirmRemove ? "settings.apiKeys.confirmRemove" : "settings.apiKeys.remove")}
							</Button>
						</div> : null}
						{removed ? removed.warn
							? <KeyWarning className="col-start-2">{removed.text}</KeyWarning>
							: <p role="status" className="col-start-2 text-xs leading-4 text-settings-muted">{removed.text}</p> : null}
					</div>}
					</div>;
				})}
				</div>
				{group.note && <p className="text-xs leading-4 text-settings-muted">{group.note}</p>}
				</div>)}
			</fieldset>
		</form>}
		{(running || verifying || connected) && <div className="space-y-3">
			{!operation?.deviceCode && <p className="text-xs leading-4 text-settings-muted">{methodLabel}</p>}
			<div className="space-y-1">
				<p role="status" aria-live="polite" className={connected ? "flex items-center gap-2.5 text-[15px] leading-5 font-medium text-settings-label" : "flex items-center gap-2 text-[13px] leading-5 text-settings-label"}>
					{connected ? <span className={`flex size-6 shrink-0 items-center justify-center rounded-full ${configured ? "bg-(--color-bg-tertiary) text-settings-muted" : "bg-success/10 text-success"}`}>{configured ? <KeyRound className="size-4" aria-hidden="true" /> : <Check className="size-4" aria-hidden="true" />}</span> : needsTerminal ? <Terminal className="size-3.5 shrink-0 text-settings-muted" aria-hidden="true" />
						: <LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />}
					{status}
				</p>
				{connected && <p className="pl-8.5 text-xs leading-4 text-settings-muted">{configured ? t("settings.harness.models.configuredProviderHint") : t("settings.harness.loginUi.connectedDescription")}</p>}
				{/* Sharing is off in AO, but AO could not take its copy back out of
				    the user environment: say so, so it is not mistaken for gone. */}
				{operation?.withdrawFailed && <KeyWarning className="pl-8.5">{t("settings.apiKeys.savedWithdrawFailed")}</KeyWarning>}
			</div>
			{operation?.deviceCode && running && <div className="inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-(--color-bg-secondary) py-1 pr-1 pl-3">
				<code className="break-all font-mono text-[15px] leading-6 font-medium tracking-wide text-settings-label" aria-label={t("settings.harness.loginUi.deviceCode")}>{operation.deviceCode}</code>
				<Button size="sm" variant="ghost" onClick={() => act(async () => { await aoBridge.clipboard.writeText(operation.deviceCode!); setCopied(true); })}>{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copied ? "settings.harness.loginUi.copied" : "settings.harness.loginUi.copyCode")}</Button>
			</div>}
			{running && operation?.nativePrompt === "menu" && nativeOptions.length > 0 && <div className="space-y-2 rounded-md bg-(--color-bg-secondary) p-3">
				<p id={titleId + "-native-options"} className="text-control leading-5 text-settings-label">{t("settings.harness.loginUi.nativeChoose")}</p>
				{/* One shared list with dividers, like the method list above it. */}
				<div role="group" aria-labelledby={titleId + "-native-options"} className="flex flex-col divide-y divide-border overflow-hidden rounded-md bg-(--color-bg-primary)">
					{nativeOptions.map((option, index) => <button key={`${index}-${option}`} type="button" disabled={busy} onClick={() => void menuAction(String(index + 1) as MenuAction, option)} className="flex min-h-8 w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-control leading-5 text-settings-label outline-none transition-colors hover:bg-(--color-interactive-hover) focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-default disabled:opacity-60">
						<span className="tabular-nums text-settings-muted">{index + 1}.</span> {option}
					</button>)}
				</div>
				<div className="flex flex-wrap items-center gap-x-2 gap-y-1">
					<Button type="button" size="sm" variant="ghost" className="-ml-2" disabled={busy} onClick={() => void menuAction("select")}>{t("settings.harness.loginUi.nativeEnter")}</Button>
					<span className="text-xs leading-4 text-settings-muted">{t("settings.harness.loginUi.nativeEnterHint")}</span>
				</div>
			</div>}
			{running && operation?.nativePrompt === "menu" && nativeOptions.length === 0 && <div className="space-y-2 rounded-md bg-(--color-bg-secondary) p-3">
				<p className="text-control leading-5 text-settings-label">{t("settings.harness.loginUi.nativeSelected", { option: operation.selectedOption })}</p>
				<div className="flex flex-wrap gap-2">
					<Button type="button" size="sm" variant="secondary" disabled={busy} onClick={() => void menuAction("previous")}><ChevronUp aria-hidden="true" />{t("settings.harness.loginUi.nativePrevious")}</Button>
					<Button type="button" size="sm" variant="secondary" disabled={busy} onClick={() => void menuAction("next")}><ChevronDown aria-hidden="true" />{t("settings.harness.loginUi.nativeNext")}</Button>
					<Button type="button" size="sm" disabled={busy} onClick={() => void menuAction("select")}><Check aria-hidden="true" />{t("settings.harness.loginUi.nativeSelect")}</Button>
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
		{error && !errorInForm && <p role="alert" className="text-[13px] leading-5 text-error">{error}</p>}
		{/* The panel's own actions sit together at the trailing edge, as outline
		    buttons like Log in again and Log out on the Account card. */}
		<div className="flex flex-wrap items-center justify-end gap-2">
			{!connected && (availableMethods.length === 0 || !needsTerminal) && <Button variant="outline" size="sm" disabled={busy || verifying} onClick={() => void leave(true)}><Terminal aria-hidden="true" />{t("settings.harness.loginUi.terminal")}</Button>}
			<div className="flex flex-wrap items-center justify-end gap-2">
				{operation?.loginUrl && running && (agentId !== "claude-code" || operation.acceptsCode) && !(remote && agentId === "codex" && operation.method === "browser") && <Button size="sm" onClick={() => act(() => aoBridge.app.openExternal(operation.loginUrl!))}><ExternalLink aria-hidden="true" />{t("settings.harness.loginUi.openBrowser")}</Button>}
				{running && operation?.method === "native" && operation.nativePrompt !== "terminal_required" && <Button variant="outline" size="sm" disabled={busy || verifying} onClick={() => void checkNativeSignIn()}>{verifying ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}{t("settings.harness.loginUi.nativeCheck")}</Button>}
				{needsTerminal && <Button size="sm" disabled={busy} onClick={() => void leave(true)}><Terminal aria-hidden="true" />{t("settings.harness.loginUi.terminal")}</Button>}
				<Button variant={connected ? "primary" : "outline"} size="sm" disabled={busy || verifying} onClick={() => void leave()}>{t(connected ? "settings.harness.loginUi.done" : "settings.harness.loginUi.cancel")}</Button>
			</div>
		</div>
	</section>;
}
