import { LoaderCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { versionLabel } from "../../../hooks/useHarnessUpdates";
import { Button } from "../../ui/button";
import type { HarnessDetails } from "./describe-harness";
import { HarnessStatusDot } from "./harness-ui";

/** The agent in one line above its accounts: whether it runs, which release, and the one action that matters now.
 * Details opens everything about its installation and health. */
export function HarnessStatusStrip({ details, onInstall, onUpdate, onDetails }: {
	details: HarnessDetails;
	onInstall: (methodId: string) => void;
	onUpdate: () => void;
	onDetails: () => void;
}) {
	const { t } = useTranslation();
	const { isInstalled, busy, installMethod, managed } = details;
	// The accounts below say how the agent signs in, so the line only speaks of its login when nothing else will.
	const label = !isInstalled ? details.statusLabel : details.rowError ?? (details.needsLogin && !managed ? details.authSummary : t("settings.harness.installed"));
	return (
		<div data-testid="harness-status-strip" className="flex min-h-10 shrink-0 flex-wrap items-center gap-x-3.5 gap-y-1 rounded-lg bg-(--color-bg-secondary) py-1 pl-3.5 pr-1 text-[13px] text-settings-muted">
			<span className="flex items-center gap-2 text-settings-label"><HarnessStatusDot tone={details.listTone} />{label}</span>
			{isInstalled && details.currentVersion ? <span className="font-mono text-xs">{details.versionText}</span> : null}
			{isInstalled && details.maintenanceMethod?.label ? <span>{details.maintenanceMethod.label}</span> : null}
			{isInstalled && details.updateAvailable ? <span className="text-settings-accent">{t("settings.harness.versionAvailable", { version: versionLabel(details.advisory!.latestVersion!) })}</span> : null}
			<span className="flex-1" />
			{busy ? <span role="status" className="flex items-center gap-1.5 px-2"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{details.progressLabel}</span>
				: !isInstalled && installMethod && !details.installationPending ? <Button type="button" data-harness-primary-action="" size="sm" onClick={() => onInstall(installMethod.id)}>{t(details.failed ? "settings.harness.retryInstall" : "settings.harness.install")}</Button>
				: isInstalled && details.showUpdate && details.canUpdate && !details.needsLogin && !details.authBusy ? <Button type="button" data-harness-primary-action="" size="sm" variant="secondary" onClick={onUpdate}>{t("settings.harness.update")}</Button>
				: null}
			<Button type="button" size="sm" variant="ghost" className="text-settings-muted" onClick={onDetails}>{t("settings.harness.health.details")}</Button>
		</div>
	);
}
