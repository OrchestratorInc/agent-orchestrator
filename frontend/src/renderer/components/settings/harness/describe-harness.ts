import type { UseQueryResult } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import type { components } from "../../../../api/schema";
import type { AgentAuthPlan } from "../../../hooks/useAgentAuth";
import type { AgentReadinessSnapshot } from "../../../hooks/useAgentReadinessQuery";
import {
	hasConfirmedHarnessUpdate,
	hasDefinitiveUpdateStatus,
	isActiveInstallJob,
	isFailedInstallJob,
	maintenanceMethodId,
	needsHarnessLogin,
	versionLabel,
	type AgentUpdateAdvisory,
	type InstallJob,
} from "../../../hooks/useHarnessUpdates";
import type { AgentId } from "../../../lib/agent-options";
import type { AuthTerminalWorkflow } from "./HarnessAuthTerminalPanel";
import type { HarnessTone } from "./harness-ui";

type AgentInstallPlan = components["schemas"]["AgentInstallPlan"];
export type AgentInstallMethod = AgentInstallPlan["methods"][number];
export type AgentOperation = "install" | "reinstall" | "update";
export type OperationAttempt = { operation: AgentOperation; startedAt?: string };
export type AgentAuthState = { pending: boolean; error: string | null };

export function supportsUpdate(method: AgentInstallMethod | undefined): boolean {
	return Boolean(method?.available && method.updateAvailable);
}

export type HarnessDescribeInput = {
	agentId: AgentId;
	t: TFunction;
	language: string;
	plan?: AgentInstallPlan;
	job?: InstallJob;
	installed: boolean;
	/** The install method the user picked after a failure, if any. */
	selectedMethodId?: string;
	advisoryQuery?: Pick<UseQueryResult<AgentUpdateAdvisory>, "data" | "isError" | "isPending" | "isFetching">;
	pending: boolean;
	actionError?: string;
	attempt?: OperationAttempt;
	readinessAgent?: AgentReadinessSnapshot;
	readinessPending: boolean;
	readinessFailed: boolean;
	authPlan?: AgentAuthPlan;
	authPlansPending: boolean;
	authPlansFailed: boolean;
	authState?: AgentAuthState;
	/** The login or logout terminal, when it belongs to this harness. */
	authWorkflow: AuthTerminalWorkflow | null;
	updatedAt?: string;
};

/** Everything the list row and the harness page show for one harness. */
export function describeHarness(input: HarnessDescribeInput) {
	const { agentId, t, plan, job, readinessAgent, authPlan, authState, attempt, actionError, pending } = input;
	const isInstalled = input.installed;
	const availableMethods = plan?.methods.filter((method) => method.available) ?? [];
	// The recommended method unless the user picked another, so an install
	// that fails one way can be retried through another available method.
	const installMethod = availableMethods.find((method) => method.id === input.selectedMethodId)
		?? availableMethods.find((method) => method.recommended) ?? availableMethods[0];
	const advisoryQuery = input.advisoryQuery;
	const advisory = advisoryQuery?.data;
	const methodId = maintenanceMethodId(advisory);
	const maintenanceMethod = plan?.methods.find((method) => method.id === methodId);
	const canUpdate = supportsUpdate(maintenanceMethod);
	// A harness with no installable method (built into AO, or an installer
	// that must run interactively here) explains itself; ownership is not the question.
	const manualOnly = Boolean(plan?.methods.length) && plan!.methods.every((method) => method.id === "manual");
	const ownershipReason = !methodId ? (manualOnly && plan?.reason ? plan.reason : t("settings.harness.ownershipUnknown")) : undefined;
	const updateReason = ownershipReason || maintenanceMethod?.updateReason || maintenanceMethod?.reason || t("settings.harness.updateUnsupported");
	const updateUnknown = advisoryQuery?.isError || !hasDefinitiveUpdateStatus(advisory);
	// A failed background fetch is not a new observation. Keep the last
	// action; the click-time probe still verifies target and ownership.
	const updateAvailable = hasConfirmedHarnessUpdate(advisory);
	const currentVersion = advisory?.currentVersion?.trim();
	const busy = pending || isActiveInstallJob(job);
	const currentOperation = attempt && (pending || actionError || attempt.startedAt === job?.startedAt) ? attempt.operation : undefined;
	const jobFailed = isFailedInstallJob(job);
	const failed = jobFailed || Boolean(actionError);
	const availableMethodsLabel = availableMethods.length > 0
		? new Intl.ListFormat(input.language, { style: "short", type: "conjunction" }).format(availableMethods.map((method) => method.label))
		: undefined;
	// Unknown inventory is not proof that installing is safe. Preserve the
	// existing cached-readiness fallback when fetching the snapshot fails.
	const installationPending = !input.readinessFailed && (input.readinessPending || readinessAgent?.installation.state === "unknown");
	const isSetupAction = authPlan?.action === "setup";
	const isDocumentationAction = isSetupAction && authPlan?.launchMode === "documentation";
	const authStatus = readinessAgent?.authentication.state;
	// Who the harness is signed in as, when its own login check names it.
	const account = authStatus === "authorized" || authStatus === "configured" ? readinessAgent?.authentication.account : undefined;
	const accountWho = account?.email || account?.name;
	// Who and how the harness is signed in. Where the credential was found
	// stays in the readiness data for diagnostics; the row does not show it.
	const accountDescription = account ? [
		accountWho ? t("settings.harness.signedInAs", { account: accountWho })
			: account.method === "api_key" ? t("settings.harness.usingApiKey")
			: account.method === "token" ? t("settings.harness.usingToken")
			: account.method === "account" ? t("settings.harness.signedInWithAccount") : undefined,
		account.plan ? account.plan.charAt(0).toUpperCase() + account.plan.slice(1) : undefined,
		account.organization,
		account.provider,
	].filter(Boolean).join(" · ") || undefined : undefined;
	const rowAuthWorkflow = input.authWorkflow;
	const authBusy = Boolean(rowAuthWorkflow || authState?.pending);
	const needsLogin = needsHarnessLogin(authPlan, authStatus);
	const showUpdate = updateAvailable && !input.authPlansPending && !input.authPlansFailed;
	// The daemon retains this component-repair instruction in persisted output,
	// including after CLI verification succeeds or AO restarts.
	const autohandRepair = "Computer control needs a separate repair: autohand computer install";
	const installWarning = agentId === "autohand" && job?.status === "succeeded" && job.output?.includes(autohandRepair) ? autohandRepair : undefined;
	const hasDiagnostics = Boolean(job && (jobFailed || installWarning) && (job.error || job.output || job.method || job.expectedDestination));
	const unverifiedProvider = readinessAgent?.authentication.diagnostics?.some((step) => step.method === "provider_validation"
		&& ["provider_unsupported", "provider_inconclusive", "provider_unreachable", "probe_timeout", "probe_cancelled"].includes(step.code))
		|| readinessAgent?.authentication.diagnostics?.some((step) => step.code === "model_selection_unresolved");
	const renewsOnRun = authStatus === "configured" && Boolean(readinessAgent?.authentication.diagnostics?.some((step) => step.code === "token_renewal_pending"));
	const authSummary = renewsOnRun ? t("settings.harness.loggedIn")
		: authStatus === "configured" ? t(unverifiedProvider ? "settings.harness.health.authUnverified" : "settings.harness.installed")
		: authStatus === "authorized" ? t("settings.harness.loggedIn")
		: authStatus === "not_applicable" || (!input.authPlansPending && (!authPlan || authPlan.action === "instructions")) ? t("settings.harness.installed")
		: authPlan && !authPlan.available ? (authPlan.reason ?? t("settings.harness.authFailed"))
		: authStatus === "unauthorized" ? t(isSetupAction ? "settings.harness.notConfigured" : "settings.harness.loginRequired")
		: t(isSetupAction ? "settings.harness.configurationUnknown" : "settings.harness.loginUnknown");
	const progressLabel = job?.status === "waiting_for_idle" ? t("settings.harness.waitingForIdle")
		: job?.status === "verifying" ? t("settings.harness.verifying")
		: currentOperation === "update" ? t("settings.harness.updating")
		: currentOperation === "install" ? t("settings.harness.installing") : t("settings.harness.working");
	const rowError = actionError ?? authState?.error ?? (jobFailed ? job?.error ?? t("settings.harness.installFailed") : undefined);
	const listTone: HarnessTone = !isInstalled ? (rowError ? "error" : "off")
		: rowError || needsLogin ? "error" : "ok";
	const authProgress = rowAuthWorkflow?.phase === "verifying" ? t(rowAuthWorkflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin")
		: rowAuthWorkflow?.phase === "closing" ? t("settings.harness.authClosing")
		: rowAuthWorkflow && rowAuthWorkflow.phase !== "running" ? rowAuthWorkflow.reason ?? t("settings.harness.loginUnknown")
		: t(rowAuthWorkflow?.action === "logout" ? "settings.harness.logoutInProgress" : "settings.harness.loginInProgress");
	// Why a harness is not installed (or cannot be), without repeating the error alert.
	const installNote = isInstalled || installationPending ? undefined
		: job?.status === "interrupted" ? t("settings.harness.interrupted")
		: installMethod && availableMethodsLabel ? t("settings.harness.availableWith", { method: availableMethodsLabel }) : plan?.reason ?? t("settings.harness.manualRequired");
	const statusLabel = isInstalled ? rowError ?? (authBusy ? authProgress : authSummary)
		: installationPending ? t("settings.harness.installationUnknown")
		: job?.status === "interrupted" ? t("settings.harness.interrupted")
		: rowError ?? installNote;
	const versionText = currentVersion ? versionLabel(currentVersion) : t("settings.harness.versionUnavailable");
	const versionUnverified = Boolean(advisoryQuery?.isError || job?.status === "verifying" || (currentOperation === "update" && (busy || failed)));
	const canRelogin = Boolean(authPlan?.available && authPlan.action !== "instructions" && (authStatus === "authorized" || authStatus === "configured"));
	return {
		agentId, plan, job, isInstalled, availableMethods, installMethod, methodId, maintenanceMethod, canUpdate, manualOnly, updateReason,
		advisoryQuery, advisory, updateUnknown, updateAvailable, currentVersion, busy, failed, jobFailed, currentOperation, readinessAgent,
		authPlan, authState, authStatus, authSummary, renewsOnRun, unverifiedProvider, accountDescription, rowAuthWorkflow, authBusy, needsLogin, isSetupAction, isDocumentationAction, canRelogin,
		showUpdate, hasDiagnostics, installWarning, rowError, versionText, versionUnverified, updatedAt: input.updatedAt, progressLabel, installationPending, installNote, listTone, statusLabel,
	};
}

export type HarnessDetails = ReturnType<typeof describeHarness>;
