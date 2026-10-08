import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ProjectSettingsRow, ProjectSettingsSection as SettingsGroup, ProjectSettingsValueRow } from "@aoagents/product-ui";
import { TriangleAlert } from "lucide-react";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCoderTemplates } from "../hooks/useCoderTemplates";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import { CLOUD_AGENT_PROVIDERS, connectedCredentialType, credentialModelScope } from "../lib/cloud-agents";
import { agentLabel } from "../lib/agent-options";
import type { CloudCpProject, CloudCpProjectCoderConfig } from "../lib/cloud-cp";
import { cloudProjectSettingsDraft, cloudProjectSettingsPatch, type CloudProjectRoleDraft, type CloudProjectSettingsDraft } from "../lib/cloud-project-settings";
import { AgentAvatar } from "./AgentAvatar";
import type { ProjectSettingsSaveState, ProjectSettingsSection as SettingsSection } from "./ProjectSettingsForm";
import { ProjectSettingsEditor, type ProjectAgentPickerProps, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { AgentSelectMenuItem } from "./settings/AgentSelectMenuItem";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

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
	if (loadError) return <p className="text-sm text-error" role="alert">{loadError}</p>;
	if (!query.data) return <p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	return <CloudSettingsAdapter key={`${cloudOrgId}/${projectId}`} project={query.data} section={section} onSaveState={onSaveState} />;
}

function CloudSettingsAdapter({ project, section, onSaveState }: { project: CloudCpProject; section: SettingsSection; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const connections = useProviderConnections();
	const opencodeCredential = connectedCredentialType(connections.data, "opencode");
	const queryClient = useQueryClient();
	const saved = useRef(cloudProjectSettingsDraft(project));
	const save = async (values: ProjectSettingsDraft) => {
		const response = await client.updateProjectSettings(project.orgId, project.id, cloudProjectSettingsPatch(saved.current, toCloudDraft(values)));
		saved.current = cloudProjectSettingsDraft(response.project);
		queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), response.project);
		void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		return { values: toEditorDraft(saved.current) };
	};
	return <ProjectSettingsEditor initialValues={toEditorDraft(cloudProjectSettingsDraft(project))} section={section}
		capabilities={{ workflow: true, sessionPrefix: true, intake: false, reviewer: true, requiredAgents: false, nameLimit: 120, requiredBranch: true, runtimeDefaults: true, lockedAgents: true }}
		details={[{ label: t("settings.project.id"), value: project.id }, { label: t("settings.project.kind"), value: t("settings.project.kind.cloud") }, { label: t("settings.project.repo"), value: project.repositoryUrl, href: project.repositoryUrl }]}
		generalExtra={<CloudProjectCoderSettings project={project} />}
		modelScope={(agent) => agent === "opencode" && opencodeCredential ? credentialModelScope(opencodeCredential) : ""}
		// Without a project reviewer, Cloud reviews with the session's agent: the worker's.
		defaultReviewer={(draft) => draft.workerAgent}
		autoReviewDescription={t("settings.cloudProject.autoReviewDescription")} renderAgent={(props) => <CloudAgentPicker {...props} />}
		save={save} onSaveState={onSaveState} />;
}

/** The coder config the control plane stores on a project (config.coder). */
function coderConfigOf(project: CloudCpProject): CloudCpProjectCoderConfig | undefined {
	const coder = project.config?.coder;
	return coder && typeof coder === "object" ? (coder as CloudCpProjectCoderConfig) : undefined;
}

/** The project's Coder template, fixed at creation and shown read-only. */
export function CloudProjectCoderSettings({ project }: { project: CloudCpProject }) {
	const { t } = useTranslation();
	const coder = coderConfigOf(project);
	const templateId = coder?.templateId?.trim() ?? "";
	const { templates, isLoading: templatesLoading } = useCoderTemplates(project.orgId, templateId !== "");
	const template = templates.find((candidate) => candidate.id === templateId);
	const templateLabel = template ? template.displayName || template.name : templatesLoading ? "…" : templateId;
	// No project template only blocks sessions for a bring-your-own-Coder org
	// without an org-default template (the control plane's
	// coder_template_required). Otherwise the deployment or org default applies,
	// and non-Coder providers need no template at all.
	const orgCoder = useOrgCoderConfig();
	const orgCoderConfig = orgCoder.data as { templateId?: string; defaultTemplateId?: string } | null | undefined;
	const orgDefaultTemplateId = (orgCoderConfig?.templateId ?? orgCoderConfig?.defaultTemplateId ?? "").trim();
	const templateRequired = templateId === "" && orgCoderConfig != null && orgDefaultTemplateId === "";
	return <SettingsGroup title={t("settings.project.coderTitle")} grouped>
		<ProjectSettingsRow label={t("settings.project.coderTemplate")}>
			{templateId !== "" ? (
				<span className="settings-row-value" title={templateId}>{templateLabel}</span>
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
	</SettingsGroup>;
}

function CloudAgentPicker({ role, draft, value, disabled, onChange }: ProjectAgentPickerProps) {
	const { t } = useTranslation();
	// The agent is fixed in Cloud; show the one a reviewer actually runs with.
	const shown = value || (role === "reviewer" ? draft.workerAgent : "");
	return <SettingsOptionMenu aria-label={`${t(`settings.models.${role}Role`)} ${t("settings.project.agent").toLocaleLowerCase()}`}
		disabled={disabled} value={shown} placeholder={t("settings.cloudProject.sessionSelection")}
		options={[{ value: "", label: t(role === "reviewer" ? "settings.cloudProject.sessionAgent" : "settings.cloudProject.sessionSelection") }, ...CLOUD_AGENT_PROVIDERS.map((agent) => ({ value: agent, label: agentLabel(agent), icon: <AgentAvatar provider={agent} className="size-icon-lg" decorative /> }))]}
		triggerClassName="w-fit" menuClassName="settings-agent-menu-surface" menuItemClassName="settings-agent-menu-item"
		renderMenuItem={(option, selected) => <AgentSelectMenuItem agentId={option.value || undefined} label={option.label} selected={selected} />}
		onChange={onChange} />;
}

function toEditorDraft(values: CloudProjectSettingsDraft): ProjectSettingsDraft {
	return {
		displayName: values.displayName, defaultBranch: values.defaultBranch, autoReview: values.autoReview,
		sessionPrefix: values.sessionPrefix, intakeEnabled: false, intakeRepo: "", intakeAssignee: "",
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
	return { displayName: values.displayName, defaultBranch: values.defaultBranch, sessionPrefix: values.sessionPrefix, autoReview: values.autoReview, worker: role("worker"), orchestrator: role("orchestrator"), reviewer: role("reviewer") };
}
