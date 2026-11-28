import { LoaderCircle } from "@/components/app-icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { removeProviderKey, type ProviderKey } from "../../../hooks/useProviderKeys";
import type { AgentId } from "../../../lib/agent-options";
import { Button } from "../../ui/button";
import { KeyWarning, REMOVE_CONFIRM_MS, savedKeySummary } from "../providerKeyUi";
import { HarnessDetailGroup, HarnessDetailRow } from "./harness-ui";

/** The keys AO saved that a harness reads, for a harness that is not
	* installed. Its sign-in panel can't open without the harness, so this is
	* where those keys, and AO's shared copy in the user environment, are
	* removed. Keys are only entered through the sign-in panel. */
export function HarnessSavedKeys({ agentId, hostId, providerKeys, onProviderKeysChanged }: {
	agentId: AgentId;
	hostId?: string;
	providerKeys?: ProviderKey[];
	onProviderKeysChanged?: () => void;
}) {
	const { t } = useTranslation();
	const [notice, setNotice] = useState<{ warn: boolean; text: string } | null>(null);
	// Gemini's own key method saves GEMINI_API_KEY in AO too, though Gemini
	// isn't listed among the key's readers.
	const keys = providerKeys?.filter((key) => key.configured
		&& (key.agents.includes(agentId) || (agentId === "gemini" && key.name === "GEMINI_API_KEY"))) ?? [];
	if (!keys.length && !notice) return null;
	return <HarnessDetailGroup title={t("settings.harness.loginUi.groupKeys")}>
		{keys.map((key) => <SavedKeyRow key={key.name} providerKey={key} hostId={hostId} onRemoved={(result) => {
			setNotice(result.withdrawFailed
				? { warn: true, text: t("settings.apiKeys.removedWithdrawFailed") }
				: { warn: false, text: t("settings.apiKeys.removed") });
			onProviderKeysChanged?.();
		}} />)}
		{notice ? <div className="px-3 py-2.5">{notice.warn
			? <KeyWarning>{notice.text}</KeyWarning>
			: <p role="status" className="text-xs leading-4 text-settings-muted">{notice.text}</p>}</div> : null}
	</HarnessDetailGroup>;
}

function SavedKeyRow({ providerKey, hostId, onRemoved }: { providerKey: ProviderKey; hostId?: string; onRemoved: (result: ProviderKey) => void }) {
	const { t } = useTranslation();
	const [confirm, setConfirm] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	useEffect(() => {
		if (!confirm || busy) return;
		const timer = window.setTimeout(() => setConfirm(false), REMOVE_CONFIRM_MS);
		return () => window.clearTimeout(timer);
	}, [confirm, busy]);
	const remove = async () => {
		if (busy) return;
		if (!confirm) {
			setConfirm(true);
			return;
		}
		setBusy(true);
		setError("");
		try {
			const result = await removeProviderKey(providerKey.name, hostId);
			if (mounted.current) onRemoved(result);
		} catch (err) {
			if (mounted.current) setError(err instanceof Error ? err.message : t("settings.apiKeys.removeFailed"));
		} finally {
			if (mounted.current) {
				setConfirm(false);
				setBusy(false);
			}
		}
	};
	return <HarnessDetailRow
		label={providerKey.label}
		description={savedKeySummary(t, providerKey)}
		secondary={error ? <p role="alert" className="w-full text-xs leading-4 text-error">{error}</p> : undefined}
	>
		<Button type="button" size="sm" variant="destructive-ghost" disabled={busy} onClick={() => void remove()}>
			{busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
			{t(confirm ? "settings.apiKeys.confirmRemove" : "settings.apiKeys.remove")}
		</Button>
	</HarnessDetailRow>;
}
