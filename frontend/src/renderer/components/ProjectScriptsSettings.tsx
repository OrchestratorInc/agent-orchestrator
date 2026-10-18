import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { Fragment, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@aoagents/product-ui";
import type { components } from "../../api/schema";
import { cloudProjectsQueryKey, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { clearTerminateSessionState } from "../hooks/useTerminateSession";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { CloudCpProject } from "../lib/cloud-cp";
import { CODER_PROVIDER, canManageBilling, projectSandboxProvider } from "../lib/cloud-offering";
import { gitHubFullName, parseScriptSteps, projectAdditionalRepos, repoDirName, serializeScriptSteps, type ScriptStepDraft } from "../lib/cloud-project-repos";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { cloudProjectSettingsQueryKey } from "./CloudProjectSettingsForm";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { Button } from "./ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

type Project = components["schemas"]["Project"];
type ScriptKind = "setup" | "cleanup";

export function ProjectScriptsSettings({ projectId, onSaveState }: { projectId: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const query = useQuery({
		queryKey: ["project", projectId],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error(t("settings.project.degraded"));
			return data.project as Project;
		},
	});
	if (query.isLoading) return <p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	if (query.isError || !query.data) return <p role="alert" className="px-(--settings-row-inset) text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p>;
	const project = query.data;
	const scratch = project.kind === "scratch";
	const save = async (steps: ScriptSteps) => {
		// A local step always runs in the project's own workspace.
		const scripts = {
			postCreate: steps.postCreate.map((step) => step.command),
			...(steps.preRemove && { preRemove: steps.preRemove.map((step) => step.command) }),
		};
		const current = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: project.id } } });
		if (current.error) throw new Error(apiErrorMessage(current.error));
		if (current.data?.status !== "ok" || !current.data.project) throw new Error(t("settings.project.degraded"));
		const latest = current.data.project as Project;
		const { error } = await apiClient.PUT("/api/v1/projects/{id}", {
			params: { path: { id: project.id } },
			body: { displayName: latest.name, config: { ...latest.config, ...scripts } },
		});
		if (error) throw new Error(apiErrorMessage(error));
	};
	return <ScriptsEditor key={projectId} projectId={project.id} scratch={scratch} postCreate={localSteps(project.config?.postCreate)} preRemove={localSteps(project.config?.preRemove)}
		save={save} onSaveState={onSaveState} onSaved={() => {
			void client.invalidateQueries({ queryKey: ["project", projectId] });
			void client.invalidateQueries({ queryKey: workspaceQueryKey });
		}} />;
}

/**
 * A Cloud project's Setup and Cleanup commands: the same settings and editor as
 * a local project, stored on the Cloud project (config.postCreate and
 * config.preRemove) and run by the session's worker. Setup runs once per
 * session after the checkout; Cleanup runs before the workspace is deleted,
 * which today only a Coder workspace supports.
 */
export function CloudProjectScriptsSettings({ projectId, cloudOrgId, onSaveState }: { projectId: string; cloudOrgId: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { client, ready, baseUrl } = useCloudCp();
	const offering = useCloudOffering();
	const { org } = useCloudOrg();
	// Setup and Cleanup run in every member's later sessions, so only owners and
	// admins change them (the control plane enforces it too). A role the
	// renderer does not know yet leaves the decision to the server.
	const readOnly = org?.id === cloudOrgId && org.role !== undefined && !canManageBilling(org);
	const queryKey = cloudProjectSettingsQueryKey(baseUrl, cloudOrgId, projectId);
	const query = useQuery({
		queryKey, enabled: ready, retry: false,
		queryFn: ({ signal }) => client.getProject(cloudOrgId, projectId, { signal }).then((response) => response.project),
	});
	if (!ready) return <p role="alert" className="px-(--settings-row-inset) text-sm text-error">{t("settings.cloudProject.signIn")}</p>;
	if (query.isLoading) return <p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	if (query.isError || !query.data) return <p role="alert" className="px-(--settings-row-inset) text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p>;
	const project: CloudCpProject = query.data;
	// A step can run in any of the project's repositories: the main one (the
	// default) or one of the additional ones checked out beside it, named by the
	// folder it checks out into.
	const repositories = projectAdditionalRepos(project.config).map((repo) => ({ folder: repoDirName(repo.url), label: gitHubFullName(repo.url) ?? repo.url }));
	const save = async (steps: ScriptSteps) => {
		// Settings merge objects but replace arrays, so only the two lists are sent;
		// an empty list clears it. Every other config key stays as stored.
		const response = await client.updateProjectSettings(project.orgId, project.id, {
			config: { postCreate: serializeScriptSteps(steps.postCreate), preRemove: serializeScriptSteps(steps.preRemove ?? []) },
		});
		queryClient.setQueryData(queryKey, response.project);
	};
	return <ScriptsEditor key={`${cloudOrgId}/${projectId}`} scratch={false}
		postCreate={parseScriptSteps(project.config?.postCreate, project.repositoryUrl)} preRemove={parseScriptSteps(project.config?.preRemove, project.repositoryUrl)}
		repositories={repositories}
		cloud={{ cleanupRuns: projectSandboxProvider(project.config, offering) === CODER_PROVIDER, readOnly }}
		save={save} onSaveState={onSaveState} onSaved={() => { void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey }); }} />;
}

type ScriptSteps = { postCreate: ScriptStepDraft[]; preRemove?: ScriptStepDraft[] };

function localSteps(commands: string[] | undefined): ScriptStepDraft[] | undefined {
	return commands?.map((command) => ({ command, folder: "" }));
}

const emptyStep = (): ScriptStepDraft => ({ command: "", folder: "" });

/** The variables a Cloud project's Setup and Cleanup steps can use. */
const cloudScriptVariables = [
	["AO_WORKTREE_PATH", "settings.project.cloudVarFolder"],
	["AO_SESSION_ID", "settings.project.cloudVarSessionId"],
	["AO_SESSION_BRANCH", "settings.project.cloudVarBranch"],
	["AO_SESSION_KIND", "settings.project.cloudVarKind"],
	["AO_PROJECT_NAME", "settings.project.cloudVarProject"],
	["AO_REPOSITORY_URL", "settings.project.cloudVarRepository"],
] as const;

function ScriptsEditor({ projectId, scratch, postCreate, preRemove, repositories = [], cloud, save, onSaveState, onSaved }: {
	/** The local project, for retrying its cleanup; absent for a Cloud project. */
	projectId?: string;
	scratch: boolean;
	postCreate?: ScriptStepDraft[];
	preRemove?: ScriptStepDraft[];
	/** A Cloud project's additional repositories, by the folder each checks out into, which a step may run in instead of the main one. */
	repositories?: { folder: string; label: string }[];
	/** Set for a Cloud project: the hints describe its session workspace, and cleanupRuns says whether Cleanup runs on its provider. */
	cloud?: { cleanupRuns: boolean; readOnly?: boolean };
	save: (scripts: ScriptSteps) => Promise<void>;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
	onSaved: () => void;
}) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const [activeTab, setActiveTab] = useState<ScriptKind>("setup");
	const [drafts, setDrafts] = useState<Record<ScriptKind, ScriptStepDraft[]>>(() => ({
		setup: postCreate?.length ? postCreate.map((step) => ({ ...step })) : [emptyStep()],
		cleanup: preRemove?.length ? preRemove.map((step) => ({ ...step })) : [emptyStep()],
	}));
	// A step keeps its folder as stored, even one that is not among the project's
	// repositories any more (it is listed by folder name), so saving never moves
	// a step into another repository on its own.
	const commands: ScriptSteps = {
		postCreate: drafts.setup.filter((step) => step.command.trim()).map((step) => ({ command: step.command, folder: step.folder })),
		...(!scratch && { preRemove: drafts.cleanup.filter((step) => step.command.trim()).map((step) => ({ command: step.command, folder: step.folder })) }),
	};
	const folderOptions = (current: string) => {
		const options = [
			{ value: "", label: t("settings.project.stepMainRepository", { defaultValue: "Main repository" }) },
			...repositories.map((repo) => ({ value: repo.folder, label: repo.label })),
		];
		if (current !== "" && !repositories.some((repo) => repo.folder === current)) {
			options.push({ value: current, label: t("settings.project.stepUnknownFolder", { defaultValue: "{{folder}} (not a project repository)", folder: current }) });
		}
		return options;
	};
	const showRepositoryMenu = repositories.length > 0 || [...drafts.setup, ...drafts.cleanup].some((step) => step.folder !== "");
	const [saved, setSaved] = useState(() => JSON.stringify(commands));
	const [savedAt, setSavedAt] = useState(false);
	const dirty = JSON.stringify(commands) !== saved;
	const mutation = useMutation({
		mutationFn: async (scripts: ScriptSteps) => {
			await save(scripts);
			return scripts;
		},
		onSuccess: (scripts) => { setSaved(JSON.stringify(scripts)); setSavedAt(true); onSaved(); },
	});
	const retry = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/cleanup", { params: { query: { project: projectId ?? "" } } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: (result) => {
			for (const sessionId of [...result.cleaned, ...result.alreadyGone]) clearTerminateSessionState(client, sessionId);
			onSaved();
		},
	});
	useEffect(() => {
		onSaveState?.({
			// This form saves only on "Save changes", so unsaved edits are reported as
			// dirty but never as the autosave "pending" phase, which the settings
			// sidebar shows as an endless "Saving" spinner.
			phase: mutation.isError ? "failed" : mutation.isPending ? "saving" : savedAt && !dirty ? "saved" : "idle",
			dirty,
			requestPending: mutation.isPending,
			error: mutation.error instanceof Error ? mutation.error.message : undefined,
		});
	}, [dirty, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt]);
	const locked = mutation.isPending || retry.isPending || cloud?.readOnly === true;
	const updateSteps = (kind: ScriptKind, steps: ScriptStepDraft[]) => {
		setDrafts((current) => ({ ...current, [kind]: steps }));
		setSavedAt(false);
	};
	return <form id="project-settings-form" className="flex min-h-full shrink-0 flex-col gap-3" onSubmit={(event) => { event.preventDefault(); mutation.mutate(commands); }}>
		<Tabs className="flex flex-1 flex-col gap-3" value={activeTab} onValueChange={(value) => setActiveTab(value as ScriptKind)}>
			<TabsList aria-label={t("settings.project.scripts")} className="shrink-0 self-start">
				<TabsTrigger value="setup">{t("settings.project.scriptSetup")}</TabsTrigger>
				<TabsTrigger value="cleanup">{t("settings.project.scriptCleanup")}</TabsTrigger>
			</TabsList>
			{(["setup", "cleanup"] as const).map((kind) => <TabsContent key={kind} value={kind}>
				{kind === "cleanup" && scratch ? <p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("settings.project.cleanupScratchUnavailable")}</p> : <ProjectSettingsSection title={t(kind === "setup" ? "settings.project.scriptSetup" : "settings.project.scriptCleanup")} titleHidden grouped>
					<div className="settings-content-row flex flex-col gap-4">
						{cloud ? <div className="space-y-1 text-pretty text-xs leading-4 text-settings-muted">
							{cloud.readOnly && <p role="note" className="text-settings-label">{t("settings.project.cloudScriptsAdminOnly")}</p>}
							<p>{t(kind === "setup" ? "settings.project.cloudSetupHint" : "settings.project.cloudCleanupHint")}</p>
							<p>{t(kind === "setup" ? "settings.project.cloudSetupShellHint" : "settings.project.cloudCleanupShellHint")}</p>
							{kind === "cleanup" && !cloud.cleanupRuns && <p role="note" className="text-settings-label">{t("settings.project.cloudCleanupCoderOnly")}</p>}
						</div> : <div className="space-y-1 text-pretty text-xs leading-4 text-settings-muted">
							<p>{t(kind === "setup" ? scratch ? "settings.project.setupHintScratch" : "settings.project.setupHintGit" : "settings.project.cleanupHint")}</p>
							<p>{t(`settings.project.${kind}ShellHint`)}</p>
							<p>{t("settings.project.setupPlatformHint")}</p>
						</div>}
						{!scratch && <details className="text-xs text-settings-muted">
							<summary className="cursor-pointer font-medium text-settings-label">{t(cloud ? "settings.project.cloudVariables" : `settings.project.${kind}Paths`)}</summary>
							{cloud ? <dl className="mt-2 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1">
								{cloudScriptVariables.map(([name, key]) => <Fragment key={name}><dt><code>{"$" + name}</code></dt><dd>{t(key)}</dd></Fragment>)}
							</dl> : <div className="mt-2 space-y-1">
								<p><code>{"AO_SOURCE_TREE_PATH"}</code>: {t(`settings.project.${kind}SourcePath`)}</p>
								<p><code>{"AO_WORKTREE_PATH"}</code>: {t(`settings.project.${kind}WorktreePath`)}</p>
							</div>}
						</details>}
						<div className="space-y-4">
							{drafts[kind].map((step, index) => <div key={index}>
								<div className="mb-1 flex items-center justify-between gap-2">
									<label className="text-sm font-medium text-settings-label" htmlFor={`${kind}-step-${index}`}>{t(`settings.project.${kind}Step`, { number: index + 1 })}</label>
									<div className="flex min-w-0 items-center gap-1">
										{cloud && showRepositoryMenu ? <SettingsOptionMenu
											aria-label={t("settings.project.stepRepository", { defaultValue: "Repository for step {{number}}", number: index + 1 })}
											disabled={locked}
											value={step.folder}
											options={folderOptions(step.folder)}
											triggerClassName="w-fit max-w-64"
											onChange={(folder) => updateSteps(kind, drafts[kind].map((item, i) => i === index ? { ...item, folder } : item))}
										/> : null}
										<button aria-label={t(kind === "setup" ? "settings.project.removeSetupStep" : "settings.project.removeCleanupStep", { number: index + 1 })} className="rounded p-1 text-settings-muted hover:text-error focus-visible:ring-2 focus-visible:ring-ring" disabled={locked} onClick={() => updateSteps(kind, drafts[kind].length === 1 ? [emptyStep()] : drafts[kind].filter((_, i) => i !== index))} type="button"><Trash2 aria-hidden="true" size={16} /></button>
									</div>
								</div>
								<textarea className="settings-field-control min-h-24 w-full px-4 py-3 font-mono text-xs" disabled={locked} id={`${kind}-step-${index}`} spellCheck={false} value={step.command} onChange={(event) => updateSteps(kind, drafts[kind].map((item, i) => i === index ? { ...item, command: event.target.value } : item))} />
							</div>)}
							{cloud && showRepositoryMenu ? <p className="text-xs text-settings-muted">{t("settings.project.stepRepositoryHint", { defaultValue: "Each step runs in the repository chosen for it, after every repository is checked out." })}</p> : null}
							<Button className="gap-1.5 self-start text-settings-muted hover:text-settings-label" disabled={locked} onClick={() => updateSteps(kind, [...drafts[kind], emptyStep()])} size="sm" type="button" variant="ghost"><Plus aria-hidden="true" size={16} />{t(kind === "setup" ? "settings.project.addSetupStep" : "settings.project.addCleanupStep")}</Button>
						</div>
					</div>
					{kind === "cleanup" && !cloud && <div className="settings-content-row space-y-2">
						<p className="text-xs text-settings-muted">{t("settings.project.cleanupRetryHint")}</p>
						<Button disabled={dirty || mutation.isPending || retry.isPending} onClick={() => retry.mutate()} type="button" variant="ghost" size="sm" className="text-settings-muted hover:text-settings-label">{t("settings.project.retryCleanup")}</Button>
						{retry.isError && <p role="alert" className="text-sm text-error">{retry.error instanceof Error ? retry.error.message : t("settings.project.cleanupRetryFailed")}</p>}
						{retry.data && <div role="status" className="space-y-1 text-sm text-settings-muted">
							<p>{t("settings.project.cleanupRetryResult", { cleaned: retry.data.cleaned.length, skipped: retry.data.skipped.length })}</p>
							{retry.data.skipped.map((item) => <p key={item.sessionId}>{item.sessionId}: {item.reason}</p>)}
						</div>}
					</div>}
				</ProjectSettingsSection>}
			</TabsContent>)}
		</Tabs>
		{mutation.isError && <p role="alert" className="px-(--settings-row-inset) text-xs text-error">{mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed")}</p>}
		<div className="flex items-center justify-end gap-3 border-t border-border pt-4">
			<Button disabled={!dirty || locked} type="submit">{t("settings.project.saveChanges")}</Button>
		</div>
	</form>;
}
