import { apiErrorException } from "../lib/api-error-exception";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useEffect } from "react";
import { ProjectSettingsSection as SettingsSection } from "@aoagents/product-ui";
import type { components } from "../../api/schema";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { useRemoteProjectQuery, workspaceQueryKeyForHost, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { useSettings } from "../hooks/useSettings";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { isLaunchableAgent } from "../lib/agent-select-options";
import { WORKER_DEFAULT_REVIEWERS } from "../lib/reviewer-harnesses";
import { captureRendererEvent } from "../lib/telemetry";
import { importableProject } from "../lib/session-import";
import { useSessionImportStore } from "../stores/session-import-store";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import { buildIntake } from "./IntakeFields";
import { ReviewerSelect, reviewerTrustWarning } from "./ReviewerSelect";
import { ProjectSettingsEditor, type ProjectAgentPickerProps, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { CloudProjectSettingsAdapter } from "./CloudProjectSettingsForm";
import { SettingsRow } from "./settings/SettingsRow";
import { Button } from "./ui/button";

type Project = components["schemas"]["Project"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type TrackerIntakeConfig = components["schemas"]["TrackerIntakeConfig"];

const DEFAULT_BRANCH_AUTO = "auto";

const projectQueryKey = (id: string, hostId?: string) => hostId ? ["project", hostId, id] as const : ["project", id] as const;

export type ProjectSettingsSection = "general" | "agents";
export type ProjectSettingsSaveState = {
	phase: "idle" | "pending" | "saving" | "saved" | "failed";
	dirty?: boolean;
	requestPending?: boolean;
	error?: string;
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
			if (error) throw apiErrorException(error);
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
	const config = project.config ?? {};
	const isScratchProject = project.kind === "scratch";
	const { settings } = useSettings(hostId);
	const intakeVisible = !isScratchProject && !!settings?.trackerIntakeEnabled;
	const workspaceQuery = useQuery({ ...workspaceQueryOptions, enabled: !hostId });
	const remoteProjectQuery = useRemoteProjectQuery(hostId ?? "", projectId);
	const workspace = hostId ? remoteProjectQuery.data : workspaceQuery.data?.find((item) => item.id === projectId);
	const openProjectImport = useSessionImportStore((state) => state.openProjectImport);
	const intake: TrackerIntakeConfig = config.trackerIntake ?? {};
	const initialValues: ProjectSettingsDraft = {
		displayName: project.name,
		defaultBranch: config.defaultBranch ?? DEFAULT_BRANCH_AUTO,
		sessionPrefix: config.sessionPrefix ?? "",
		workerAgent: config.worker?.agent ?? "",
		orchestratorAgent: config.orchestrator?.agent ?? "",
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
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	useEnsureAgentReadiness({ hostId });
	const persist = async (values: ProjectSettingsDraft) => {
		const displayName = values.displayName.trim();
		const { model: _legacyModel, mode: _legacyMode, effort: _legacyEffort, permissions: _legacyPermissions, ...sharedAgentConfig } = config.agentConfig ?? {};
		const existingReviewer = config.reviewers?.[0];
		const existingReviewerAgentConfig = existingReviewer?.harness === values.reviewerHarness ? existingReviewer.agentConfig : undefined;
		const next: ProjectConfig = isScratchProject
			? {
					...scratchSupportedConfig(config),
					worker: {
						...config.worker,
						agent: values.workerAgent || undefined,
						agentConfig: buildRoleAgentConfig(config.worker?.agentConfig, values.workerModel, values.workerMode, values.workerEffort, values.workerPermissions),
					},
					orchestrator: {
						...config.orchestrator,
						agent: values.orchestratorAgent || undefined,
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
						agent: values.workerAgent || undefined,
						agentConfig: buildRoleAgentConfig(config.worker?.agentConfig, values.workerModel, values.workerMode, values.workerEffort, values.workerPermissions),
					},
					orchestrator: {
						...config.orchestrator,
						agent: values.orchestratorAgent || undefined,
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
		if (error) throw apiErrorException(error);
	};
	const save = async (values: ProjectSettingsDraft) => {
		try {
			await persist(values);
			void captureRendererEvent("ao.settings.saved", { project_id: projectId });
			void queryClient.invalidateQueries({ queryKey: projectQueryKey(projectId, hostId) });
			void queryClient.invalidateQueries({ queryKey: hostId ? ["project-config", hostId, projectId] : ["project-config", projectId] });
			void onSaved();
			return {};
		} catch (error) {
			void captureRendererEvent("ao.renderer.settings_save_failed", { project_id: projectId });
			throw error;
		}
	};
	return <ProjectSettingsEditor initialValues={initialValues} section={section} disabled={!hostConnected}
		capabilities={{ workflow: !isScratchProject, sessionPrefix: !isScratchProject, intake: intakeVisible, reviewer: !isScratchProject, workersRequestReview: !isScratchProject }}
		details={[{ label: t("settings.project.path"), value: project.path, href: hostId ? undefined : `file://${encodeURI(project.path)}` }, { label: t("settings.project.repo"), value: project.repo || "—", href: project.repo ? repositoryHref(project.repo) : undefined }]}
		workspaceRepos={project.kind === "workspace" ? project.workspaceRepos ?? [] : undefined} repository={project.repo}
		modelScope={() => ""} defaultReviewer={(draft) => WORKER_DEFAULT_REVIEWERS[draft.workerAgent] ?? "claude-code"}
		reviewerWarning={reviewerTrustWarning} save={save} saveUnchanged onSaveState={onSaveState}
		modelHostId={hostId}
		generalExtra={workspace && importableProject(workspace) ? <SettingsSection title={t("sessionImport.settingsTitle")} grouped>
			<SettingsRow label={t("sessionImport.settingsLabel")} description={t("sessionImport.settingsDescription")}>
				<Button type="button" variant="quiet" size="sm" onClick={() => openProjectImport(projectId)}>{t("sessionImport.menuItem")}</Button>
			</SettingsRow>
		</SettingsSection> : undefined}
		renderAgent={(props) => <LocalAgentPicker {...props} projectId={projectId} hostId={hostId} agentsQuery={agentsQuery} />} />;
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
