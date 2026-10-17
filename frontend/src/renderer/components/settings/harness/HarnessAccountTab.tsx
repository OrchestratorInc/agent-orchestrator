import { BookOpen, Check, Copy, ExternalLink, LoaderCircle, LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";
import { versionLabel, type InstallJob } from "../../../hooks/useHarnessUpdates";
import type { TerminalSessionState } from "../../../hooks/useTerminalSession";
import { aoBridge } from "../../../lib/bridge";
import { cn } from "../../../lib/utils";
import { MiddleTruncatedText } from "../../MiddleTruncatedText";
import { Button } from "../../ui/button";
import { SettingsOptionMenu } from "../SettingsOptionMenu";
import type { HarnessDetails } from "./describe-harness";
import { HarnessAuthTerminalPanel } from "./HarnessAuthTerminalPanel";
import { HarnessDetailGroup, HarnessDetailRow, HarnessTimestamp } from "./harness-ui";

const ACTION_CLASS = "h-8 min-w-20 px-3 focus-visible:ring-2 focus-visible:ring-ring";

/** Install, update and login each own one row. While a job or login runs,
	* the row that started it shows progress instead. */
export function HarnessAccountTab({ details, hostId, authWorkflowActive, copied, diagnosticsExpanded, onToggleDiagnostics, onCopyDiagnostics, onInstall, onSelectMethod, onCopyCommand, onUpdate, onLogin, onLogout, onVerify, onCloseAuth, onRetryAuth, onTerminalState }: {
	details: HarnessDetails;
	hostId?: string;
	/** A login or logout terminal is open for any harness. */
	authWorkflowActive: boolean;
	copied: boolean;
	diagnosticsExpanded: boolean;
	onToggleDiagnostics: () => void;
	onCopyDiagnostics: (job: InstallJob) => void;
	onInstall: (methodId: string) => void;
	onSelectMethod: (methodId: string) => void;
	onCopyCommand: (command: string) => void;
	onUpdate: () => void;
	onLogin: () => void;
	onLogout: () => void;
	onVerify: () => void;
	onCloseAuth: () => void;
	onRetryAuth: () => void;
	onTerminalState: (state: TerminalSessionState) => void;
}) {
	const { t } = useTranslation();
	const { plan, job, isInstalled, authPlan, rowAuthWorkflow, authBusy, installMethod, availableMethods, failed } = details;
	// Progress reads as a solid state, not a greyed-out action.
	const progressAction = <span role="status"><Button type="button" size="sm" variant="secondary" className={cn(ACTION_CLASS, "disabled:opacity-100")} aria-busy="true" disabled><LoaderCircle className="animate-spin" aria-hidden="true" />{details.progressLabel}</Button></span>;
	const authBusyAction = <Button type="button" size="sm" className={ACTION_CLASS} disabled>
		{rowAuthWorkflow && !["running", "verifying", "closing"].includes(rowAuthWorkflow.phase) ? null : <LoaderCircle className="animate-spin" aria-hidden="true" />}
		{rowAuthWorkflow?.phase === "verifying" ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin") : rowAuthWorkflow?.action === "logout" ? t("settings.harness.logout") : details.isSetupAction ? t("settings.harness.setup") : t("settings.harness.login")}
	</Button>;
	const installationAction = details.busy ? progressAction
		: authBusy ? null
		: !isInstalled ? (
			!details.installationPending && installMethod ? <span className="flex items-center gap-2">
				{/* One click installs the recommended method; after a failure the
				    user can retry through another available method. */}
				{failed && availableMethods.length > 1 ? <SettingsOptionMenu
					aria-label={t("settings.harness.installMethod")}
					value={installMethod.id}
					options={availableMethods.map((method) => ({ value: method.id, label: method.label }))}
					onChange={onSelectMethod}
					triggerClassName="h-8 w-fit"
				/> : null}
				<Button type="button" data-harness-primary-action="" size="sm" className={ACTION_CLASS} onClick={() => onInstall(installMethod.id)}>
					{failed ? t("settings.harness.retryInstall") : t("settings.harness.install")}
				</Button>
			</span> : !details.installationPending && plan?.command ? <Button type="button" size="sm" variant="outline" className={ACTION_CLASS} onClick={() => onCopyCommand(plan.command!)}>
				{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copied ? "settings.harness.copied" : "settings.harness.copyCommand")}
			</Button> : !details.installationPending && plan?.documentationUrl ? <Button type="button" data-harness-primary-action="" size="sm" variant="outline" className={ACTION_CLASS} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>
				<ExternalLink aria-hidden="true" />{t("settings.harness.installGuide")}
			</Button> : null
		) : details.showUpdate && !details.needsLogin ? (
			details.canUpdate ? <Button type="button" data-harness-primary-action="" size="sm" className={ACTION_CLASS} onClick={onUpdate}>
				{t(failed && details.currentOperation === "update" ? "settings.harness.retryUpdate" : "settings.harness.update")}
			</Button> : plan?.documentationUrl ? <Button type="button" data-harness-primary-action="" size="sm" variant="outline" className={ACTION_CLASS} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button> : null
		) : null;
	const accountAction = authBusy ? authBusyAction
		: details.busy ? null
		: details.needsLogin ? <Button type="button" data-harness-primary-action="" data-terminal-focus-handoff="true" size="sm" className={ACTION_CLASS} disabled={!authPlan?.available || authWorkflowActive} onClick={onLogin}>
			{t(details.isDocumentationAction ? "settings.harness.viewDocumentation" : details.isSetupAction ? "settings.harness.setup" : details.authState?.error ? "settings.harness.retryLogin" : "settings.harness.login")}
		</Button>
		: details.canRelogin ? <Button type="button" data-terminal-focus-handoff="true" size="sm" variant="outline" className={ACTION_CLASS} disabled={authWorkflowActive} onClick={onLogin}>
			{t(details.isDocumentationAction ? "settings.harness.viewDocumentation" : details.isSetupAction ? "settings.harness.openSetup" : "settings.harness.loginAgain")}
		</Button> : null;
	return <div className="flex flex-col gap-6">
		<HarnessDetailGroup title={t("settings.harness.installation")}>
			<HarnessDetailRow
				label={isInstalled ? t(details.versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")
					: details.installationPending ? t("settings.harness.installationUnknown") : t("settings.harness.notInstalled")}
				description={isInstalled ? (details.maintenanceMethod?.label ? t("settings.harness.installedWith", { method: details.maintenanceMethod.label }) : undefined) : details.installNote}
			>
				{isInstalled ? <span className="break-all font-mono text-xs text-settings-label">{details.currentVersion ? <MiddleTruncatedText className="whitespace-nowrap" text={details.versionText} /> : details.versionText}{details.versionUnverified && details.currentVersion ? ` · ${t("settings.harness.unverified")}` : ""}</span> : null}
				{isInstalled && details.updateAvailable ? <MiddleTruncatedText className="whitespace-nowrap text-xs font-medium text-settings-accent" text={versionLabel(details.advisory!.latestVersion!)} format={(version) => t("settings.harness.versionAvailable", { version })} /> : null}
				{isInstalled && !details.updateUnknown && !details.updateAvailable && !details.busy ? <span className="text-xs text-settings-muted">{t(details.advisory?.status === "ahead_of_latest" ? "settings.harness.aheadOfLatest" : "settings.harness.latest")}</span> : null}
				{installationAction}
			</HarnessDetailRow>
			{isInstalled && details.updatedAt ? <HarnessDetailRow label={t("settings.harness.lastUpdated")}>
				<span className="text-xs text-settings-label"><HarnessTimestamp value={details.updatedAt} /></span>
			</HarnessDetailRow> : null}
		</HarnessDetailGroup>
		{isInstalled && details.showUpdate && !details.canUpdate ? <p className="-mt-3 text-xs text-settings-muted">{details.updateReason}</p> : null}
		{isInstalled && details.updateUnknown && !details.advisoryQuery?.isPending && !details.manualOnly ? <p className="-mt-3 text-xs text-settings-muted">{t("settings.harness.updateCheckUnavailable")}</p> : null}
		{/* A harness with no installable method explains how it is maintained instead. */}
		{isInstalled && details.manualOnly && plan?.reason && !(details.showUpdate && !details.canUpdate) ? <p className="-mt-3 text-xs text-settings-muted">{plan.reason}</p> : null}

		{isInstalled || rowAuthWorkflow ? <HarnessDetailGroup title={t("settings.harness.account")}>
			<HarnessDetailRow
				label={authBusy && rowAuthWorkflow ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.logoutInProgress" : "settings.harness.loginInProgress") : details.authSummary}
				description={authBusy ? undefined : details.accountDescription}
			>
				{accountAction}
				{isInstalled && authPlan?.logoutCommand && authPlan.available && details.authStatus !== "unauthorized" ? <Button type="button" size="sm" variant="outline" disabled={details.busy || authWorkflowActive} onClick={onLogout}><LogOut aria-hidden="true" />{t("settings.harness.logout")}</Button> : null}
			</HarnessDetailRow>
		</HarnessDetailGroup> : null}
		{authPlan?.action === "instructions" && authPlan.documentationUrl ? <div><Button type="button" size="sm" variant="outline" onClick={() => void aoBridge.app.openExternal(authPlan.documentationUrl)}><BookOpen aria-hidden="true" />{t("settings.harness.instructions")}</Button></div> : null}
		{rowAuthWorkflow ? <HarnessAuthTerminalPanel
			workflow={rowAuthWorkflow}
			hostId={hostId}
			onClose={onCloseAuth}
			onRetry={onRetryAuth}
			onTerminalState={onTerminalState}
		/> : null}

		{details.rowError ? <p role="alert" className="text-xs text-error">{details.rowError}</p> : null}
		{details.installWarning ? <p role="status" className="text-xs text-settings-label">{details.installWarning}</p> : null}
		{details.jobFailed && !authBusy ? <div><Button type="button" size="sm" variant="outline" disabled={details.busy} onClick={onVerify}>{t("settings.harness.verifyAgain")}</Button></div> : null}
		{details.hasDiagnostics ? <div className="space-y-2">
			<Button type="button" aria-expanded={diagnosticsExpanded} size="sm" variant="ghost" onClick={onToggleDiagnostics}>
				{t(diagnosticsExpanded ? "settings.harness.hideDiagnostics" : "settings.harness.showDiagnostics")}
			</Button>
			{diagnosticsExpanded ? <div data-harness-diagnostics="" className="rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) p-3 text-xs text-settings-muted">
				{job?.method ? <p><span className="font-medium text-settings-label">{t("settings.harness.method")}:</span> {job.method}</p> : null}
				{job?.expectedDestination ? <p className="break-all"><span className="font-medium text-settings-label">{t("settings.harness.expectedDestination")}:</span> {job.expectedDestination}</p> : null}
				{job?.error ? <p className="mt-2 whitespace-pre-wrap text-error">{job.error}</p> : null}
				{job?.output ? <pre className="settings-thin-scrollbar mt-2 max-h-40 overflow-auto overscroll-contain whitespace-pre-wrap break-words font-mono">{job.output}</pre> : null}
				<Button type="button" data-harness-copy-diagnostics="" className="mt-2" size="sm" variant="outline" onClick={() => job && onCopyDiagnostics(job)}><Copy aria-hidden="true" />{t("settings.harness.copyDiagnostics")}</Button>
			</div> : null}
		</div> : null}
	</div>;
}
