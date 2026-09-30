import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ProjectSettingsFormView, ProjectSettingsRow, ProjectSettingsSection, ProjectSettingsValueRow } from "@aoagents/product-ui";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import { agentLabel } from "../lib/agent-options";
import type { CloudCpProject, CloudCpProjectAgentConfig } from "../lib/cloud-cp";
import { cloudProjectSettingsDraft, cloudProjectSettingsPatch, emptyCloudAgentConfig, type CloudProjectRoleDraft, type CloudProjectSettingsDraft } from "../lib/cloud-project-settings";
import type { ProjectSettingsSaveState, ProjectSettingsSection as SettingsSection } from "./ProjectSettingsForm";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { Button } from "./ui/button";
import { Switch } from "./ui/switch";

export const cloudProjectSettingsQueryKey = (baseUrl: string, orgId: string, projectId: string) => ["cloud-project-settings", baseUrl, orgId, projectId] as const;

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
	const queryClient = useQueryClient();
	const [draft, setDraft] = useState(() => cloudProjectSettingsDraft(project));
	const saved = useRef(draft);
	const [error, setError] = useState<string>();
	const [didSave, setDidSave] = useState(false);
	const mutation = useMutation({
		mutationFn: (values: CloudProjectSettingsDraft) => client.updateProjectSettings(project.orgId, project.id, cloudProjectSettingsPatch(saved.current, values)),
		onSuccess: (response) => {
			setDraft(cloudProjectSettingsDraft(response.project));
			saved.current = cloudProjectSettingsDraft(response.project);
			setDidSave(true);
			queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), response.project);
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		},
		onError: (cause) => setError(cause instanceof Error ? cause.message : t("settings.project.saveFailed")),
	});
	const dirty = JSON.stringify(draft) !== JSON.stringify(saved.current);
	useEffect(() => {
		onSaveState?.({
			phase: mutation.isPending ? "saving" : error ? "failed" : didSave && !dirty ? "saved" : "idle",
			dirty, requestPending: mutation.isPending, error,
		});
	}, [didSave, dirty, error, mutation.isPending, onSaveState]);
	const save = () => {
		if (mutation.isPending || !dirty) return;
		if (!draft.displayName.trim() || [...draft.displayName.trim()].length > 120 || !draft.defaultBranch.trim() || [...draft.defaultBranch.trim()].length > 255) {
			setError(t("settings.cloudProject.identityValidation"));
			return;
		}
		setError(undefined);
		setDidSave(false);
		mutation.mutate(draft);
	};
	const updateRole = (role: "worker" | "orchestrator" | "reviewer", value: CloudProjectRoleDraft) => {
		setDidSave(false);
		setDraft((current) => ({ ...current, [role]: value }));
	};
	return (
		<ProjectSettingsFormView id="project-settings-form" className="project-settings-form" onSubmit={save}>
			<fieldset disabled={mutation.isPending} className="flex min-w-0 flex-col gap-5">
				{section === "general" ? <ProjectSettingsSection title={t("settings.cloudProject.title")} grouped>
					<ProjectSettingsValueRow label={t("settings.project.id")} value={project.id} />
					<ProjectSettingsValueRow label={t("settings.project.repo")} value={project.repositoryUrl} />
					<ProjectSettingsRow label={t("settings.project.name")}><input className="settings-inline-edit-input" aria-label={t("settings.project.name")} value={draft.displayName} maxLength={120} onChange={(event) => setDraft((value) => ({ ...value, displayName: event.target.value }))} /></ProjectSettingsRow>
					<ProjectSettingsRow label={t("settings.project.defaultBranch")}><input className="settings-inline-edit-input" aria-label={t("settings.project.defaultBranch")} value={draft.defaultBranch} maxLength={255} onChange={(event) => setDraft((value) => ({ ...value, defaultBranch: event.target.value }))} /></ProjectSettingsRow>
				</ProjectSettingsSection> : <>
					<CloudRoleSettings role="worker" value={draft.worker} onChange={(value) => updateRole("worker", value)} />
					<CloudRoleSettings role="orchestrator" value={draft.orchestrator} onChange={(value) => updateRole("orchestrator", value)} />
					<CloudRoleSettings role="reviewer" value={draft.reviewer} onChange={(value) => updateRole("reviewer", value)} />
					<ProjectSettingsRow label={t("settings.project.autoReviewToggle")}><Switch aria-label={t("settings.project.autoReviewToggle")} checked={draft.autoReview} onCheckedChange={(autoReview) => setDraft((value) => ({ ...value, autoReview }))} /></ProjectSettingsRow>
				</>}
			</fieldset>
			{error && !onSaveState && <p role="alert" className="text-sm text-error">{error}</p>}
			<div className="flex justify-end"><Button type="submit" disabled={!dirty || mutation.isPending}>{mutation.isPending ? t("settings.project.saving") : t("files.saveFile")}</Button></div>
		</ProjectSettingsFormView>
	);
}

function CloudRoleSettings({ role, value, onChange }: {
	role: "worker" | "orchestrator" | "reviewer";
	value: CloudProjectRoleDraft;
	onChange: (value: CloudProjectRoleDraft) => void;
}) {
	const { t } = useTranslation();
	const label = t(`settings.models.${role}Role`);
	const reviewer = role === "reviewer";
	const fieldLabel = (field: string) => `${label} ${field.toLocaleLowerCase()}`;
	const updateConfig = (config: Partial<CloudCpProjectAgentConfig>) => onChange({ ...value, agentConfig: { ...value.agentConfig, ...config } });
	const agents = CLOUD_AGENT_PROVIDERS.map((agent) => ({ value: agent, label: agentLabel(agent) }));
	const efforts: Array<Required<CloudCpProjectAgentConfig>["effort"]> = value.agent === "claude-code" ? ["", "low", "medium", "high", "max"] : ["", "low", "medium", "high", "xhigh", "max"];
	const permissions: Array<Required<CloudCpProjectAgentConfig>["permissions"]> = ["", "default", "auto", ...(value.agent === "opencode" ? [] : ["accept-edits" as const]), "bypass-permissions"];
	return <ProjectSettingsSection title={label} grouped>
		<ProjectSettingsRow label={t("settings.project.agent")}><SettingsOptionMenu aria-label={fieldLabel(t("settings.project.agent"))} value={value.agent} placeholder={t("settings.cloudProject.sessionSelection")} options={[...(reviewer ? [{ value: "" as const, label: t("settings.cloudProject.sessionAgent") }] : []), ...agents]} onChange={(agent) => onChange({ agent, agentConfig: emptyCloudAgentConfig() })} /></ProjectSettingsRow>
		<fieldset disabled={value.agent === ""}>
			<ProjectSettingsRow label={t("newTask.model")}><input aria-label={t(`settings.models.${role}Model`)} className="settings-inline-edit-input" value={value.agentConfig.model} placeholder={reviewer && value.agent === "" ? t("settings.cloudProject.sessionModel") : t("settings.cloudProject.agentDefault")} maxLength={500} onChange={(event) => updateConfig({ model: event.target.value })} /></ProjectSettingsRow>
			{(value.agent === "codex" || value.agent === "claude-code") && <ProjectSettingsRow label={t("settings.models.effort")}><SettingsOptionMenu aria-label={fieldLabel(t("settings.models.effort"))} value={value.agentConfig.effort} options={efforts.map((effort) => ({ value: effort, label: effort || t("settings.cloudProject.agentDefault") }))} onChange={(effort) => updateConfig({ effort })} /></ProjectSettingsRow>}
			{value.agent === "cursor" && <ProjectSettingsRow label={t("settings.cloudProject.mode")}><SettingsOptionMenu aria-label={t(`settings.models.${role}Mode`)} value={value.agentConfig.mode} options={[{ value: "", label: t("settings.project.agent") }, { value: "plan", label: t("settings.cloudProject.plan") }, { value: "ask", label: t("settings.cloudProject.ask") }]} onChange={(mode) => updateConfig({ mode })} /></ProjectSettingsRow>}
			<ProjectSettingsRow label={t("settings.cloudProject.permissions")}><SettingsOptionMenu aria-label={fieldLabel(t("settings.cloudProject.permissions"))} value={value.agentConfig.permissions} options={permissions.map((permission) => ({ value: permission, label: permission === "" ? t("settings.cloudProject.sessionPolicy") : permission === "default" ? t("settings.cloudProject.agentDefault") : permission === "bypass-permissions" ? t("settings.project.permissionBypass") : permission === "accept-edits" ? t("settings.project.permissionAcceptEdits") : t("settings.project.permissionAuto") }))} onChange={(permissions) => updateConfig({ permissions })} /></ProjectSettingsRow>
		</fieldset>
	</ProjectSettingsSection>;
}
