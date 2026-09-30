import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ProjectSettingsFormView, ProjectSettingsInputRow, ProjectSettingsSection, ProjectSettingsValueRow } from "@aoagents/product-ui";
import { Pencil } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import { CLOUD_AGENT_PROVIDERS, connectedCredentialType, credentialModelScope } from "../lib/cloud-agents";
import { agentLabel } from "../lib/agent-options";
import type { CloudCpProject, CloudCpProjectAgentConfig } from "../lib/cloud-cp";
import { cloudProjectSettingsDraft, cloudProjectSettingsPatch, emptyCloudAgentConfig, type CloudProjectRoleDraft, type CloudProjectSettingsDraft } from "../lib/cloud-project-settings";
import { AgentAvatar } from "./AgentAvatar";
import { ProductExternalLink } from "./ProductExternalLink";
import type { ProjectSettingsSaveState, ProjectSettingsSection as SettingsSection } from "./ProjectSettingsForm";
import { AgentSelectMenuItem } from "./settings/AgentSelectMenuItem";
import { AgentModelField, ProjectAgentRoleHeader, ProjectAgentRoleRow, ProjectAutoReviewToggle } from "./settings/ProjectAgentRoleControls";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { Button } from "./ui/button";

export const cloudProjectSettingsQueryKey = (baseUrl: string, orgId: string, projectId: string) => ["cloud-project-settings", baseUrl, orgId, projectId] as const;
const roles = ["worker", "orchestrator", "reviewer"] as const;
type CloudRole = typeof roles[number];

export function CloudProjectSettingsForm({ projectId, cloudOrgId, section = "general", onSaveState }: {
	projectId: string;
	cloudOrgId: string;
	section?: SettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const query = useQuery({
		queryKey: cloudProjectSettingsQueryKey(baseUrl, cloudOrgId, projectId),
		enabled: ready,
		retry: false,
		queryFn: ({ signal }) => client.getProject(cloudOrgId, projectId, { signal }).then((response) => response.project),
	});
	const loadError = !ready ? t("settings.cloudProject.signIn") : query.isError
		? query.error instanceof Error ? query.error.message : t("settings.project.loadFailed") : undefined;
	useEffect(() => {
		if (loadError) onSaveState?.({ phase: "failed", error: loadError });
	}, [loadError, onSaveState]);
	if (loadError) return <p className="text-sm text-error" role="alert">{loadError}</p>;
	if (!query.data) return <p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	return <CloudSettingsBody key={`${cloudOrgId}/${projectId}`} project={query.data} section={section} onSaveState={onSaveState} />;
}

function CloudSettingsBody({ project, section, onSaveState }: {
	project: CloudCpProject;
	section: SettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
}) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const connections = useProviderConnections(project.orgId);
	const opencodeCredential = connectedCredentialType(connections.data, "opencode");
	const queryClient = useQueryClient();
	const [draft, setDraft] = useState(() => cloudProjectSettingsDraft(project));
	const saved = useRef(draft);
	const failedKey = useRef<string | undefined>(undefined);
	const [error, setError] = useState<string>();
	const [didSave, setDidSave] = useState(false);
	const [tuningValidity, setTuningValidity] = useState({ worker: true, orchestrator: true, reviewer: true });
	const mutation = useMutation({
		mutationFn: (values: CloudProjectSettingsDraft) => client.updateProjectSettings(project.orgId, project.id, cloudProjectSettingsPatch(saved.current, values)),
		onSuccess: (response, values) => {
			const normalized = cloudProjectSettingsDraft(response.project);
			saved.current = normalized;
			failedKey.current = undefined;
			setDraft((current) => JSON.stringify(current) === JSON.stringify(values) ? normalized : current);
			setDidSave(true);
			queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), response.project);
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		},
		onError: (cause, values) => {
			failedKey.current = JSON.stringify(values);
			setError(cause instanceof Error ? cause.message : t("settings.project.saveFailed"));
		},
	});
	const dirty = JSON.stringify(draft) !== JSON.stringify(saved.current);
	const save = useCallback(() => {
		if (mutation.isPending || !dirty) return;
		if (!draft.displayName.trim() || [...draft.displayName.trim()].length > 120 || !draft.defaultBranch.trim() || [...draft.defaultBranch.trim()].length > 255) {
			setError(t("settings.cloudProject.identityValidation"));
			return;
		}
		if (roles.some((role) => !tuningValidity[role])) {
			setError(t("settings.project.tuningInvalid"));
			return;
		}
		setError(undefined);
		setDidSave(false);
		mutation.mutate(draft);
	}, [dirty, draft, mutation.isPending, mutation.mutate, t, tuningValidity]);
	useEffect(() => {
		if (!dirty || mutation.isPending || JSON.stringify(draft) === failedKey.current) return;
		const timeout = window.setTimeout(save, 650);
		return () => window.clearTimeout(timeout);
	}, [dirty, draft, mutation.isPending, save]);
	useEffect(() => {
		onSaveState?.({
			phase: mutation.isPending ? "saving" : error ? "failed" : dirty ? "pending" : didSave ? "saved" : "idle",
			dirty, requestPending: mutation.isPending, error,
		});
	}, [didSave, dirty, error, mutation.isPending, onSaveState]);
	useEffect(() => {
		if (!didSave) return;
		const timeout = window.setTimeout(() => setDidSave(false), 1800);
		return () => window.clearTimeout(timeout);
	}, [didSave]);
	const updateRoleConfig = (role: CloudRole, config: Partial<CloudCpProjectAgentConfig>) => {
		setDraft((current) => ({ ...current, [role]: { ...current[role], agentConfig: { ...current[role].agentConfig, ...config } } }));
	};
	return (
		<ProjectSettingsFormView id="project-settings-form" className="project-settings-form gap-5" onSubmit={save}>
			<fieldset disabled={mutation.isPending} className="flex min-w-0 flex-col gap-5">
				{section === "general" ? <>
					<ProjectSettingsSection title={t("settings.project.details")} grouped>
						<ProjectSettingsInputRow id="project-name" label={t("settings.project.name")} editLabel={t("settings.field.edit", { label: t("settings.project.name") })} editIcon={<Pencil className="settings-inline-edit-icon" aria-hidden="true" />} value={draft.displayName} onChange={(displayName) => setDraft((value) => ({ ...value, displayName }))} />
						<ProjectSettingsValueRow label={t("settings.project.id")} value={project.id} />
						<ProjectSettingsValueRow label={t("settings.project.repo")} value={project.repositoryUrl} href={project.repositoryUrl} externalLink={ProductExternalLink} />
					</ProjectSettingsSection>
					<ProjectSettingsSection title={t("settings.project.worktrees")} grouped>
						<ProjectSettingsInputRow id="project-default-branch" label={t("settings.project.defaultBranch")} editLabel={t("settings.field.edit", { label: t("settings.project.defaultBranch") })} editIcon={<Pencil className="settings-inline-edit-icon" aria-hidden="true" />} value={draft.defaultBranch} onChange={(defaultBranch) => setDraft((value) => ({ ...value, defaultBranch }))} />
					</ProjectSettingsSection>
					<ProjectSettingsSection title={t("settings.project.pullRequests")} grouped>
						<ProjectAutoReviewToggle checked={draft.autoReview} onCheckedChange={(autoReview) => setDraft((value) => ({ ...value, autoReview }))} />
					</ProjectSettingsSection>
				</> : <ProjectSettingsSection title={t("settings.project.agents")} titleHidden grouped>
					<ProjectAgentRoleHeader />
					{roles.map((role) => <ProjectAgentRoleRow key={role} label={t(`settings.models.${role}Role`)}
						agent={<CloudRoleAgent role={role} value={draft[role]} onChange={(agent) => setDraft((current) => ({ ...current, [role]: { agent, agentConfig: emptyCloudAgentConfig() } }))} />}
						model={<AgentModelField role={role} agentId={draft[role].agent}
							// Model discovery is agent-scoped; Cloud project IDs never go to local project APIs.
							projectId={draft[role].agent === "opencode" && opencodeCredential ? credentialModelScope(opencodeCredential) : ""}
							model={draft[role].agentConfig.model} mode={draft[role].agentConfig.mode} effort={draft[role].agentConfig.effort} allowCustomFallback
							onModelChange={(model) => updateRoleConfig(role, { model })}
							onModeChange={(mode) => { if (mode === "" || mode === "plan" || mode === "ask") updateRoleConfig(role, { mode }); }}
							onEffortChange={(effort) => { if (effort === "" || effort === "low" || effort === "medium" || effort === "high" || effort === "xhigh" || effort === "max") updateRoleConfig(role, { effort }); }}
							onValidityChange={(valid) => setTuningValidity((current) => current[role] === valid ? current : { ...current, [role]: valid })} />}
					/>)}
					<div className="grid grid-cols-3 gap-3 border-t border-border/60 pt-4">
						{roles.map((role) => <CloudRolePermissions key={role} role={role} value={draft[role]} onChange={(permissions) => updateRoleConfig(role, { permissions })} />)}
					</div>
				</ProjectSettingsSection>}
			</fieldset>
			{error && !onSaveState && <p role="alert" className="text-sm text-error">{error}</p>}
			{!onSaveState && <div className="flex justify-end"><Button type="submit" disabled={!dirty || mutation.isPending}>{mutation.isPending ? t("settings.project.saving") : t("files.saveFile")}</Button></div>}
		</ProjectSettingsFormView>
	);
}

function CloudRoleAgent({ role, value, onChange }: {
	role: CloudRole;
	value: CloudProjectRoleDraft;
	onChange: (agent: CloudProjectRoleDraft["agent"]) => void;
}) {
	const { t } = useTranslation();
	return <SettingsOptionMenu aria-label={`${t(`settings.models.${role}Role`)} ${t("settings.project.agent").toLocaleLowerCase()}`}
		value={value.agent} placeholder={t("settings.cloudProject.sessionSelection")}
		options={[...(role === "reviewer" ? [{ value: "" as const, label: t("settings.cloudProject.sessionAgent") }] : []), ...CLOUD_AGENT_PROVIDERS.map((agent) => ({ value: agent, label: agentLabel(agent), icon: <AgentAvatar provider={agent} className="size-icon-lg" decorative /> }))]}
		triggerClassName="w-full justify-between" menuClassName="settings-agent-menu-surface" menuItemClassName="settings-agent-menu-item"
		renderMenuItem={(option, selected) => <AgentSelectMenuItem agentId={option.value || undefined} label={option.label} selected={selected} />}
		onChange={onChange} />;
}

function CloudRolePermissions({ role, value, onChange }: {
	role: CloudRole;
	value: CloudProjectRoleDraft;
	onChange: (permissions: Required<CloudCpProjectAgentConfig>["permissions"]) => void;
}) {
	const { t } = useTranslation();
	const label = t("settings.project.roleApproval", { role: t(`settings.models.${role}Role`) });
	const permissions: Array<Required<CloudCpProjectAgentConfig>["permissions"]> = ["", "default", "auto", ...(value.agent === "opencode" ? [] : ["accept-edits" as const]), "bypass-permissions"];
	return <div className="min-w-0 space-y-1.5">
		<span className="text-xs text-settings-muted">{label}</span>
		<SettingsOptionMenu aria-label={label} value={value.agentConfig.permissions} disabled={value.agent === ""} triggerClassName="w-full justify-between"
			options={permissions.map((permission) => ({ value: permission, label: permission === "" ? t("settings.cloudProject.sessionPolicy") : permission === "default" ? t("settings.cloudProject.agentDefault") : permission === "bypass-permissions" ? t("settings.project.permissionBypass") : permission === "accept-edits" ? t("settings.project.permissionAcceptEdits") : t("settings.project.permissionAuto") }))}
			onChange={onChange} />
	</div>;
}
