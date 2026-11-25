import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Check, CircleAlert, Loader2 } from "@/components/app-icons";
import { AoError } from "../../../../packages/shared/telemetry/ao-error";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { cloudProjectsQueryKey, cloudSessionsQueryKey, useCloudProjectsQuery, useCloudSessionsQuery, workspaceQueryKeyForHost, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { apiErrorException } from "../lib/api-error-exception";
import { uploadCloudTranscript, uploadCloudTranscriptSidecar } from "../lib/cloud-attachments";
import { unsettledMoveHereKey, unsettledMoveKey, useUnfinishedMoveStore, useUnsettledMoveHereStore, useUnsettledMoveStore } from "../lib/move-finish";
import { agentLabel } from "../lib/agent-options";
import { CLOUD_AGENT_PROVIDERS, isCloudHarnessConnected } from "../lib/cloud-agents";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { CloudCpError } from "../lib/cloud-cp";
import { CLOUD_BRANCH_FEATURE, cloudHarness, localHarness, MoveError, moveSession, moveSessionHere, type MoveDeps, type MoveHereResult, type MoveResult, type MoveStep, type MoveTarget } from "../lib/move-session";
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

/** The feature AO Cloud advertises once a moved session can resume its conversation. */
const CLOUD_TRANSCRIPT_FEATURE = "session.transcriptImport";

const STEPS: MoveStep[] = ["stop", "export", "import", "complete"];

/** git status --short letters, so a deleted file does not read as one that moves; ?? is an untracked file. */
const STATUS_LETTER = { added: "A", modified: "M", deleted: "D" } as const;

function MoveSessionDialogBody({ session, onClose }: { session: WorkspaceSession; onClose: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const { client: cloudClient, baseUrl: cloudBaseUrl, userId } = useCloudCp();
	const { org } = useCloudOrg();
	const offering = useCloudOffering();
	const cloudProjects = useCloudProjectsQuery();
	const cloudSessions = useCloudSessionsQuery();
	const sourceHost: HostId = session.hostId ?? LOCAL_HOST;
	const [step, setStep] = useState<MoveStep>();
	const [result, setResult] = useState<MoveResult>();
	const [error, setError] = useState<MoveError | Error>();
	const moving = step !== undefined && !result && !error;
	// A move whose Cloud create was sent and not settled is kept past this
	// dialog, and past a quit, so reopening it finishes that move under the same
	// id instead of starting a second Cloud session. A move for another
	// organization or account is left alone.
	const attemptKey = unsettledMoveKey(session.hostId, session.id);
	const storedAttempt = useRef(useUnsettledMoveStore.getState().moves[attemptKey]).current;
	const saved =
		storedAttempt && (!org || storedAttempt.orgId === org.id) && (!storedAttempt.accountId || !userId || storedAttempt.accountId === userId)
			? storedAttempt
			: undefined;
	// One move per dialog: Try again reuses its id and Cloud request, so a
	// Cloud session whose response was lost is returned instead of duplicated.
	// A move whose export pushed the session and did not finish keeps its id
	// on this computer too.
	const moveId = useRef(saved?.moveId ?? session.moveStarted ?? crypto.randomUUID()).current;
	const cloudRequest = useRef<NonNullable<MoveDeps["cloudRequest"]>>(
		saved ? { current: saved.request, key: saved.key, pushed: saved.pushed, createUnsettled: true } : {},
	).current;
	// A Cloud project the move created; Try again moves into it instead of creating another.
	// An earlier move being finished goes to the project its request names.
	const createdProject = useRef<string | undefined>(saved?.projectId ?? saved?.request.projectId);
	const [steps, setSteps] = useState<MoveStep[]>(STEPS);
	// Finishing an earlier move needs neither a new preview nor its checks.
	const finishing = step !== undefined || Boolean(saved);

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
	const connections = useProviderConnections(signedIn);
	const loading = signedIn && (sourceRepo.isPending || cloudProjects.isPending || cloudSessions.isPending || preview.isPending || connections.isPending);
	const repo = sourceRepo.data?.key ?? "";
	const project = repo ? (cloudProjects.data ?? []).find((candidate) => repoKey(candidate.repositoryUrl) === repo) : undefined;
	// The repository's default branch: the daemon resolves it from origin when
	// the project only knows it as "auto", which Cloud would take literally.
	const localDefault = sourceRepo.data?.defaultBranch === "auto" ? "" : sourceRepo.data?.defaultBranch;
	const defaultBranch = preview.data?.defaultBranch || localDefault || "";
	// Cloud has no project for this GitHub repository: the move creates one first.
	const newProject = !loading && !project && repo.startsWith("github.com/")
		? { displayName: sourceRepo.data?.name || repo.split("/").at(-1) || repo, repositoryUrl: `https://${repo}`, defaultBranch }
		: undefined;
	const harness = cloudHarness(session.provider);
	const branch = preview.data?.branch ?? "";
	// Cloud refuses a second live session on a branch, but only after the push.
	// Checked before the first attempt only: after it, the holder can be this move's own session.
	const branchHolder =
		project && !finishing
			? (cloudSessions.data ?? []).find((s) => s.projectId === project.id && s.branch === branch && !s.isTerminated)
			: undefined;
	// Refused here so nothing is pushed: Cloud would start the session on the
	// project's default branch, and the push would land the work on it.
	const onCloudDefault = Boolean(project && branch && project.defaultBranch === branch);
	const unavailable = !signedIn
		? t("move.signedOut")
		: loading
			? undefined
			: !project && !newProject
				? // Without an origin there is no repository to look up; the daemon says why.
					(!repo && preview.error?.message) || t("move.noCloudProject")
				: !offering.features.includes(CLOUD_BRANCH_FEATURE)
					? t("move.cloudNeedsUpdate")
					: finishing
						? undefined
						: !(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(harness)
							? t("move.cloudAgentUnsupported", { agent: harness })
							: !isCloudHarnessConnected(connections.data, harness)
								? t("move.cloudAgentNotConnected", { agent: agentLabel(harness) })
								: preview.error
									? preview.error.message
									: onCloudDefault
										? t("move.onDefaultBranch", { branch })
										: newProject && !defaultBranch
											? t("move.noDefaultBranch")
											: branchHolder
												? t("move.branchInUse", { branch, session: branchHolder.displayName })
												: undefined;
	// An empty projectId has the move create newProject.
	const target: MoveTarget | undefined =
		org && (project || newProject || finishing) && !loading && !unavailable
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
					onCreate: ({ request, key, pushed }) => {
						useUnsettledMoveStore.getState().set(attemptKey, {
							moveId, orgId: target.orgId, request, key, pushed, projectId: createdProject.current, accountId: userId,
						});
					},
					...(carriesTranscript
						? {
							uploadTranscript: (importId: string, harness: string, agentSessionId: string, gzip: Uint8Array) => uploadCloudTranscript(cloudBaseUrl, target.orgId, importId, harness, agentSessionId, gzip),
							uploadTranscriptSidecar: (importId: string, gzip: Uint8Array) => uploadCloudTranscriptSidecar(cloudBaseUrl, target.orgId, importId, gzip),
						}
						: {}),
				},
			);
			useUnsettledMoveStore.getState().set(attemptKey, undefined);
			if (moved.completeError) {
				// The session runs in Cloud; recording that here is retried until it lands.
				useUnfinishedMoveStore.getState().add({
					kind: "complete", host: session.hostId, sessionId: session.id, moveId, orgId: target.orgId, cloudSessionId: moved.sessionId, accountId: userId,
				});
				setResult(moved);
				return;
			}
			onClose();
			navigateToSession(moved.projectId, moved.sessionId);
			// Say so when the conversation was to move but only the note did.
			showGlobalToast(t("move.done"), carriesTranscript && moved.conversation === "note" ? t("move.doneNote") : undefined, { tone: "success" });
		} catch (failure) {
			// Kept only while Cloud may hold a session of this move; otherwise a
			// later move starts afresh, from the session as it is then.
			useUnsettledMoveStore.getState().set(
				attemptKey,
				cloudRequest.createUnsettled && cloudRequest.current
					? { moveId, orgId: target.orgId, request: cloudRequest.current, key: cloudRequest.key, pushed: cloudRequest.pushed, projectId: createdProject.current, accountId: userId }
					: undefined,
			);
			setError(failure instanceof Error ? failure : new AoError("SESSION_MOVE_FAILED", String(failure)));
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
						) : saved ? (
							<p className="text-sm text-settings-muted">{t("move.finishEarlier")}</p>
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
								<WithheldList files={preview.data?.withheld ?? []} />
								<p className="text-sm text-settings-muted">
									{changes.length > 0 ? t("move.consequence", { branch }) : t("move.consequenceClean", { branch })}
								</p>
								<p className="text-sm text-settings-muted">{carriesTranscript ? t("move.conversationUploaded") : t("move.noteUploaded")}</p>
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
							{moveFailureNote(error, {
								unknown: t("move.failedUnknown"),
								notRolledBack: t("move.failedNotRolledBack"),
								resumed: t("move.failedResumed"),
								rolledBack: t("move.failedRolledBack"),
							})}
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

/** Every uncommitted path, scrolling past a few lines; untracked files, the ones a user may not expect to push, are marked ??. */
function UncommittedList({ changes }: { changes: { path: string; status: keyof typeof STATUS_LETTER; untracked?: boolean }[] }) {
	const { t } = useTranslation();
	if (changes.length === 0) return null;
	const anyUntracked = changes.some((change) => change.untracked);
	return (
		<>
			<ul
				className="settings-thin-scrollbar flex max-h-40 flex-col gap-0.5 overflow-y-auto font-mono text-xs text-settings-muted"
				aria-label={t("move.uncommitted")}
			>
				{changes.map((change) => (
					<li key={change.path} className={cn("break-all", change.untracked && "text-warning")}>
						{change.untracked ? (
							<abbr title={t("move.untracked")} className="no-underline">
								??
							</abbr>
						) : (
							STATUS_LETTER[change.status]
						)}{" "}
						{change.path}
					</li>
				))}
			</ul>
			{anyUntracked ? <p className="text-sm text-warning">{t("move.untrackedNote")}</p> : null}
		</>
	);
}

/** The secret environment files a move leaves on this computer; they are never pushed. */
function WithheldList({ files }: { files: string[] }) {
	const { t } = useTranslation();
	if (files.length === 0) return null;
	return (
		<div className="space-y-1">
			<p className="text-sm text-settings-label">{t("move.withheldNote")}</p>
			<ul className="settings-thin-scrollbar flex max-h-24 flex-col gap-0.5 overflow-y-auto font-mono text-xs text-settings-muted" aria-label={t("move.withheld")}>
				{files.map((file) => (
					<li key={file} className="break-all">
						{file}
					</li>
				))}
			</ul>
		</div>
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
	const { client: cloudClient, userId } = useCloudCp();
	const offering = useCloudOffering(true, orgId);
	const cloudProjects = useCloudProjectsQuery();
	const localProjects = useQuery(workspaceQueryOptions);
	const readiness = useAgentReadinessQuery();
	const [step, setStep] = useState<MoveStep>();
	const [result, setResult] = useState<MoveHereResult>();
	const [error, setError] = useState<Error>();
	const [chosen, setChosen] = useState<string>();
	const [steps, setSteps] = useState<MoveStep[]>(STEPS);
	const moving = step !== undefined && !result && !error;
	// A move whose import here may have started a session is kept past this
	// dialog, and past a quit, so reopening it finishes that move.
	const hereKey = unsettledMoveHereKey(orgId, session.id);
	const savedHere = useRef(useUnsettledMoveHereStore.getState().moves[hereKey]).current;
	// One move per dialog: Try again reuses its id, so this computer returns
	// the session it already started instead of starting a second one.
	const moveId = useRef(savedHere?.moveId ?? crypto.randomUUID()).current;
	// A project the move cloned, or the one an earlier attempt imported into;
	// Try again moves into it instead of cloning or choosing again.
	const cloned = useRef<string | undefined>(savedHere?.projectId);
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
			: !offering.features.includes(CLOUD_BRANCH_FEATURE)
				? t("move.cloudNeedsUpdate")
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
								: // Finishing an earlier move replays it here; its own session may hold the branch.
									savedHere
									? undefined
									: target?.refusal?.message;
	// Try again moves into the project the first attempt chose or cloned, so it needs neither.
	const ready = !loading && !unavailable && (step !== undefined || savedHere !== undefined || target !== undefined || cloneInto !== undefined);
	const changes = preview.data?.changes ?? [];
	const transcript = ["claude-code", "codex", "opencode", "cursor"].includes(session.provider) ? preview.data?.transcript : undefined;

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
					expectTranscript: Boolean(preview.data?.transcript),
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
					onImport: (projectId) => {
						useUnsettledMoveHereStore.getState().set(hereKey, { moveId, projectId });
					},
				},
			);
			useUnsettledMoveHereStore.getState().set(hereKey, undefined);
			if (moved.deleteError) {
				// The session runs here; deleting the Cloud copy is retried until it lands.
				useUnfinishedMoveStore.getState().add({ kind: "delete-cloud", orgId, cloudSessionId: session.id, accountId: userId });
				setResult(moved);
				return;
			}
			onClose();
			navigateToSession(moved.projectId, moved.sessionId);
			const body = [t("moveHere.doneBody", { title: session.title, project: target?.name ?? repoName.split("/").at(-1) }), ...(moved.keptInCloud ? [t("moveHere.doneKeptInCloud")] : []), ...moved.warnings];
			showGlobalToast(t("moveHere.done"), body.join(" "), { tone: "success" });
		} catch (failure) {
			// Kept only while the import here may have started a session.
			const unsettled = failure instanceof MoveError && failure.step === "import" && failure.followUp !== undefined;
			if (!unsettled) useUnsettledMoveHereStore.getState().set(hereKey, undefined);
			setError(failure instanceof Error ? failure : new AoError("SESSION_MOVE_FAILED", String(failure)));
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
										{savedHere ? null : target ? (
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
								) : savedHere ? (
									<p className="text-sm text-settings-muted">{t("moveHere.finishEarlier")}</p>
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
							{error instanceof MoveError && error.step === "clone" ? (
								<p className="text-settings-muted">{t("moveHere.failedClone")}</p>
							) : (
								moveFailureNote(error, {
									unknown: t("moveHere.failedUnknown"),
									notRolledBack: t("moveHere.failedNotRestarted"),
									resumed: t("moveHere.failedRestarted"),
									rolledBack: t("moveHere.failedRestarted"),
								})
							)}
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

/**
 * The line under a failed move's error that says where the session stands.
 * Nothing is claimed for a failure that is not a MoveError, and a MoveError
 * whose message already says what to do (Try again finishes the move, or
 * check the session) gets no line that could contradict it.
 */
function moveFailureNote(error: Error, notes: { unknown: string; notRolledBack: string; resumed: string; rolledBack: string }) {
	const note = !(error instanceof MoveError)
		? notes.unknown
		: error.followUp
			? undefined
			: !error.rolledBack
				? notes.notRolledBack
				: error.pushed
					? notes.resumed
					: notes.rolledBack;
	return note ? <p className="text-settings-muted">{note}</p> : null;
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
