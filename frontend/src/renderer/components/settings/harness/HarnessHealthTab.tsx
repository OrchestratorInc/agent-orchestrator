import { Check, Copy, LoaderCircle, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { versionLabel } from "../../../hooks/useHarnessUpdates";
import { MiddleTruncatedText } from "../../MiddleTruncatedText";
import { Button } from "../../ui/button";
import type { HarnessDetails } from "./describe-harness";
import { detectedCredential } from "./harness-diagnostics";
import { HarnessDetailGroup, HarnessDetailRow, HarnessStateText, HarnessTimestamp, hasValidTimestamp, type HarnessTone } from "./harness-ui";

/** Read-only facts AO observed about one harness. */
export function HarnessHealthTab({ details, checking, authCheckFailed, copied, onCheckAgain, onCopyReport }: {
	details: HarnessDetails;
	checking: boolean;
	authCheckFailed: boolean;
	copied: boolean;
	onCheckAgain: () => void;
	onCopyReport: () => void;
}) {
	const { t } = useTranslation();
	const { readinessAgent, advisory, isInstalled } = details;
	const installation = readinessAgent?.installation;
	const installState: { tone: HarnessTone; label: string } = installation?.state === "installed" ? { tone: "ok", label: t("settings.harness.installed") }
		: installation?.state === "not_installed" ? { tone: "off", label: t("settings.harness.notInstalled") }
		: { tone: "off", label: t("settings.harness.installationUnknown") };
	// An expired login token the harness renews by itself is signed in, not a problem to fix.
	const renewsOnRun = details.authStatus === "configured" && Boolean(readinessAgent?.authentication.diagnostics?.some((step) => step.code === "token_renewal_pending"));
	const authTone: HarnessTone = authCheckFailed || readinessAgent?.authentication.freshness !== "fresh" ? "off"
		: renewsOnRun || details.authStatus === "authorized" || details.authStatus === "not_applicable" ? "ok"
		: details.authStatus === "unauthorized" ? "error" : "off";
	const authLabel = renewsOnRun ? t("settings.harness.health.authRenewsOnRun")
		: details.authStatus === "configured" ? t("settings.harness.health.authUnverified") : details.authSummary;
	// Only facts AO has observed: a row with nothing to report is left out rather than filled with a placeholder.
	const credential = detectedCredential(details, t);
	const executable = advisory?.binaryPath || details.job?.expectedDestination;
	const hasDetails = Boolean((isInstalled && details.currentVersion) || advisory?.latestVersion || details.maintenanceMethod);
	return <div className="flex flex-col gap-6">
		<HarnessDetailGroup title={t("settings.harness.health.status")}>
			<HarnessDetailRow label={t("settings.harness.installation")} description={installation?.state !== "installed" ? installation?.reason || undefined : undefined}>
				<HarnessStateText tone={installState.tone}>{installState.label}</HarnessStateText>
			</HarnessDetailRow>
			{isInstalled ? <HarnessDetailRow label={t("settings.harness.health.authentication")}>
				<HarnessStateText tone={authTone}>{authLabel}</HarnessStateText>
			</HarnessDetailRow> : null}
			{executable ? <HarnessDetailRow label={t("settings.harness.health.executable")}>
				<span className="min-w-0 break-all font-mono text-xs text-settings-label">{executable}</span>
			</HarnessDetailRow> : null}
			{isInstalled && credential ? <HarnessDetailRow label={t("settings.harness.health.detectedCredential")}>
				<span className="min-w-0 break-all font-mono text-xs text-settings-label">{credential}</span>
			</HarnessDetailRow> : null}
			{hasValidTimestamp(installation?.checkedAt) ? <HarnessDetailRow label={t("settings.harness.health.lastChecked")}>
				<span className="text-xs text-settings-label"><HarnessTimestamp value={installation?.checkedAt} />{installation?.freshness === "stale" ? ` · ${t("settings.harness.health.stale")}` : ""}</span>
			</HarnessDetailRow> : null}
		</HarnessDetailGroup>
		{hasDetails ? <HarnessDetailGroup title={t("settings.harness.health.details")}>
			{/* One version row: the installed release with how it compares to the latest. */}
			{isInstalled && details.currentVersion ? <HarnessDetailRow label={t(details.versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")}>
				<MiddleTruncatedText className="whitespace-nowrap font-mono text-xs text-settings-label" text={details.versionText} />
				{details.updateAvailable ? <MiddleTruncatedText className="whitespace-nowrap text-xs font-medium text-settings-accent" text={versionLabel(advisory!.latestVersion!)} format={(version) => t("settings.harness.versionAvailable", { version })} />
					: advisory?.latestVersion && !details.updateUnknown ? <span className="text-xs text-settings-muted">{t(advisory.status === "ahead_of_latest" ? "settings.harness.aheadOfLatest" : "settings.harness.latest")}</span> : null}
			</HarnessDetailRow> : null}
			{advisory?.latestVersion && !(isInstalled && details.currentVersion) ? <HarnessDetailRow label={t("settings.harness.health.latestVersion")}>
				<MiddleTruncatedText className="whitespace-nowrap font-mono text-xs text-settings-label" text={versionLabel(advisory.latestVersion)} />
				{details.updateAvailable ? <span className="whitespace-nowrap text-xs font-medium text-settings-accent">{t("settings.harness.updateAvailable")}</span> : null}
			</HarnessDetailRow> : null}
			{details.maintenanceMethod ? <HarnessDetailRow label={t("settings.harness.installMethod")}>
				<span className="text-xs text-settings-label">{details.maintenanceMethod.label}</span>
			</HarnessDetailRow> : null}
		</HarnessDetailGroup> : null}
		<div className="flex flex-wrap gap-2">
			<Button type="button" size="sm" variant="outline" disabled={checking || details.busy} onClick={onCheckAgain}>
				{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
				{t(checking ? "settings.harness.health.checking" : "settings.harness.health.checkAgain")}
			</Button>
			<Button type="button" size="sm" variant="ghost" onClick={onCopyReport}>
				{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copied ? "settings.harness.copied" : "settings.harness.copyDiagnostics")}
			</Button>
		</div>
		{authCheckFailed ? <p role="alert" className="text-xs text-error">{t("settings.harness.health.checkFailed")}</p> : null}
	</div>;
}
