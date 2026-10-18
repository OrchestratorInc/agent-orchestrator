import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ProjectSettingsSection } from "@aoagents/product-ui";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useEffect, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { useRemoteProjectQuery, workspaceQueryKeyForHost, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { useSettings } from "../hooks/useSettings";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost, clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { GatewayProvidersSection, gatewayConfigQueryKey } from "./settings/GatewayProvidersSection";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { PromptOverrideDialog } from "./settings/PromptOverrideDialog";
import { isLaunchableAgent } from "../lib/agent-select-options";
import { WORKER_DEFAULT_REVIEWERS } from "../lib/reviewer-harnesses";
import { captureOrchestratorReplacementFailure } from "../lib/orchestrator-replacement-telemetry";
import { OrchestratorSpawnError, spawnOrchestrator } from "../lib/spawn-orchestrator";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { captureRendererEvent } from "../lib/telemetry";
import { type OrchestratorReplacementFailure, useUiStore } from "../stores/ui-store";
import { newestActiveOrchestrator } from "../types/workspace";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import { buildIntake } from "./IntakeFields";
import { ReviewerSelect, reviewerTrustWarning } from "./ReviewerSelect";
import { ProjectSettingsEditor, type ProjectAgentPickerProps, type ProjectProviderPickerProps, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { CloudProjectSettingsAdapter } from "./CloudProjectSettingsForm";

type Project = components["schemas"]["Project"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type TrackerIntakeConfig = components["schemas"]["TrackerIntakeConfig"];

const DEFAULT_BRANCH_AUTO = "auto";

const projectQueryKey = (id: string, hostId?: string) => hostId ? ["project", hostId, id] as const : ["project", id] as const;

type SettingsSaveResult = {
	savedKey: string;
	replacementError: string | null;
	replacementSessionId: string | null;
	replacementFailure: OrchestratorReplacementFailure | null;
	spawnError: unknown;
};

export type ProjectSettingsSection = "general" | "agents" | "gateway";
export type ProjectSettingsSaveState = {
	phase: "idle" | "pending" | "saving" | "saved" | "failed";
	dirty?: boolean;
	requestPending?: boolean;
	error?: string;
	replacementError?: string;
	/** The draft is dirty but cannot be saved as it stands (e.g. an invalid name). */
	unsaveable?: boolean;
	retry?: () => void;
};

type ProjectSettingsFormProps = {
	projectId: string;
	/** Installation ID of the self-hosted daemon that owns this project. */
	hostId?: string;
	cloudOrgId?: string;
	section?: ProjectSettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
};

export function ProjectSettingsForm(props: ProjectSettingsFormProps) {
	return props.cloudOrgId !== undefined
		? <CloudProjectSettingsAdapter {...props} cloudOrgId={props.cloudOrgId} />
		: <LocalProjectSettingsAdapter {...props} />;
}

function LocalProjectSettingsAdapter({
	projectId,
	hostId,
	section = "general",
	onSaveState,
}: ProjectSettingsFormProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const connected = useConnectedHosts();
	const hostConnected = !hostId || connected.includes(hostId);
	useEffect(() => {
		if (!hostConnected) onSaveState?.({ phase: "idle" });
	}, [hostConnected, onSaveState]);

	const query = useQuery({
		queryKey: projectQueryKey(projectId, hostId),
		enabled: hostConnected,
		queryFn: async () => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error(t("settings.project.degraded"));
			return data.project as Project;
		},
	});

	return (
		<>
			{!hostConnected ? <p role="alert" className="text-pretty text-sm text-error">{t("remote.hostOffline")}</p> : null}
			{!query.data && hostConnected && query.isLoading ? (
				<p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>
			) : !query.data && hostConnected ? (
				<p className="text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p>
			) : query.data ? (
				<div hidden={!hostConnected}>
				<SettingsBody
					key={refKey({ host: hostId ?? LOCAL_HOST, id: projectId })}
					project={query.data}
					onSaved={() =>
						queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }).catch(() => {
							// Saving succeeds even if the cache refresh fails.
						})
					}
					projectId={projectId}
					hostId={hostId}
					hostConnected={hostConnected}
					section={section}
					onSaveState={onSaveState}
				/>
				</div>
			) : null}
		</>
	);
}

function SettingsBody({ project, projectId, hostId, hostConnected, onSaved, section = "general", onSaveState }: {
	project: Project;
	projectId: string;
	hostId?: string;
	hostConnected: boolean;
	onSaved: () => Promise<void>;
	section?: ProjectSettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const workspaceQuery = useQuery({ ...workspaceQueryOptions, enabled: !hostId });
	const remoteProjectQuery = useRemoteProjectQuery(hostId ?? "", projectId);
	const config = project.config ?? {};
	const isScratchProject = project.kind === "scratch";
	const { settings } = useSettings(hostId);
	const intakeVisible = !isScratchProject && !!settings?.trackerIntakeEnabled;
	const workspace = hostId ? remoteProjectQuery.data : workspaceQuery.data?.find((item) => item.id === projectId);
	const activeOrchestrator = newestActiveOrchestrator(workspace?.sessions ?? []);
	const intake: TrackerIntakeConfig = config.trackerIntake ?? {};
	const [promptOverrideOpen, setPromptOverrideOpen] = useState(false);
	const initialValues: ProjectSettingsDraft = {
		displayName: project.name,
		defaultBranch: config.defaultBranch ?? DEFAULT_BRANCH_AUTO,
		sessionPrefix: config.sessionPrefix ?? "",
		workerAgent: config.worker?.agent ?? "",
		orchestratorAgent: config.orchestrator?.agent ?? "",
		workerProvider: config.worker?.provider ?? "",
		orchestratorProvider: config.orchestrator?.provider ?? "",
		reviewerProvider: config.reviewers?.[0]?.provider ?? "",
		workerModel: config.worker?.agentConfig?.model ?? config.agentConfig?.model ?? "",
		workerEffort: config.worker?.agentConfig?.effort ?? config.agentConfig?.effort ?? "",
		workerPermissions: config.worker?.agentConfig?.permissions ?? config.agentConfig?.permissions ?? "",
		orchestratorModel: config.orchestrator?.agentConfig?.model ?? config.agentConfig?.model ?? "",
		orchestratorEffort: config.orchestrator?.agentConfig?.effort ?? config.agentConfig?.effort ?? "",
		orchestratorPermissions: config.orchestrator?.agentConfig?.permissions ?? config.agentConfig?.permissions ?? "",
		workerMode: config.worker?.agentConfig?.mode ?? config.agentConfig?.mode ?? "",
		orchestratorMode: config.orchestrator?.agentConfig?.mode ?? config.agentConfig?.mode ?? "",
		reviewerHarness: config.reviewers?.[0]?.harness ?? "",
		reviewerModel: config.reviewers?.[0]?.agentConfig?.model ?? config.agentConfig?.model ?? "",
		reviewerMode: config.reviewers?.[0]?.agentConfig?.mode ?? config.agentConfig?.mode ?? "",
		reviewerEffort: config.reviewers?.[0]?.agentConfig?.effort ?? config.agentConfig?.effort ?? "",
		reviewerPermissions: config.reviewers?.[0]?.agentConfig?.permissions ?? config.agentConfig?.permissions ?? "",
		autoReview: config.autoReview ?? false,
		workersRequestReview: config.workersRequestReview ?? false,
		intakeEnabled: intake.enabled ?? false,
		intakeRepo: intake.repo ?? "",
		intakeAssignee: intake.assignee ?? "",
	};
	const lastOrchestratorRef = useRef(config.orchestrator?.agent ?? "");
	const replacementAttemptedRef = useRef(false);
	const replacementFailedRef = useRef(false);
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	useEnsureAgentReadiness({ hostId });
	// Per-role provider pins (#6156) are chosen among the configured gateway
	// entries; the same GET the Gateway section uses reports both scopes. On a
	// remote host the gateway settings live on that host's daemon, not local.
	const gatewayQuery = useQuery({
		queryKey: gatewayConfigQueryKey(projectId, hostId),
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/settings/gateway", {
				params: projectId ? { query: { projectId } } : undefined,
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
	});
	const providerOptions = gatewayProviderOptions(gatewayQuery.data, t);
	const persist = async (values: ProjectSettingsDraft): Promise<SettingsSaveResult> => {
		const savedKey = JSON.stringify(values);
		void captureRendererEvent("ao.renderer.settings_save_requested", {
			project_id: projectId,
		});
		const displayName = values.displayName.trim();
		const { model: _legacyModel, mode: _legacyMode, effort: _legacyEffort, permissions: _legacyPermissions, ...sharedAgentConfig } = config.agentConfig ?? {};
		const existingReviewer = config.reviewers?.[0];
		const existingReviewerAgentConfig = existingReviewer?.harness === values.reviewerHarness ? existingReviewer.agentConfig : undefined;
		const next: ProjectConfig = isScratchProject
			? {
					...scratchSupportedConfig(config),
					worker: {
						...config.worker,
						agent: values.workerAgent,
						provider: values.workerProvider || undefined,
						agentConfig: buildRoleAgentConfig(config.worker?.agentConfig, values.workerModel, values.workerMode, values.workerEffort, values.workerPermissions),
					},
					orchestrator: {
						...config.orchestrator,
						agent: values.orchestratorAgent,
						provider: values.orchestratorProvider || undefined,
						agentConfig: buildRoleAgentConfig(
							config.orchestrator?.agentConfig,
							values.orchestratorModel,
							values.orchestratorMode,
							values.orchestratorEffort,
							values.orchestratorPermissions,
						),
					},
					agentConfig: blankToUndefined({
						...sharedAgentConfig,
						permissions: undefined,
					}),
				}
			: {
					...config,
					defaultBranch: values.defaultBranch.trim() === DEFAULT_BRANCH_AUTO ? undefined : values.defaultBranch || undefined,
					sessionPrefix: values.sessionPrefix || undefined,
					worker: {
						...config.worker,
						agent: values.workerAgent,
						provider: values.workerProvider || undefined,
						agentConfig: buildRoleAgentConfig(config.worker?.agentConfig, values.workerModel, values.workerMode, values.workerEffort, values.workerPermissions),
					},
					orchestrator: {
						...config.orchestrator,
						agent: values.orchestratorAgent,
						provider: values.orchestratorProvider || undefined,
						agentConfig: buildRoleAgentConfig(
							config.orchestrator?.agentConfig,
							values.orchestratorModel,
							values.orchestratorMode,
							values.orchestratorEffort,
							values.orchestratorPermissions,
						),
					},
					agentConfig: blankToUndefined({
						...sharedAgentConfig,
						permissions: undefined,
					}),
					reviewers: values.reviewerHarness
						? [
								{
									harness: values.reviewerHarness,
									provider: values.reviewerProvider || undefined,
									agentConfig: buildRoleAgentConfig(
										existingReviewerAgentConfig,
										values.reviewerModel,
										values.reviewerMode,
										values.reviewerEffort,
										values.reviewerPermissions,
									),
								},
							]
						: undefined,
					trackerIntake: buildIntake(
						{
							enabled: values.intakeEnabled,
							repo: values.intakeRepo,
							assignee: values.intakeAssignee,
						},
						config.trackerIntake,
					),
					autoReview: values.autoReview,
					workersRequestReview: values.workersRequestReview || undefined,
				};
		const { error } = await (hostId ? clientForHost(hostId) : apiClient).PUT("/api/v1/projects/{id}", {
			params: { path: { id: projectId } },
			body: { displayName, config: next },
		});
		if (error) throw new Error(apiErrorMessage(error));
		const replaceOrchestrator = replacementFailedRef.current || values.orchestratorAgent !== lastOrchestratorRef.current ||
			(Boolean(activeOrchestrator && activeOrchestrator.provider !== values.orchestratorAgent) && !replacementAttemptedRef.current);
		lastOrchestratorRef.current = values.orchestratorAgent;
		if (replaceOrchestrator) {
			replacementAttemptedRef.current = true;
			try {
				const sessionId = hostId
					? await openRemoteOrchestrator(hostId, projectId, undefined, undefined, true, "settings")
					: await spawnOrchestrator(projectId, "settings", true);
				replacementFailedRef.current = false;
				return {
					replacementError: null,
					replacementSessionId: sessionId,
					replacementFailure: null,
					spawnError: null,
					savedKey,
				} satisfies SettingsSaveResult;
			} catch (error) {
				replacementFailedRef.current = true;
				const replacementFailure: OrchestratorReplacementFailure = {
					message: error instanceof Error ? error.message : t("settings.project.replaceOrchestratorFailed"),
					...(error instanceof OrchestratorSpawnError
						? {
								code: error.code,
								requestId: error.requestId,
								details: error.details,
							}
						: {}),
				};
				return {
					replacementError: replacementFailure.message,
					replacementSessionId: null,
					replacementFailure,
					spawnError: error,
					savedKey,
				} satisfies SettingsSaveResult;
			}
		}
		return {
			replacementError: null,
			replacementSessionId: null,
			replacementFailure: null,
			spawnError: null,
			savedKey,
		} satisfies SettingsSaveResult;
	};
	const save = async (values: ProjectSettingsDraft) => {
		try {
			const result = await persist(values);
			void captureRendererEvent("ao.renderer.settings_save_succeeded", { project_id: projectId });
			void queryClient.invalidateQueries({ queryKey: projectQueryKey(projectId, hostId) });
			void queryClient.invalidateQueries({ queryKey: hostId ? ["project-config", hostId, projectId] : ["project-config", projectId] });
			void onSaved();
			if (result.replacementFailure) {
				if (!hostId) setOrchestratorReplacementError(projectId, result.replacementFailure);
				if (result.spawnError) captureOrchestratorReplacementFailure(result.spawnError, hostId ? refKey({ host: hostId, id: projectId }) : projectId);
			}
			return { replacementError: result.replacementError };
		} catch (error) {
			void captureRendererEvent("ao.renderer.settings_save_failed", { project_id: projectId });
			throw error;
		}
	};
	return <ProjectSettingsEditor initialValues={initialValues} section={section} disabled={!hostConnected}
		capabilities={{ workflow: !isScratchProject, sessionPrefix: !isScratchProject, intake: intakeVisible, reviewer: !isScratchProject, requiredAgents: true, workersRequestReview: !isScratchProject }}
		details={[{ label: t("settings.project.path"), value: project.path, href: hostId ? undefined : `file://${encodeURI(project.path)}` }, { label: t("settings.project.repo"), value: project.repo || "—", href: project.repo ? repositoryHref(project.repo) : undefined }]}
		workspaceRepos={project.kind === "workspace" ? project.workspaceRepos ?? [] : undefined} repository={project.repo}
		modelScope={() => projectId} defaultReviewer={(draft) => WORKER_DEFAULT_REVIEWERS[draft.workerAgent] ?? "claude-code"}
		reviewerWarning={reviewerTrustWarning} save={save} saveUnchanged onSaveState={onSaveState}
		modelHostId={hostId}
		gatewayExtra={<GatewayProvidersSection projectId={projectId} />}
		agentsExtra={<>
			<ProjectSettingsSection title={t("settings.project.agentDefaults")} grouped>
				<button
					type="button"
					className="w-full rounded-md bg-[var(--color-bg-settings-row)] px-4 py-3 text-left"
					onClick={() => setPromptOverrideOpen(true)}
				>
					{t("settings.project.promptOverride")}
				</button>
			</ProjectSettingsSection>
			{promptOverrideOpen && (
				<PromptOverrideDialog
					open={promptOverrideOpen}
					onOpenChange={setPromptOverrideOpen}
					scope="project"
					projectId={projectId}
				/>
			)}
		</>}
		renderAgent={(props) => <LocalAgentPicker {...props} projectId={projectId} hostId={hostId} agentsQuery={agentsQuery} />}
		renderProvider={(props) => <LocalProviderPicker {...props} options={providerOptions} />} />;
}

function LocalAgentPicker({ role, draft, value, invalid, onChange, projectId, hostId, agentsQuery }: ProjectAgentPickerProps & { projectId: string; hostId?: string; agentsQuery: ReturnType<typeof useAgentReadinessQuery> }) {
	const { t } = useTranslation();
	useEnsureAgentReadiness({ agentIds: [draft.workerAgent, draft.orchestratorAgent, draft.reviewerHarness], enabled: role === "worker" && Boolean(draft.workerAgent || draft.orchestratorAgent || draft.reviewerHarness), hostId, purpose: hostId ? "launch" : "display" });
	const disabled = agentsQuery.isFetching && agentsQuery.data === undefined;
	const agents = hostId ? agentsQuery.data?.agents.filter(isLaunchableAgent) : agentsQuery.data?.agents;
	return role === "reviewer"
		? <ReviewerSelect value={value} model={draft.reviewerModel} mode={draft.reviewerMode} projectId={projectId} hostId={hostId} harnessOnly defaultHarness={WORKER_DEFAULT_REVIEWERS[draft.workerAgent] ?? "claude-code"} triggerClassName="w-full" onChange={onChange} ariaLabel={t("settings.project.defaultReviewer")} agents={agents} disabled={disabled} />
		: <RequiredAgentField id={`${role}Agent`} variant="settings-control" value={value} placeholder={t(role === "worker" ? "settings.project.selectWorker" : "settings.project.selectOrchestrator")} label={t(role === "worker" ? "settings.project.defaultWorker" : "settings.project.defaultOrchestrator")} agents={agents} hostId={hostId} disabled={disabled} invalid={invalid} onChange={onChange} />;
}

function LocalProviderPicker({ value, onChange, options }: ProjectProviderPickerProps & { options: { value: string; label: string }[] }) {
	const { t } = useTranslation();
	// A persisted pin that no longer matches a configured gateway must stay
	// visible as its own "unknown" state, not masquerade as the default.
	const all = value !== "" && !options.some((option) => option.value === value)
		? [...options, { value, label: t("settings.project.providerUnknown") }]
		: options;
	return (
		<SettingsOptionMenu
			aria-label={t("settings.project.providerLabel")}
			value={value}
			options={all}
			triggerClassName="w-full justify-between"
			onChange={onChange}
		/>
	);
}

function repositoryHref(repository: string): string | undefined {
	if (/^https?:\/\//i.test(repository)) return repository;
	if (repository.startsWith("git@")) {
		const [host, path] = repository.slice(4).split(":", 2);
		return `https://${host}/${path.replace(/\.git$/, "")}`;
	}
	if (repository.startsWith("ssh://")) {
		try {
			const parsed = new URL(repository);
			return `https://${parsed.hostname}${parsed.pathname.replace(/\.git$/, "")}`;
		} catch {
			return undefined;
		}
	}
	return undefined;
}

function scratchSupportedConfig(config: ProjectConfig): ProjectConfig {
	const {
		defaultBranch: _defaultBranch,
		reviewers: _reviewers,
		autoReview: _legacyAutoReview,
		workersRequestReview: _workersRequestReview,
		trackerIntake: _trackerIntake,
		...supported
	} = config as ProjectConfig;
	return supported;
}

function blankToUndefined<T extends object>(obj: T): T | undefined {
	return Object.values(obj).some((v) => v !== undefined) ? obj : undefined;
}

function buildRoleAgentConfig(
	existing: components["schemas"]["AgentConfig"] | undefined,
	model: string,
	mode: string,
	effort: string,
	permissions: string,
): components["schemas"]["AgentConfig"] | undefined {
	const next = { ...existing };
	if (model) next.model = model;
	else delete next.model;
	if (mode) next.mode = mode;
	else delete next.mode;
	if (effort) next.effort = effort;
	else delete next.effort;
	if (permissions) next.permissions = permissions as components["schemas"]["AgentConfig"]["permissions"];
	else delete next.permissions;
	return Object.keys(next).length > 0 ? next : undefined;
}

type GatewayConfigResponse = components["schemas"]["ControllersGatewayConfigResponse"];

// gatewayProviderOptions lists the per-role provider choices: follow the
// gateway resolution, bypass every gateway, or pin one of the configured
// entries. A pinned entry is labeled with the scope that wins the resolution
// (project overrides app) so the effective source stays visible.
function gatewayProviderOptions(config: GatewayConfigResponse | undefined, t: TFunction): { value: string; label: string }[] {
	const options = [
		{ value: "", label: t("settings.project.providerDefault") },
		{ value: "direct", label: t("settings.project.providerDirect") },
	];
	const pushEntry = (baseUrl: string | undefined, scopeLabel: string) => {
		if (!baseUrl) return;
		let host = baseUrl;
		try {
			host = new URL(baseUrl).host;
		} catch {
			// Not a parseable URL — show it verbatim rather than hiding the entry.
		}
		options.push({ value: baseUrl, label: `${host} (${scopeLabel})` });
	};
	pushEntry(config?.project?.baseUrl, t("settings.project.providerScopeProject"));
	pushEntry(config?.app?.baseUrl, t("settings.project.providerScopeApp"));
	return options;
}
