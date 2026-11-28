import { useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useCloudDataRetention } from "../../hooks/useCloudDataRetention";
import { useCloudGate } from "../../hooks/useCloudGate";
import { CloudCpError } from "../../lib/cloud-cp";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * How long the signed-in user's cloud sessions and their conversation content
 * are kept, counted from each session's creation. It is the user's own setting
 * and covers every cloud session they started, team sessions included, so it
 * shows whenever they are signed in to Cloud. "Default (N days)" follows the
 * deployment's default, and keeps following it if it changes; a period of
 * their own stays as chosen. A new period is checked with a dry run first;
 * when it would remove sessions' content the change waits for confirmation.
 */
export function DataRetentionSection() {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <DataRetentionSectionInner />;
}

type Notice = { kind: "saved" } | { kind: "error"; message: string };

/** The select's value for following the deployment's default. */
const DEFAULT_CHOICE = "default";

function subscribeOnline(onChange: () => void) {
	window.addEventListener("online", onChange);
	window.addEventListener("offline", onChange);
	return () => {
		window.removeEventListener("online", onChange);
		window.removeEventListener("offline", onChange);
	};
}

function useOnline(): boolean {
	return useSyncExternalStore(subscribeOnline, () => navigator.onLine, () => true);
}

/** No response came back: the request never reached the control plane. */
function unreachable(error: unknown): boolean {
	return !(error instanceof CloudCpError) || error.status === 0;
}

/** The control plane's own words for a refused period (400); ours otherwise. */
function retentionError(error: unknown, t: TFunction): string {
	if (unreachable(error)) return t("settings.dataRetention.offline");
	const cpError = error as CloudCpError;
	if (cpError.status === 400) return cpError.code && cpError.message ? cpError.message : t("settings.dataRetention.invalid");
	if (cpError.status === 401) return t("settings.dataRetention.signedOut");
	return t("settings.dataRetention.saveFailed");
}

function DataRetentionSectionInner() {
	const { t } = useTranslation();
	const retention = useCloudDataRetention();
	const online = useOnline();
	const [saving, setSaving] = useState(false);
	const [notice, setNotice] = useState<Notice | null>(null);
	// days null: back to the default.
	const [pending, setPending] = useState<{ days: number | null; sessions: number } | null>(null);
	const [confirming, setConfirming] = useState(false);
	const [confirmError, setConfirmError] = useState<string | null>(null);

	// Only for someone signed in to Cloud, against a control plane that has the setting.
	if (!retention.expected || retention.data === null) return null;

	const data = retention.data;
	const period = (days: number) => t("settings.dataRetention.days", { count: days });
	const label = t("settings.dataRetention.label");
	// A control plane that predates followsDefault has no way back to the
	// default; until the user chooses a period there (updatedAt), they follow it.
	const followsDefault = data ? (data.followsDefault ?? data.updatedAt === null) : false;
	const selected = data ? (followsDefault ? DEFAULT_CHOICE : String(data.retentionDays)) : "";

	const change = async (next: string) => {
		if (!data || next === selected) return;
		// Such a control plane takes the default's days instead of null.
		const days = next === DEFAULT_CHOICE ? (data.followsDefault === undefined ? data.defaultDays : null) : Number(next);
		setSaving(true);
		setNotice(null);
		try {
			const preview = await retention.preview(days);
			if (preview.sessionsToDelete > 0) {
				setConfirmError(null);
				setPending({ days, sessions: preview.sessionsToDelete });
				return;
			}
			await retention.apply(days);
			setNotice({ kind: "saved" });
		} catch (error) {
			setNotice({ kind: "error", message: retentionError(error, t) });
		} finally {
			setSaving(false);
		}
	};

	const confirm = async () => {
		if (!pending) return;
		setConfirming(true);
		setConfirmError(null);
		try {
			await retention.apply(pending.days);
			setPending(null);
			setNotice({ kind: "saved" });
		} catch (error) {
			setConfirmError(retentionError(error, t));
		} finally {
			setConfirming(false);
		}
	};

	let control;
	let note: { text: string; role?: "alert" | "status" } | null = null;
	if (!data) {
		if (retention.isPending) {
			control = (
				<SettingsOptionMenu aria-label={label} value="" options={[]} placeholder={t("common.loading")} onChange={() => undefined} disabled />
			);
		} else {
			control = (
				<Button type="button" size="sm" variant="ghost" onClick={retention.refetch}>
					{t("settings.dataRetention.retry")}
				</Button>
			);
			const offline = !online || unreachable(retention.error);
			note = { text: t(offline ? "settings.dataRetention.offline" : "settings.dataRetention.loadFailed"), role: "alert" };
		}
	} else {
		// A period of their own outside today's options (the deployment changed them) still shows as itself.
		const days = followsDefault || data.options.includes(data.retentionDays)
			? data.options
			: [...data.options, data.retentionDays].sort((a, b) => a - b);
		const options: SettingsOption<string>[] = [
			{ value: DEFAULT_CHOICE, label: t("settings.dataRetention.defaultDays", { count: data.defaultDays }) },
			...days.map((choice) => ({ value: String(choice), label: period(choice) })),
		];
		control = (
			<SettingsOptionMenu
				aria-label={label}
				value={selected}
				options={options}
				onChange={(next) => void change(next)}
				disabled={!online || saving || pending !== null}
			/>
		);
		if (notice?.kind === "error") note = { text: notice.message, role: "alert" };
		else if (!online) note = { text: t("settings.dataRetention.offline"), role: "alert" };
		else if (notice?.kind === "saved") note = { text: t("settings.dataRetention.saved"), role: "status" };
	}

	return (
		<>
			<SettingsSection title={t("settings.dataRetention.title")} sectionId="dataRetention" grouped>
				<div className="flex w-full flex-col">
					<SettingsRow className="rounded-none" label={label} description={t("settings.dataRetention.description")}>
						{control}
					</SettingsRow>
					{note ? (
						<p
							role={note.role}
							className={cn("px-3 pt-0 pb-4 text-xs leading-relaxed", note.role === "alert" ? "text-destructive" : "text-muted-foreground")}
						>
							{note.text}
						</p>
					) : null}
				</div>
			</SettingsSection>
			<ConfirmDialog
				open={pending !== null}
				destructive
				title={t("settings.dataRetention.confirmTitle")}
				description={pending && data
					? t("settings.dataRetention.confirmDescription", { count: pending.sessions, period: period(pending.days ?? data.defaultDays) })
					: ""}
				confirmLabel={t("settings.dataRetention.confirmAction")}
				busy={confirming}
				busyLabel={t("settings.dataRetention.saving")}
				error={confirmError}
				onConfirm={() => void confirm()}
				onOpenChange={(open) => {
					if (!open) {
						setPending(null);
						setConfirmError(null);
					}
				}}
			/>
		</>
	);
}
