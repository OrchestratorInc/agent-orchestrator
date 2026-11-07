import { Lock } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNotificationPreferences } from "../../hooks/useNotificationPreferences";
import type { CloudCpNotificationPreference, CloudCpNotificationScope } from "../../lib/cloud-cp/types";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Switch } from "../ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type NotificationGroup = "needsYou" | "agents" | "pullRequests";

/** Each kind the control plane knows today, its label and its group. */
const KINDS = {
	approval_needed: { label: "settings.notifications.kind.approvalNeeded", group: "needsYou" },
	needs_input: { label: "settings.notifications.kind.needsInput", group: "needsYou" },
	merge_requested: { label: "settings.notifications.kind.mergeRequested", group: "needsYou" },
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

// The control columns, shared by each group's heading and its rows so they line up.
const SCOPE_COLUMN = "w-28";
const CHANNEL_COLUMN = "w-14 flex justify-center";

/**
 * Settings › Notifications, for a control plane with team notifications:
 * the desktop's sound switch (the same setting as in General), then each kind
 * of notification by group, with whose sessions it covers and whether it
 * shows in the app and on the desktop. Kinds that block someone keep their
 * in-app notification on, shown with a lock.
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

	const change = (preference: CloudCpNotificationPreference, next: Partial<Pick<CloudCpNotificationPreference, "inApp" | "desktop" | "scope">>) => {
		setSaveFailed(false);
		void preferences.save([{
			kind: preference.kind,
			inApp: next.inApp ?? preference.inApp,
			desktop: next.desktop ?? preference.desktop,
			scope: next.scope ?? preference.scope,
		}]).catch(() => setSaveFailed(true));
	};

	const columns = (
		<div className="flex items-center gap-2 text-caption text-settings-muted" aria-hidden="true">
			<span className={SCOPE_COLUMN}>{t("settings.notifications.column.scope")}</span>
			<span className={CHANNEL_COLUMN}>{t("settings.notifications.column.inApp")}</span>
			<span className={CHANNEL_COLUMN}>{t("settings.notifications.column.desktop")}</span>
		</div>
	);

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
			</SettingsSection>
			{preferences.isError ? (
				<SettingsSection title={t("settings.notifications.events")} grouped>
					<SettingsRow label={<span className="text-error" role="alert">{t("settings.notifications.loadFailed")}</span>}>
						<Button onClick={() => void preferences.refetch()} size="sm" type="button" variant="ghost">{t("settings.notifications.retry")}</Button>
					</SettingsRow>
				</SettingsSection>
			) : preferences.preferences ? (
				<>
					{saveFailed ? <p className="px-(--settings-row-inset) text-control text-error" role="alert">{t("settings.notifications.saveFailed")}</p> : null}
					{GROUPS.map((group) => {
						const rows = (preferences.preferences ?? []).filter((preference) => kindGroup(preference) === group.id);
						if (rows.length === 0) return null;
						return (
							<SettingsSection action={columns} key={group.id} title={t(group.title)} grouped>
								{rows.map((preference) => {
									const label = kindLabel(preference.kind, t);
									return (
										<SettingsRow
											data-notification-kind={preference.kind}
											key={preference.kind}
											label={(
												<span className="inline-flex items-center gap-1.5">
													{label}
													{preference.locked ? (
														<Tooltip>
															<TooltipTrigger asChild>
																<Lock aria-label={t("settings.notifications.locked")} className="size-icon-xs text-settings-muted" role="img" />
															</TooltipTrigger>
															<TooltipContent>{t("settings.notifications.locked")}</TooltipContent>
														</Tooltip>
													) : null}
												</span>
											)}
										>
											<div className="flex items-center gap-2">
												<div className={SCOPE_COLUMN}>
													<SettingsOptionMenu
														aria-label={t("settings.notifications.scopeFor", { kind: label })}
														onChange={(scope) => change(preference, { scope })}
														options={scopeOptions}
														triggerClassName="w-full"
														value={preference.scope}
													/>
												</div>
												<div className={CHANNEL_COLUMN}>
													<Checkbox
														aria-label={t("settings.notifications.inAppFor", { kind: label })}
														checked={preference.locked || preference.inApp}
														disabled={preference.locked}
														onCheckedChange={(checked) => change(preference, { inApp: checked === true })}
													/>
												</div>
												<div className={CHANNEL_COLUMN}>
													<Checkbox
														aria-label={t("settings.notifications.desktopFor", { kind: label })}
														checked={preference.desktop}
														onCheckedChange={(checked) => change(preference, { desktop: checked === true })}
													/>
												</div>
											</div>
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
