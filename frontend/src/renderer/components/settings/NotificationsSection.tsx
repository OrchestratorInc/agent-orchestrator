import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNotificationPreferences } from "../../hooks/useNotificationPreferences";
import type { CloudCpNotificationPreferences } from "../../lib/cloud-cp/types";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * Settings › Notifications, on a control plane with mentions: a switch for
 * mentions of you, one for mentions of a team you're in, and, once the
 * control plane has asides, one for new replies in asides you're in. Off
 * stops the desktop alert and the phone push; the bell lists them anyway.
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

	// A control plane with asides answers the third switch; one without leaves it out.
	const asides = current?.asideReplies !== undefined;
	const rows: Array<{ key: keyof CloudCpNotificationPreferences; label: string }> = [
		{ key: "mentions", label: t("settings.notifications.mentionMe") },
		{ key: "teamMentions", label: t("settings.notifications.mentionTeam") },
		...(asides ? [{ key: "asideReplies" as const, label: t("settings.notifications.asideReplies") }] : []),
	];

	return (
		<>
			<SettingsSection title={asides ? t("settings.notifications.mentionsAndAsides") : t("settings.notifications.mentions")} sectionId="notifications" grouped>
				{preferences.isError ? (
					<SettingsRow label={<span className="text-error" role="alert">{t("settings.notifications.loadFailed")}</span>}>
						<Button onClick={() => void preferences.refetch()} size="sm" type="button" variant="ghost">{t("settings.notifications.retry")}</Button>
					</SettingsRow>
				) : current ? rows.map((row) => (
					<SettingsRow data-notification-preference={row.key} description={t("settings.notifications.alertTargets")} key={row.key} label={row.label}>
						<Switch
							aria-label={row.label}
							checked={current[row.key] !== false}
							onCheckedChange={(checked) => save({ [row.key]: checked })}
						/>
					</SettingsRow>
				)) : null}
			</SettingsSection>
			{saveFailed ? <p className="px-(--settings-row-inset) text-control text-error" role="alert">{t("settings.notifications.saveFailed")}</p> : null}
			{current ? <p className="px-(--settings-row-inset) text-control text-settings-muted">{asides ? t("settings.notifications.bellNoteAll") : t("settings.notifications.bellNote")}</p> : null}
		</>
	);
}
