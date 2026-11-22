import { LoaderCircle, X } from "@/components/app-icons";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { clearAgentEndpoint, setAgentEndpoint, type AgentEndpointStatus, type SetAgentEndpointRequest } from "../../hooks/useAgentEndpoint";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

type AuthStyle = NonNullable<SetAgentEndpointRequest["authStyle"]>;

/** Hostname for the compact row summary; falls back to the raw value. */
export function endpointHost(baseUrl: string | undefined): string {
	if (!baseUrl) return "";
	try {
		return new URL(baseUrl).host;
	} catch {
		return baseUrl;
	}
}

export function AgentEndpointDialog({
	agentId,
	agentName,
	endpoint,
	hostId,
	open,
	onOpenChange,
	onChanged,
}: {
	agentId: string;
	agentName: string;
	endpoint: AgentEndpointStatus;
	hostId?: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onChanged: () => void | Promise<void>;
}) {
	const { t } = useTranslation();
	// The dialog opens from a button, not a DialogTrigger, so focus goes back
	// to that button explicitly when it closes.
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [pending, setPending] = useState<"save" | "remove" | null>(null);
	const [baseUrl, setBaseUrl] = useState("");
	const [apiKey, setApiKey] = useState("");
	const [authStyle, setAuthStyle] = useState<AuthStyle>(endpoint.defaultAuthStyle);
	const [model, setModel] = useState("");
	const [error, setError] = useState("");
	const busy = pending !== null;
	const styles = endpoint.authStyles.filter((style): style is AuthStyle => style === "bearer" || style === "api-key");

	// Reset from the saved, display-safe status each time the dialog opens. The
	// key field always starts empty: the daemon never returns the stored key.
	useEffect(() => {
		if (!open) return;
		setBaseUrl(endpoint.baseUrl ?? "");
		setApiKey("");
		setAuthStyle(endpoint.authStyle ?? endpoint.defaultAuthStyle);
		setModel(endpoint.model ?? "");
		setError("");
	}, [open, endpoint.baseUrl, endpoint.authStyle, endpoint.defaultAuthStyle, endpoint.model]);

	const canSave = baseUrl.trim() !== "" && (endpoint.hasApiKey || apiKey.trim() !== "") && !busy;

	const save = async (event: FormEvent) => {
		event.preventDefault();
		if (!canSave) return;
		setError("");
		const body: SetAgentEndpointRequest = { baseUrl: baseUrl.trim(), authStyle };
		if (apiKey.trim()) body.apiKey = apiKey.trim();
		if (endpoint.supportsModel && model.trim()) body.model = model.trim();
		setPending("save");
		try {
			await setAgentEndpoint(agentId, body, hostId);
			setApiKey("");
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("settings.harness.endpoint.saveFailed"));
			setPending(null);
			return;
		}
		setPending(null);
		onOpenChange(false);
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
		onOpenChange(false);
		await onChanged();
	};

	const styleLabel = (style: AuthStyle) => style === "bearer" ? t("settings.harness.endpoint.authStyleBearer") : t("settings.harness.endpoint.authStyleApiKey");
	const fieldLabel = "grid gap-1.5 text-xs text-muted-foreground";

	return (
		<Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
			<DialogContent className={settingsDialogContentClass} onCloseAutoFocus={restoreFocusOnClose} showCloseButton={false}>
				<DialogClose asChild>
					<button aria-label={t("common.close")} className="settings-dialog-close-button settings-close-button" disabled={busy} type="button">
						<X aria-hidden="true" className="size-5" />
					</button>
				</DialogClose>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("settings.harness.endpoint.title")}</DialogTitle>
					<DialogDescription>{t("settings.harness.endpoint.description", { agent: agentName })}</DialogDescription>
				</div>

				<form className="contents" onSubmit={(event) => void save(event)}>
					<div className={settingsDialogBodyClass}>
						{error ? <p className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive" role="alert">{error}</p> : null}
						<label className={fieldLabel}>
							<span>{t("settings.harness.endpoint.baseUrl")}</span>
							<Input autoComplete="off" disabled={busy} inputMode="url" onChange={(event) => setBaseUrl(event.target.value)} placeholder={t("settings.harness.endpoint.baseUrlPlaceholder")} spellCheck={false} type="url" value={baseUrl} />
						</label>
						<label className={fieldLabel}>
							<span>{t("settings.harness.endpoint.apiKey")}</span>
							<Input
								autoComplete="off"
								disabled={busy}
								onChange={(event) => setApiKey(event.target.value)}
								placeholder={endpoint.hasApiKey ? t("settings.harness.endpoint.apiKeyKeep") : t("settings.harness.endpoint.apiKeyPlaceholder")}
								spellCheck={false}
								type="password"
								value={apiKey}
							/>
						</label>
						{styles.length > 1 ? (
							<div className={fieldLabel}>
								<span id={`agent-endpoint-style-${agentId}`}>{t("settings.harness.endpoint.authStyle")}</span>
								<Select disabled={busy} onValueChange={(value) => setAuthStyle(value as AuthStyle)} value={authStyle}>
									<SelectTrigger aria-labelledby={`agent-endpoint-style-${agentId}`} className="w-full border-border bg-background">
										<SelectValue>{styleLabel(authStyle)}</SelectValue>
									</SelectTrigger>
									<SelectContent align="start" className="w-[var(--radix-select-trigger-width)]">
										{styles.map((style) => (
											<SelectItem key={style} value={style}>{styleLabel(style)}</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
						) : null}
						{endpoint.supportsModel ? (
							<label className={fieldLabel}>
								<span>{t("settings.harness.endpoint.model")}</span>
								<Input autoComplete="off" disabled={busy} onChange={(event) => setModel(event.target.value)} placeholder={t("settings.harness.endpoint.modelPlaceholder")} spellCheck={false} value={model} />
							</label>
						) : null}
						<p className="text-xs text-muted-foreground">{t("settings.harness.endpoint.storageNote")}</p>
					</div>

					<div className={settingsDialogFooterClass}>
						{endpoint.configured ? (
							<Button disabled={busy} onClick={() => void remove()} type="button" variant="destructive-ghost">
								{pending === "remove" ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : null}
								{t("settings.harness.endpoint.remove")}
							</Button>
						) : null}
						<div className="flex-1" />
						<DialogClose asChild>
							<Button disabled={busy} type="button" variant="secondary">{t("confirm.cancel")}</Button>
						</DialogClose>
						<Button disabled={!canSave} type="submit" variant="primary">
							{pending === "save" ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : null}
							{pending === "save" ? t("settings.harness.endpoint.saving") : t("settings.harness.endpoint.save")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
