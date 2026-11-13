import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNotificationPreferences } from "../../hooks/useNotificationPreferences";
import type { CloudCpNotificationPreferences } from "../../lib/cloud-cp/types";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * Settings › Notifications, on a control plane with mentions: two switches,
 * one for mentions of you and one for mentions of a team you're in. Off stops
 * the desktop alert and the phone push; the bell lists every mention anyway.
 */
export function NotificationsSection(_props: { titleHidden: boolean }) {
	const { t } = useTranslation();
	const preferences = useNotificationPreferences();
	const [saveFailed, setSaveFailed] = useState(false);
	const current = preferences.preferences;

	const save = (change: Partial<CloudCpNotificationPreferences>) => {
		setSaveFailed(false);
		void preferences.save(change).catch(() => setSaveFailed(true));
	};

	const rows: Array<{ key: keyof CloudCpNotificationPreferences; label: string }> = [
		{ key: "mentions", label: t("settings.notifications.mentionMe") },
		{ key: "teamMentions", label: t("settings.notifications.mentionTeam") },
	];

	return (
		<>
			<SettingsSection title={t("settings.notifications.mentions")} sectionId="notifications" grouped>
				{preferences.isError ? (
					<SettingsRow label={<span className="text-error" role="alert">{t("settings.notifications.loadFailed")}</span>}>
						<Button onClick={() => void preferences.refetch()} size="sm" type="button" variant="ghost">{t("settings.notifications.retry")}</Button>
					</SettingsRow>
				) : current ? rows.map((row) => (
					<SettingsRow data-notification-preference={row.key} description={t("settings.notifications.alertTargets")} key={row.key} label={row.label}>
						<Switch
							aria-label={row.label}
							checked={current[row.key]}
							onCheckedChange={(checked) => save({ [row.key]: checked })}
						/>
					</SettingsRow>
				)) : null}
			</SettingsSection>
			{saveFailed ? <p className="px-(--settings-row-inset) text-control text-error" role="alert">{t("settings.notifications.saveFailed")}</p> : null}
			{current ? <p className="px-(--settings-row-inset) text-control text-settings-muted">{t("settings.notifications.bellNote")}</p> : null}
		</>
	);
}
