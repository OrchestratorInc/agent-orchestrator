import { ExternalLink, LoaderCircle } from "@/components/app-icons";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { checkProviderKey, removeProviderKey, saveProviderKey, useInvalidateProviderKeys, useProviderKeys } from "../../hooks/useProviderKeys";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { KEY_FIELD_CLASS, REMOVE_CONFIRM_MS, savedKeySummary } from "./providerKeyUi";
import { SettingsSection } from "./SettingsSection";

/** The provider key AO's GMI tools read. It belongs to no agent: AO's own MCP
 * server reads it from AO's store, so a session never carries it. */
export const GMI_PROVIDER_KEY = "GMI_API_KEY";

/** Where GMI Cloud issues API keys. */
const GMI_API_KEYS_URL = "https://console.gmicloud.ai/user-setting/api-keys";

/**
 * GMI Cloud's API key: the one setting that turns on AO's GMI tools. The key
 * stays in AO's store — it is never published to the OS user environment and
 * never reaches an agent's environment, so it is saved without a device
 * setting here.
 */
export function GMISettingsSection({ titleHidden, hostId }: { titleHidden?: boolean; hostId?: string }) {
	const { t } = useTranslation();
	const { data: keys, isPending, error } = useProviderKeys(hostId);
	const invalidate = useInvalidateProviderKeys(hostId);
	const key = keys?.find((item) => item.name === GMI_PROVIDER_KEY);
	const [secret, setSecret] = useState("");
	const [busy, setBusy] = useState(false);
	const [message, setMessage] = useState<{ tone: "error" | "muted"; text: string }>();
	// Removing a saved key takes a second click, which disarms itself after a
	// few seconds.
	const [confirmRemove, setConfirmRemove] = useState(false);

	useEffect(() => {
		if (!confirmRemove || busy) return;
		const timer = window.setTimeout(() => setConfirmRemove(false), REMOVE_CONFIRM_MS);
		return () => window.clearTimeout(timer);
	}, [confirmRemove, busy]);

	const save = async () => {
		const credential = secret.trim();
		if (!credential || busy) return;
		setBusy(true);
		setMessage(undefined);
		try {
			// A new key is checked first, so a mistyped one is never saved.
			if (key?.checkable) {
				const result = await checkProviderKey(GMI_PROVIDER_KEY, credential, hostId);
				if (result.status === "invalid") {
					setSecret(credential);
					setMessage({ tone: "error", text: t("settings.apiKeys.rejected") });
					return;
				}
			}
			await saveProviderKey(GMI_PROVIDER_KEY, credential, false, hostId);
			setSecret("");
			await invalidate();
		} catch (cause) {
			// Keep the typed key, so the user can try again without retyping it.
			setSecret(credential);
			setMessage({ tone: "error", text: cause instanceof Error ? cause.message : t("settings.apiKeys.checkFailed") });
		} finally {
			setBusy(false);
		}
	};

	const remove = async () => {
		if (!key?.configured || busy) return;
		if (!confirmRemove) {
			setConfirmRemove(true);
			return;
		}
		setBusy(true);
		setMessage(undefined);
		try {
			await removeProviderKey(GMI_PROVIDER_KEY, hostId);
			setSecret("");
			setConfirmRemove(false);
			await invalidate();
		} catch (cause) {
			setMessage({ tone: "error", text: cause instanceof Error ? cause.message : t("settings.apiKeys.removeFailed") });
		} finally {
			setBusy(false);
		}
	};

	const saved = savedKeySummary(t, key);

	return (
		<SettingsSection title={t("settings.gmi")} sectionId="gmi" titleHidden={titleHidden} grouped>
			<div className="settings-content-row space-y-2">
				<label htmlFor="gmi-api-key" className="block text-xs leading-4 text-settings-muted">{t("settings.gmi.apiKey")}</label>
				<div className="flex min-w-0 items-center gap-2">
					<Input
						id="gmi-api-key"
						className={`${KEY_FIELD_CLASS} min-w-0 flex-1`}
						type="password"
						value={secret}
						disabled={busy || isPending}
						autoComplete="off"
						spellCheck={false}
						maxLength={16384}
						aria-invalid={message?.tone === "error" ? true : undefined}
						placeholder={key?.configured ? t("settings.apiKeys.keepSaved") : t("settings.gmi.apiKeyPlaceholder")}
						onChange={(event) => { setSecret(event.target.value); setMessage(undefined); }}
					/>
					<Button type="button" disabled={busy || isPending || !secret.trim()} onClick={() => void save()}>
						{busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
						{t("settings.gmi.save")}
					</Button>
				</div>
				<p className="text-xs leading-4 text-settings-muted">{t("settings.gmi.description")}</p>
				{error ? (
					<p role="alert" className="text-xs leading-4 text-error">{error instanceof Error ? error.message : t("settings.apiKeys.checkFailed")}</p>
				) : message ? (
					<p role={message.tone === "error" ? "alert" : "status"} className={message.tone === "error" ? "text-xs leading-4 text-error" : "text-xs leading-4 text-settings-muted"}>{message.text}</p>
				) : null}
				{saved ? (
					<div className="flex items-center justify-between gap-2">
						<p className="text-xs leading-4 text-settings-muted">{saved}</p>
						<Button type="button" size="sm" variant="destructive-ghost" disabled={busy} onClick={() => void remove()}>
							{busy && confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
							{t(confirmRemove ? "settings.apiKeys.confirmRemove" : "settings.apiKeys.remove")}
						</Button>
					</div>
				) : null}
				{!key?.configured ? (
					<div className="flex">
						<Button type="button" size="sm" variant="quiet" className="-ml-2.5" onClick={() => void aoBridge.app.openExternal(GMI_API_KEYS_URL)}>
							<ExternalLink aria-hidden="true" />{t("settings.gmi.getKey")}
						</Button>
					</div>
				) : null}
			</div>
		</SettingsSection>
	);
}
