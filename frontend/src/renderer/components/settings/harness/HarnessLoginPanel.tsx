import { Building2, Check, ChevronDown, Copy, ExternalLink, KeyRound, LoaderCircle, Smartphone, Terminal, UserRound } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../api/schema";
import { cancelAgentUILogin, getAgentUILogin, startAgentUILogin, submitAgentUILoginCode, type AgentLoginSnapshot } from "../../../hooks/useAgentAuth";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiErrorCode } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { LOCAL_HOST } from "../../../lib/hosts";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";

export function HarnessLoginPanel({ agentId, methods, hostId, onVerify, onClose, onTerminal }: {
	agentId: AgentId;
	methods: components["schemas"]["AgentLoginMethod"][];
	hostId?: string;
	onVerify: () => Promise<boolean>;
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
	const [loginCode, setLoginCode] = useState("");
	const [operation, setOperation] = useState<AgentLoginSnapshot>();
	const [busy, setBusy] = useState(false);
	const [verifying, setVerifying] = useState(false);
	const [connected, setConnected] = useState(false);
	const [error, setError] = useState("");
	const [copied, setCopied] = useState(false);
	const operationRef = useRef<AgentLoginSnapshot | undefined>(undefined);
	const mounted = useRef(true);
	const pending = useRef(false);
	const verifyRef = useRef(onVerify);
	verifyRef.current = onVerify;
	const method = availableMethods.find((item) => item.id === methodId) ?? availableMethods[0];
	const selectedMethodId = method?.id ?? methodId;
	const running = operation?.status === "running";
	const locked = busy || running || verifying;
	const updateOperation = (next: AgentLoginSnapshot | undefined) => {
		operationRef.current = next;
		setOperation(next);
	};

	useEffect(() => {
		mounted.current = true;
		panelRef.current?.querySelector<HTMLInputElement>("input:checked")?.focus({ preventScroll: true });
		return () => {
			mounted.current = false;
			const current = operationRef.current;
			if (current) void cancelAgentUILogin(agentId, current.id, hostId).catch(() => undefined);
		};
	}, [agentId, hostId]);

	useEffect(() => {
		if (!operation || operation.status !== "running" || busy) return;
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
				if (apiErrorCode(err) === "AGENT_AUTH_OPERATION_NOT_FOUND" || (err instanceof Error && "status" in err && (err.status === 404 || err.status === 410))) {
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
	}, [agentId, hostId, operation?.id, operation?.status, busy, t]);

	useEffect(() => {
		if (operation?.status !== "succeeded") return;
		let active = true;
		setVerifying(true);
		void verifyRef.current().then((verified) => {
			if (!active) return;
			setConnected(verified);
			if (!verified) setError(t("settings.harness.loginUi.unverified"));
		}).catch(() => {
			if (active) setError(t("settings.harness.loginUi.unverified"));
		}).finally(() => { if (active) setVerifying(false); });
		return () => { active = false; };
	}, [operation?.id, operation?.status, t]);

	const start = async () => {
		if (pending.current || locked || !method || (method.secret && !secret.trim())) return;
		pending.current = true;
		setBusy(true);
		setError("");
		setCopied(false);
		const credential = secret.trim();
		setSecret("");
		setLoginCode("");
		try {
			const next = await startAgentUILogin(agentId, selectedMethodId, credential, hostId);
			if (!mounted.current) {
				await cancelAgentUILogin(agentId, next.id, hostId);
				return;
			}
			setMethodId(next.method);
			updateOperation(next);
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
			if (current) await cancelAgentUILogin(agentId, current.id, hostId);
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
	const act = (action: () => Promise<unknown>) => void action().catch(() => setError(t("settings.harness.authFailed")));
	const status = verifying ? t("settings.harness.checkingLogin")
		: connected ? t("settings.harness.loginUi.connected")
		: operation?.status === "failed" ? t("settings.harness.loginUi.failed")
		: operation?.status === "timed_out" ? t("settings.harness.loginUi.timedOut")
		: operation?.status === "cancelled" ? t("settings.harness.loginUi.cancelled")
		: running ? t(method?.secret ? "settings.harness.loginUi.saving" : operation?.deviceCode ? "settings.harness.loginUi.waitingForCode" : "settings.harness.loginUi.waiting") : "";

	const selectedMethodKey = agentId === "claude-code" && selectedMethodId === "browser" ? "claude" : selectedMethodId;
	const methodLabel = t(`settings.harness.loginUi.methods.${selectedMethodKey}`, { defaultValue: method?.label });

	return <section ref={panelRef} aria-labelledby={titleId} className="@container min-w-0 space-y-4 p-4" data-settings-inline-edit="">
		<div className="space-y-1">
			<h4 id={titleId} className="text-sm font-medium text-settings-label">{t("settings.harness.loginUi.title", { agent: agentLabel(agentId) })}</h4>
			{!connected && !locked && <p className="text-xs text-settings-muted">{t("settings.harness.loginUi.method")}</p>}
			{remote && !connected && <p className="text-xs text-settings-muted">{t(agentId === "codex" ? "settings.harness.loginUi.remoteCodex" : "settings.harness.loginUi.remoteClaude")}</p>}
		</div>
		{!connected && !running && !verifying && <form id={titleId + "-form"} className="space-y-4" onSubmit={(event) => { event.preventDefault(); void start(); }}>
			<fieldset disabled={locked} className="grid auto-rows-fr grid-cols-1 gap-2 @min-[32rem]:grid-cols-2">
				<legend className="sr-only">{t("settings.harness.loginUi.method")}</legend>
				{availableMethods.map((item) => {
					const key = agentId === "claude-code" && item.id === "browser" ? "claude" : item.id;
					const label = t(`settings.harness.loginUi.methods.${key}`, { defaultValue: item.label });
					const MethodIcon = item.secret ? KeyRound : item.id === "device_code" ? Smartphone : item.id === "sso" || item.id === "console" ? Building2 : UserRound;
					return <label key={item.id} className="relative flex cursor-pointer items-start gap-3 rounded-lg border border-border p-3 transition-colors hover:bg-(--color-interactive-hover) has-[:checked]:border-foreground/60 has-[:checked]:bg-(--color-bg-primary) has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring has-[:disabled]:cursor-default has-[:disabled]:opacity-60">
						<input type="radio" className="peer sr-only" name={titleId} aria-label={label} aria-describedby={titleId + "-" + item.id} value={item.id} checked={selectedMethodId === item.id} onChange={() => { setMethodId(item.id); setSecret(""); setLoginCode(""); setError(""); updateOperation(undefined); }} />
						<span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-(--color-bg-tertiary) text-settings-muted peer-checked:text-settings-label"><MethodIcon className="size-4" aria-hidden="true" /></span>
						<span className="min-w-0 flex-1 space-y-1">
							<span className="block text-sm font-medium text-settings-label">{label}</span>
							<span id={titleId + "-" + item.id} className="block text-xs leading-4 text-settings-muted">{t(`settings.harness.loginUi.hints.${key}`, { defaultValue: item.label })}</span>
						</span>
						<Check className="mt-1 size-3.5 shrink-0 text-settings-label opacity-0 peer-checked:opacity-100" aria-hidden="true" />
					</label>;
				})}
			</fieldset>
			{method?.secret && <label className="block space-y-2 text-sm">
				<span className="font-medium text-settings-label">{t(methodId === "api_key" ? "settings.harness.loginUi.apiKey" : "settings.harness.loginUi.accessToken")}</span>
				<Input className="max-w-lg font-mono" type="password" value={secret} disabled={locked} autoComplete="off" spellCheck={false} maxLength={16384} onChange={(event) => setSecret(event.target.value)} />
			</label>}
		</form>}
		{(running || verifying || connected) && <div className="space-y-3 rounded-lg border border-border bg-(--color-bg-primary) p-4">
			<p className="text-xs font-medium text-settings-muted">{methodLabel}</p>
			<p role="status" aria-live="polite" className="flex items-center gap-2 text-sm text-settings-label">
				{connected ? <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-success/10 text-success"><Check className="size-4" aria-hidden="true" /></span> : <LoaderCircle className="size-4 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />}
				{status}
			</p>
			{connected && <p className="text-xs text-settings-muted">{t("settings.harness.loginUi.connectedDescription")}</p>}
			{operation?.deviceCode && running && <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-(--color-bg-secondary) p-3">
				<code className="break-all text-lg tracking-widest" aria-label={t("settings.harness.loginUi.deviceCode")}>{operation.deviceCode}</code>
				<Button size="sm" variant="ghost" onClick={() => act(async () => { await aoBridge.clipboard.writeText(operation.deviceCode!); setCopied(true); })}>{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copied ? "settings.harness.loginUi.copied" : "settings.harness.loginUi.copyCode")}</Button>
			</div>}
		</div>}
		{running && operation.acceptsCode && <form className="max-w-lg space-y-2" onSubmit={(event) => { event.preventDefault(); void submitCode(); }}>
			<label className="block space-y-2 text-sm">
				<span className="font-medium text-settings-label">{t("settings.harness.loginUi.signInCode")}</span>
				<Input type="password" value={loginCode} disabled={busy} autoComplete="off" spellCheck={false} maxLength={4096} onChange={(event) => setLoginCode(event.target.value)} />
			</label>
			<p className="text-xs text-settings-muted">{t("settings.harness.loginUi.codeHint")}</p>
			<Button type="submit" size="sm" disabled={busy || !loginCode.trim()}>{busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}{t("settings.harness.loginUi.submitCode")}</Button>
		</form>}
		{status && !running && !verifying && !connected && <p role="status" className="text-sm text-settings-muted">{status}</p>}
		{error && <p role="alert" className="text-sm text-error">{error}</p>}
		<div className="flex flex-wrap items-center gap-2">
			{!connected && !running && !verifying && <Button type="submit" form={titleId + "-form"} size="sm" disabled={busy || !method || (method?.secret && !secret.trim())}>
				{busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : !method?.secret && <ExternalLink aria-hidden="true" />}
				{t(method?.secret ? "settings.harness.loginUi.save" : selectedMethodId === "device_code" ? "settings.harness.loginUi.createDeviceCode" : "settings.harness.loginUi.continueBrowser")}
			</Button>}
			{operation?.loginUrl && running && (agentId !== "claude-code" || operation.acceptsCode) && !(remote && agentId === "codex" && operation.method === "browser") && <Button size="sm" onClick={() => act(() => aoBridge.app.openExternal(operation.loginUrl!))}><ExternalLink aria-hidden="true" />{t("settings.harness.loginUi.openBrowser")}</Button>}
			<Button variant={connected ? "primary" : "ghost"} size="sm" disabled={busy || verifying} onClick={() => void leave()}>{t(connected ? "settings.harness.loginUi.done" : "settings.harness.loginUi.cancel")}</Button>
		</div>
		{!connected && <details className="group border-t border-border pt-3">
			<summary className="flex w-fit cursor-pointer list-none items-center gap-1 text-xs text-settings-muted outline-none hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden"><ChevronDown className="size-3.5 transition-transform group-open:rotate-180" aria-hidden="true" />{t("settings.harness.loginUi.otherOptions")}</summary>
			<Button className="mt-2 -ml-2" variant="ghost" size="sm" disabled={busy || verifying} onClick={() => void leave(true)}><Terminal aria-hidden="true" />{t("settings.harness.loginUi.terminal")}</Button>
		</details>}
	</section>;
}
