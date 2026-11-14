import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ProjectSettingsRow, ProjectSettingsSection as SettingsGroup, ProjectSettingsValueRow } from "@aoagents/product-ui";
import { TriangleAlert } from "@/components/app-icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCoderTemplates } from "../hooks/useCoderTemplates";
import { useCoderTemplateParameters } from "../hooks/useCoderTemplateParameters";
import { changedParameterOverrides, parameterOverridesUpdate } from "../lib/coder-template-parameters";
import { CoderTemplateOptions } from "./CoderTemplateOptions";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import { defaultReviewerHarness } from "../lib/reviewer-harnesses";
import { CLOUD_AGENT_PROVIDERS, connectedCredentialType, credentialModelScope } from "../lib/cloud-agents";
import { agentLabel } from "../lib/agent-options";
import { coderWorkspaceNamePrefixFor, isValidCoderWorkspaceNamePrefix } from "../lib/coder-workspace-name";
import { CODER_PROVIDER, projectSandboxProvider } from "../lib/cloud-offering";
import { isCloudWorkspaceProject, isStandaloneCloudProject, projectAdditionalRepos, projectConfigText, projectRepositoryUrls, repoDirName } from "../lib/cloud-project-repos";
import { CloudCpError, type CloudCpProject, type CloudCpProjectCoderConfig, type CloudCpProjectSettingsRequest } from "../lib/cloud-cp";
import { cloudProjectSettingsDraft, cloudProjectSettingsPatch, type CloudProjectRoleDraft, type CloudProjectSettingsDraft } from "../lib/cloud-project-settings";
import { AgentAvatar } from "./AgentAvatar";
import type { ProjectSettingsSaveState, ProjectSettingsSection as SettingsSection } from "./ProjectSettingsForm";
import { ProjectSettingsEditor, type ProjectAgentPickerProps, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { AgentSelectMenuItem } from "./settings/AgentSelectMenuItem";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { SettingsInlineInput, SettingsRow } from "./settings/SettingsRow";
import { useUiStore } from "../stores/ui-store";

export const cloudProjectSettingsQueryKey = (baseUrl: string, orgId: string, projectId: string) => ["cloud-project-settings", baseUrl, orgId, projectId] as const;

export function CloudProjectSettingsAdapter({ projectId, cloudOrgId, section = "general", onSaveState }: {
	projectId: string;
	cloudOrgId: string;
	section?: SettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const query = useQuery({
		queryKey: cloudProjectSettingsQueryKey(baseUrl, cloudOrgId, projectId), enabled: ready, retry: false,
		queryFn: ({ signal }) => client.getProject(cloudOrgId, projectId, { signal }).then((response) => response.project),
	});
	const loadError = !ready ? t("settings.cloudProject.signIn") : query.isError
		? query.error instanceof Error ? query.error.message : t("settings.project.loadFailed") : undefined;
	useEffect(() => {
		if (loadError) onSaveState?.({ phase: "failed", error: loadError, retry: ready ? () => { void query.refetch({ cancelRefetch: false }); } : undefined });
	}, [loadError, onSaveState, ready, query.refetch]);
	if (loadError) return <p className="px-(--settings-row-inset) text-sm text-error" role="alert">{loadError}</p>;
	if (!query.data) return <p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	return <CloudSettingsAdapter key={`${cloudOrgId}/${projectId}`} project={query.data} section={section} onSaveState={onSaveState} />;
}

function CloudSettingsAdapter({ project, section, onSaveState }: { project: CloudCpProject; section: SettingsSection; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const connections = useProviderConnections();
	const opencodeCredential = connectedCredentialType(connections.data, "opencode");
	const queryClient = useQueryClient();
	const saved = useRef(cloudProjectSettingsDraft(project));
	const offering = useCloudOffering();
	const usesCoder = offering.hasCoder && projectSandboxProvider(project.config, offering) === CODER_PROVIDER;
	const standalone = isStandaloneCloudProject(project);
	const additionalRepos = projectAdditionalRepos(project.config);
	const workspace = isCloudWorkspaceProject(project);
	const intakeVisible = !standalone;
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const save = async (values: ProjectSettingsDraft) => {
		const patch = cloudProjectSettingsPatch(saved.current, toCloudDraft(values));
		let updated: CloudCpProject | undefined;
		try {
			updated = (await client.updateProjectSettings(project.orgId, project.id, patch)).project;
		} catch (error) {
			if (patch.config?.coAuthorCommits === undefined || !coAuthorCommitsUnsupported(error)) throw error;
			const { coAuthorCommits: _unsupported, ...config } = patch.config;
			const rest: CloudCpProjectSettingsRequest = { ...patch };
			if (Object.keys(config).length > 0) rest.config = config;
			else delete rest.config;
			if (Object.keys(rest).length > 0) updated = (await client.updateProjectSettings(project.orgId, project.id, rest)).project;
			showGlobalToast(t("settings.cloudProject.coAuthorCommitsUnsupported"), undefined, "error");
		}
		if (updated) {
			saved.current = cloudProjectSettingsDraft(updated);
			queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), updated);
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		}
		return { values: toEditorDraft(saved.current) };
	};
	return <ProjectSettingsEditor initialValues={toEditorDraft(cloudProjectSettingsDraft(project))} section={section}
		capabilities={{ workflow: !standalone, sessionPrefix: true, intake: intakeVisible, reviewer: true, requiredAgents: true, workersRequestReview: !standalone, coAuthorCommits: !standalone, nameLimit: 120, requiredBranch: true, runtimeDefaults: true }}
		repository={standalone ? undefined : project.repositoryUrl}
		details={[
			{ label: t("settings.project.id"), value: project.id },
			{ label: t("settings.project.kind"), value: standalone ? t("settings.project.kind.cloudStandalone", { defaultValue: "Cloud, without a repository" }) : workspace ? t("settings.project.kind.cloudWorkspace", { defaultValue: "Cloud workspace" }) : t("settings.project.kind.cloud") },
			...(workspace ? [] : [standalone
				? { label: t("settings.project.repo"), value: t("settings.cloudProject.noRepository", { defaultValue: "No repository" }) }
				: { label: t("settings.project.repo"), value: project.repositoryUrl, href: project.repositoryUrl },
			...additionalRepos.map((repo, index) => ({ label: t("settings.cloudProject.additionalRepositoryN", { defaultValue: "Additional repository {{number}}", number: index + 1 }), value: repo.url, href: repo.url }))]),
		]}
		workspaceRepos={workspace ? projectRepositoryUrls(project).map((url) => ({ name: repoDirName(url), relativePath: repoDirName(url), repo: url })) : undefined}
		generalExtra={usesCoder || intakeVisible ? <>
			{intakeVisible ? <CloudIntakeStatus project={project} /> : null}
			{usesCoder ? <CloudProjectCoderSettings project={project} /> : null}
		</> : undefined}
		modelScope={(agent) => agent === "opencode" && opencodeCredential ? credentialModelScope(opencodeCredential) : ""}
		cloudOrgId={project.orgId}
		defaultReviewer={(draft) => defaultReviewerHarness(draft.workerAgent, true)}
		autoReviewDescription={t("settings.cloudProject.autoReviewDescription")}
		intakeDescription={t("settings.cloudProject.intakeDescription")} renderAgent={(props) => <CloudAgentPicker {...props} />}
		save={save} onSaveState={onSaveState} />;
}

function coAuthorCommitsUnsupported(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 422 && error.code === "validation_error" &&
		error.message === "unsupported or null config.coAuthorCommits";
}

function coderConfigOf(project: CloudCpProject): CloudCpProjectCoderConfig | undefined {
	const coder = project.config?.coder;
	return coder && typeof coder === "object" ? (coder as CloudCpProjectCoderConfig) : undefined;
}

export function CloudProjectCoderSettings({ project }: { project: CloudCpProject }) {
	const { t } = useTranslation();
	const coder = coderConfigOf(project);
	const templateId = coder?.templateId?.trim() ?? "";
	const { templates, isLoading: templatesLoading } = useCoderTemplates(project.orgId, templateId !== "");
	const template = templates.find((candidate) => candidate.id === templateId);
	const templateName = template ? template.displayName || template.name : "";
	const orgCoder = useOrgCoderConfig(true, project.orgId);
	const orgCoderConfig = orgCoder.data as { templateId?: string; defaultTemplateId?: string } | null | undefined;
	const orgDefaultTemplateId = (orgCoderConfig?.templateId ?? orgCoderConfig?.defaultTemplateId ?? "").trim();
	const templateRequired = templateId === "" && orgCoderConfig != null && orgDefaultTemplateId === "";
	return <SettingsGroup title={t("settings.project.coderTitle")} grouped>
		<ProjectSettingsRow label={t("settings.project.coderTemplate")}>
			{templateId !== "" ? (
				templateName ? (
					<span className="settings-row-value flex min-w-0 items-baseline justify-end gap-2" title={templateId}>
						<span className="shrink-0">{templateName}</span>
						<span className="min-w-0 truncate font-mono text-xs text-settings-muted" data-testid="coder-template-id">{templateId}</span>
					</span>
				) : (
					<span className="settings-row-value font-mono" title={templateId}>{templatesLoading ? "…" : templateId}</span>
				)
			) : templateRequired ? (
				<span className="settings-row-value flex items-center gap-1.5 text-error" role="alert">
					<TriangleAlert className="size-4 shrink-0" aria-hidden="true" />
					{t("settings.project.coderTemplateMissing")}
				</span>
			) : (
				<span className="settings-row-value">{orgCoder.isLoading ? "…" : t("settings.project.coderTemplateDefault")}</span>
			)}
		</ProjectSettingsRow>
		{coder?.size ? <ProjectSettingsValueRow label={t("settings.project.coderSize")} value={coder.size} /> : null}
		{orgCoderConfig != null || (coder?.workspaceNamePrefix?.trim() ?? "") !== "" ? (
			<CoderWorkspaceNamePrefixRow project={project} />
		) : null}
		{templateId !== "" ? <CoderTemplateParametersSettings project={project} templateId={templateId} /> : null}
	</SettingsGroup>;
}

function CoderTemplateParametersSettings({ project, templateId }: { project: CloudCpProject; templateId: string }) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const { parameters } = useCoderTemplateParameters(project.orgId, templateId);
	const saved = useRef<Record<string, string>>(coderConfigOf(project)?.parameters ?? {});
	const [draft, setDraft] = useState<Record<string, string> | null>(null);
	const shown = draft ?? changedParameterOverrides(parameters, saved.current);
	const save = useMutation({
		mutationFn: (patch: Record<string, string>) =>
			client.updateProjectSettings(project.orgId, project.id, { config: { coder: { parameters: patch } } }),
		onSuccess: (response, patch) => {
			saved.current = coderConfigOf(response.project)?.parameters ?? patch;
			queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), response.project);
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		},
	});
	const { mutate, isPending, isError } = save;
	const patch = draft === null ? undefined : parameterOverridesUpdate(parameters, saved.current, draft);
	const patchKey = patch ? JSON.stringify(patch) : "";
	const failedKey = isError && save.variables ? JSON.stringify(save.variables) : "";
	useEffect(() => {
		if (!patch || isPending || patchKey === failedKey) return;
		const timeout = window.setTimeout(() => mutate(patch), 650);
		return () => window.clearTimeout(timeout);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [patchKey, failedKey, isPending, mutate]);
	return (
		<div className="settings-content-row">
			<CoderTemplateOptions
				orgId={project.orgId}
				templateId={templateId}
				overrides={shown}
				onChange={setDraft}
				repositoryUrls={isStandaloneCloudProject(project) ? [] : [project.repositoryUrl, ...projectAdditionalRepos(project.config).map((repo) => repo.url)]}
				projectWorkingDirectory={projectConfigText(project.config, "workingDirectory")}
				footer={save.isError ? (
					<p className="text-xs text-error" role="alert">
						{save.error instanceof Error ? save.error.message : t("settings.project.saveFailed")}
					</p>
				) : (
					<p className="text-xs text-settings-muted">{t("coder.templateOptions.settingsHint")}</p>
				)}
			/>
		</div>
	);
}

function CoderWorkspaceNamePrefixRow({ project }: { project: CloudCpProject }) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const coder = coderConfigOf(project);
	const savedPrefix = useRef(coder?.workspaceNamePrefix?.trim() ?? "");
	const [draft, setDraft] = useState(savedPrefix.current);
	const trimmed = draft.trim();
	const invalid = !isValidCoderWorkspaceNamePrefix(trimmed);
	const save = useMutation({
		mutationFn: (workspaceNamePrefix: string) =>
			client.updateProjectSettings(project.orgId, project.id, { config: { coder: { workspaceNamePrefix } } }),
		onSuccess: (response, workspaceNamePrefix) => {
			savedPrefix.current = coderConfigOf(response.project)?.workspaceNamePrefix?.trim() ?? workspaceNamePrefix;
			queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), response.project);
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		},
	});
	const { mutate, isPending, isError, reset } = save;
	const failedValue = isError ? save.variables : undefined;
	useEffect(() => {
		if (trimmed === savedPrefix.current) {
			if (isError) reset();
			return;
		}
		if (invalid || isPending || trimmed === failedValue) return;
		const timeout = window.setTimeout(() => mutate(trimmed), 650);
		return () => window.clearTimeout(timeout);
	}, [failedValue, invalid, isError, isPending, mutate, reset, trimmed]);
	const label = t("coder.workspacePrefix.label");
	const description = invalid ? (
		<span className="text-error" role="alert">{t("coder.workspacePrefix.invalid")}</span>
	) : save.isError ? (
		<span className="text-error" role="alert">
			{save.error instanceof Error ? save.error.message : t("settings.project.saveFailed")}
		</span>
	) : t("coder.workspacePrefix.settingsHint");
	return <SettingsRow label={label} description={description}>
		<SettingsInlineInput id="coderWorkspaceNamePrefix" label={label} value={draft} placeholder={coderWorkspaceNamePrefixFor(project.displayName)} onChange={setDraft} />
	</SettingsRow>;
}

function CloudAgentPicker({ role, draft, value, disabled, onChange }: ProjectAgentPickerProps) {
	const { t } = useTranslation();
	const shown = value || (role === "reviewer" ? defaultReviewerHarness(draft.workerAgent, true) : "");
	const agents = CLOUD_AGENT_PROVIDERS.map((agent) => ({ value: agent, label: agentLabel(agent), icon: <AgentAvatar provider={agent} className="size-icon-lg" decorative /> }));
	const label = role === "orchestrator" ? t("settings.project.defaultOrchestrator") : role === "worker" ? t("settings.project.defaultWorker") : `${t(`settings.models.${role}Role`)} ${t("settings.project.agent").toLocaleLowerCase()}`;
	return <SettingsOptionMenu aria-label={label}
		disabled={disabled} value={shown} placeholder={t("settings.cloudProject.sessionSelection")}
		options={role === "reviewer" ? [{ value: "", label: t("settings.cloudProject.sessionAgent") }, ...agents] : agents}
		triggerClassName="w-fit" menuClassName="settings-agent-menu-surface" menuItemClassName="settings-agent-menu-item"
		renderMenuItem={(option, selected) => <AgentSelectMenuItem agentId={option.value || undefined} label={option.label} selected={selected} />}
		onChange={onChange} />;
}

function toEditorDraft(values: CloudProjectSettingsDraft): ProjectSettingsDraft {
	return {
		displayName: values.displayName, defaultBranch: values.defaultBranch, autoReview: values.autoReview,
		sessionPrefix: values.sessionPrefix, workersRequestReview: values.workersRequestReview, coAuthorCommits: values.coAuthorCommits,
		intakeEnabled: values.intake.enabled, intakeRepo: values.intake.repo, intakeAssignee: values.intake.assignee,
		workerAgent: values.worker.agent, orchestratorAgent: values.orchestrator.agent, reviewerHarness: values.reviewer.agent,
		workerModel: values.worker.agentConfig.model, orchestratorModel: values.orchestrator.agentConfig.model, reviewerModel: values.reviewer.agentConfig.model,
		workerMode: values.worker.agentConfig.mode, orchestratorMode: values.orchestrator.agentConfig.mode, reviewerMode: values.reviewer.agentConfig.mode,
		workerEffort: values.worker.agentConfig.effort, orchestratorEffort: values.orchestrator.agentConfig.effort, reviewerEffort: values.reviewer.agentConfig.effort,
		workerPermissions: values.worker.agentConfig.permissions, orchestratorPermissions: values.orchestrator.agentConfig.permissions, reviewerPermissions: values.reviewer.agentConfig.permissions,
	};
}

function toCloudDraft(values: ProjectSettingsDraft): CloudProjectSettingsDraft {
	const role = (name: "worker" | "orchestrator" | "reviewer"): CloudProjectRoleDraft => {
		const agent = name === "reviewer" ? values.reviewerHarness : values[`${name}Agent`];
		const model = values[`${name}Model`];
		const mode = values[`${name}Mode`];
		const effort = values[`${name}Effort`];
		const permissions = values[`${name}Permissions`];
		const provider = agent === "" ? "" : CLOUD_AGENT_PROVIDERS.find((provider) => provider === agent);
		if (provider === undefined) throw new Error("Unsupported Cloud agent");
		if (mode !== "" && mode !== "plan" && mode !== "ask") throw new Error("Unsupported Cloud mode");
		if (effort !== "" && effort !== "low" && effort !== "medium" && effort !== "high" && effort !== "xhigh" && effort !== "max") throw new Error("Unsupported Cloud effort");
		if (permissions !== "" && permissions !== "default" && permissions !== "auto" && permissions !== "accept-edits" && permissions !== "bypass-permissions") throw new Error("Unsupported Cloud permissions");
		return { agent: provider, agentConfig: { model, mode, effort, permissions } };
	};
	return {
		displayName: values.displayName, defaultBranch: values.defaultBranch, sessionPrefix: values.sessionPrefix, autoReview: values.autoReview,
		workersRequestReview: values.workersRequestReview ?? false,
		coAuthorCommits: values.coAuthorCommits ?? true,
		intake: { enabled: values.intakeEnabled, repo: values.intakeRepo, assignee: values.intakeAssignee },
		worker: role("worker"), orchestrator: role("orchestrator"), reviewer: role("reviewer"),
	};
}

function CloudIntakeStatus({ project }: { project: CloudCpProject }) {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const status = useQuery({
		queryKey: ["cloud-project-intake", baseUrl, project.orgId, project.id],
		enabled: ready && project.config.trackerIntake?.enabled === true,
		refetchInterval: 60_000,
		retry: false,
		queryFn: ({ signal }) => client.getProjectIntakeStatus(project.orgId, project.id, { signal }),
	});
	if (!status.data?.enabled || !status.data.lastError) return null;
	return <p className="px-(--settings-row-inset) text-xs text-error" role="alert">{t("settings.cloudProject.intakeLastError", { error: status.data.lastError })}</p>;
}
