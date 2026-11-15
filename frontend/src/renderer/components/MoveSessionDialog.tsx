import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Check, CircleAlert, Loader2 } from "@/components/app-icons";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { cloudSessionsQueryKey, useCloudProjectsQuery, useCloudSessionsQuery, workspaceQueryKeyForHost, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { apiErrorException } from "../lib/api-error-exception";
import { uploadCloudTranscript, uploadCloudTranscriptSidecar } from "../lib/cloud-attachments";
import { useUnfinishedMoveStore } from "../lib/move-finish";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { CloudCpError } from "../lib/cloud-cp";
import { MoveError, moveSession, moveSessionHere, type CloudSessionRequest, type MoveHereResult, type MoveResult, type MoveStep, type MoveTarget } from "../lib/move-session";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { repoKey } from "../lib/repo-key";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { formatBytes } from "./ReadOnlyFileView";
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

	const sourceRepo = useQuery({
		queryKey: ["move-source-repo", sourceHost, session.workspaceId],
		queryFn: async () => {
			const project = await clientForSessionHost(sourceHost).GET("/api/v1/projects/{id}", { params: { path: { id: session.workspaceId } } });
			return repoKey(projectRepo(project.data?.project));
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
	const project = sourceRepo.data
		? (cloudProjects.data ?? []).find((candidate) => repoKey(candidate.repositoryUrl) === sourceRepo.data)
		: undefined;
	const harness = session.provider;
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
			: !project
				? // Without an origin there is no repository to look up; the daemon says why.
					(!sourceRepo.data && preview.error?.message) || t("move.noCloudProject")
				: !offering.features.includes(CLOUD_BRANCH_FEATURE)
					? t("move.cloudNeedsUpdate")
					: !(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(harness)
						? t("move.cloudAgentUnsupported", { agent: harness })
						: preview.error
							? preview.error.message
							: branchHolder
								? t("move.branchInUse", { branch, session: branchHolder.displayName })
								: undefined;
	const target: MoveTarget | undefined =
		org && project && !loading && !unavailable ? { orgId: org.id, projectId: project.id, harness } : undefined;
	const changes = preview.data?.changes ?? [];
	const transcript = preview.data?.transcript;
	// Cloud resumes the agent's conversation; without it the session starts from the handoff note.
	const carriesTranscript =
		offering.features.includes(CLOUD_TRANSCRIPT_FEATURE) && Boolean(transcript && !transcript.unavailable && transcript.bytes > 0);

	const start = async () => {
		if (!target || !cloudClient) return;
		setError(undefined);
		setResult(undefined);
		try {
			const moved = await moveSession(
				{ sessionId: session.id, moveId, target, ...(sourceHost !== LOCAL_HOST ? { sourceLabel: labelForHost(sourceHost) } : {}) },
				{
					source: clientForSessionHost(sourceHost),
					cloud: cloudClient,
					onStep: setStep,
					cloudRequest,
					...(carriesTranscript
						? {
							uploadTranscript: (importId: string, agentSessionId: string, gzip: Uint8Array) => uploadCloudTranscript(cloudBaseUrl, target.orgId, importId, agentSessionId, gzip),
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
			navigateToSession(target.projectId, moved.sessionId);
			// Say so when the conversation was to move but only the note did.
			showGlobalToast(t("move.done"), carriesTranscript && moved.conversation === "note" ? t("move.doneNote") : undefined, { tone: "success" });
		} catch (failure) {
			setError(failure instanceof Error ? failure : new Error(String(failure)));
		} finally {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(sourceHost) });
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		}
	};

	const openMoved = () => {
		if (!result || !target) return;
		onClose();
		navigateToSession(target.projectId, result.sessionId);
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
									<dd className="min-w-0 truncate text-settings-label">{project?.displayName}</dd>
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
							step={step}
							error={error}
							finished={Boolean(result)}
							lastFailed={Boolean(result?.completeError)}
							label={(candidate) => t(`move.step.${candidate}`, { branch })}
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
function MoveStepList({ step, error, finished, lastFailed, label }: {
	step: MoveStep;
	error?: Error;
	finished: boolean;
	lastFailed: boolean;
	label: (step: MoveStep) => string;
}) {
	const current = STEPS.indexOf(step);
	const stepState = (candidate: MoveStep): "done" | "running" | "failed" | "pending" => {
		const index = STEPS.indexOf(candidate);
		if (error instanceof MoveError && error.step === candidate) return "failed";
		if (finished && candidate === "complete" && lastFailed) return "failed";
		if (index < current || (finished && index <= current)) return "done";
		return index === current && !error ? "running" : "pending";
	};
	return (
		<ol className="flex flex-col gap-2" aria-live="polite">
			{STEPS.map((candidate) => {
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
	const requestCreateProject = useUiStore((state) => state.requestCreateProject);
	const { client: cloudClient } = useCloudCp();
	const cloudProjects = useCloudProjectsQuery();
	const localProjects = useQuery(workspaceQueryOptions);
	const readiness = useAgentReadinessQuery();
	const [step, setStep] = useState<MoveStep>();
	const [result, setResult] = useState<MoveHereResult>();
	const [error, setError] = useState<Error>();
	const [chosen, setChosen] = useState<string>();
	const moving = step !== undefined && !result && !error;
	// One move per dialog: Try again reuses its id, so this computer returns
	// the session it already started instead of starting a second one.
	const moveId = useRef(crypto.randomUUID()).current;

	const repo = repoKey(cloudProjects.data?.find((project) => project.id === session.workspaceId)?.repositoryUrl ?? "");
	const preview = useQuery({
		queryKey: ["move-here-preview", orgId, session.id],
		queryFn: () => cloudClient!.previewSessionMove(orgId, session.id),
		enabled: Boolean(cloudClient),
		retry: false,
		gcTime: 0,
	});
	const branch = preview.data?.branch ?? "";
	// Refetched when a project is added here, so Add project… comes back to this dialog ready.
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
	const agent = readiness.data?.agents.find((candidate) => candidate.id === session.provider);
	const loading = Boolean(cloudClient) && (preview.isPending || cloudProjects.isPending || (Boolean(repo && branch) && targets.isPending));
	const candidates = targets.data ?? [];
	const target = candidates.find((candidate) => candidate.projectId === chosen) ?? candidates.find((candidate) => !candidate.refusal) ?? candidates[0];
	const noProject = !loading && targets.isSuccess && candidates.length === 0;
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
							: noProject
								? t("moveHere.noProject", { repo: repo.split("/").slice(1).join("/") })
								: target?.refusal?.message;
	const ready = !loading && !unavailable && target !== undefined;
	const changes = preview.data?.changes ?? [];
	const transcript = session.provider === "claude-code" ? preview.data?.transcript : undefined;

	const start = async () => {
		if (!ready || !cloudClient || !target) return;
		setError(undefined);
		setResult(undefined);
		try {
			const moved = await moveSessionHere(
				{
					orgId,
					cloudSessionId: session.id,
					projectId: target.projectId,
					moveId,
					settings: {
						autoReview: session.autoReviewEnabled ?? false,
						terminateOnPrMerge: session.terminateOnPrMerge ?? false,
						autoInjectReview: session.autoInjectReview ?? false,
						autoInjectCI: session.autoInjectCI ?? false,
					},
				},
				{ daemon: clientForSessionHost(LOCAL_HOST), cloud: cloudClient, onStep: setStep },
			);
			if (moved.deleteError) {
				// The session runs here; deleting the Cloud copy is retried until it lands.
				useUnfinishedMoveStore.getState().add({ kind: "delete-cloud", orgId, cloudSessionId: session.id });
				setResult(moved);
				return;
			}
			onClose();
			navigateToSession(target.projectId, moved.sessionId);
			showGlobalToast(t("moveHere.done"), [t("moveHere.doneBody", { title: session.title, project: target.name }), ...moved.warnings].join(" "), { tone: "success" });
		} catch (failure) {
			setError(failure instanceof Error ? failure : new Error(String(failure)));
		} finally {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(LOCAL_HOST) });
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		}
	};

	const openMoved = () => {
		if (!result || !target) return;
		onClose();
		navigateToSession(target.projectId, result.sessionId);
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
							step={step}
							error={error}
							finished={Boolean(result)}
							lastFailed={Boolean(result?.deleteError)}
							label={(candidate) => (candidate === "export" ? t("move.step.export", { branch }) : t(`moveHere.step.${candidate}`))}
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
								{error instanceof MoveError && !error.rolledBack ? t("moveHere.failedNotRestarted") : t("moveHere.failedRestarted")}
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
							{noProject && step === undefined ? (
								<Button type="button" variant="primary" onClick={() => requestCreateProject()}>
									{t("moveHere.addProject")}
								</Button>
							) : (
								<Button type="button" variant="primary" onClick={() => void start()} disabled={!ready || moving}>
									{moving ? t("move.moving") : error ? t("move.retry") : t("moveHere.submit")}
								</Button>
							)}
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
	return project && "repo" in project && typeof project.repo === "string" ? project.repo : "";
}
