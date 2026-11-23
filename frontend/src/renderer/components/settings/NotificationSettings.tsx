import { useEffect, useRef, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import {
	Bell,
	CheckCheck,
	ChevronLeft,
	ChevronRight,
	CircleAlert,
	Cloud,
	GitMerge,
	GitPullRequest,
	GitPullRequestArrow,
	GitPullRequestClosed,
	Laptop,
	MessageSquareDiff,
	MessageSquareDot,
	Server,
	Users,
	XCircle,
	type AppIcon,
} from "@/components/app-icons";
import { useCloudGate } from "../../hooks/useCloudGate";
import type { MessageKey } from "../../i18n/messages";
import {
	NOTIFICATION_KINDS,
	remoteNotificationSource,
	type NotificationKindId,
	type NotificationSource,
} from "../../lib/notification-preferences";
import { savedRemoteHosts, subscribeSavedRemoteHosts } from "../../lib/saved-remote-hosts";
import { useNotificationPreferencesStore } from "../../stores/notification-preferences-store";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

const KIND_COPY: Record<NotificationKindId, { icon: AppIcon; label: MessageKey; description: MessageKey }> = {
	needs_input: {
		icon: MessageSquareDot,
		label: "settings.notifications.kind.needsInput",
		description: "settings.notifications.kind.needsInput.description",
	},
	agent_completed: {
		icon: CheckCheck,
		label: "settings.notifications.kind.agentCompleted",
		description: "settings.notifications.kind.agentCompleted.description",
	},
	agent_failed: {
		icon: CircleAlert,
		label: "settings.notifications.kind.agentFailed",
		description: "settings.notifications.kind.agentFailed.description",
	},
	pr_opened: {
		icon: GitPullRequest,
		label: "settings.notifications.kind.prOpened",
		description: "settings.notifications.kind.prOpened.description",
	},
	ci_failed: {
		icon: XCircle,
		label: "settings.notifications.kind.ciFailed",
		description: "settings.notifications.kind.ciFailed.description",
	},
	review_completed: {
		icon: CheckCheck,
		label: "settings.notifications.kind.reviewCompleted",
		description: "settings.notifications.kind.reviewCompleted.description",
	},
	review_changes: {
		icon: MessageSquareDiff,
		label: "settings.notifications.kind.reviewChanges",
		description: "settings.notifications.kind.reviewChanges.description",
	},
	ready_to_merge: {
		icon: GitPullRequestArrow,
		label: "settings.notifications.kind.readyToMerge",
		description: "settings.notifications.kind.readyToMerge.description",
	},
	merge_requests: {
		icon: Users,
		label: "settings.notifications.kind.mergeRequests",
		description: "settings.notifications.kind.mergeRequests.description",
	},
	pr_merged: {
		icon: GitMerge,
		label: "settings.notifications.kind.prMerged",
		description: "settings.notifications.kind.prMerged.description",
	},
	pr_closed: {
		icon: GitPullRequestClosed,
		label: "settings.notifications.kind.prClosed",
		description: "settings.notifications.kind.prClosed.description",
	},
	other: {
		icon: Bell,
		label: "settings.notifications.kind.other",
		description: "settings.notifications.kind.other.description",
	},
};

/**
 * General › Notifications: the master switch, sound, and the way into Manage
 * notifications, which only shows while notifications are on.
 */
export function NotificationsSettingsSection({ onManage, focusManage }: { onManage: () => void; focusManage: boolean }) {
	const { t } = useTranslation();
	const enabled = useNotificationPreferencesStore((state) => state.enabled);
	const loaded = useNotificationPreferencesStore((state) => state.loaded);
	const saving = useNotificationPreferencesStore((state) => state.saving);
	const saveError = useNotificationPreferencesStore((state) => state.saveError);
	const setEnabled = useNotificationPreferencesStore((state) => state.setEnabled);
	const soundEnabled = useSoundNotificationsStore((state) => state.enabled);
	const setSoundEnabled = useSoundNotificationsStore((state) => state.setEnabled);
	const soundSaving = useSoundNotificationsStore((state) => state.saving);
	const soundSaveError = useSoundNotificationsStore((state) => state.saveError);
	const manageRef = useRef<HTMLButtonElement>(null);
	// Back from Manage notifications lands on the row it was opened from.
	useEffect(() => {
		if (focusManage) manageRef.current?.focus();
	}, [focusManage]);

	return (
		<SettingsSection title={t("settings.notifications")} grouped>
			<SettingsRow
				label={t("settings.notifications")}
				description={
					saveError ? (
						<span role="alert" className="text-error">{t("settings.notifications.saveFailed")}</span>
					) : (
						t("settings.notifications.description")
					)
				}
			>
				<Switch
					aria-label={t("settings.notifications")}
					checked={enabled}
					disabled={!loaded || saving}
					onCheckedChange={(next) => {
						void setEnabled(next);
					}}
				/>
			</SettingsRow>
			<SettingsRow
				label={t("settings.soundNotifications")}
				description={soundSaveError ? <span role="alert" className="text-error">{t("settings.soundNotifications.saveFailed")}</span> : undefined}
			>
				<Switch
					aria-label={t("settings.soundNotifications")}
					checked={soundEnabled}
					disabled={soundSaving}
					onCheckedChange={(next) => {
						void setSoundEnabled(next);
					}}
				/>
			</SettingsRow>
			{enabled ? (
				<Pressable
					ref={manageRef}
					className="settings-row-bar flex w-full items-center gap-3 text-left transition-colors duration-(--duration-fast) hover:bg-(--color-bg-settings-row-hover) focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
					disabled={saving}
					onClick={onManage}
					data-manage-notifications=""
				>
					<span className="min-w-0 flex-1">
						<span className="block truncate text-sm leading-5 text-settings-label">{t("settings.notifications.manage")}</span>
						<span className="block text-control leading-4 text-settings-muted">{t("settings.notifications.manageDescription")}</span>
					</span>
					<ChevronRight className="size-icon-md shrink-0 text-passive" aria-hidden="true" />
				</Pressable>
			) : null}
		</SettingsSection>
	);
}

/**
 * Manage notifications: which kinds and which machines notify. Opens in place
 * of General, as a team opens in place of Teams, with the way back on top.
 */
export function ManageNotificationsPage({ onBack }: { onBack: () => void }) {
	const { t } = useTranslation();
	const filters = useNotificationPreferencesStore((state) => state.filters);
	// Each save writes every preference, so the switches wait until the saved
	// ones have loaded, and for one save to finish before the next: a second in
	// flight would carry the first's change past its rollback.
	const locked = useNotificationPreferencesStore((state) => !state.loaded || state.saving);
	const saveError = useNotificationPreferencesStore((state) => state.saveError);
	const setKindMuted = useNotificationPreferencesStore((state) => state.setKindMuted);
	const setSourceMuted = useNotificationPreferencesStore((state) => state.setSourceMuted);
	const { cloudEnabled } = useCloudGate();
	const remoteHosts = useSyncExternalStore(subscribeSavedRemoteHosts, savedRemoteHosts, savedRemoteHosts);
	const backRef = useRef<HTMLButtonElement>(null);
	// Opening a page puts focus on its way back, so the keyboard starts here.
	useEffect(() => {
		backRef.current?.focus();
	}, []);

	const machines: { source: NotificationSource; icon: AppIcon; label: string }[] = [
		{ source: "local", icon: Laptop, label: t("settings.notifications.thisComputer") },
		...remoteHosts.map((host) => ({ source: remoteNotificationSource(host.hostId), icon: Server, label: host.label })),
		...(cloudEnabled ? [{ source: "cloud" as const, icon: Cloud, label: t("settings.notifications.cloud") }] : []),
	];

	return (
		<div className="flex flex-col gap-(--size-settings-section-gap,24px)" data-manage-notifications-page="">
			<div className="flex flex-col gap-2 px-(--settings-row-inset)">
				<Button
					ref={backRef}
					type="button"
					variant="quiet"
					size="none"
					className="-ml-1 h-6 gap-1 self-start px-1 text-control [&_svg]:size-3.5"
					onClick={onBack}
					data-manage-notifications-back=""
				>
					<ChevronLeft aria-hidden="true" />
					{t("settings.general")}
				</Button>
				<h2 className="truncate text-settings-heading font-semibold text-settings-title">{t("settings.notifications.manage")}</h2>
				{saveError ? <p role="alert" className="text-control text-error">{t("settings.notifications.saveFailed")}</p> : null}
			</div>
			<SettingsSection title={t("settings.notifications.types")} description={t("settings.notifications.typesDescription")} grouped>
				{NOTIFICATION_KINDS.map(({ id }) => {
					const copy = KIND_COPY[id];
					return (
						<SettingsRow key={id} icon={copy.icon} label={t(copy.label)} description={t(copy.description)} data-notification-kind={id}>
							<Switch
								aria-label={t(copy.label)}
								checked={!filters.mutedKinds.includes(id)}
								disabled={locked}
								onCheckedChange={(on) => {
									void setKindMuted(id, !on);
								}}
							/>
						</SettingsRow>
					);
				})}
			</SettingsSection>
			<SettingsSection title={t("settings.notifications.machines")} description={t("settings.notifications.machinesDescription")} grouped>
				{machines.map((machine) => (
					<SettingsRow key={machine.source} icon={machine.icon} label={machine.label} data-notification-source={machine.source}>
						<Switch
							aria-label={machine.label}
							checked={!filters.mutedSources.includes(machine.source)}
							disabled={locked}
							onCheckedChange={(on) => {
								void setSourceMuted(machine.source, !on);
							}}
						/>
					</SettingsRow>
				))}
			</SettingsSection>
		</div>
	);
}
