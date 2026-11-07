import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNotificationPreferences } from "../../hooks/useNotificationPreferences";
import type { CloudCpNotificationPreference, CloudCpNotificationScope } from "../../lib/cloud-cp/types";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type NotificationGroup = "needsYou" | "agents" | "pullRequests";

/** Each kind the control plane knows today, its label and its group. */
const KINDS = {
	approval_needed: { label: "settings.notifications.kind.approvalNeeded", group: "needsYou" },
	needs_input: { label: "settings.notifications.kind.needsInput", group: "needsYou" },
	merge_requested: { label: "settings.notifications.kind.mergeRequested", group: "needsYou" },
	mentioned: { label: "settings.notifications.kind.mentioned", group: "needsYou" },
	suggestion_dismissed: { label: "settings.notifications.kind.suggestionDismissed", group: "agents" },
	agent_failed: { label: "settings.notifications.kind.agentFailed", group: "agents" },
	turn_finished: { label: "settings.notifications.kind.turnFinished", group: "agents" },
	ci_failed: { label: "settings.notifications.kind.ciFailed", group: "pullRequests" },
	pr_opened: { label: "settings.notifications.kind.prOpened", group: "pullRequests" },
	ready_to_merge: { label: "settings.notifications.kind.readyToMerge", group: "pullRequests" },
	pr_merged: { label: "settings.notifications.kind.prMerged", group: "pullRequests" },
	pr_closed_unmerged: { label: "settings.notifications.kind.prClosedUnmerged", group: "pullRequests" },
	review_feedback: { label: "settings.notifications.kind.reviewFeedback", group: "pullRequests" },
	review_completed: { label: "settings.notifications.kind.reviewCompleted", group: "pullRequests" },
	merge_request_merged: { label: "settings.notifications.kind.mergeRequestMerged", group: "pullRequests" },
	merge_request_rejected: { label: "settings.notifications.kind.mergeRequestRejected", group: "pullRequests" },
} as const satisfies Record<string, { label: string; group: NotificationGroup }>;

const GROUPS = [
	{ id: "needsYou", title: "settings.notifications.group.needsYou" },
	{ id: "agents", title: "settings.notifications.group.agents" },
	{ id: "pullRequests", title: "settings.notifications.group.pullRequests" },
] as const satisfies ReadonlyArray<{ id: NotificationGroup; title: string }>;

function knownKind(kind: string): kind is keyof typeof KINDS {
	return kind in KINDS;
}

/** A kind added after this build still gets a row: grouped by how urgent it is, named as the server names it. */
function kindGroup(preference: CloudCpNotificationPreference): NotificationGroup {
	if (knownKind(preference.kind)) return KINDS[preference.kind].group;
	return preference.tier === 0 ? "needsYou" : "agents";
}

function kindLabel(kind: string, t: TFunction): string {
	return knownKind(kind) ? t(KINDS[kind].label) : kind.replace(/_/g, " ");
}

/**
 * Settings › Notifications, for a control plane with team notifications. Two
 * decisions, nothing more: whose sessions notify you (one choice for every
 * kind), and which kinds also alert the desktop (one switch each). Everything
 * always shows in the bell; how closely you follow one session is set from
 * that session's Following menu.
 */
export function NotificationsSection({ titleHidden }: { titleHidden: boolean }) {
	const { t } = useTranslation();
	const preferences = useNotificationPreferences();
	const soundEnabled = useSoundNotificationsStore((state) => state.enabled);
	const setSoundEnabled = useSoundNotificationsStore((state) => state.setEnabled);
	const soundSaving = useSoundNotificationsStore((state) => state.saving);
	const soundSaveError = useSoundNotificationsStore((state) => state.saveError);
	const [saveFailed, setSaveFailed] = useState(false);
	const scopeOptions = [
		{ value: "following", label: t("settings.notifications.scope.following") },
		{ value: "mine", label: t("settings.notifications.scope.mine") },
	] satisfies Array<{ value: CloudCpNotificationScope; label: string }>;
	const items = preferences.preferences ?? [];
	// One scope for every kind: "only mine" once every kind says so.
	const scope: CloudCpNotificationScope = items.length > 0 && items.every((preference) => preference.scope === "mine") ? "mine" : "following";

	const save = (changes: CloudCpNotificationPreference[]) => {
		setSaveFailed(false);
		void preferences.save(changes.map((preference) => ({
			kind: preference.kind,
			inApp: true,
			desktop: preference.desktop,
			scope: preference.scope,
		}))).catch(() => setSaveFailed(true));
	};

	return (
		<>
			<SettingsSection title={t("settings.notifications")} titleHidden={titleHidden} sectionId="notifications" grouped>
				<SettingsRow
					label={t("settings.soundNotifications")}
					description={soundSaveError ? <span role="alert" className="text-error">{t("settings.soundNotifications.saveFailed")}</span> : undefined}
				>
					<Switch
						aria-label={t("settings.soundNotifications")}
						checked={soundEnabled}
						disabled={soundSaving}
						onCheckedChange={(next) => { void setSoundEnabled(next); }}
					/>
				</SettingsRow>
				{preferences.preferences ? (
					<SettingsRow label={t("settings.notifications.scope.label")}>
						<SettingsOptionMenu
							aria-label={t("settings.notifications.scope.label")}
							onChange={(next) => save(items.map((preference) => ({ ...preference, scope: next })))}
							options={scopeOptions}
							value={scope}
						/>
					</SettingsRow>
				) : null}
			</SettingsSection>
			{preferences.isError ? (
				<SettingsSection title={t("settings.notifications.events")} grouped>
					<SettingsRow label={<span className="text-error" role="alert">{t("settings.notifications.loadFailed")}</span>}>
						<Button onClick={() => void preferences.refetch()} size="sm" type="button" variant="ghost">{t("settings.notifications.retry")}</Button>
					</SettingsRow>
				</SettingsSection>
			) : preferences.preferences ? (
				<>
					<p className="px-(--settings-row-inset) text-control text-settings-muted">{t("settings.notifications.desktopIntro")}</p>
					{saveFailed ? <p className="px-(--settings-row-inset) text-control text-error" role="alert">{t("settings.notifications.saveFailed")}</p> : null}
					{GROUPS.map((group) => {
						const rows = items.filter((preference) => kindGroup(preference) === group.id);
						if (rows.length === 0) return null;
						return (
							<SettingsSection key={group.id} title={t(group.title)} grouped>
								{rows.map((preference) => {
									const label = kindLabel(preference.kind, t);
									return (
										<SettingsRow data-notification-kind={preference.kind} key={preference.kind} label={label}>
											<Switch
												aria-label={t("settings.notifications.desktopFor", { kind: label })}
												checked={preference.desktop}
												onCheckedChange={(desktop) => save([{ ...preference, desktop, scope }])}
											/>
										</SettingsRow>
									);
								})}
							</SettingsSection>
						);
					})}
				</>
			) : null}
		</>
	);
}
