import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Check, CircleAlert, Loader2 } from "@/components/app-icons";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { cloudProjectsQueryKey, cloudSessionsQueryKey, useCloudProjectsQuery, useCloudSessionsQuery, workspaceQueryKeyForHost, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { apiErrorException } from "../lib/api-error-exception";
import { uploadCloudTranscript, uploadCloudTranscriptSidecar } from "../lib/cloud-attachments";
import { useUnfinishedMoveStore } from "../lib/move-finish";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { CloudCpError } from "../lib/cloud-cp";
import { cloudHarness, localHarness, MoveError, moveSession, moveSessionHere, type CloudSessionRequest, type MoveHereResult, type MoveResult, type MoveStep, type MoveTarget } from "../lib/move-session";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { repoKey } from "../lib/repo-key";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { formatBytes } from "./ReadOnlyFileView";
import { LAST_CLONE_DESTINATION_KEY } from "./CloneRepositoryDialog";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

/** The session the Move to AO Cloud dialog is open for, mounted once in the shell. */
export function MoveSessionDialog() {
	const session = useUiStore((state) => state.moveSessionTarget);
	const close = useUiStore((state) => state.closeMoveSession);
	if (!session) return null;
	return session.cloud ? (
		<MoveHereDialogBody key={session.id} session={session} orgId={session.cloud.orgId} onClose={close} />
	) : (
		<MoveSessionDialogBody key={session.id} session={session} onClose={close} />
	);
}

/** The session-move feature AO Cloud advertises once createSession takes a branch. */
const CLOUD_BRANCH_FEATURE = "session.branch";

/** The feature AO Cloud advertises once a moved session can resume its conversation. */
const CLOUD_TRANSCRIPT_FEATURE = "session.transcriptImport";

const STEPS: MoveStep[] = ["stop", "export", "import", "complete"];

/** Uncommitted paths listed before the rest are summed up as a count. */
const LISTED_FILES = 5;

/** git status --short letters, so a deleted file does not read as one that moves. */
const STATUS_LETTER = { added: "A", modified: "M", deleted: "D" } as const;

function MoveSessionDialogBody({ session, onClose }: { session: WorkspaceSession; onClose: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const { client: cloudClient, baseUrl: cloudBaseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const offering = useCloudOffering();
	const cloudProjects = useCloudProjectsQuery();
	const cloudSessions = useCloudSessionsQuery();
	const sourceHost: HostId = session.hostId ?? LOCAL_HOST;
	const [step, setStep] = useState<MoveStep>();
	const [result, setResult] = useState<MoveResult>();
	const [error, setError] = useState<MoveError | Error>();
	const moving = step !== undefined && !result && !error;
	// One move per dialog: Try again reuses its id and Cloud request, so a
	// Cloud session whose response was lost is returned instead of duplicated.
	const moveId = useRef(crypto.randomUUID()).current;
	const cloudRequest = useRef<CloudSessionRequest>(undefined);
	// A Cloud project the move created; Try again moves into it instead of creating another.
	const createdProject = useRef<string>(undefined);
	const [steps, setSteps] = useState<MoveStep[]>(STEPS);

	const sourceRepo = useQuery({
		queryKey: ["move-source-repo", sourceHost, session.workspaceId],
		queryFn: async () => {
			const project = (await clientForSessionHost(sourceHost).GET("/api/v1/projects/{id}", { params: { path: { id: session.workspaceId } } })).data?.project;
			return { key: repoKey(projectRepo(project)), name: projectField(project, "name"), defaultBranch: projectField(project, "defaultBranch") };
		},
	});
	// What the move would push now, or why it cannot move.
	const preview = useQuery({
		queryKey: ["move-preview", sourceHost, session.id],
		queryFn: async () => {
			const { data, error: failure } = await clientForSessionHost(sourceHost).GET("/api/v1/sessions/{sessionId}/move/preview", {
				params: { path: { sessionId: session.id } },
			});
			if (failure || !data) throw apiErrorException(failure, "Could not read the session's changes");
			return data;
		},
		retry: false,
		// Reopening the dialog must not show the last preview while it reloads.
		gcTime: 0,
	});
	const signedIn = Boolean(cloudClient && org);
	const loading = signedIn && (sourceRepo.isPending || cloudProjects.isPending || cloudSessions.isPending || preview.isPending);
	const repo = sourceRepo.data?.key ?? "";
	const project = repo ? (cloudProjects.data ?? []).find((candidate) => repoKey(candidate.repositoryUrl) === repo) : undefined;
	// Cloud has no project for this GitHub repository: the move creates one first.
	const newProject = !loading && !project && repo.startsWith("github.com/")
		? { displayName: sourceRepo.data?.name || repo.split("/").at(-1) || repo, repositoryUrl: `https://${repo}`, defaultBranch: sourceRepo.data?.defaultBranch || "main" }
		: undefined;
	const harness = cloudHarness(session.provider);
	const branch = preview.data?.branch ?? "";
	// Cloud refuses a second live session on a branch, but only after the push.
	// Checked before the first attempt only: after it, the holder can be this move's own session.
	const branchHolder =
		project && step === undefined
			? (cloudSessions.data ?? []).find((s) => s.projectId === project.id && s.branch === branch && !s.isTerminated)
			: undefined;
	const unavailable = !signedIn
		? t("move.signedOut")
		: loading
			? undefined
			: !project && !newProject
				? // Without an origin there is no repository to look up; the daemon says why.
					(!repo && preview.error?.message) || t("move.noCloudProject")
				: !offering.features.includes(CLOUD_BRANCH_FEATURE)
					? t("move.cloudNeedsUpdate")
					: !(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(harness)
						? t("move.cloudAgentUnsupported", { agent: harness })
						: preview.error
							? preview.error.message
							: branchHolder
								? t("move.branchInUse", { branch, session: branchHolder.displayName })
								: undefined;
	// An empty projectId has the move create newProject.
	const target: MoveTarget | undefined =
		org && (project || newProject || step !== undefined) && !loading && !unavailable
			? { orgId: org.id, projectId: createdProject.current ?? project?.id ?? "", harness }
			: undefined;
	const changes = preview.data?.changes ?? [];
	const transcript = preview.data?.transcript;
	// Cloud resumes the agent's conversation; without it the session starts from the handoff note.
	const carriesTranscript =
		offering.features.includes(CLOUD_TRANSCRIPT_FEATURE) && Boolean(transcript && !transcript.unavailable && transcript.bytes > 0);

	const start = async () => {
		if (!target || !cloudClient) return;
		if (!target.projectId && step === undefined) setSteps(["project", ...STEPS]);
		setError(undefined);
		setResult(undefined);
		try {
			const moved = await moveSession(
				{
					sessionId: session.id,
					moveId,
					target,
					...(target.projectId || !newProject ? {} : { createProject: newProject }),
					...(sourceHost !== LOCAL_HOST ? { sourceLabel: labelForHost(sourceHost) } : {}),
				},
				{
					source: clientForSessionHost(sourceHost),
					cloud: cloudClient,
					onStep: setStep,
					onProjectCreated: (id) => {
						createdProject.current = id;
					},
					cloudRequest,
					...(carriesTranscript
						? {
							uploadTranscript: (importId: string, harness: string, agentSessionId: string, gzip: Uint8Array) => uploadCloudTranscript(cloudBaseUrl, target.orgId, importId, harness, agentSessionId, gzip),
							uploadTranscriptSidecar: (importId: string, gzip: Uint8Array) => uploadCloudTranscriptSidecar(cloudBaseUrl, target.orgId, importId, gzip),
						}
						: {}),
				},
			);
			if (moved.completeError) {
				// The session runs in Cloud; recording that here is retried until it lands.
				useUnfinishedMoveStore.getState().add({
					kind: "complete", host: session.hostId, sessionId: session.id, moveId, orgId: target.orgId, cloudSessionId: moved.sessionId,
				});
				setResult(moved);
				return;
			}
			onClose();
			navigateToSession(moved.projectId, moved.sessionId);
			// Say so when the conversation was to move but only the note did.
			showGlobalToast(t("move.done"), carriesTranscript && moved.conversation === "note" ? t("move.doneNote") : undefined, { tone: "success" });
		} catch (failure) {
			setError(failure instanceof Error ? failure : new Error(String(failure)));
		} finally {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(sourceHost) });
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
			void queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		}
	};

	const openMoved = () => {
		if (!result) return;
		onClose();
		navigateToSession(result.projectId, result.sessionId);
	};

	return (
		<Dialog open onOpenChange={(open) => !open && !moving && onClose()}>
			<DialogContent
				className={settingsDialogContentClass}
				data-testid="move-session-dialog"
				showCloseButton={!moving}
				onEscapeKeyDown={(event) => moving && event.preventDefault()}
				onInteractOutside={(event) => moving && event.preventDefault()}
			>
				<div className={cn(settingsDialogHeaderClass, "py-4")}>
					<DialogTitle>{t("move.title", { title: session.title })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3 pt-4")}>
					{step === undefined ? (
						loading ? (
							<p role="status" className="flex items-center gap-2 text-sm text-settings-muted">
								<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
								{t("move.loading")}
							</p>
						) : unavailable ? (
							<p className="text-sm text-settings-muted">{unavailable}</p>
						) : (
							<>
								<dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-sm">
									<dt className="text-settings-muted">{t("move.branch")}</dt>
									<dd className="min-w-0 truncate font-mono text-settings-label">{branch}</dd>
									<dt className="text-settings-muted">{t("move.cloudProject")}</dt>
									{project ? (
										<dd className="min-w-0 truncate text-settings-label">{project.displayName}</dd>
									) : (
										<dd className="flex min-w-0 gap-2">
											<span className="truncate text-settings-label">{newProject?.displayName}</span>
											<span className="truncate text-settings-muted">{t("move.newCloudProject")}</span>
										</dd>
									)}
									<dt className="text-settings-muted">{t("move.conversation")}</dt>
									<dd className="text-settings-label">
										{carriesTranscript && transcript
											? transcript.subagents
												? t("move.conversationTranscriptSubagents", { count: transcript.subagents, size: formatBytes(transcript.bytes) })
												: t("move.conversationTranscript", { size: formatBytes(transcript.bytes) })
											: transcript?.unavailable === "too_large"
												? t("move.conversationTooLarge")
												: t("move.conversationNote")}
									</dd>
									<AgentRow session={session} />
									<dt className="text-settings-muted">{t("move.uncommitted")}</dt>
									<dd className="text-settings-label">{changes.length || t("move.uncommittedNone")}</dd>
								</dl>
								<UncommittedList changes={changes} />
								<p className="text-sm text-settings-muted">
									{changes.length > 0 ? t("move.consequence", { branch }) : t("move.consequenceClean", { branch })}
								</p>
							</>
						)
					) : (
						<MoveStepList
							steps={steps}
							step={step}
							error={error}
							finished={Boolean(result)}
							lastFailed={Boolean(result?.completeError)}
							label={(candidate) =>
								candidate === "project"
									? t("move.step.project", { repo: repo.split("/").slice(1).join("/") })
									: t(`move.step.${candidate as Exclude<MoveStep, "clone" | "project">}`, { branch })}
						/>
					)}
					{error ? (
						<div role="alert" className="space-y-1 text-sm text-destructive">
							<p>{error.message}</p>
							{error instanceof MoveError && error.pushed ? (
								<p className="text-settings-muted">
									{error.pushed.wip
										? t("move.failedPushedWip", { commit: error.pushed.tip.slice(0, 7), branch: error.pushed.branch })
										: t("move.failedPushed", { branch: error.pushed.branch })}
								</p>
							) : null}
							<p className="text-settings-muted">
								{error instanceof MoveError && !error.rolledBack
									? t("move.failedNotRolledBack")
									: error instanceof MoveError && error.pushed
										? t("move.failedResumed")
										: t("move.failedRolledBack")}
							</p>
						</div>
					) : null}
					{result?.completeError ? (
						<p role="alert" className="text-sm text-settings-label">{t("move.completeFailed")}</p>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "py-4")}>
					{result ? (
						<Button type="button" variant="primary" onClick={openMoved}>
							{t("move.open")}
						</Button>
					) : (
						<>
							<Button type="button" variant="secondary" onClick={onClose} disabled={moving}>
								{t("confirm.cancel")}
							</Button>
							<Button type="button" variant="primary" onClick={() => void start()} disabled={!target || moving}>
								{moving ? t("move.moving") : error ? t("move.retry") : t("move.submit")}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}

/** Says a working agent is stopped by the move; absent while it is idle. */
function AgentRow({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const state = session.activity?.state;
	if (state !== "active" && state !== "blocked") return null;
	return (
		<>
			<dt className="text-settings-muted">{t("move.agent")}</dt>
			<dd className="text-settings-label">{t("move.agentWorking")}</dd>
		</>
	);
}

function UncommittedList({ changes }: { changes: { path: string; status: keyof typeof STATUS_LETTER }[] }) {
	const { t } = useTranslation();
	if (changes.length === 0) return null;
	return (
		<ul className="flex flex-col gap-0.5 font-mono text-xs text-settings-muted" aria-label={t("move.uncommitted")}>
			{changes.slice(0, LISTED_FILES).map((change) => (
				<li key={change.path} className="break-all">
					{STATUS_LETTER[change.status]} {change.path}
				</li>
			))}
			{changes.length > LISTED_FILES ? <li className="font-sans">{t("move.uncommittedMore", { count: changes.length - LISTED_FILES })}</li> : null}
		</ul>
	);
}

/** The move's four steps; finished is set once the session runs at its destination. */
function MoveStepList({ steps = STEPS, step, error, finished, lastFailed, label }: {
	steps?: MoveStep[];
	step: MoveStep;
	error?: Error;
	finished: boolean;
	lastFailed: boolean;
	label: (step: MoveStep) => string;
}) {
	const current = steps.indexOf(step);
	const stepState = (candidate: MoveStep): "done" | "running" | "failed" | "pending" => {
		const index = steps.indexOf(candidate);
		if (error instanceof MoveError && error.step === candidate) return "failed";
		if (finished && candidate === "complete" && lastFailed) return "failed";
		if (index < current || (finished && index <= current)) return "done";
		return index === current && !error ? "running" : "pending";
	};
	return (
		<ol className="flex flex-col gap-2" aria-live="polite">
			{steps.map((candidate) => {
				const state = stepState(candidate);
				return (
					<li key={candidate} className="flex items-center gap-2.5 text-sm">
						{state === "done" ? (
							<Check className="size-icon-sm text-success" aria-hidden="true" />
						) : state === "running" ? (
							<Loader2 className="size-icon-sm animate-spin text-muted-foreground" aria-hidden="true" />
						) : state === "failed" ? (
							<CircleAlert className="size-icon-sm text-destructive" aria-hidden="true" />
						) : (
							<span className="size-icon-sm" aria-hidden="true" />
						)}
						<span className={state === "pending" ? "text-settings-muted" : "text-settings-label"}>{label(candidate)}</span>
					</li>
				);
			})}
		</ol>
	);
}

/** Moves an AO Cloud session to a project on this computer. */
function MoveHereDialogBody({ session, orgId, onClose }: { session: WorkspaceSession; orgId: string; onClose: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const { client: cloudClient } = useCloudCp();
	const cloudProjects = useCloudProjectsQuery();
	const localProjects = useQuery(workspaceQueryOptions);
	const readiness = useAgentReadinessQuery();
	const [step, setStep] = useState<MoveStep>();
	const [result, setResult] = useState<MoveHereResult>();
	const [error, setError] = useState<Error>();
	const [chosen, setChosen] = useState<string>();
	const [steps, setSteps] = useState<MoveStep[]>(STEPS);
	const moving = step !== undefined && !result && !error;
	// One move per dialog: Try again reuses its id, so this computer returns
	// the session it already started instead of starting a second one.
	const moveId = useRef(crypto.randomUUID()).current;
	// A project the move cloned; Try again moves into it instead of cloning again.
	const cloned = useRef<string>(undefined);
	// The folder the New project flow last cloned into, as it defaults to.
	const cloneParent = useRef(window.localStorage.getItem(LAST_CLONE_DESTINATION_KEY) || "~/ao/projects").current;

	const repositoryUrl = cloudProjects.data?.find((project) => project.id === session.workspaceId)?.repositoryUrl ?? "";
	const repo = repoKey(repositoryUrl);
	const repoName = repo.split("/").slice(1).join("/");
	const preview = useQuery({
		queryKey: ["move-here-preview", orgId, session.id],
		queryFn: () => cloudClient!.previewSessionMove(orgId, session.id),
		enabled: Boolean(cloudClient),
		retry: false,
		gcTime: 0,
	});
	const branch = preview.data?.branch ?? "";
	// Refetched when a project is added here.
	const localProjectIds = (localProjects.data ?? []).map((project) => project.id).sort().join(",");
	const targets = useQuery({
		queryKey: ["move-here-targets", repo, branch, localProjectIds],
		queryFn: async () => {
			const { data, error: failure } = await clientForSessionHost(LOCAL_HOST).GET("/api/v1/sessions/move/targets", {
				params: { query: { repoKey: repo, branch } },
			});
			if (failure || !data) throw apiErrorException(failure, "Could not look for a project here");
			return data.targets;
		},
		enabled: Boolean(repo && branch) && step === undefined,
		retry: false,
		gcTime: 0,
	});
	const agent = readiness.data?.agents.find((candidate) => candidate.id === localHarness(session.provider));
	// The targets query stops once a move starts; a clone then changes its key, leaving it pending.
	const loading = Boolean(cloudClient) && (preview.isPending || cloudProjects.isPending || (step === undefined && Boolean(repo && branch) && targets.isPending));
	const candidates = targets.data ?? [];
	const target = candidates.find((candidate) => candidate.projectId === chosen) ?? candidates.find((candidate) => !candidate.refusal) ?? candidates[0];
	// No project here has the repository: the move clones it first.
	const cloneInto = !loading && targets.isSuccess && candidates.length === 0 ? cloneParent : undefined;
	const unavailable = !cloudClient
		? t("move.signedOut")
		: loading
			? undefined
			: preview.error
				? preview.error instanceof CloudCpError && (preview.error.status === 0 || preview.error.status >= 500)
					? t("moveHere.sandboxUnreachable")
					: preview.error.message
				: !repo
					? t("moveHere.noRepository")
					: agent?.effectiveReadiness === "not_ready"
						? t("moveHere.agentNotSetUp", { agent: agent.label })
						: targets.error
							? targets.error.message
							: target?.refusal?.message;
	// Try again moves into the project the first attempt chose or cloned, so it needs neither.
	const ready = !loading && !unavailable && (step !== undefined || target !== undefined || cloneInto !== undefined);
	const changes = preview.data?.changes ?? [];
	const transcript = ["claude-code", "codex", "opencode"].includes(session.provider) ? preview.data?.transcript : undefined;

	const start = async () => {
		if (!ready || !cloudClient) return;
		const projectId = cloned.current ?? target?.projectId;
		if (!projectId && step === undefined) setSteps(["clone", ...STEPS]);
		setError(undefined);
		setResult(undefined);
		try {
			const moved = await moveSessionHere(
				{
					orgId,
					cloudSessionId: session.id,
					...(projectId ? { projectId } : { clone: { remoteUrl: repositoryUrl, destinationParent: cloneParent, repoKey: repo } }),
					moveId,
					settings: {
						autoReview: session.autoReviewEnabled ?? false,
						terminateOnPrMerge: session.terminateOnPrMerge ?? false,
						autoInjectReview: session.autoInjectReview ?? false,
						autoInjectCI: session.autoInjectCI ?? false,
					},
				},
				{
					daemon: clientForSessionHost(LOCAL_HOST),
					cloud: cloudClient,
					onStep: setStep,
					onCloned: (id) => {
						cloned.current = id;
					},
				},
			);
			if (moved.deleteError) {
				// The session runs here; deleting the Cloud copy is retried until it lands.
				useUnfinishedMoveStore.getState().add({ kind: "delete-cloud", orgId, cloudSessionId: session.id });
				setResult(moved);
				return;
			}
			onClose();
			navigateToSession(moved.projectId, moved.sessionId);
			const body = [t("moveHere.doneBody", { title: session.title, project: target?.name ?? repoName.split("/").at(-1) }), ...(moved.keptInCloud ? [t("moveHere.doneKeptInCloud")] : []), ...moved.warnings];
			showGlobalToast(t("moveHere.done"), body.join(" "), { tone: "success" });
		} catch (failure) {
			setError(failure instanceof Error ? failure : new Error(String(failure)));
		} finally {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(LOCAL_HOST) });
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		}
	};

	const openMoved = () => {
		if (!result) return;
		onClose();
		navigateToSession(result.projectId, result.sessionId);
	};

	return (
		<Dialog open onOpenChange={(open) => !open && !moving && onClose()}>
			<DialogContent
				className={settingsDialogContentClass}
				data-testid="move-here-dialog"
				showCloseButton={!moving}
				onEscapeKeyDown={(event) => moving && event.preventDefault()}
				onInteractOutside={(event) => moving && event.preventDefault()}
			>
				<div className={cn(settingsDialogHeaderClass, "py-4")}>
					<DialogTitle>{t("moveHere.title", { title: session.title })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3 pt-4")}>
					{step === undefined ? (
						loading ? (
							<p role="status" className="flex items-center gap-2 text-sm text-settings-muted">
								<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
								{t("move.loading")}
							</p>
						) : (
							<>
								{preview.data ? (
									<dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-sm">
										<dt className="text-settings-muted">{t("move.branch")}</dt>
										<dd className="min-w-0 truncate font-mono text-settings-label">{branch}</dd>
										{target ? (
											<>
												<dt className="self-center text-settings-muted">{t("moveHere.project")}</dt>
												<dd className="min-w-0">
													{candidates.length > 1 ? (
														<Select value={target.projectId} onValueChange={setChosen}>
															<SelectTrigger size="sm" className="w-full text-control" aria-label={t("moveHere.project")}>
																<SelectValue />
															</SelectTrigger>
															<SelectContent position="popper" side="bottom" align="start" sideOffset={4} className="max-h-64">
																{candidates.map((candidate) => (
																	<SelectItem key={candidate.projectId} value={candidate.projectId}>
																		{candidate.name} <span className="text-settings-muted">{homePath(candidate.path)}</span>
																	</SelectItem>
																))}
															</SelectContent>
														</Select>
													) : (
														<span className="flex min-w-0 gap-2">
															<span className="truncate text-settings-label">{target.name}</span>
															<span className="truncate text-settings-muted">{homePath(target.path)}</span>
														</span>
													)}
												</dd>
											</>
										) : cloneInto ? (
											<>
												<dt className="text-settings-muted">{t("moveHere.project")}</dt>
												<dd className="flex min-w-0 gap-2">
													<span className="truncate text-settings-label">{repoName.split("/").at(-1)}</span>
													<span className="truncate text-settings-muted">{t("moveHere.cloneInto", { path: homePath(cloneParent) })}</span>
												</dd>
											</>
										) : null}
										<dt className="text-settings-muted">{t("move.conversation")}</dt>
										<dd className="text-settings-label">
											{transcript && transcript.bytes > 0
												? transcript.subagents
													? t("move.conversationTranscriptSubagents", { count: transcript.subagents, size: formatBytes(transcript.bytes) })
													: t("move.conversationTranscript", { size: formatBytes(transcript.bytes) })
												: t("move.conversationNote")}
										</dd>
										{ready ? (
											<>
												<AgentRow session={session} />
												<dt className="text-settings-muted">{t("move.uncommitted")}</dt>
												<dd className="text-settings-label">{changes.length || t("move.uncommittedNone")}</dd>
											</>
										) : null}
									</dl>
								) : null}
								{unavailable ? (
									<p className="text-sm text-settings-muted">{unavailable}</p>
								) : (
									<>
										<UncommittedList changes={changes} />
										<p className="text-sm text-settings-muted">
											{changes.length > 0 ? t("moveHere.consequence", { branch }) : t("moveHere.consequenceClean", { branch })}
										</p>
									</>
								)}
							</>
						)
					) : (
						<MoveStepList
							steps={steps}
							step={step}
							error={error}
							finished={Boolean(result)}
							lastFailed={Boolean(result?.deleteError)}
							label={(candidate) =>
								candidate === "export"
									? t("move.step.export", { branch })
									: candidate === "clone"
										? t("moveHere.step.clone", { repo: repoName, path: homePath(cloneParent) })
										: t(`moveHere.step.${candidate as Exclude<MoveStep, "clone" | "export" | "project">}`)}
						/>
					)}
					{error ? (
						<div role="alert" className="space-y-1 text-sm text-destructive">
							<p>{error.message}</p>
							{error instanceof MoveError && error.pushed ? (
								<p className="text-settings-muted">
									{error.pushed.wip
										? t("moveHere.failedPushedWip", { commit: error.pushed.tip.slice(0, 7), branch: error.pushed.branch })
										: t("move.failedPushed", { branch: error.pushed.branch })}
								</p>
							) : null}
							<p className="text-settings-muted">
								{error instanceof MoveError && error.step === "clone"
									? t("moveHere.failedClone")
									: error instanceof MoveError && !error.rolledBack
										? t("moveHere.failedNotRestarted")
										: t("moveHere.failedRestarted")}
							</p>
						</div>
					) : null}
					{result?.deleteError ? (
						<p role="alert" className="text-sm text-settings-label">{t("moveHere.deleteFailed", { title: session.title })}</p>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "py-4")}>
					{result ? (
						<Button type="button" variant="primary" onClick={openMoved}>
							{t("move.open")}
						</Button>
					) : (
						<>
							<Button type="button" variant="secondary" onClick={onClose} disabled={moving}>
								{t("confirm.cancel")}
							</Button>
							<Button type="button" variant="primary" onClick={() => void start()} disabled={!ready || moving}>
								{moving ? t("move.moving") : error ? t("move.retry") : t("moveHere.submit")}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}

function homePath(path: string): string {
	return path.replace(/^\/(?:Users|home)\/[^/]+/, "~");
}

/** A project's origin URL; a project the daemon could not resolve has none. */
function projectRepo(project: object | undefined): string {
	return projectField(project, "repo");
}

function projectField(project: object | undefined, field: "repo" | "name" | "defaultBranch"): string {
	const value = project && field in project ? (project as Record<string, unknown>)[field] : undefined;
	return typeof value === "string" ? value : "";
}
