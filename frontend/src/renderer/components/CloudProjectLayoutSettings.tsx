import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudGitHubAppReposQueryKey, useCloudGitHubAppRepositories } from "../hooks/useCloudGitHubAppRepositories";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import { CloudCpError, type CloudCpAgentStartsIn, type CloudCpProject, type CloudCpProjectSettingsRequest } from "../lib/cloud-cp";
import { CODER_PROVIDER, canManageBilling, projectSandboxProvider } from "../lib/cloud-offering";
import {
	additionalReposProblem,
	canonicalGitHubUrl,
	gitHubFullName,
	gitHubRepositoryNameFromFolder,
	isCloudWorkspaceProject,
	isValidGitHubRepositoryName,
	isStandaloneCloudProject,
	MAX_PROJECT_ADDITIONAL_REPOS,
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
import { additionalReposProblemMessage, CloudAdditionalReposEditor } from "./CloudAdditionalReposEditor";
import { useMountedRef, useReportBusy } from "./CloudProjectSetup";
import { cloudProjectSettingsQueryKey } from "./CloudProjectSettingsForm";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { SettingsSaveFooter as SaveFooter } from "./settings/SettingsSaveFooter";
import { Input } from "./ui/input";
import { Button } from "./ui/button";
import { FolderPlus, LoaderCircle } from "@/components/app-icons";
import { aoBridge } from "../lib/bridge";
import {
	committedUploadHint,
	createdRepositoryUrl,
	createFolderRepository,
	describeLocalFolder,
	emptyRepositoryNotice,
	folderLabel,
	FolderPushError,
	folderPushErrorText,
	grantedRepositoryForRemote,
	leftOutNotice,
	pushFolderToCreatedRepository,
	repositoryOwner,
	type CreatedFolderRepository,
} from "../lib/cloud-folder-upload";

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
/**
 * busy is other work the page must finish before the dialog closes (a folder
 * upload): it reads as a save in flight, so closing waits for it.
 */
function useSettingsPageSave(project: CloudCpProject, dirty: boolean, unsaveable: boolean, onSaveState?: (state: ProjectSettingsSaveState) => void, busy = false) {
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
			phase: mutation.isPending || busy ? "saving" : mutation.isError ? "failed" : savedAt && !dirty ? "saved" : "idle",
			dirty,
			requestPending: mutation.isPending || busy,
			// An invalid draft cannot be saved, so closing the dialog drops it
			// instead of waiting for a save that never starts.
			unsaveable: dirty && unsaveable,
			error: mutation.error instanceof Error ? mutation.error.message : undefined,
		});
	}, [busy, dirty, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt, unsaveable]);
	return { mutation, edited: () => setSavedAt(false) };
}

/** labelHidden keeps the label for assistive technology only, where the pane's title already says it. */
function Field({ label, htmlFor, hint, labelHidden, children }: { label: string; htmlFor?: string; hint?: ReactNode; labelHidden?: boolean; children: ReactNode }) {
	return <div className="space-y-2">
		<label className={labelHidden ? "sr-only" : "block text-sm font-medium text-settings-label"} htmlFor={htmlFor}>{label}</label>
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
	// A workspace's agent always starts in the folder holding every repository.
	const workspace = isCloudWorkspaceProject(project);
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
	// A folder upload in flight keeps the dialog open until it ends.
	const [uploading, setUploading] = useState(false);
	const { mutation, edited } = useSettingsPageSave(project, dirty, reposInvalid || workingDirectoryInvalid, onSaveState, uploading);
	const locked = readOnly || mutation.isPending;
	const savedRepos = (urls: readonly string[]) => urls.map((url) => {
		const branch = storedRepos.find((repo) => sameRepository(repo.url, url))?.branch;
		return branch ? { url, branch } : { url };
	});
	// What the server last saved, read by a save when it runs rather than when
	// it was started: a folder upload's save starts when the upload does, and
	// the page may have saved other changes (a repository removed) meanwhile.
	const savedRef = useRef(saved);
	const commitSaved = (next: typeof saved) => {
		savedRef.current = next;
		setSaved(next);
	};
	// Saves run one at a time, each on top of what the one before it saved.
	const saveQueue = useRef<Promise<unknown>>(Promise.resolve());
	const queueSave = <T,>(save: () => Promise<T>): Promise<T> => {
		const run = saveQueue.current.then(save);
		saveQueue.current = run.catch(() => undefined);
		return run;
	};
	const addToDraft = (url: string) => setExtras((current) => current.some((item) => sameRepository(item, url)) ? current : [...current.filter((item) => item !== ""), url]);
	// What the saved workspace and the draft would make of one more
	// repository; checked before a folder's repository is made.
	const workspaceFolderProblem = (url: string) => {
		for (const list of [saved.extras, filledExtras]) {
			const problem = additionalReposProblem(project.repositoryUrl, [...list, url]);
			if (problem && (problem.kind === "limit" || sameRepository(problem.url, url))) return additionalReposProblemMessage(t, problem);
		}
		return undefined;
	};
	// An uploaded folder's repository is saved into the workspace at once, on
	// top of what is saved when its save runs (the draft's other edits stay a
	// draft).
	const saveUploadedRepository = (url: string) => queueSave(async () => {
		const latest = savedRef.current;
		if (sameRepository(project.repositoryUrl, url) || latest.extras.some((item) => sameRepository(item, url))) {
			addToDraft(url);
			return;
		}
		const extras = [...latest.extras, url];
		try {
			await mutation.mutateAsync({ config: { repos: savedRepos(extras) } });
		} catch (err) {
			// Left in the draft, so Save tries again.
			addToDraft(url);
			edited();
			throw err;
		}
		commitSaved({ ...savedRef.current, extras });
		addToDraft(url);
	});
	const submit = () => {
		if (reposInvalid || workingDirectoryInvalid) return;
		// The draft as it is now, saved against what is saved when the save runs.
		const draft = { extras: filledExtras, startsIn, workingDirectory: normalizeWorkingDirectory(workingDirectory) };
		void queueSave(async () => {
			const latest = savedRef.current;
			const config: NonNullable<CloudCpProjectSettingsRequest["config"]> = {};
			if (JSON.stringify(draft.extras) !== JSON.stringify(latest.extras)) {
				// Keep a stored branch for a repository that stays.
				config.repos = savedRepos(draft.extras);
			}
			if (!workspace && draft.startsIn !== latest.startsIn) config.agentStartsIn = draft.startsIn;
			// Saved as the control plane stores it, so the field shows what was saved.
			if (draft.workingDirectory !== latest.workingDirectory) config.workingDirectory = draft.workingDirectory;
			await mutation.mutateAsync({ config });
			setWorkingDirectory(draft.workingDirectory);
			commitSaved(draft);
		}).catch(() => {
			// The footer shows the mutation's error.
		});
	};
	return <form id="project-settings-form" className="flex min-h-full shrink-0 flex-col gap-6" onSubmit={(event) => { event.preventDefault(); submit(); }}>
		{readOnly ? <p role="note" className="text-xs text-settings-label">{t("settings.cloudProject.layoutAdminOnly", { defaultValue: "Only organization owners and admins can change a project's repositories and layout." })}</p> : null}
		{standalone ? (
			<Field label={t("settings.cloudProject.repositories", { defaultValue: "Repositories" })}>
				<p className="text-sm text-settings-muted">{t("settings.cloudProject.standaloneRepositories", { defaultValue: "No repository. Sessions start in an empty folder." })}</p>
			</Field>
		) : workspace ? (
			<Field label={t("settings.cloudProject.workspaceRepositories", { defaultValue: "Workspace repositories" })} labelHidden
				hint={t("settings.cloudProject.workspaceRepositoriesHint", { defaultValue: "Checked out side by side in the workspace folder, on the session branch, and the agent starts in that folder. The first one cannot be removed." })}>
				<p className="font-mono text-sm text-settings-label">{gitHubFullName(project.repositoryUrl) ?? project.repositoryUrl}</p>
				<CloudAdditionalReposEditor
					available={repos.data ?? []}
					mainUrl={project.repositoryUrl}
					value={extras}
					onChange={(next) => { setExtras(next); edited(); }}
					disabled={locked}
					loading={repos.isLoading}
				/>
				<AddLocalFolder
					project={project}
					disabled={locked}
					atLimit={saved.extras.length >= MAX_PROJECT_ADDITIONAL_REPOS || filledExtras.length >= MAX_PROJECT_ADDITIONAL_REPOS}
					folderProblem={workspaceFolderProblem}
					onAddExisting={(url) => {
						if (extras.some((item) => sameRepository(item, url)) || sameRepository(project.repositoryUrl, url)) return;
						addToDraft(url);
						edited();
					}}
					onUploaded={saveUploadedRepository}
					onBusyChange={setUploading}
				/>
				{repos.isError ? <p role="alert" className="text-xs text-error">{t("createProject.githubReposFailed", { defaultValue: "Failed to load repositories." })}</p> : null}
				{removedInUse.length > 0 ? <p role="alert" className="text-xs text-error">{t("settings.cloudProject.repositoryUsedByScripts", { defaultValue: "{{repositories}} is still used by a Setup or Cleanup step. Move or delete that step in Scripts first.", repositories: removedInUse.join(", ") })}</p> : null}
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
 * Uploads a local folder into a new private repository in the workspace's
 * GitHub account (or, when its origin is a repository the org already grants,
 * hands that one to the editor). The new repository's name starts as the
 * folder's and can be changed before upload. Once uploaded, onUploaded saves it
 * into the workspace right away, so leaving Settings never strands a
 * repository that was made and filled. While it runs, onBusyChange keeps the
 * Settings dialog open: an upload cannot be cancelled.
 */
function AddLocalFolder({ project, disabled, atLimit, folderProblem, onAddExisting, onUploaded, onBusyChange }: {
	project: CloudCpProject;
	disabled: boolean;
	/** The workspace has as many repositories as it can hold. */
	atLimit: boolean;
	/** Why a repository at this URL could not join the workspace, if it could not. */
	folderProblem: (url: string) => string | undefined;
	onAddExisting: (url: string) => void;
	onUploaded: (url: string) => Promise<void>;
	onBusyChange: (busy: boolean) => void;
}) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const repos = useCloudGitHubAppRepositories(project.orgId);
	const mounted = useMountedRef();
	const [phase, setPhase] = useState<"idle" | "creating" | "uploading" | "saving">("idle");
	const [pending, setPending] = useState<{ folder: string; name: string; committed?: { branch?: string }; leftOut?: string[] } | null>(null);
	// Kept across a failed upload, so a retry pushes to the repository already made.
	const [created, setCreated] = useState<{ folder: string; repository: CreatedFolderRepository } | null>(null);
	// note: what became of a repository made for the folder (emptyRepositoryNotice).
	const [error, setError] = useState<{ message: string; detail?: string; note?: string } | null>(null);
	const busy = phase !== "idle";
	useReportBusy(busy, onBusyChange);
	const owner = repositoryOwner(gitHubFullName(project.repositoryUrl) ?? "");
	// While a folder is picked, then checked (folder, once picked): the checks
	// are every check the upload makes, which can take a while on a long
	// history, so Add waits for them rather than starting another.
	const [choosing, setChoosing] = useState<{ folder?: string } | null>(null);
	const choose = async () => {
		setError(null);
		setChoosing({});
		try {
			const folder = await aoBridge.app.chooseDirectory({ title: t("cloudWorkspace.chooseFolder", { defaultValue: "Choose a folder for the workspace" }) });
			if (!folder || !mounted.current) return;
			setChoosing({ folder });
			const { remote, committed, leftOut, refusal } = await describeLocalFolder(folder);
			if (!mounted.current) return;
			const existing = grantedRepositoryForRemote(remote, repos.data ?? []);
			if (existing) {
				onAddExisting(canonicalGitHubUrl(existing.fullName));
				return;
			}
			// A folder that cannot be uploaded at all is refused before a repository is made.
			if (refusal) {
				setError(folderPushErrorText(t, refusal, folder));
				return;
			}
			setCreated(null);
			setPending({ folder, name: gitHubRepositoryNameFromFolder(folderLabel(folder)), ...(committed ? { committed } : {}), ...(leftOut?.length ? { leftOut } : {}) });
		} finally {
			if (mounted.current) setChoosing(null);
		}
	};
	const madeForPending = pending !== null && created?.folder === pending.folder ? created.repository : undefined;
	const nameInvalid = pending !== null && !madeForPending && !isValidGitHubRepositoryName(pending.name.trim());
	// Checked before anything is made: a repository the workspace cannot take
	// would be left on GitHub.
	const plannedUrl = pending === null || nameInvalid ? undefined
		: madeForPending ? createdRepositoryUrl(madeForPending) : canonicalGitHubUrl(`${owner || "new"}/${pending.name.trim()}`);
	const problem = plannedUrl ? folderProblem(plannedUrl) : undefined;
	const upload = async () => {
		if (!pending || nameInvalid || problem) return;
		setError(null);
		let repository = madeForPending;
		try {
			if (!repository) {
				setPhase("creating");
				repository = await createFolderRepository(client, project.orgId, { name: pending.name.trim(), owner: owner || undefined });
				setCreated({ folder: pending.folder, repository });
			}
			setPhase("uploading");
			await pushFolderToCreatedRepository(pending.folder, repository);
		} catch (caught) {
			if ((caught instanceof CloudCpError && caught.status === 401) || (caught instanceof FolderPushError && caught.code === "signed_out")) void aoBridge.cloud.signIn();
			if (mounted.current) {
				setError({
					...(caught instanceof FolderPushError
						? folderPushErrorText(t, caught, pending.folder)
						: { message: caught instanceof Error ? caught.message : t("settings.project.saveFailed") }),
					...(repository ? { note: emptyRepositoryNotice(t, repository, pending.folder) } : {}),
				});
				setPhase("idle");
			}
			return;
		}
		void queryClient.invalidateQueries({ queryKey: cloudGitHubAppReposQueryKey(baseUrl, project.orgId) });
		const url = createdRepositoryUrl(repository);
		// The repository is filled; it joins the workspace even if this page
		// has gone, so it is never left out.
		setPhase("saving");
		try {
			await onUploaded(url);
			if (!mounted.current) return;
			setPending(null);
			setCreated(null);
		} catch (caught) {
			if (caught instanceof CloudCpError && caught.status === 401) void aoBridge.cloud.signIn();
			if (!mounted.current) return;
			setPending(null);
			setCreated(null);
			setError({
				message: t("cloudWorkspace.uploadedNotSaved", { defaultValue: "{{name}} was uploaded, but adding it to the workspace failed. Save to add it.", name: repository.repository.fullName }),
				...(caught instanceof Error && caught.message ? { detail: caught.message } : {}),
			});
		} finally {
			if (mounted.current) setPhase("idle");
		}
	};
	return <div className="space-y-2">
		{pending ? <div className="space-y-1">
			<div className="flex flex-wrap items-center gap-2">
				<span className="min-w-0 truncate font-mono text-sm text-settings-label" title={pending.folder}>{folderLabel(pending.folder)}</span>
				<Input
					aria-label={t("cloudWorkspace.repositoryName", { defaultValue: "Repository name for {{folder}}", folder: pending.folder })}
					className="h-7 w-48 font-mono text-[12px]"
					value={madeForPending ? madeForPending.repository.name : pending.name}
					maxLength={100}
					spellCheck={false}
					autoCapitalize="off"
					autoComplete="off"
					disabled={disabled || busy || madeForPending !== undefined}
					aria-invalid={nameInvalid || problem !== undefined || undefined}
					onChange={(event) => { setPending({ ...pending, name: event.target.value }); setError(null); }}
				/>
				<Button type="button" variant="secondary" size="sm" disabled={disabled || busy || nameInvalid || problem !== undefined} onClick={() => void upload()}>
					{busy ? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
					{phase === "creating"
						? t("cloudWorkspace.creatingRepository", { defaultValue: "Creating repository" })
						: phase === "uploading"
							? t("cloudWorkspace.uploading", { defaultValue: "Uploading" })
							: phase === "saving"
								? t("cloudWorkspace.addingToWorkspace", { defaultValue: "Adding to the workspace" })
								: t("cloudWorkspace.upload", { defaultValue: "Upload" })}
				</Button>
				<Button type="button" variant="quiet" size="sm" disabled={busy} onClick={() => { setPending(null); setError(null); }}>
					{t("createProject.cancel", { defaultValue: "Cancel" })}
				</Button>
			</div>
			<p className="text-xs text-settings-muted">
				{pending.committed
					? committedUploadHint(t, pending.committed)
					: t("cloudWorkspace.newPrivateRepository", { defaultValue: "Uploaded to a new private repository" })}
			</p>
			{pending.leftOut?.length ? <p className="text-pretty text-xs text-settings-muted [overflow-wrap:anywhere]">{leftOutNotice(t, pending.leftOut)}</p> : null}
		</div> : <Button type="button" variant="secondary" size="sm" disabled={disabled || atLimit || choosing !== null} onClick={() => void choose()}>
			<FolderPlus aria-hidden="true" className="size-icon-sm" />
			{t("cloudWorkspace.addFolder", { defaultValue: "Add a local folder" })}
		</Button>}
		{choosing?.folder ? <p role="status" className="flex items-center gap-2 text-xs text-settings-muted">
			<LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin" />
			{t("cloudWorkspace.checkingFolder", { defaultValue: "Checking {{folder}}", folder: folderLabel(choosing.folder) })}
		</p> : null}
		{atLimit && !pending ? <p className="text-xs text-settings-muted">{t("cloudRepos.problemLimit", { defaultValue: "A project can have at most {{count}} additional repositories.", count: MAX_PROJECT_ADDITIONAL_REPOS })}</p> : null}
		{nameInvalid ? <p role="alert" className="text-xs text-error">{t("cloudWorkspace.invalidRepositoryName", { defaultValue: "{{name}} is not a valid repository name: use letters, digits, \".\", \"-\" and \"_\", up to 100 characters.", name: pending?.name ?? "" })}</p> : null}
		{problem ? <p role="alert" className="text-xs text-error">{problem}</p> : null}
		{error ? <div role="alert" className="text-xs">
			<p className="text-error">{error.message}</p>
			{error.detail ? <p className="whitespace-pre-line text-settings-muted">{error.detail}</p> : null}
			{error.note ? <p className="text-pretty text-settings-muted [overflow-wrap:anywhere]">{error.note}</p> : null}
		</div> : null}
	</div>;
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
