import { BookOpen, Check, Copy, ExternalLink, LoaderCircle, LogOut } from "@/components/app-icons";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountProviderFor } from "../../../hooks/useAgentAccounts";
import { advisoryInUse, isWaitingForIdleJob, versionLabel, type HarnessInUse, type InstallJob } from "../../../hooks/useHarnessUpdates";
import { IdleUpdateStatus } from "./HarnessSessionsDialog";
import type { TerminalSessionState } from "../../../hooks/useTerminalSession";
import { aoBridge } from "../../../lib/bridge";
import { labelForHost } from "../../../lib/host-clients";
import { MiddleTruncatedText } from "../../MiddleTruncatedText";
import { Button } from "../../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { AgentAccountsPanel } from "../AgentAccountsSection";
import { SettingsOptionMenu } from "../SettingsOptionMenu";
import { installStage, showsInstallProgress, type HarnessDetails } from "./describe-harness";
import { HarnessAuthTerminalPanel } from "./HarnessAuthTerminalPanel";
import { HarnessCloudLine, type HarnessCloudState } from "./HarnessCloudLine";
import { HarnessInstallProgress, useInstallJustFinished } from "./HarnessInstallProgress";
import { HarnessDetailGroup, HarnessDetailRow, HarnessTimestamp } from "./harness-ui";

/** Install, update and login each own one row. While a job or login runs,
	* the row that started it shows progress instead. */
export function HarnessAccountTab({ details, hostId, cloud, authWorkflowActive, loginPanel, copied, diagnosticsExpanded, onToggleDiagnostics, onCopyDiagnostics, onInstall, onSelectMethod, onCopyCommand, onUpdate, onCloudUpdate, onCloudReset, onLogin, onLogout, onVerify, onCloseAuth, onRetryAuth, onTerminalState, onViewSessions }: {
	details: HarnessDetails;
	hostId?: string;
	/** AO Cloud's side of this harness, when AO Cloud runs it: a second line of the installation row. */
	cloud?: HarnessCloudState;
	/** A login or logout terminal is open for any harness. */
	authWorkflowActive: boolean;
	loginPanel?: ReactNode;
	copied: boolean;
	diagnosticsExpanded: boolean;
	onToggleDiagnostics: () => void;
	onCopyDiagnostics: (job: InstallJob) => void;
	onInstall: (methodId: string) => void;
	onSelectMethod: (methodId: string) => void;
	onCopyCommand: (command: string) => void;
	/** Updates every place that has an update: this host and, for an admin, AO Cloud. */
	onUpdate: () => void;
	/** Updates AO Cloud alone (the harness is not installed here). */
	onCloudUpdate?: () => void;
	/** Has AO Cloud run the AO-tested release again. */
	onCloudReset?: () => void;
	onLogin: () => void;
	onLogout: () => void;
	onVerify: () => void;
	onCloseAuth: () => void;
	onRetryAuth: () => void;
	onTerminalState: (state: TerminalSessionState) => void;
	/** Lists sessions an update waits for. */
	onViewSessions?: (inUse: HarnessInUse) => void;
}) {
	const { t } = useTranslation();
	const { plan, job, isInstalled, authPlan, rowAuthWorkflow, authBusy, installMethod, availableMethods, failed } = details;
	// Sessions that must end first turn Update into a way to find them.
	const blockedBy = advisoryInUse(details.advisory);
	const updateBlocked = Boolean(blockedBy);
	// Subscription accounts are for local Claude Code and Codex.
	const accountProvider = !hostId && isInstalled ? agentAccountProviderFor(details.agentId) : null;
	// The running job's steps stay in the Installation card until it finishes on Done.
	const justFinished = useInstallJustFinished(job);
	const stage = justFinished ? "done" : installStage(job);
	// Progress reads as a solid state, not a greyed-out action.
	const progress = (label: string) => <span role="status"><Button type="button" size="default" variant="secondary" className="min-w-20 disabled:opacity-100" aria-busy="true" disabled><LoaderCircle className="animate-spin" aria-hidden="true" />{label}</Button></span>;
	const progressAction = progress(details.progressLabel);
	// One Update covers every place with an update: this host, and AO Cloud
	// for someone who may change it. Which places it covers shows on hover.
	const localUpdate = details.showUpdate && !details.needsLogin && details.canUpdate;
	const cloudUpdate = Boolean(cloud?.details?.actionable) && !cloud?.pending;
	const updateButton = <Button type="button" data-harness-primary-action="" size="default" variant={updateBlocked && !cloudUpdate ? "secondary" : undefined} className="min-w-20" onClick={onUpdate}>
		{blockedBy && !cloudUpdate ? t("settings.harness.viewSessions", { count: blockedBy.count })
			: t(failed && details.currentOperation === "update" ? "settings.harness.retryUpdate" : "settings.harness.update")}
	</Button>;

	const authBusyAction = <Button type="button" size="default" className="min-w-20" disabled>
		{rowAuthWorkflow && !["running", "verifying", "closing"].includes(rowAuthWorkflow.phase) ? null : <LoaderCircle className="animate-spin" aria-hidden="true" />}
		{rowAuthWorkflow?.phase === "verifying" ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin") : rowAuthWorkflow?.action === "logout" ? t("settings.harness.logout") : details.isSetupAction ? t("settings.harness.setup") : t("settings.harness.login")}
	</Button>;
	const installationAction = details.busy ? progressAction
		: isInstalled && cloud?.pending ? progress(t("settings.harness.updating"))
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
				<Button type="button" data-harness-primary-action="" size="default" className="min-w-20" onClick={() => onInstall(installMethod.id)}>
					{failed ? t("settings.harness.retryInstall") : t("settings.harness.install")}
				</Button>
			</span> : !details.installationPending && plan?.command ? <Button type="button" size="default" variant="secondary" className="min-w-20" onClick={() => onCopyCommand(plan.command!)}>
				{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}{t(copied ? "settings.harness.copied" : "settings.harness.copyCommand")}
			</Button> : !details.installationPending && plan?.documentationUrl ? <Button type="button" data-harness-primary-action="" size="default" variant="secondary" className="min-w-20" onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>
				<ExternalLink aria-hidden="true" />{t("settings.harness.installGuide")}
			</Button> : null
		) : cloudUpdate ? <Tooltip>
			<TooltipTrigger asChild>{updateButton}</TooltipTrigger>
			<TooltipContent>{!localUpdate ? t("settings.harness.cloud.updatesCloud")
				: hostId ? t("settings.harness.cloud.updatesBoth", { host: labelForHost(hostId) ?? hostId }) : t("settings.harness.cloud.updatesBothLocal")}</TooltipContent>
		</Tooltip>
		: localUpdate ? updateButton
		: details.showUpdate && !details.needsLogin && plan?.documentationUrl ? <Button type="button" data-harness-primary-action="" size="default" variant="secondary" className="min-w-20" onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button>
		: null;
	const accountAction = authBusy ? authBusyAction
		: details.busy ? null
		: details.needsLogin || details.showSetup ? <Button type="button" data-harness-primary-action="" data-terminal-focus-handoff="true" size="default" className="min-w-20" disabled={!authPlan?.available || authWorkflowActive} onClick={onLogin}>
			{t(details.isDocumentationAction ? "settings.harness.viewDocumentation" : details.isSetupAction ? "settings.harness.setup" : details.authState?.error ? "settings.harness.retryLogin" : "settings.harness.login")}
		</Button>
		: details.canRelogin ? <Button type="button" data-terminal-focus-handoff="true" size="default" variant="secondary" className="min-w-20" disabled={authWorkflowActive} onClick={onLogin}>
			{t(details.isDocumentationAction ? "settings.harness.viewDocumentation" : details.isSetupAction ? "settings.harness.openSetup" : "settings.harness.loginAgain")}
		</Button> : null;
	return <div className="flex flex-col gap-6">
		<HarnessDetailGroup title={t("settings.harness.installation")}>
			<HarnessDetailRow
				label={isInstalled ? t(details.versionUnverified ? "settings.harness.lastObservedVersion" : "settings.harness.installedVersion")
					: details.installationPending ? t("settings.harness.installationUnknown") : t("settings.harness.notInstalled")}
				description={isInstalled ? (details.maintenanceMethod?.label ? t("settings.harness.installedWith", { method: details.maintenanceMethod.label }) : undefined) : details.installNote}
				secondary={cloud ? <HarnessCloudLine state={cloud} standaloneUpdate={!isInstalled} onUpdate={() => onCloudUpdate?.()} onReset={() => onCloudReset?.()} /> : undefined}
			>
				{isInstalled ? <span className="break-all font-mono text-xs text-settings-label">{details.currentVersion ? <MiddleTruncatedText className="whitespace-nowrap" text={details.versionText} /> : details.versionText}{details.versionUnverified && details.currentVersion ? ` · ${t("settings.harness.unverified")}` : ""}</span> : null}
				{isInstalled && details.updateAvailable ? <MiddleTruncatedText className="whitespace-nowrap text-xs font-medium text-settings-accent" text={versionLabel(details.advisory!.latestVersion!)} format={(version) => t("settings.harness.versionAvailable", { version })} /> : null}
				{isInstalled && !details.updateUnknown && !details.updateAvailable && !details.busy ? <span className="text-xs text-settings-muted">{t(details.advisory?.status === "ahead_of_latest" ? "settings.harness.aheadOfLatest" : "settings.harness.latest")}</span> : null}
				{installationAction}
			</HarnessDetailRow>
			{job && (justFinished || showsInstallProgress(job)) ? <HarnessInstallProgress job={job} stage={stage} label={justFinished ? t("settings.harness.progress.done") : details.progressLabel} /> : null}
			{isInstalled && details.updatedAt ? <HarnessDetailRow label={t("settings.harness.lastUpdated")}>
				<span className="text-xs text-settings-label"><HarnessTimestamp value={details.updatedAt} /></span>
			</HarnessDetailRow> : null}
		</HarnessDetailGroup>
		{job && isWaitingForIdleJob(job) ? <div className="-mt-3"><IdleUpdateStatus agentId={details.agentId} job={job} hostId={hostId} onViewSessions={(waiting) => onViewSessions?.(waiting)} /></div> : null}
		{isInstalled && details.showUpdate && !details.canUpdate ? <p className="-mt-3 text-xs text-settings-muted">{details.updateReason}</p> : null}
		{isInstalled && details.updateUnknown && !details.advisoryQuery?.isPending && !details.manualOnly ? <p className="-mt-3 text-xs text-settings-muted">{t("settings.harness.updateCheckUnavailable")}</p> : null}
		{/* A harness with no installable method explains how it is maintained instead. */}
		{isInstalled && details.manualOnly && plan?.reason && !(details.showUpdate && !details.canUpdate) ? <p className="-mt-3 text-xs text-settings-muted">{plan.reason}</p> : null}

		{/* With accounts listed, the CLI's own login is a row there, not a card of its own. */}
		{(isInstalled || rowAuthWorkflow) && !accountProvider ? <HarnessDetailGroup title={t("settings.harness.account")}>
			{!loginPanel && <HarnessDetailRow
				label={authBusy && rowAuthWorkflow ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.logoutInProgress" : "settings.harness.loginInProgress") : details.authSummary}
				description={authBusy ? undefined : details.accountDescription}
			>
				{accountAction}
				{isInstalled && authPlan?.logoutCommand && authPlan.available && details.authStatus !== "unauthorized" ? <Button type="button" size="sm" variant="secondary" disabled={details.busy || authWorkflowActive} onClick={onLogout}><LogOut aria-hidden="true" />{t("settings.harness.logout")}</Button> : null}
			</HarnessDetailRow>}
			{loginPanel}
		</HarnessDetailGroup> : null}
		{accountProvider ? <AgentAccountsPanel provider={accountProvider} cliLogin={{
			disabled: authBusy || authWorkflowActive || details.busy || !authPlan?.available || authPlan.action === "instructions",
			canLogout: Boolean(authPlan?.logoutCommand),
			onSignIn: onLogin,
			onLogout,
		}} /> : null}
		{accountProvider && loginPanel ? <HarnessDetailGroup title={t("settings.harness.account")}>{loginPanel}</HarnessDetailGroup> : null}
		{authPlan?.action === "instructions" && authPlan.documentationUrl ? <div><Button type="button" size="sm" variant="secondary" onClick={() => void aoBridge.app.openExternal(authPlan.documentationUrl)}><BookOpen aria-hidden="true" />{t("settings.harness.instructions")}</Button></div> : null}
		{rowAuthWorkflow ? <HarnessAuthTerminalPanel
			workflow={rowAuthWorkflow}
			hostId={hostId}
			onClose={onCloseAuth}
			onRetry={onRetryAuth}
			onTerminalState={onTerminalState}
		/> : null}

		{details.rowError ? <p role="alert" className="text-xs text-error">{details.rowError}</p> : null}
		{details.installWarning ? <p role="status" className="text-xs text-settings-label">{details.installWarning}</p> : null}
		{details.jobFailed && !authBusy ? <div><Button type="button" size="sm" variant="secondary" disabled={details.busy} onClick={onVerify}>{t("settings.harness.verifyAgain")}</Button></div> : null}
		{details.hasDiagnostics ? <div className="space-y-2">
			<Button type="button" aria-expanded={diagnosticsExpanded} size="sm" variant="ghost" onClick={onToggleDiagnostics}>
				{t(diagnosticsExpanded ? "settings.harness.hideDiagnostics" : "settings.harness.showDiagnostics")}
			</Button>
			{diagnosticsExpanded ? <div data-harness-diagnostics="" className="rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) p-3 text-xs text-settings-muted">
				{job?.method ? <p><span className="font-medium text-settings-label">{t("settings.harness.method")}:</span> {job.method}</p> : null}
				{job?.expectedDestination ? <p className="break-all"><span className="font-medium text-settings-label">{t("settings.harness.expectedDestination")}:</span> {job.expectedDestination}</p> : null}
				{job?.error ? <p className="mt-2 whitespace-pre-wrap text-error">{job.error}</p> : null}
				{job?.output ? <pre className="settings-thin-scrollbar mt-2 max-h-40 overflow-auto overscroll-contain whitespace-pre-wrap break-words font-mono">{job.output}</pre> : null}
				<Button type="button" data-harness-copy-diagnostics="" className="mt-2" size="sm" variant="secondary" onClick={() => job && onCopyDiagnostics(job)}><Copy aria-hidden="true" />{t("settings.harness.copyDiagnostics")}</Button>
			</div> : null}
		</div> : null}
	</div>;
}
