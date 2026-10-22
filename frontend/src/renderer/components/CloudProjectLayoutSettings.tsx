import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudGitHubAppRepositories } from "../hooks/useCloudGitHubAppRepositories";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import type { CloudCpAgentStartsIn, CloudCpProject, CloudCpProjectSettingsRequest } from "../lib/cloud-cp";
import { CODER_PROVIDER, canManageBilling, projectSandboxProvider } from "../lib/cloud-offering";
import {
	additionalReposProblem,
	gitHubFullName,
	isStandaloneCloudProject,
	MAX_PROJECT_RULES_BYTES,
	normalizeWorkingDirectory,
	projectAdditionalRepos,
	projectAgentStartsIn,
	projectConfigText,
	repoDirName,
	sameRepository,
	scriptStepFolders,
	utf8Bytes,
	workingDirectoryProblem,
} from "../lib/cloud-project-repos";
import { CloudAdditionalReposEditor } from "./CloudAdditionalReposEditor";
import { cloudProjectSettingsQueryKey } from "./CloudProjectSettingsForm";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { SettingsSaveFooter as SaveFooter } from "./settings/SettingsSaveFooter";
import { Input } from "./ui/input";

type LoadedProps = { project: CloudCpProject; readOnly: boolean; onSaveState?: (state: ProjectSettingsSaveState) => void };

/** Loads a Cloud project for one of its settings pages, with the same loading and error states as Scripts. */
function CloudProjectSettingsPage({ projectId, cloudOrgId, onSaveState, children }: {
	projectId: string;
	cloudOrgId: string;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
	children: (props: LoadedProps) => ReactNode;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	// Like Setup and Cleanup, these shape every member's later sessions, so only
	// owners and admins change them. A role the renderer does not know yet
	// leaves the decision to the server.
	const readOnly = org?.id === cloudOrgId && org.role !== undefined && !canManageBilling(org);
	const query = useQuery({
		queryKey: cloudProjectSettingsQueryKey(baseUrl, cloudOrgId, projectId), enabled: ready, retry: false,
		queryFn: ({ signal }) => client.getProject(cloudOrgId, projectId, { signal }).then((response) => response.project),
	});
	if (!ready) return <p role="alert" className="text-sm text-error">{t("settings.cloudProject.signIn")}</p>;
	if (query.isLoading) return <p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	if (query.isError || !query.data) return <p role="alert" className="text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p>;
	return <>{children({ project: query.data, readOnly, onSaveState })}</>;
}

/**
 * Saves one settings page's patch on "Save changes" and reports its state to
 * the dialog the way the Scripts page does.
 */
function useSettingsPageSave(project: CloudCpProject, dirty: boolean, unsaveable: boolean, onSaveState?: (state: ProjectSettingsSaveState) => void) {
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const [savedAt, setSavedAt] = useState(false);
	const mutation = useMutation({
		mutationFn: (patch: CloudCpProjectSettingsRequest) => client.updateProjectSettings(project.orgId, project.id, patch),
		onSuccess: (response) => {
			queryClient.setQueryData(cloudProjectSettingsQueryKey(baseUrl, project.orgId, project.id), response.project);
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
			setSavedAt(true);
		},
	});
	useEffect(() => {
		onSaveState?.({
			phase: mutation.isError ? "failed" : mutation.isPending ? "saving" : savedAt && !dirty ? "saved" : "idle",
			dirty,
			requestPending: mutation.isPending,
			// An invalid draft cannot be saved, so closing the dialog drops it
			// instead of waiting for a save that never starts.
			unsaveable: dirty && unsaveable,
			error: mutation.error instanceof Error ? mutation.error.message : undefined,
		});
	}, [dirty, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt, unsaveable]);
	return { mutation, edited: () => setSavedAt(false) };
}

function Field({ label, htmlFor, hint, children }: { label: string; htmlFor?: string; hint?: ReactNode; children: ReactNode }) {
	return <div className="space-y-2">
		<label className="block text-sm font-medium text-settings-label" htmlFor={htmlFor}>{label}</label>
		{children}
		{hint ? <div className="text-pretty text-xs leading-4 text-settings-muted">{hint}</div> : null}
	</div>;
}


/** The control plane's reason for each working directory problem (domain.ValidateCoderWorkingDirectory). */
function workingDirectoryMessage(t: TFunction, problem: ReturnType<typeof workingDirectoryProblem>): string {
	switch (problem) {
		case "length":
			return t("settings.cloudProject.workingDirectoryTooLong");
		case "characters":
			return t("settings.cloudProject.workingDirectoryCharacters");
		case "segments":
			return t("settings.cloudProject.workingDirectorySegments");
		case "aoState":
			return t("settings.cloudProject.workingDirectoryAOState");
		case "standaloneRepo":
			return t("settings.cloudProject.workingDirectoryStandaloneRepo");
		case "placeholder":
			return t("settings.cloudProject.workingDirectoryPlaceholderOnly");
		default:
			return t("settings.cloudProject.workingDirectoryInvalid");
	}
}

/**
 * A Cloud project's repositories and layout: the main repository (fixed), the
 * additional ones every session clones beside it, where the agent starts, and
 * (bring-your-own Coder) the folder sessions work in.
 */
export function CloudProjectRepositoriesSettings(props: { projectId: string; cloudOrgId: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	return <CloudProjectSettingsPage {...props}>{(loaded) => <RepositoriesForm key={`${props.cloudOrgId}/${props.projectId}`} {...loaded} />}</CloudProjectSettingsPage>;
}

function RepositoriesForm({ project, readOnly, onSaveState }: LoadedProps) {
	const { t } = useTranslation();
	const offering = useCloudOffering();
	const standalone = isStandaloneCloudProject(project);
	const storedRepos = projectAdditionalRepos(project.config);
	const [extras, setExtras] = useState<string[]>(() => storedRepos.map((repo) => repo.url));
	const [startsIn, setStartsIn] = useState<CloudCpAgentStartsIn>(() => projectAgentStartsIn(project.config));
	const [workingDirectory, setWorkingDirectory] = useState(() => projectConfigText(project.config, "workingDirectory"));
	const [saved, setSaved] = useState(() => ({ extras: storedRepos.map((repo) => repo.url), startsIn: projectAgentStartsIn(project.config), workingDirectory: normalizeWorkingDirectory(projectConfigText(project.config, "workingDirectory")) }));
	const filledExtras = extras.filter((url) => url !== "");
	const reposChanged = JSON.stringify(filledExtras) !== JSON.stringify(saved.extras);
	const dirty = reposChanged || startsIn !== saved.startsIn || normalizeWorkingDirectory(workingDirectory) !== saved.workingDirectory;
	const repos = useCloudGitHubAppRepositories(project.orgId, !standalone);
	// The working folder is the folder a session run as the workspace user
	// adopts, and, once the control plane checks ao-worker sessions out there
	// (offering.coderWorkerWorkingFolder), where any Coder session checks the
	// main repository out; show it for a bring-your-own Coder org, or while a
	// value is stored so it can be cleared.
	const orgOwnsCoder = useOrgCoderConfig(offering.hasCoder, project.orgId).data != null;
	const showWorkingDirectory = (orgOwnsCoder && projectSandboxProvider(project.config, offering) === CODER_PROVIDER) || saved.workingDirectory !== "";
	// A Setup or Cleanup step runs in its repository's folder, so a repository a
	// step still uses cannot be removed until that step is moved or deleted (the
	// control plane refuses it too).
	const stepFolders = scriptStepFolders(project.config, project.repositoryUrl);
	const removedInUse = storedRepos
		.filter((repo) => !filledExtras.some((url) => sameRepository(url, repo.url)) && stepFolders.has(repoDirName(repo.url)))
		.map((repo) => gitHubFullName(repo.url) ?? repo.url);
	const reposInvalid = additionalReposProblem(project.repositoryUrl, extras) !== undefined || removedInUse.length > 0;
	const workingDirectoryError = workingDirectoryProblem(workingDirectory, { standalone });
	const workingDirectoryInvalid = workingDirectoryError !== undefined;
	const { mutation, edited } = useSettingsPageSave(project, dirty, reposInvalid || workingDirectoryInvalid, onSaveState);
	const locked = readOnly || mutation.isPending;
	const submit = () => {
		if (reposInvalid || workingDirectoryInvalid) return;
		const config: NonNullable<CloudCpProjectSettingsRequest["config"]> = {};
		if (reposChanged) {
			// Keep a stored branch for a repository that stays.
			config.repos = filledExtras.map((url) => {
				const branch = storedRepos.find((repo) => sameRepository(repo.url, url))?.branch;
				return branch ? { url, branch } : { url };
			});
		}
		if (startsIn !== saved.startsIn) config.agentStartsIn = startsIn;
		// Saved as the control plane stores it, so the field shows what was saved.
		const normalizedDirectory = normalizeWorkingDirectory(workingDirectory);
		if (normalizedDirectory !== saved.workingDirectory) config.workingDirectory = normalizedDirectory;
		mutation.mutate({ config }, {
			onSuccess: () => {
				setWorkingDirectory(normalizedDirectory);
				setSaved({ extras: filledExtras, startsIn, workingDirectory: normalizedDirectory });
			},
		});
	};
	return <form id="project-settings-form" className="flex min-h-full shrink-0 flex-col gap-6" onSubmit={(event) => { event.preventDefault(); submit(); }}>
		{readOnly ? <p role="note" className="text-xs text-settings-label">{t("settings.cloudProject.layoutAdminOnly", { defaultValue: "Only organization owners and admins can change a project's repositories and layout." })}</p> : null}
		{standalone ? (
			<Field label={t("settings.cloudProject.repositories", { defaultValue: "Repositories" })}>
				<p className="text-sm text-settings-muted">{t("settings.cloudProject.standaloneRepositories", { defaultValue: "No repository. Sessions start in an empty folder." })}</p>
			</Field>
		) : <>
			<Field label={t("settings.cloudProject.mainRepository", { defaultValue: "Main repository" })}
				hint={t("settings.cloudProject.mainRepositoryHint", { defaultValue: "Sessions branch from this repository." })}>
				<p className="font-mono text-sm text-settings-label">{gitHubFullName(project.repositoryUrl) ?? project.repositoryUrl}</p>
			</Field>
			<Field label={t("settings.cloudProject.additionalRepositories", { defaultValue: "Additional repositories" })}
				hint={t("settings.cloudProject.additionalRepositoriesHint", { defaultValue: "Cloned beside the main repository, on the session branch. One task can change them all and open a pull request in each." })}>
				<CloudAdditionalReposEditor
					available={repos.data ?? []}
					mainUrl={project.repositoryUrl}
					value={extras}
					onChange={(next) => { setExtras(next); edited(); }}
					disabled={locked}
					loading={repos.isLoading}
				/>
				{repos.isError ? <p role="alert" className="text-xs text-error">{t("createProject.githubReposFailed", { defaultValue: "Failed to load repositories." })}</p> : null}
				{removedInUse.length > 0 ? <p role="alert" className="text-xs text-error">{t("settings.cloudProject.repositoryUsedByScripts", { defaultValue: "{{repositories}} is still used by a Setup or Cleanup step. Move or delete that step in Scripts first.", repositories: removedInUse.join(", ") })}</p> : null}
			</Field>
			<Field label={t("settings.cloudProject.agentStartsIn", { defaultValue: "Agent starts in" })}
				hint={<>
					<p>{startsIn === "parent"
						? t("settings.cloudProject.agentStartsInParentHint", { defaultValue: "Starts in the folder that holds every repository, such as ~/work. Falls back to the main repository when there is no shared folder." })
						: t("settings.cloudProject.agentStartsInRepositoryHint", { defaultValue: "Starts in the main repository, with access to the others." })}</p>
				</>}>
				<SettingsOptionMenu<CloudCpAgentStartsIn>
					aria-label={t("settings.cloudProject.agentStartsIn", { defaultValue: "Agent starts in" })}
					disabled={locked}
					value={startsIn}
					options={[
						{ value: "repository", label: t("settings.cloudProject.agentStartsInRepository", { defaultValue: "Main repository" }) },
						{ value: "parent", label: t("settings.cloudProject.agentStartsInParent", { defaultValue: "Folder with all repositories" }) },
					]}
					triggerClassName="w-fit"
					onChange={(value) => { setStartsIn(value); edited(); }}
				/>
			</Field>
		</>}
		{showWorkingDirectory ? (
			<Field label={t("settings.cloudProject.workingDirectory")} htmlFor="cloudProjectWorkingDirectory"
				hint={workingDirectoryInvalid
					? <span role="alert" className="text-error">{workingDirectoryMessage(t, workingDirectoryError)}</span>
					: standalone
						? t("settings.cloudProject.workingDirectoryHintStandalone")
						: offering.coderWorkerWorkingFolder
							? t("settings.cloudProject.workingDirectoryHintCheckout")
							: t("settings.cloudProject.workingDirectoryHint")}>
				<Input id="cloudProjectWorkingDirectory" className="font-mono" value={workingDirectory} disabled={locked}
					placeholder={standalone ? t("settings.cloudProject.workingDirectoryPlaceholderStandalone") : t("settings.cloudProject.workingDirectoryPlaceholder")} spellCheck={false} autoCapitalize="off" autoComplete="off"
					aria-invalid={workingDirectoryInvalid || undefined}
					onChange={(event) => { setWorkingDirectory(event.target.value); edited(); }} />
			</Field>
		) : null}
		<div className="flex-1" />
		<SaveFooter dirty={dirty} disabled={locked || reposInvalid || workingDirectoryInvalid}
			error={mutation.isError ? mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed") : undefined} />
	</form>;
}

/**
 * A Cloud project's instructions: text added to every worker's prompt
 * (agentRules) and to the orchestrator's (orchestratorRules).
 */
export function CloudProjectInstructionsSettings(props: { projectId: string; cloudOrgId: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	return <CloudProjectSettingsPage {...props}>{(loaded) => <InstructionsForm key={`${props.cloudOrgId}/${props.projectId}`} {...loaded} />}</CloudProjectSettingsPage>;
}

function InstructionsForm({ project, readOnly, onSaveState }: LoadedProps) {
	const { t } = useTranslation();
	const [agentRules, setAgentRules] = useState(() => projectConfigText(project.config, "agentRules"));
	const [orchestratorRules, setOrchestratorRules] = useState(() => projectConfigText(project.config, "orchestratorRules"));
	const [saved, setSaved] = useState(() => ({ agentRules: projectConfigText(project.config, "agentRules"), orchestratorRules: projectConfigText(project.config, "orchestratorRules") }));
	const dirty = agentRules !== saved.agentRules || orchestratorRules !== saved.orchestratorRules;
	const tooLong = utf8Bytes(agentRules) > MAX_PROJECT_RULES_BYTES || utf8Bytes(orchestratorRules) > MAX_PROJECT_RULES_BYTES;
	const { mutation, edited } = useSettingsPageSave(project, dirty, tooLong, onSaveState);
	const locked = readOnly || mutation.isPending;
	const submit = () => {
		if (tooLong) return;
		const config: NonNullable<CloudCpProjectSettingsRequest["config"]> = {};
		if (agentRules !== saved.agentRules) config.agentRules = agentRules;
		if (orchestratorRules !== saved.orchestratorRules) config.orchestratorRules = orchestratorRules;
		mutation.mutate({ config }, { onSuccess: () => setSaved({ agentRules, orchestratorRules }) });
	};
	const box = (id: string, label: string, hint: string, value: string, onChange: (next: string) => void) => (
		<Field label={label} htmlFor={id} hint={<>
			<p>{hint}</p>
			{utf8Bytes(value) > MAX_PROJECT_RULES_BYTES ? <p role="alert" className="text-error">{t("settings.cloudProject.instructionsTooLong", { defaultValue: "Instructions must be at most 16 KB." })}</p> : null}
		</>}>
			<textarea className="settings-field-control min-h-40 w-full px-4 py-3 text-sm" disabled={locked} id={id} value={value}
				onChange={(event) => { onChange(event.target.value); edited(); }} />
		</Field>
	);
	return <form id="project-settings-form" className="flex min-h-full shrink-0 flex-col gap-6" onSubmit={(event) => { event.preventDefault(); submit(); }}>
		{readOnly ? <p role="note" className="text-xs text-settings-label">{t("settings.cloudProject.instructionsAdminOnly", { defaultValue: "Only organization owners and admins can change a project's instructions." })}</p> : null}
		{box("cloudProjectAgentRules", t("settings.cloudProject.workerInstructions", { defaultValue: "Worker instructions" }),
			t("settings.cloudProject.workerInstructionsHint", { defaultValue: "Added to every worker's prompt." }),
			agentRules, setAgentRules)}
		{box("cloudProjectOrchestratorRules", t("settings.cloudProject.orchestratorInstructions", { defaultValue: "Orchestrator instructions" }),
			t("settings.cloudProject.orchestratorInstructionsHint", { defaultValue: "Added to the orchestrator's prompt." }),
			orchestratorRules, setOrchestratorRules)}
		<div className="flex-1" />
		<SaveFooter dirty={dirty} disabled={locked || tooLong}
			error={mutation.isError ? mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed") : undefined} />
	</form>;
}
