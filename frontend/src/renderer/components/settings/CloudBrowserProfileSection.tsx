import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudBrowserProfile } from "../../hooks/useCloudBrowserProfile";
import { formatRelativeTime } from "../../lib/usage-format";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * The person's saved Cloud browser sign-ins: whether Cloud sessions start
 * signed in, and a wipe. Hidden when the control plane keeps none.
 */
export function CloudBrowserProfileSection() {
	const { t, i18n } = useTranslation();
	const { available, profile, wipe } = useCloudBrowserProfile();
	const [confirming, setConfirming] = useState(false);
	if (!available) return null;
	const saved = profile.data?.saved === true;
	const updated = saved && profile.data?.updatedAt
		? formatRelativeTime(profile.data.updatedAt, new Date(), i18n.language)
		: "";
	const description = profile.isLoading
		? t("settings.cloudBrowserProfile.loading")
		: profile.isError
			? t("settings.cloudBrowserProfile.loadFailed")
			: saved
				? updated ? t("settings.cloudBrowserProfile.savedUpdated", { time: updated }) : t("settings.cloudBrowserProfile.saved")
				: t("settings.cloudBrowserProfile.none");
	const running = wipe.data?.runningSessions ?? 0;
	return (
		<>
			<SettingsSection title={t("settings.cloudBrowserProfile.title")} grouped>
				<SettingsRow label={t("settings.cloudBrowserProfile.label")} description={description}>
					<Button
						disabled={!saved || wipe.isPending}
						onClick={() => setConfirming(true)}
						size="sm"
						type="button"
						variant="destructive-ghost"
					>
						{t("settings.cloudBrowserProfile.wipe")}
					</Button>
				</SettingsRow>
				{wipe.isSuccess && running > 0 ? (
					<div className="settings-row-bar text-sm text-settings-muted" role="status">
						{t("settings.cloudBrowserProfile.runningSessions", { count: running })}
					</div>
				) : null}
			</SettingsSection>
			<ConfirmDialog
				open={confirming}
				onOpenChange={(open) => {
					if (!wipe.isPending) setConfirming(open);
				}}
				title={t("settings.cloudBrowserProfile.confirmTitle")}
				description={t("settings.cloudBrowserProfile.confirmDescription")}
				confirmLabel={t("settings.cloudBrowserProfile.wipe")}
				destructive
				busy={wipe.isPending}
				error={wipe.isError ? t("settings.cloudBrowserProfile.wipeFailed") : undefined}
				onConfirm={() => {
					wipe.mutate(undefined, { onSuccess: () => setConfirming(false) });
				}}
			/>
		</>
	);
}
