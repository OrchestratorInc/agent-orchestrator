// Creates a cloud workspace: like a local workspace, several repositories side
// by side in one folder where the agent starts. Members are GitHub App
// repositories or folders from this machine; each folder gets a new private
// repository (or, when its origin is a granted repository, is that
// repository). The project itself is an ordinary GitHub project whose config
// says layout "workspace": its first member authorizes it, the rest are
// config.repos. See lib/cloud-folder-upload.ts for the folder half.

import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Folder, FolderPlus, LoaderCircle, Plus, X, XCircle } from "@/components/app-icons";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { aoBridge } from "../lib/bridge";
import { CloudCpError } from "../lib/cloud-cp";
import {
	createdRepositoryUrl,
	createFolderRepository,
	describeLocalFolder,
	emptyRepositoryNotice,
	folderLabel,
	FolderPushError,
	committedUploadHint,
	folderPushErrorText,
	grantedRepositoryForRemote,
	leftOutNotice,
	pushFolderToCreatedRepository,
	repositoryOwner,
	type CreatedFolderRepository,
} from "../lib/cloud-folder-upload";
import { CODER_PROVIDER, canManageBilling } from "../lib/cloud-offering";
import {
	canonicalGitHubUrl,
	CLOUD_WORKSPACE_LAYOUT,
	gitHubRepositoryNameFromFolder,
	isValidGitHubRepositoryName,
	MAX_WORKSPACE_REPOSITORIES,
	repoDirName,
	sameRepository,
	workspaceRepositoriesProblem,
} from "../lib/cloud-project-repos";
import {
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFooterActionsClass,
	onboardingFormLabelClass,
	onboardingPanelBodyClass,
	onboardingPanelClass,
	onboardingPanelTitleClass,
} from "../lib/onboarding-ui";
import { cn } from "../lib/utils";
import { additionalReposProblemMessage } from "./CloudAdditionalReposEditor";
import {
	CloudAgentSetupStep,
	CloudCoderSetupFields,
	CloudCreateKindTabs,
	CloudGitHubConnectCard,
	CloudGitHubConnectionStatus,
	type CloudCreateKind,
	useCloudCoderSetup,
	useCloudGitHubAppConnection,
	useFinishCloudProjectCreate,
	useMountedRef,
	useReportBusy,
} from "./CloudProjectSetup";
import { GitHubIcon } from "./GitHubIcon";
import { SearchablePicker } from "./SearchablePicker";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

/** One repository of the workspace being built. */
export type WorkspaceMember =
	| {
		id: string;
		kind: "repository";
		githubRepositoryId: string;
		fullName: string;
		/** The local folder it was added from, when its origin is this repository. */
		fromFolder?: string;
	}
	| {
		id: string;
		kind: "folder";
		/** The local folder, an absolute path. */
		path: string;
		/** The new repository's name, which is also its folder in the workspace. */
		name: string;
		/**
		 * Set when the folder is the top of a git repository with a commit: its
		 * checked-out commit and history are uploaded, not its uncommitted
		 * changes. branch is absent when HEAD is detached.
		 */
		committed?: { branch?: string };
		/** The secret-looking files the upload leaves out, shown before it runs. */
		leftOut?: string[];
		/** Set once the control plane made the repository; a retry never makes it again. */
		created?: CreatedFolderRepository;
		/** The folder is in its repository; a retry skips it. */
		pushed?: boolean;
	};

type MemberProgress = { phase: "creating" | "uploading" | "done" | "failed"; message?: string; detail?: string };

let nextMemberId = 0;
const memberId = () => `member-${++nextMemberId}`;

/** The repository URL a member checks out from (a folder's planned one until it exists). */
export function workspaceMemberUrl(member: WorkspaceMember, owner: string | undefined): string {
	if (member.kind === "repository") return canonicalGitHubUrl(member.fullName);
	if (member.created) return createdRepositoryUrl(member.created);
	return canonicalGitHubUrl(`${owner || "new"}/${member.name}`);
}

export function CloudWorkspaceCard({
	dialog = false,
	onAuthRequired,
	onBack,
	onClose,
	onCreated,
	onKindChange,
	onBusyChange,
}: {
	dialog?: boolean;
	onAuthRequired: () => void;
	onBack: () => void;
	onClose?: () => void;
	onCreated: (projectId: string) => void;
	/** Switches the card back to a single project. */
	onKindChange?: (kind: CloudCreateKind) => void;
	/** Whether a create (uploads included) is running; a dialog stays open meanwhile. */
	onBusyChange?: (busy: boolean) => void;
}) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const queryClient = useQueryClient();
	const offering = useCloudOffering();
	const usesCoder = offering.defaultProvider === CODER_PROVIDER;
	const coderSetup = useCloudCoderSetup(org?.id, usesCoder, offering.hasCoder);
	const github = useCloudGitHubAppConnection(onAuthRequired);
	const granted = github.repositories.data ?? [];
	const finishCreate = useFinishCloudProjectCreate();

	const [name, setName] = useState("");
	const [members, setMembers] = useState<WorkspaceMember[]>([]);
	// Folders picked whose checks (describeLocalFolder) are still running.
	const [checking, setChecking] = useState<string[]>([]);
	// Folder dialogs still open. The dialog does not block this window, so
	// Create could be pressed meanwhile, and a folder picked once the create
	// had begun would never be uploaded or added.
	const [pickingFolders, setPickingFolders] = useState(0);
	const [progress, setProgress] = useState<Record<string, MemberProgress>>({});
	const [pickingRepository, setPickingRepository] = useState(false);
	const [chosenOwner, setChosenOwner] = useState("");
	const [ownerRequired, setOwnerRequired] = useState(false);
	// The account a new repository goes in has no usable AO GitHub App
	// installation: connecting GitHub again is the way on.
	const [installationNeeded, setInstallationNeeded] = useState(false);
	const [addError, setAddError] = useState<{ message: string; detail?: string } | null>(null);
	const [submitError, setSubmitError] = useState<string | null>(null);
	const [isCreating, setIsCreating] = useState(false);
	// The create request's key, kept while its body stays the same so a retry
	// after a lost answer cannot make a second project.
	const idempotency = useRef<{ body: string; key: string } | null>(null);
	const mounted = useMountedRef();
	useReportBusy(isCreating, onBusyChange);

	const nameTrimmed = name.trim();
	const nameInvalid = nameTrimmed === "" || [...nameTrimmed].length > 120;
	const repositoryMembers = members.filter((member) => member.kind === "repository");
	const folderMembers = members.filter((member) => member.kind === "folder");
	// An installation covers one GitHub account, and every repository of a
	// workspace comes through the same installation: the account of the
	// repositories already chosen, else the one picked for new repositories.
	const repositoryOwners = [...new Set(repositoryMembers.map((member) => repositoryOwner(member.fullName).toLowerCase()))];
	const installationAccounts = github.activeInstallations.map((installation) => installation.accountLogin);
	const owner = repositoryMembers[0]
		? repositoryOwner(repositoryMembers[0].fullName)
		: chosenOwner || (installationAccounts.length === 1 ? installationAccounts[0] : undefined);
	const showOwnerPicker = folderMembers.some((member) => !member.created) && repositoryMembers.length === 0 && (installationAccounts.length > 1 || ownerRequired);
	const ownerMissing = showOwnerPicker && !owner;
	const mixedOwners = repositoryOwners.length > 1;
	const urls = members.map((member) => workspaceMemberUrl(member, owner));
	const problem = workspaceRepositoriesProblem(urls);
	const problemMessage = problem?.kind === "limit"
		? t("cloudWorkspace.problemLimit", { defaultValue: "A workspace can have at most {{count}} repositories and folders.", count: MAX_WORKSPACE_REPOSITORIES })
		: additionalReposProblemMessage(t, problem);
	const badName = folderMembers.find((member) => !member.created && !isValidGitHubRepositoryName(member.name));
	// Additional repositories are an owner and admin setting, so a workspace of
	// more than one needs one; a role the app does not know leaves it to the server.
	const memberRoleBlocks = org?.role !== undefined && !canManageBilling(org) && members.length > 1;
	const atLimit = members.length >= MAX_WORKSPACE_REPOSITORIES;
	// A folder still being picked or checked would be left out of a workspace created now.
	const createBlocked = nameInvalid || members.length === 0 || pickingFolders > 0 || checking.length > 0 || problem !== undefined || badName !== undefined || mixedOwners || ownerMissing || memberRoleBlocks || coderSetup.workspaceNamePrefixInvalid;

	// Every change applies to the list as it is when it lands: a folder's
	// checks can end after other members joined, left, or were renamed.
	const update = (change: (current: WorkspaceMember[]) => WorkspaceMember[]) => {
		setMembers(change);
		setSubmitError(null);
	};
	const sameMember = (a: WorkspaceMember, b: WorkspaceMember) => a.kind === "repository"
		? b.kind === "repository" && a.githubRepositoryId === b.githubRepositoryId
		: b.kind === "folder" && a.path === b.path;
	const append = (member: WorkspaceMember) => update((current) => (current.some((item) => sameMember(item, member)) ? current : [...current, member]));
	const addRepository = (githubRepositoryId: string) => {
		setPickingRepository(false);
		const repo = granted.find((candidate) => candidate.githubRepositoryId === githubRepositoryId);
		if (!repo) return;
		append({ id: memberId(), kind: "repository", githubRepositoryId, fullName: repo.fullName });
	};
	const addFolder = async () => {
		setAddError(null);
		setPickingFolders((open) => open + 1);
		let path: string | null;
		try {
			path = await aoBridge.app.chooseDirectory({ title: t("cloudWorkspace.chooseFolder", { defaultValue: "Choose a folder for the workspace" }) });
		} finally {
			// In the same render as the folder's check starts below, so Create
			// never comes back on between them.
			setPickingFolders((open) => open - 1);
		}
		if (!path) return;
		if (members.some((member) => member.kind === "folder" && member.path === path) || checking.includes(path)) {
			setAddError({ message: t("cloudWorkspace.folderAlreadyAdded", { defaultValue: "{{folder}} is already in this workspace.", folder: folderLabel(path) }) });
			return;
		}
		// A git repository whose origin the org already grants joins as that
		// repository instead of being uploaded again.
		setChecking((current) => [...current, path]);
		let described: Awaited<ReturnType<typeof describeLocalFolder>>;
		try {
			described = await describeLocalFolder(path);
		} finally {
			setChecking((current) => current.filter((item) => item !== path));
		}
		const { remote, committed, leftOut, refusal } = described;
		const existing = grantedRepositoryForRemote(remote, granted);
		if (existing) {
			if (members.some((member) => member.kind === "repository" && member.githubRepositoryId === existing.githubRepositoryId)) {
				setAddError({ message: t("cloudWorkspace.repositoryAlreadyAdded", { defaultValue: "{{name}} is already in this workspace.", name: existing.fullName }) });
				return;
			}
			append({ id: memberId(), kind: "repository", githubRepositoryId: existing.githubRepositoryId, fullName: existing.fullName, fromFolder: path });
			return;
		}
		// A folder that cannot be uploaded at all (the upload would refuse it)
		// is refused now, not after its repository is made.
		if (refusal) {
			setAddError(folderPushErrorText(t, refusal, path));
			return;
		}
		append({ id: memberId(), kind: "folder", path, name: gitHubRepositoryNameFromFolder(folderLabel(path)), ...(committed ? { committed } : {}), ...(leftOut?.length ? { leftOut } : {}) });
	};
	const remove = (id: string) => {
		update((current) => current.filter((member) => member.id !== id));
		setProgress((current) => {
			const { [id]: _removed, ...rest } = current;
			return rest;
		});
	};
	const rename = (id: string, value: string) => update((current) => current.map((member) => (member.id === id && member.kind === "folder" ? { ...member, name: value } : member)));

	const create = async (selection: { workerAgent: string; orchestratorAgent: string }) => {
		if (isCreating || org === undefined || createBlocked || coderSetup.templateMissing) return;
		setSubmitError(null);
		setInstallationNeeded(false);
		setIsCreating(true);
		// Members as they stand after each step, so a failure keeps what was done.
		const working = [...members];
		const save = (index: number, member: WorkspaceMember) => {
			working[index] = member;
			setMembers((current) => current.map((item) => (item.id === member.id ? member : item)));
		};
		const mark = (id: string, value: MemberProgress) => setProgress((current) => ({ ...current, [id]: value }));
		let current: { id: string; path: string } | undefined;
		// Which request failed: only the project create is refused for a
		// member's role, so only its 403 says an owner or admin is needed.
		let step: "repository" | "upload" | "project" = "repository";
		try {
			for (const [index, member] of working.entries()) {
				if (member.kind !== "folder" || member.pushed) continue;
				current = { id: member.id, path: member.path };
				let created = member.created;
				if (!created) {
					step = "repository";
					mark(member.id, { phase: "creating" });
					created = await createFolderRepository(client, org.id, { name: member.name.trim(), owner });
					// Closed meanwhile: nothing more runs for a card that is gone.
					if (!mounted.current) return;
					save(index, { ...member, created });
				}
				step = "upload";
				mark(member.id, { phase: "uploading" });
				await pushFolderToCreatedRepository(member.path, created);
				if (!mounted.current) return;
				save(index, { ...member, created, pushed: true });
				mark(member.id, { phase: "done" });
			}
			current = undefined;
			step = "project";
			// The new repositories are the org's now; the pickers list them.
			void queryClient.invalidateQueries({ queryKey: ["cloud-github-app-repos", baseUrl, org.id] });

			const repositoryIdOf = (member: WorkspaceMember) => (member.kind === "repository" ? member.githubRepositoryId : member.created?.repository.githubRepositoryId ?? "");
			const [first, ...rest] = working;
			const coder = coderSetup.coderConfig();
			// The server starts a workspace's agent in its folder; agentStartsIn is
			// an owner and admin setting, so it is not sent.
			const config = {
				worker: { agent: selection.workerAgent },
				orchestrator: { agent: selection.orchestratorAgent },
				...(coder ? { coder } : {}),
				...(offering.offersProviderChoice && offering.defaultProvider ? { sandboxProvider: offering.defaultProvider } : {}),
				layout: CLOUD_WORKSPACE_LAYOUT,
				...(rest.length > 0 ? { repos: rest.map((member) => ({ url: workspaceMemberUrl(member, owner) })) } : {}),
			};
			const body = { githubRepositoryId: first ? repositoryIdOf(first) : "", displayName: nameTrimmed, config };
			const bodyKey = JSON.stringify(body);
			if (idempotency.current?.body !== bodyKey) idempotency.current = { body: bodyKey, key: crypto.randomUUID() };
			const { project } = await client.createGitHubProject(org.id, body, { idempotencyKey: idempotency.current.key });
			await finishCreate(project, offering);
			if (!mounted.current) return;
			onCreated(project.id);
		} catch (err) {
			if (!mounted.current) return;
			if ((err instanceof CloudCpError && err.status === 401) || (err instanceof FolderPushError && err.code === "signed_out")) onAuthRequired();
			if (err instanceof CloudCpError && err.code === "repository_owner_required") setOwnerRequired(true);
			if (err instanceof CloudCpError && (err.code === "github_installation_required" || err.code === "github_installation_ineligible")) setInstallationNeeded(true);
			const failure = err instanceof FolderPushError && current !== undefined
				? folderPushErrorText(t, err, current.path)
				: {
					message: err instanceof CloudCpError && err.status === 403 && step === "project"
						? t("cloudWorkspace.forbidden", { defaultValue: "{{message}} A workspace with more than one repository needs an organization owner or admin.", message: err.message })
						: err instanceof Error ? err.message : t("createProject.couldNotAdd"),
				};
			// A folder's failure shows once, under that folder; anything else
			// shows below the list.
			if (current !== undefined) mark(current.id, { phase: "failed", ...failure });
			setSubmitError(current !== undefined ? null : failure.message);
			setIsCreating(false);
		}
	};

	const title = t("cloudWorkspace.title", { defaultValue: "Create cloud workspace" });
	const showSetup = members.length > 0 && org !== undefined;
	return (
		<div className={onboardingPanelClass}>
			{dialog ? (
				<>
					<Dialog.Title className={cn(onboardingPanelTitleClass, onClose && "pr-12")}>{title}</Dialog.Title>
					<Dialog.Description className="sr-only">{t("cloudWorkspace.description", { defaultValue: "Several repositories and folders, side by side in one folder where the agent starts." })}</Dialog.Description>
				</>
			) : (
				<h2 className={onboardingPanelTitleClass}>{title}</h2>
			)}
			{dialog && onClose ? (
				<CloseButton className="absolute right-3 top-3 z-10" aria-label={t("createProject.closeDialog")} disabled={isCreating} onClick={onClose} />
			) : null}

			<div className={cn(onboardingPanelBodyClass, "pt-4")}>
				{onKindChange ? <CloudCreateKindTabs value="workspace" onChange={onKindChange} disabled={isCreating} /> : null}
				<div className="flex flex-col gap-2 text-sm">
					<Label htmlFor="cloudWorkspaceName" className={onboardingFormLabelClass}>
						{t("cloudWorkspace.name", { defaultValue: "Workspace name" })}
					</Label>
					<Input id="cloudWorkspaceName" value={name} maxLength={120} disabled={isCreating} autoComplete="off"
						onChange={(event) => { setName(event.target.value); setSubmitError(null); }} />
				</div>

				<div className="space-y-2">
					<Label className={onboardingFormLabelClass}>{t("cloudWorkspace.members", { defaultValue: "Repositories and folders" })}</Label>
					<p className={onboardingFieldHintClass}>
						{t("cloudWorkspace.membersHint", { defaultValue: "Each session checks them all out side by side in one folder, on the same branch, and the agent starts in that folder. Each folder from this computer is uploaded to a new private GitHub repository." })}
					</p>
					{!github.appConnected || github.noRepositoriesShared || installationNeeded ? (
						<CloudGitHubConnectCard connection={github} disabled={isCreating} />
					) : null}
					<CloudGitHubConnectionStatus connection={github} />

					{members.length > 0 ? (
						<ul className="divide-y divide-border/50 overflow-hidden rounded-lg border border-border/50 bg-[var(--color-bg-import-card)]">
							{members.map((member) => (
								<WorkspaceMemberRow key={member.id} member={member} owner={owner} progress={progress[member.id]} disabled={isCreating}
									onRename={(value) => rename(member.id, value)} onRemove={() => remove(member.id)} />
							))}
						</ul>
					) : null}
					{checking.map((path) => (
						<p key={path} className={cn(onboardingFieldHintClass, "flex items-center gap-2")} role="status">
							<LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin" />
							{t("cloudWorkspace.checkingFolder", { defaultValue: "Checking {{folder}}", folder: folderLabel(path) })}
						</p>
					))}

					{pickingRepository ? (
						<SearchablePicker
							ariaLabel={t("cloudWorkspace.addRepository", { defaultValue: "Add a repository" })}
							fixedScroll
							defaultOpen
							loading={github.repositories.isLoading}
							loadingText={t("createProject.githubReposLoading", { defaultValue: "Loading repositories..." })}
							placeholder={t("createProject.selectRepository", { defaultValue: "Select a repository" })}
							searchPlaceholder={t("createProject.searchRepositories", { defaultValue: "Search repositories" })}
							value=""
							options={granted
								.filter((repo) => !members.some((member) => member.kind === "repository" && sameRepository(canonicalGitHubUrl(member.fullName), canonicalGitHubUrl(repo.fullName))))
								.map((repo) => ({ value: repo.githubRepositoryId, label: repo.fullName, private: repo.isPrivate }))}
							onChange={addRepository}
							onDismiss={() => setPickingRepository(false)}
							action={{
								label: t("createProject.connectMoreRepositories", { defaultValue: "Connect more repositories" }),
								disabled: isCreating || github.busy,
								onSelect: () => void github.connect(),
							}}
						/>
					) : null}
					<div className="flex flex-wrap items-center gap-2">
						<Button type="button" variant="secondary" size="sm" disabled={isCreating || atLimit || !github.appConnected || pickingRepository} onClick={() => setPickingRepository(true)}>
							<Plus aria-hidden="true" className="size-icon-sm" />
							{t("cloudWorkspace.addRepository", { defaultValue: "Add a repository" })}
						</Button>
						<Button type="button" variant="secondary" size="sm" disabled={isCreating || atLimit || !github.appConnected} onClick={() => void addFolder()}>
							<FolderPlus aria-hidden="true" className="size-icon-sm" />
							{t("cloudWorkspace.addFolder", { defaultValue: "Add a local folder" })}
						</Button>
					</div>
					{addError ? <div role="alert">
						<p className={onboardingFieldErrorClass}>{addError.message}</p>
						{addError.detail ? <p className="whitespace-pre-line text-pretty text-[12px] leading-5 text-muted-foreground [overflow-wrap:anywhere]">{addError.detail}</p> : null}
					</div> : null}
					{problemMessage ? <p className={onboardingFieldErrorClass} role="alert">{problemMessage}</p> : null}
					{badName ? (
						<p className={onboardingFieldErrorClass} role="alert">
							{t("cloudWorkspace.invalidRepositoryName", { defaultValue: "{{name}} is not a valid repository name: use letters, digits, \".\", \"-\" and \"_\", up to 100 characters.", name: badName.name || folderLabel(badName.path) })}
						</p>
					) : null}
					{mixedOwners ? (
						<p className={onboardingFieldErrorClass} role="alert">
							{t("cloudWorkspace.mixedOwners", { defaultValue: "A workspace's repositories must all belong to one GitHub account, connected through the same installation. These belong to {{owners}}.", owners: repositoryOwners.join(", ") })}
						</p>
					) : null}
					{memberRoleBlocks ? (
						<p className={onboardingFieldErrorClass} role="alert">
							{t("cloudWorkspace.adminRequired", { defaultValue: "Only organization owners and admins can create a workspace with more than one repository. Ask one to create it, or keep a single repository." })}
						</p>
					) : null}
				</div>

				{showOwnerPicker ? (
					<div className="flex flex-col gap-2 text-sm">
						<Label htmlFor="cloudWorkspaceOwner" className={onboardingFormLabelClass}>
							{t("cloudWorkspace.owner", { defaultValue: "Create new repositories in" })}
						</Label>
						<Select value={owner ?? ""} onValueChange={(value) => { setChosenOwner(value); setSubmitError(null); }} disabled={isCreating}>
							<SelectTrigger id="cloudWorkspaceOwner" className="w-fit min-w-48">
								<SelectValue placeholder={t("cloudWorkspace.ownerPlaceholder", { defaultValue: "Choose a GitHub account" })} />
							</SelectTrigger>
							<SelectContent position="popper" side="bottom" align="start" sideOffset={4}>
								{installationAccounts.map((account) => <SelectItem key={account} value={account}>{account}</SelectItem>)}
							</SelectContent>
						</Select>
					</div>
				) : null}

				{usesCoder && showSetup ? (
					<CloudCoderSetupFields setup={coderSetup} disabled={isCreating} projectName={nameTrimmed} repositoryUrls={urls} />
				) : null}

				{showSetup ? (
					<CloudAgentSetupStep
						onBack={onBack}
						onCreate={(selection) => void create(selection)}
						isCreating={isCreating}
						createError={submitError}
						templateMissing={coderSetup.templateMissing}
						createBlocked={createBlocked}
					/>
				) : (
					<div className={onboardingFooterActionsClass}>
						<Button type="button" variant="secondary" onClick={onBack} disabled={isCreating}>
							{t("createProject.back", { defaultValue: "Back" })}
						</Button>
					</div>
				)}
			</div>
		</div>
	);
}

function WorkspaceMemberRow({ member, owner, progress, disabled, onRename, onRemove }: {
	member: WorkspaceMember;
	owner: string | undefined;
	progress?: MemberProgress;
	disabled: boolean;
	onRename: (value: string) => void;
	onRemove: () => void;
}) {
	const { t } = useTranslation();
	const label = member.kind === "repository" ? member.fullName : folderLabel(member.path);
	const folder = repoDirName(workspaceMemberUrl(member, owner));
	const detail = member.kind === "repository"
		? member.fromFolder
			? t("cloudWorkspace.fromFolder", { defaultValue: "The origin of {{folder}}", folder: folderLabel(member.fromFolder) })
			: undefined
		: member.created
			? member.created.repository.fullName
			: member.committed
				? committedUploadHint(t, member.committed)
				: t("cloudWorkspace.newPrivateRepository", { defaultValue: "Uploaded to a new private repository" });
	return (
		<li className="flex min-w-0 items-center gap-3 px-3 py-2">
			<span className="grid size-6 shrink-0 place-items-center text-muted-foreground" aria-hidden="true">
				{member.kind === "repository" ? <GitHubIcon className="size-4" /> : <Folder className="size-4" />}
			</span>
			<span className="min-w-0 flex-1">
				<span className="block text-[13px] font-medium text-foreground [overflow-wrap:anywhere]" title={member.kind === "folder" ? member.path : undefined}>{label}</span>
				{detail ? <span className="block text-pretty text-[12px] leading-5 text-muted-foreground [overflow-wrap:anywhere]">{detail}</span> : null}
				{member.kind === "folder" && member.leftOut?.length ? (
					<span className="block text-pretty text-[12px] leading-5 text-muted-foreground [overflow-wrap:anywhere]">{leftOutNotice(t, member.leftOut)}</span>
				) : null}
				{progress?.phase === "creating" || progress?.phase === "uploading" ? (
					<span className="block text-[12px] leading-5 text-muted-foreground" role="status">
						{progress.phase === "creating"
							? t("cloudWorkspace.creatingRepository", { defaultValue: "Creating repository" })
							: t("cloudWorkspace.uploading", { defaultValue: "Uploading" })}
					</span>
				) : null}
				{progress?.phase === "failed" && progress.message ? (
					<span className="block text-pretty text-[12px] leading-5 text-destructive" role="alert">{progress.message}</span>
				) : null}
				{progress?.phase === "failed" && progress.detail ? (
					<span className="block whitespace-pre-line text-pretty text-[12px] leading-5 text-muted-foreground">{progress.detail}</span>
				) : null}
				{progress?.phase === "failed" && member.kind === "folder" && member.created && !member.pushed ? (
					<span className="block text-pretty text-[12px] leading-5 text-muted-foreground [overflow-wrap:anywhere]">{emptyRepositoryNotice(t, member.created, member.path)}</span>
				) : null}
			</span>
			{member.kind === "folder" && !member.created ? (
				<Input
					aria-label={t("cloudWorkspace.repositoryName", { defaultValue: "Repository name for {{folder}}", folder: label })}
					className="h-7 w-40 shrink-0 font-mono text-[12px]"
					value={member.name}
					maxLength={100}
					spellCheck={false}
					autoCapitalize="off"
					autoComplete="off"
					disabled={disabled}
					onChange={(event) => onRename(event.target.value)}
				/>
			) : (
				<span className="hidden shrink-0 font-mono text-[12px] text-muted-foreground sm:inline" title={t("cloudWorkspace.folderHint", { defaultValue: "Its folder in the workspace" })}>
					{`${folder}/`}
				</span>
			)}
			<MemberProgressIcon progress={progress} />
			<Button type="button" variant="destructive-ghost" size="icon-sm" disabled={disabled}
				aria-label={t("cloudRepos.remove", { defaultValue: "Remove {{name}}", name: label })} onClick={onRemove}>
				<X aria-hidden="true" className="size-icon-sm" />
			</Button>
		</li>
	);
}

function MemberProgressIcon({ progress }: { progress?: MemberProgress }) {
	const { t } = useTranslation();
	if (!progress) return null;
	switch (progress.phase) {
		case "creating":
		case "uploading":
			// The words sit under the folder's name (WorkspaceMemberRow), so the
			// name keeps its width while the folder uploads.
			return <LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-muted-foreground" />;
		case "done":
			return <CheckCircle2 aria-label={t("cloudWorkspace.uploaded", { defaultValue: "Uploaded" })} className="size-4 shrink-0 text-success" />;
		case "failed":
			return <XCircle aria-label={t("cloudWorkspace.uploadFailed", { defaultValue: "Upload failed" })} className="size-4 shrink-0 text-destructive" />;
	}
}
