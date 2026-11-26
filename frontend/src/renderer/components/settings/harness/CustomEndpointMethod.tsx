import { ChevronDown, Globe, LoaderCircle } from "@/components/app-icons";
import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { clearAgentEndpoint, setAgentEndpoint, type AgentEndpointStatus, type SetAgentEndpointRequest } from "../../../hooks/useAgentEndpoint";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { Pressable } from "../../ui/pressable";
import { KEY_FIELD_CLASS, KeyWarning } from "../providerKeyUi";

/** Hostname for compact summaries; falls back to the raw value. */
export function endpointHost(baseUrl: string | undefined): string {
	if (!baseUrl) return "";
	try {
		return new URL(baseUrl).host;
	} catch {
		return baseUrl;
	}
}

/** Whether agentId has a saved custom endpoint, usable or not. */
export function endpointSaved(endpoint: AgentEndpointStatus | undefined): boolean {
	return Boolean(endpoint && (endpoint.configured || endpoint.error));
}

/** One sign-in option beside the API keys: the harness sends its requests to
 * the user's own base URL and key. It expands in place like a key row; the
 * saved key is never shown, and leaving the key field empty keeps it. How the
 * key is sent (Bearer or x-api-key) is not asked: the daemon tries each with
 * the endpoint on save and keeps the one it accepts. The model is not asked
 * either: the endpoint's models appear in the Models section, where one can be
 * made the default; until then the first model the endpoint lists is used. */
export function CustomEndpointMethod({ agentId, endpoint, hostId, open, disabled, onToggle, onChanged }: {
	agentId: string;
	endpoint: AgentEndpointStatus;
	hostId?: string;
	open: boolean;
	disabled?: boolean;
	onToggle: () => void;
	onChanged: () => void | Promise<void>;
}) {
	const { t } = useTranslation();
	const id = useId();
	const rowRef = useRef<HTMLDivElement>(null);
	const [pending, setPending] = useState<"save" | "remove" | null>(null);
	const [baseUrl, setBaseUrl] = useState("");
	const [apiKey, setApiKey] = useState("");
	const [error, setError] = useState("");
	const busy = pending !== null;
	const locked = busy || Boolean(disabled);
	const unusable = Boolean(endpoint.error);
	const saved = endpointSaved(endpoint);

	// Each opening starts from the saved, display-safe status. The key field
	// always starts empty: the daemon never returns the stored key.
	useEffect(() => {
		if (!open) return;
		setBaseUrl(endpoint.baseUrl ?? "");
		setApiKey("");
		setError("");
		rowRef.current?.querySelector("input")?.focus({ preventScroll: true });
	}, [open, endpoint.baseUrl]);

	// An unusable endpoint has no key AO can keep, so it needs a new one.
	const canSave = baseUrl.trim() !== "" && ((endpoint.hasApiKey && !unusable) || apiKey.trim() !== "") && !locked;

	const save = async () => {
		if (!canSave) return;
		setError("");
		const body: SetAgentEndpointRequest = { baseUrl: baseUrl.trim() };
		if (apiKey.trim()) body.apiKey = apiKey.trim();
		setPending("save");
		try {
			await setAgentEndpoint(agentId, body, hostId);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("settings.harness.endpoint.saveFailed"));
			setPending(null);
			return;
		}
		setPending(null);
		setApiKey("");
		await onChanged();
	};

	const remove = async () => {
		setError("");
		setPending("remove");
		try {
			await clearAgentEndpoint(agentId, hostId);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("settings.harness.endpoint.removeFailed"));
			setPending(null);
			return;
		}
		setPending(null);
		await onChanged();
	};

	// The row sits inside the sign-in panel's form: Enter saves this endpoint,
	// never the panel's selected method, and Escape collapses the row.
	const onFieldKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (event.key === "Escape") {
			event.stopPropagation();
			onToggle();
		} else if (event.key === "Enter" && event.target instanceof HTMLInputElement) {
			event.preventDefault();
			void save();
		}
	};

	const label = t("settings.harness.endpoint.method");
	const trailing = unusable ? t("settings.harness.endpoint.unusable")
		: endpoint.configured ? t("settings.harness.endpoint.active", { host: endpointHost(endpoint.baseUrl) })
		: t("settings.harness.endpoint.notSet");
	const fieldLabelClass = "block text-xs leading-4 text-settings-muted";

	return <div data-custom-endpoint-method="">
		<Pressable aria-label={label} aria-expanded={open} aria-describedby={id + "-status"} data-login-method="" disabled={locked} onClick={onToggle} className="group/method relative flex min-h-9 w-full cursor-pointer items-center gap-2.5 px-3 py-2 transition-colors hover:bg-(--color-interactive-hover) aria-expanded:hover:bg-transparent focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-default disabled:opacity-60">
			<Globe className="size-3.5 shrink-0 text-settings-muted group-aria-expanded/method:text-settings-label" aria-hidden="true" />
			<span className="min-w-0 flex-1 truncate text-control leading-5 text-settings-label">{label}</span>
			<span id={id + "-status"} className={unusable ? "shrink-0 truncate text-xs leading-4 text-error" : "shrink-0 truncate text-xs leading-4 text-settings-muted"}>{trailing}</span>
			{busy ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
				: <ChevronDown className="size-3.5 shrink-0 text-settings-muted transition-transform group-aria-expanded/method:rotate-180" aria-hidden="true" />}
		</Pressable>
		{open ? <div ref={rowRef} className="grid scroll-mb-4 grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2.5 pt-0.5 pr-3 pb-3.5 pl-9" onKeyDown={onFieldKeyDown}>
			{unusable ? <KeyWarning className="col-start-2">{t("settings.harness.endpoint.unusableSummary")}</KeyWarning> : null}
			<label htmlFor={id + "-url"} className={fieldLabelClass}>{t("settings.harness.endpoint.baseUrl")}</label>
			<Input id={id + "-url"} className={`${KEY_FIELD_CLASS} placeholder:font-sans`} type="url" inputMode="url" value={baseUrl} disabled={locked} autoComplete="off" spellCheck={false} maxLength={2048} placeholder={t("settings.harness.endpoint.baseUrlPlaceholder")} onChange={(event) => { setBaseUrl(event.target.value); setError(""); }} />
			<label htmlFor={id + "-key"} className={fieldLabelClass}>{t("settings.harness.endpoint.apiKey")}</label>
			<div className="flex min-w-0 items-center gap-2">
				<Input id={id + "-key"} className={`${KEY_FIELD_CLASS} min-w-0 flex-1`} type="password" value={apiKey} disabled={locked} autoComplete="off" spellCheck={false} maxLength={16384} aria-invalid={error ? true : undefined} placeholder={endpoint.hasApiKey && !unusable ? t("settings.harness.endpoint.apiKeyKeep") : t("settings.harness.endpoint.apiKeyPlaceholder")} onChange={(event) => { setApiKey(event.target.value); setError(""); }} />
				<Button type="button" disabled={!canSave} onClick={() => void save()}>
					{pending === "save" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
					{t("settings.harness.endpoint.save")}
				</Button>
			</div>
			{error ? <p role="alert" className="col-start-2 text-xs leading-4 text-error">{error}</p> : null}
			<p className="col-start-2 text-xs leading-4 text-settings-muted">{t("settings.harness.endpoint.storageNote")}</p>
			{saved ? <div className="col-start-2 flex justify-end">
				<Button type="button" size="sm" variant="destructive-ghost" disabled={locked} onClick={() => void remove()}>
					{pending === "remove" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
					{t("settings.harness.endpoint.remove")}
				</Button>
			</div> : null}
		</div> : null}
	</div>;
}
