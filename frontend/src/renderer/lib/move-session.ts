import { apiErrorCode, apiErrorDetails, apiErrorMessage } from "./api-client";
import { CloudCpError, type CloudCpClient, type CloudCpCreateProjectRequest, type CloudCpMoveExportResponse, type CloudCpMoveExportResult, type CloudCpMoveExportRunning } from "./cloud-cp";
import type { clientForSessionHost } from "./host-clients";
import { repoKey } from "./repo-key";

type DaemonClient = ReturnType<typeof clientForSessionHost>;

/** The session-move feature AO Cloud advertises once createSession takes a branch and a session can move out. */
export const CLOUD_BRANCH_FEATURE = "session.branch";

/** The AO Cloud project a session moves to; an empty projectId has the move create one. */
export interface MoveTarget {
	orgId: string;
	projectId: string;
	harness: string;
	/** Orchestrator Cloud runs harness in Chat (the account's chatHarnesses); a session in Chat stays in Chat only then. */
	chat?: boolean;
}

export type MoveStep = "project" | "clone" | "stop" | "export" | "import" | "complete";

/** Something the move left behind that the dialog had said would move, and why. */
export interface MoveWarning {
	kind: "transcript" | "sidecar" | "interface";
	/** Why, for transcript and sidecar; for interface, the agent that runs in the terminal instead of Chat. */
	reason: string;
}

export interface MoveResult {
	projectId: string;
	sessionId: string;
	/** The Cloud agent resumes the conversation, or starts from the handoff note. */
	conversation: "transcript" | "note";
	/** What did not move as said, such as a conversation Cloud refused to take. */
	warnings?: MoveWarning[];
	/** Set when the session runs in AO Cloud but this machine did not record it. */
	completeError?: string;
}

/** What a move had pushed to origin before it failed. */
export interface MovePushed {
	branch: string;
	tip: string;
	/** The tip is the commit of the session's uncommitted files. */
	wip: boolean;
}

/**
 * What a failed move that was not rolled back leaves to the user: "settle"
 * when Try again finishes it (its message says so), "unknown" when a call got
 * no answer, so nobody knows how far it got.
 */
export type MoveFollowUp = "settle" | "unknown";

/**
 * A failed move. rolledBack says the source agent was resumed. pushed is set
 * once the export pushed the branch: the uncommitted files are then a commit
 * on it, here and on origin, so the session is no longer as it was.
 */
export class MoveError extends Error {
	constructor(
		message: string,
		readonly step: MoveStep,
		readonly code: string | undefined,
		readonly rolledBack: boolean,
		readonly pushed?: MovePushed,
		readonly followUp?: MoveFollowUp,
		/** The branch Orchestrator Cloud may already have pushed when its export did not finish. */
		readonly mayBePushed?: string,
	) {
		super(message);
	}
}

/** How long a move export may run in Orchestrator Cloud: its request's lease. */
export const MOVE_EXPORT_BUDGET_MS = 5 * 60_000;

/** Cloud's export can no longer finish; details.branchMayBePushed says whether a worker took it. */
export const MOVE_EXPORT_TIMED_OUT = "MOVE_EXPORT_TIMED_OUT";

function exportRunning(answer: CloudCpMoveExportResult): answer is CloudCpMoveExportRunning {
	return "status" in answer && answer.status === "running";
}

/** How long to wait before asking for a running export again: Cloud's Retry-After, within 3 to 5 s. */
export function exportPollDelay(retryAfterMs: number | undefined): number {
	return Math.min(5_000, Math.max(3_000, retryAfterMs ?? 4_000));
}

/** The message of a move whose call to this computer's Orchestrator got no answer. */
const NO_ANSWER = "This computer's Orchestrator did not answer, so the outcome is unknown. Check the session before trying again.";

/**
 * Makes a daemon call that may throw (no answer, a dropped connection) into
 * one whose failure is a MoveError that claims nothing was undone.
 */
async function unanswered<T>(call: () => Promise<T>, step: MoveStep, pushed?: MovePushed): Promise<T> {
	try {
		return await call();
	} catch {
		throw new MoveError(NO_ANSWER, step, undefined, false, pushed, "unknown");
	}
}

/** The reasoning efforts Orchestrator Cloud's createSession accepts. */
const CLOUD_EFFORTS = new Set(["default", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]);

/** The Cloud reasoning effort for a local one, or undefined to leave Cloud's default. */
export function cloudReasoningEffort(local: string | undefined): string | undefined {
	const effort = local?.trim().toLowerCase();
	if (!effort) return undefined;
	if (effort === "off") return "none";
	return CLOUD_EFFORTS.has(effort) ? effort : undefined;
}

const CLOUD_APPROVAL_MODES = new Set(["default", "accept-edits", "auto", "bypass-permissions"]);

/** The session's approval mode as Cloud's createSession takes it, or undefined to leave Cloud's. */
export function cloudApprovalMode(local: string | undefined): CloudSessionRequest["approvalMode"] {
	return local && CLOUD_APPROVAL_MODES.has(local) ? (local as CloudSessionRequest["approvalMode"]) : undefined;
}

/** The body AO Cloud's createSession takes. */
export type CloudSessionRequest = Parameters<CloudCpClient["createSession"]>[1];

/**
 * A move's Cloud request across attempts. key is the createSession
 * idempotency key and the transcript import id: the move id at first, and a
 * fresh one after Cloud refused a create, since the next request differs.
 */
export interface MoveCloudRequest {
	current?: CloudSessionRequest;
	key?: string;
	pushed?: MovePushed;
	createUnsettled?: boolean;
}

/** Cloud refused a move's export because it could not save the latest conversation. */
export const TRANSCRIPT_NOT_CAPTURED = "MOVE_TRANSCRIPT_NOT_CAPTURED";

/** Cloud kept a moved session's copy: it was restarted or used after the move's export. */
export const MOVE_COPY_IN_USE = "MOVE_COPY_IN_USE";

/** Cloud did not start the agent again: the move's export still runs and holds the session. */
export const MOVE_IN_PROGRESS = "MOVE_IN_PROGRESS";

/**
 * Refusals of a move's export that Cloud makes before it asks the worker
 * anything (messages still queued, an interface or agent switch in progress):
 * nothing stopped, committed or pushed.
 */
const EXPORT_REFUSED_UNTOUCHED = new Set(["MOVE_TURNS_QUEUED", "INTERFACE_TRANSITION_IN_PROGRESS"]);

/** Whether a failed move left the Cloud session as it was: its export was refused before anything stopped. */
export function moveLeftSessionAsItWas(error: MoveError): boolean {
	return error.step === "export" && error.code !== undefined && EXPORT_REFUSED_UNTOUCHED.has(error.code);
}

export interface MoveDeps {
	source: DaemonClient;
	cloud: Pick<CloudCpClient, "createSession" | "createProject">;
	onStep?: (step: MoveStep) => void;
	/** Called with the Cloud project the move created, so Try again moves into it. */
	onProjectCreated?: (projectId: string) => void;
	/**
	 * Holds the Cloud request of the move's first export. AO Cloud accepts a
	 * reused idempotency key only with the same body, and a retry's export
	 * differs (a new note time, a branch already pushed), so a retry under the
	 * same moveId resends this one, with pushed, what its export pushed.
	 * createUnsettled is set once a create got no clear answer: Try again then
	 * asks Cloud for that session before anything else, and nothing resumes the
	 * agent here.
	 */
	cloudRequest?: MoveCloudRequest;
	/**
	 * Called with the Cloud request just before each create, so the caller can
	 * keep it past a quit: a relaunch then finishes this move under the same
	 * key instead of starting a second Cloud session.
	 */
	onCreate?: (pending: { request: CloudSessionRequest; key: string; pushed?: MovePushed }) => void;
	/**
	 * Uploads the agent's conversation, gzipped, under importId for the Cloud
	 * session to resume. Unset when Cloud cannot resume it, which leaves the
	 * handoff note.
	 */
	uploadTranscript?: (importId: string, harness: string, agentSessionId: string, gzip: Uint8Array) => Promise<void>;
	/** Uploads the conversation's sidecar folder (subagents, saved tool output) to the same import. */
	uploadTranscriptSidecar?: (importId: string, gzip: Uint8Array) => Promise<void>;
}

/**
 * Moves a worker session to AO Cloud: stop its agent, push its branch, start
 * a Cloud session on that branch, then mark it moved here. Until the Cloud
 * session exists a failure resumes the source agent; after the push the error
 * says what was pushed. moveId makes each call safe to retry.
 */
export async function moveSession(
	input: { sessionId: string; sourceLabel?: string; target: MoveTarget; createProject?: CloudCpCreateProjectRequest; moveId?: string },
	deps: MoveDeps,
): Promise<MoveResult> {
	const { sessionId, target } = input;
	const moveId = input.moveId ?? crypto.randomUUID();
	const path = { params: { path: { sessionId } } };

	let projectId = target.projectId;
	if (!projectId) {
		if (!input.createProject) throw new MoveError("No Orchestrator Cloud project to move the session into", "project", undefined, true);
		const defaultBranch = input.createProject.defaultBranch?.trim();
		if (!defaultBranch || defaultBranch === "auto") {
			throw new MoveError("Orchestrator could not tell the repository's default branch, so it cannot create the Cloud project.", "project", undefined, true);
		}
		// The agent here has not stopped yet, so a failure leaves the session as it was.
		deps.onStep?.("project");
		try {
			const { project } = await deps.cloud.createProject(target.orgId, input.createProject, { idempotencyKey: `${moveId}-project` });
			projectId = project.id;
		} catch (error) {
			const reason = error instanceof CloudCpError && error.status !== 0 ? error : "Could not reach Orchestrator Cloud.";
			throw new MoveError(apiErrorMessage(reason, "Could not create the Orchestrator Cloud project"), "project", errorCode(error), true);
		}
		deps.onProjectCreated?.(projectId);
	}

	const state = deps.cloudRequest ?? {};
	const settle = "The agent here stays stopped; Try again finishes the move.";
	// Said after the move: what the conversation upload could not carry.
	let warnings: MoveWarning[] = [];
	// While a create of this move may have made the Cloud session, the agent here
	// must not resume: both could then work on the branch.
	const rollback = async (step: MoveStep, error: unknown, fallback: string, pushed?: MovePushed): Promise<never> => {
		if (state.createUnsettled) {
			throw new MoveError(
				`${apiErrorMessage(error, fallback)} An earlier attempt may already have started the session in Orchestrator Cloud. ${settle}`,
				step,
				apiErrorCode(error),
				false,
				pushed,
				"settle",
			);
		}
		let resumed: boolean | undefined;
		try {
			resumed = !(await deps.source.POST("/api/v1/sessions/{sessionId}/resume-agent", path)).error;
		} catch {
			// No answer: the agent may or may not be running again.
		}
		throw new MoveError(
			resumed === undefined ? `${apiErrorMessage(error, fallback)} ${NO_ANSWER}` : apiErrorMessage(error, fallback),
			step,
			apiErrorCode(error),
			resumed === true,
			pushed,
			resumed === undefined ? "unknown" : undefined,
		);
	};

	// A create whose answer was lost may still have made the session, and asking
	// again under the same key returns it. The agent here resumes only when
	// Cloud refuses and no create of this move went unanswered; a later refusal
	// (an expired sign-in, say) does not say the earlier one made nothing.
	const startInCloud = async (request: CloudSessionRequest, pushed?: MovePushed): Promise<MoveResult> => {
		let cloudSessionId: string | undefined;
		let failure: unknown;
		const key = state.key ?? moveId;
		deps.onCreate?.({ request, key, pushed });
		for (let attempt = 0; attempt < 2 && cloudSessionId === undefined; attempt++) {
			try {
				cloudSessionId = (await deps.cloud.createSession(target.orgId, request, { idempotencyKey: key })).session.id;
			} catch (error) {
				failure = error;
				if (cloudRefused(error)) break;
				state.createUnsettled = true;
			}
		}
		if (cloudSessionId === undefined) {
			if (cloudRefused(failure) && !state.createUnsettled) {
				// Cloud made no session, and the agent here resumes and may go on:
				// Try again builds a fresh request, with the conversation as it is
				// then, under a fresh key.
				state.current = undefined;
				state.key = crypto.randomUUID();
				return rollback("import", failure, "Could not start the session in Orchestrator Cloud", pushed);
			}
			throw new MoveError(
				cloudRefused(failure)
					? `${apiErrorMessage(failure)} An earlier attempt may already have started the session in Orchestrator Cloud. ${settle}`
					: `Orchestrator Cloud did not answer, so the session may already be starting there. ${settle}`,
				"import",
				errorCode(failure),
				false,
				pushed,
				"settle",
			);
		}

		deps.onStep?.("complete");
		// The session runs in Cloud whatever happens here; the caller retries a failed record.
		let completeError: string | undefined;
		try {
			const completed = await deps.source.POST("/api/v1/sessions/{sessionId}/move/complete", {
				...path,
				body: { moveId, orgId: target.orgId, sessionId: cloudSessionId },
			});
			if (completed.error) completeError = apiErrorMessage(completed.error, "Could not mark the session as moved");
		} catch (error) {
			completeError = apiErrorMessage(error, "Could not mark the session as moved");
		}
		return {
			projectId: request.projectId,
			sessionId: cloudSessionId,
			conversation: request.transcriptImportId ? "transcript" : "note",
			...(warnings.length > 0 ? { warnings } : {}),
			...(completeError ? { completeError } : {}),
		};
	};

	// Try again after an unanswered create asks Cloud for that session first:
	// exporting again could find the branch already moved on by it.
	if (state.createUnsettled && state.current) {
		deps.onStep?.("import");
		return startInCloud(state.current, state.pushed);
	}

	deps.onStep?.("stop");
	const stopped = await unanswered(() => deps.source.POST("/api/v1/sessions/{sessionId}/exit-agent", path), "stop");
	if (stopped.error && apiErrorCode(stopped.error) !== "AGENT_EXITED") {
		throw new MoveError(apiErrorMessage(stopped.error, "Could not stop the agent"), "stop", apiErrorCode(stopped.error), false);
	}

	deps.onStep?.("export");
	// No answer may still mean the branch was pushed, so nothing resumes the agent.
	const exported = await unanswered(
		() =>
			deps.source.POST("/api/v1/sessions/{sessionId}/move/export", {
				...path,
				body: { moveId, ...(input.sourceLabel ? { sourceLabel: input.sourceLabel } : {}) },
			}),
		"export",
	);
	if (exported.error || !exported.data) return rollback("export", exported.error, "Could not push the session");

	deps.onStep?.("import");
	const m = exported.data.manifest;
	const sameAgent = target.harness === cloudHarness(m.harness);
	const effort = sameAgent ? cloudReasoningEffort(m.effort) : undefined;
	// The approval mode is a session setting like review and CI: it moves with the session.
	const approvalMode = sameAgent ? cloudApprovalMode(m.permissions) : undefined;
	// Cloud resumes a conversation only in the agent that wrote it.
	const carried = state.current || !sameAgent ? undefined : await carryTranscript(deps, path, state.key ?? moveId, target.harness);
	const transcriptImportId = carried?.importId;
	warnings = carried?.warnings ?? [];
	// The session keeps its interface: a session in Chat stays in Chat when
	// Cloud runs its agent there, and otherwise continues in the terminal.
	const inChat = m.mode === "chat";
	if (inChat && !target.chat) warnings.push({ kind: "interface", reason: target.harness });
	const request: CloudSessionRequest = state.current ?? {
		projectId,
		kind: "worker",
		harness: target.harness,
		displayName: (m.displayName || target.harness).slice(0, 100),
		prompt: exported.data.handoffNote,
		branch: m.branch,
		interfaceMode: inChat && target.chat ? "chat" : "tui",
		...(sameAgent && m.model ? { model: m.model } : {}),
		...(effort ? { reasoningEffort: effort } : {}),
		...(approvalMode ? { approvalMode } : {}),
		...(transcriptImportId ? { transcriptImportId } : {}),
	};
	state.current = request;
	state.pushed = { branch: m.branch, tip: m.tip, wip: m.wip };
	return startInCloud(request, state.pushed);
}

/** Orchestrator Cloud runs OpenCode 2 as "opencode"; this machine runs it as "opencode-v2". */
export function cloudHarness(local: string): string {
	return local === "opencode-v2" ? "opencode" : local;
}

/** The harness this machine runs Orchestrator Cloud's harness as. */
export function localHarness(cloud: string): string {
	return cloud === "opencode" ? "opencode-v2" : cloud;
}

/**
 * Uploads the stopped agent's conversation for the Cloud session to resume
 * and returns the id createSession names it by. A conversation that cannot
 * move leaves the session to start from the handoff note, and a sidecar folder
 * that cannot leaves the transcript moving alone; either is returned as a
 * warning with its reason, never dropped silently.
 */
async function carryTranscript(
	deps: MoveDeps,
	path: { params: { path: { sessionId: string } } },
	importId: string,
	harness: string,
): Promise<{ importId?: string; warnings: MoveWarning[] }> {
	if (!deps.uploadTranscript) return { warnings: [] };
	const decode = (base64: string) => Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
	let transcript;
	try {
		const answer = await deps.source.GET("/api/v1/sessions/{sessionId}/move/transcript", path);
		if (!answer.data) return { warnings: [{ kind: "transcript", reason: apiErrorMessage(answer.error, "This computer could not read the conversation.") }] };
		transcript = answer.data;
	} catch {
		return { warnings: [{ kind: "transcript", reason: "This computer's Orchestrator did not answer." }] };
	}
	try {
		await deps.uploadTranscript(importId, harness, transcript.agentSessionId, decode(transcript.transcriptGzip));
	} catch (error) {
		return { warnings: [{ kind: "transcript", reason: apiErrorMessage(error, "The upload failed.") }] };
	}
	const warnings: MoveWarning[] = [];
	if (transcript.sidecarOmitted) {
		warnings.push({ kind: "sidecar", reason: transcript.sidecarOmitted === "too_large" ? "It is over Orchestrator Cloud's upload limit." : "This computer could not read it." });
	} else if (transcript.sidecarGzip && deps.uploadTranscriptSidecar) {
		try {
			await deps.uploadTranscriptSidecar(importId, decode(transcript.sidecarGzip));
		} catch (error) {
			warnings.push({ kind: "sidecar", reason: apiErrorMessage(error, "The upload failed.") });
		}
	}
	return { importId, warnings };
}

export interface MoveHereDeps {
	daemon: DaemonClient;
	cloud: Pick<CloudCpClient, "exportSessionMove" | "getSessionMoveExport" | "restartSessionAgent" | "deleteSession">;
	/**
	 * Called with the export's moveId while Orchestrator Cloud is still on it,
	 * and with undefined once it is done or can no longer finish: Try again
	 * waits on a running export instead of starting another.
	 */
	onExportMove?: (moveId: string | undefined) => void;
	/** Called while Orchestrator Cloud is still on the export, with how long it has run. */
	onExportRunning?: (elapsedMs: number) => void;
	/** Waits between asks for a running export; tests make it instant. */
	wait?: (ms: number) => Promise<void>;
	now?: () => number;
	onStep?: (step: MoveStep) => void;
	/** Called with the project a clone made, so Try again moves into it instead of cloning again. */
	onCloned?: (projectId: string) => void;
	/**
	 * Called with the project just before the import here, so the caller can
	 * keep the move id past a quit: this computer then returns the session it
	 * started instead of starting a second one.
	 */
	onImport?: (projectId: string) => void;
	/**
	 * Called when an import got no clear answer (no reply, a server error, or a
	 * replay this computer could not settle): the caller keeps it, and passes
	 * importUnsettled on every later attempt of this move.
	 */
	onImportUnsettled?: () => void;
}

/** The repository to clone when no project here has it, and the folder to clone it into. */
export interface MoveHereClone {
	remoteUrl: string;
	destinationParent: string;
	repoKey: string;
}

/** The Cloud session's settings the session keeps here; Cloud's export does not carry them. */
export type MoveHereSettings = { autoReview: boolean; terminateOnPrMerge: boolean; autoInjectReview: boolean; autoInjectCI: boolean };

export interface MoveHereResult {
	projectId: string;
	sessionId: string;
	conversation: "transcript" | "note";
	/** What did not carry over, such as subagent transcripts that stayed in Cloud. */
	warnings: string[];
	/** Set when the session runs here but Orchestrator Cloud did not delete its copy. */
	deleteError?: string;
	/** The Cloud session is kept: it holds the only full copy of a conversation that did not all move here. */
	keptInCloud?: true;
	/** The Cloud session is kept because it was restarted or used after the move's export. */
	copyInUse?: true;
	/** The export's id, which deletes the Cloud session only as the copy this move left. */
	exportMoveId?: string;
}

/**
 * Moves an Orchestrator Cloud session to a project on this computer, cloning
 * the repository first when no project here has it. Cloud
 * stops its agent and pushes its branch, this computer starts the session on
 * that branch, then the Cloud session is deleted, unless part of its
 * conversation did not move here. Until this computer has the session a
 * failure starts the Cloud agent again. moveId makes each call safe to retry.
 */
export async function moveSessionHere(
	input: {
		orgId: string;
		cloudSessionId: string;
		projectId?: string;
		clone?: MoveHereClone;
		moveId: string;
		settings: MoveHereSettings;
		/** Cloud's preview listed a conversation: the move must bring it, or keep the Cloud session. */
		expectTranscript?: boolean;
		/** An export Cloud is still running for this move (a 202): Try again waits on it. */
		exportMoveId?: string;
		/** The session's branch, as Cloud's preview named it, for what a failed export may have pushed. */
		branch?: string;
		/**
		 * An earlier attempt's import got no clear answer, so a session may be
		 * running here under this moveId. Cloud's agent then stays stopped on
		 * any failure until the import is reconciled (Try again returns that
		 * session) or this computer refuses it outright.
		 */
		importUnsettled?: boolean;
	},
	deps: MoveHereDeps,
): Promise<MoveHereResult> {
	const { orgId, cloudSessionId } = input;
	let projectId = input.projectId;
	if (!projectId) {
		if (!input.clone) throw new MoveError("No project here to move the session into", "clone", undefined, true);
		// Nothing in Cloud has stopped yet, so a failed clone leaves the session as it was.
		deps.onStep?.("clone");
		try {
			projectId = await cloneForMove(deps.daemon, input.clone);
		} catch (error) {
			throw error instanceof MoveError ? error : new MoveError(apiErrorMessage(error, "Could not clone the repository"), "clone", undefined, true);
		}
		deps.onCloned?.(projectId);
	}
	// Set when Cloud refuses the restart because the move's export still runs
	// (MOVE_IN_PROGRESS): it holds the session until that export ends.
	let exportHoldsSession = false;
	const restart = () =>
		deps.cloud.restartSessionAgent(orgId, cloudSessionId).then(
			() => true,
			(error: unknown) => {
				exportHoldsSession = error instanceof CloudCpError && error.code === MOVE_IN_PROGRESS;
				return false;
			},
		);

	// One Cloud call stops the agent and pushes; a failure is shown on the push.
	// Cloud answers within about 50 s, with the move or with the export still
	// running (its moveId), which is asked again until done, within its lease.
	deps.onStep?.("stop");
	const wait = deps.wait ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
	const now = deps.now ?? Date.now;
	const started = now();
	let exportMoveId = input.exportMoveId;
	let exported: CloudCpMoveExportResponse;
	try {
		let answer = await deps.cloud.exportSessionMove(orgId, cloudSessionId, exportMoveId ? { moveId: exportMoveId } : undefined);
		while (exportRunning(answer)) {
			exportMoveId = answer.moveId;
			deps.onExportMove?.(exportMoveId);
			if (now() - started >= MOVE_EXPORT_BUDGET_MS) {
				throw new MoveError(
					"Orchestrator Cloud is still moving the session. Its agent stays stopped; Try again waits on that move.",
					"export", undefined, false, undefined, "settle", input.branch,
				);
			}
			deps.onExportRunning?.(now() - started);
			await wait(exportPollDelay(answer.retryAfterMs));
			answer = await deps.cloud.getSessionMoveExport(orgId, cloudSessionId, exportMoveId);
		}
		exported = answer;
	} catch (error) {
		deps.onStep?.("export");
		if (error instanceof MoveError) throw error;
		// An earlier import may have started the session here: Cloud's agent
		// must not run beside it, so nothing starts it again.
		if (input.importUnsettled) {
			// An export that timed out can no longer answer: Try again must not
			// wait on its id forever. Cloud still holds the session for the
			// export the earlier import came from, so asking again answers with
			// that one; nothing exports afresh, and nothing restarts.
			if (error instanceof CloudCpError && error.code === MOVE_EXPORT_TIMED_OUT) deps.onExportMove?.(undefined);
			throw new MoveError(
				`${apiErrorMessage(error, "Orchestrator Cloud did not finish the export.")} ${IMPORT_UNSETTLED}`,
				"export", errorCode(error), false, undefined, "settle", input.branch,
			);
		}
		// Cloud pushed but could not save the latest conversation: the Cloud
		// session is kept and its agent starts again.
		if (error instanceof CloudCpError && error.code === TRANSCRIPT_NOT_CAPTURED) {
			deps.onExportMove?.(undefined);
			throw new MoveError(apiErrorMessage(error, "Orchestrator Cloud could not save the latest conversation."), "export", TRANSCRIPT_NOT_CAPTURED,
				await restart(), undefined, undefined, input.branch || "the session's branch");
		}
		if (error instanceof CloudCpError && error.code === MOVE_EXPORT_TIMED_OUT) {
			// That export can no longer finish: Try again starts another, which
			// reuses a WIP commit it pushed.
			deps.onExportMove?.(undefined);
			const mayBePushed = error.details?.branchMayBePushed === true;
			throw new MoveError(
				mayBePushed ? "Orchestrator Cloud did not finish this move in time." : "The move did not start in time: nothing was stopped, committed or pushed.",
				"export", MOVE_EXPORT_TIMED_OUT, await restart(), undefined, undefined, mayBePushed ? input.branch || "the session's branch" : undefined,
			);
		}
		// An export known to be running is not refused by a lost answer: the
		// agent stays stopped, and Try again waits on it.
		if (exportMoveId && !cloudRefused(error)) {
			throw new MoveError(
				"Orchestrator Cloud did not answer while it moved the session. Its agent stays stopped; Try again waits on that move.",
				"export", errorCode(error), false, undefined, "settle", input.branch,
			);
		}
		deps.onExportMove?.(undefined);
		const reason = error instanceof CloudCpError && error.status !== 0 ? error : "Could not reach Orchestrator Cloud.";
		const message = apiErrorMessage(reason, "Could not push the session");
		// Refused before the worker was asked: nothing stopped, so nothing is
		// started again, and a restart could cut into a switch in progress.
		if (error instanceof CloudCpError && EXPORT_REFUSED_UNTOUCHED.has(error.code ?? "")) {
			throw new MoveError(message, "export", error.code, true);
		}
		const restarted = await restart();
		// The export runs after all (its answer was lost before its moveId
		// came back): Try again asks Cloud again, which answers with that move.
		if (exportHoldsSession) {
			throw new MoveError(
				"Orchestrator Cloud is still moving the session. Its agent stays stopped; Try again waits on that move.",
				"export", MOVE_IN_PROGRESS, false, undefined, "settle", input.branch,
			);
		}
		throw new MoveError(message, "export", errorCode(error), restarted);
	}
	// A later Try again asks Cloud again: while this move holds the session
	// Cloud answers with it (the same moveId), and once the move ended or was
	// abandoned it runs a new export, as the agent may have run again by then.
	deps.onExportMove?.(undefined);

	deps.onStep?.("import");
	const { interface: cloudInterface, ...m } = exported.manifest;
	const pushed = { branch: m.branch, tip: m.tip, wip: m.wip };
	// The session starts here in the interface it had in Cloud; this computer
	// runs it in the terminal when its agent has no Chat here, and says so.
	const mode = cloudInterface === "chat" ? "chat" : cloudInterface === "terminal" ? "tui" : undefined;
	deps.onImport?.(projectId);
	const importing = deps.daemon.POST("/api/v1/sessions/move/import", {
		body: {
			projectId,
			moveId: input.moveId,
			manifest: { ...m, ...input.settings, ...(mode ? { mode } : {}) },
			handoffNote: exported.handoffNote,
			...(exported.transcript ? { transcript: exported.transcript } : {}),
		},
	});
	// No answer may still mean the session started here, and Try again returns
	// it under the same move id, so Cloud's agent stays stopped.
	const imported = await importing.catch(() => {
		deps.onImportUnsettled?.();
		throw new MoveError(
			"This computer's Orchestrator did not answer, so the session may already be starting here. The Cloud agent stays stopped; Try again finishes the move.",
			"import",
			undefined,
			false,
			pushed,
			"settle",
		);
	});
	if (imported.error || !imported.data) {
		// Only a refusal proves no session started here. A server error, or a
		// replay that could not settle an earlier import, does not: an earlier
		// attempt may have started it, so Cloud's agent stays stopped.
		if (input.importUnsettled || !importRefused(imported)) {
			deps.onImportUnsettled?.();
			throw new MoveError(
				`${apiErrorMessage(imported.error, "Could not start the session on this computer")} ${IMPORT_UNSETTLED}`,
				"import",
				apiErrorCode(imported.error),
				false,
				pushed,
				"settle",
			);
		}
		throw new MoveError(
			apiErrorMessage(imported.error, "Could not start the session on this computer"),
			"import",
			apiErrorCode(imported.error),
			await restart(),
			pushed,
		);
	}

	const moved = {
		projectId,
		sessionId: imported.data.session.id,
		conversation: exported.transcript ? "transcript" : "note",
		warnings: imported.data.warnings ?? [],
		...(exported.moveId ? { exportMoveId: exported.moveId } : {}),
	} as const;
	// The Cloud session holds the only full copy of a conversation that did not
	// all move here, or that Cloud's preview listed and its export left out.
	const conversationMissing = Boolean(input.expectTranscript && !exported.transcript);
	if (imported.data.conversationIncomplete || conversationMissing) return { ...moved, keptInCloud: true };

	deps.onStep?.("complete");
	let deleteError: string | undefined;
	try {
		// With the export's id, Cloud deletes the session only as the copy this
		// move left, and keeps it if it was restarted or used since.
		await (exported.moveId ? deps.cloud.deleteSession(orgId, cloudSessionId, { moveId: exported.moveId }) : deps.cloud.deleteSession(orgId, cloudSessionId));
	} catch (error) {
		if (error instanceof CloudCpError && error.code === MOVE_COPY_IN_USE) return { ...moved, keptInCloud: true, copyInUse: true };
		// Cloud refuses to delete a session whose latest conversation it could not save.
		if (error instanceof CloudCpError && error.code === TRANSCRIPT_NOT_CAPTURED) return { ...moved, keptInCloud: true };
		if (!(error instanceof CloudCpError && error.status === 404)) {
			deleteError = error instanceof Error ? error.message : String(error);
		}
	}
	return { ...moved, ...(deleteError ? { deleteError } : {}) };
}

/**
 * Clones the repository into destinationParent and adds it as a project. A
 * checkout of the same repository already at that path is added instead.
 */
async function cloneForMove(daemon: DaemonClient, clone: MoveHereClone): Promise<string> {
	const fail = (error: unknown, fallback: string): never => {
		throw new MoveError(apiErrorMessage(error, fallback), "clone", apiErrorCode(error), true);
	};
	const cloned = await daemon.POST("/api/v1/projects/clone", { body: { remoteUrl: clone.remoteUrl, destinationParent: clone.destinationParent } });
	if (cloned.data) return cloned.data.project.id;
	const existing = apiErrorCode(cloned.error) === "CLONE_DESTINATION_EXISTS" ? apiErrorDetails(cloned.error)?.path : undefined;
	if (typeof existing !== "string") return fail(cloned.error, "Could not clone the repository");
	const otherRepository = `${existing} holds another repository. Move or rename it, then try again.`;
	const added = await daemon.POST("/api/v1/projects", { body: { path: existing } });
	// A project there with this repository would have been listed as a target.
	if (apiErrorCode(added.error) === "PATH_ALREADY_REGISTERED") return fail(undefined, otherRepository);
	if (!added.data) return fail(added.error, `Could not add ${existing} as a project`);
	if (repoKey(added.data.project.repo ?? "") !== clone.repoKey) {
		await daemon.DELETE("/api/v1/projects/{id}", { params: { path: { id: added.data.project.id } } });
		return fail(undefined, otherRepository);
	}
	return added.data.project.id;
}

/** Said when a session may already run here under this move: Cloud's agent stays stopped. */
const IMPORT_UNSETTLED = "The session may already be running on this computer, so the Cloud agent stays stopped; Try again finishes the move.";

/** Answers from a move/import replay that say a session under this moveId exists here but is not settled. */
const IMPORT_REPLAY_UNSETTLED = new Set(["CLIENT_REQUEST_INCOMPLETE", "CLIENT_REQUEST_CONFLICT"]);

/**
 * Whether move/import refused outright, so no session started here: a 4xx
 * answer other than an unsettled replay. A 5xx, or an answer without a
 * status, proves nothing.
 */
function importRefused(answer: { error?: unknown; response?: { status?: number } }): boolean {
	const status = answer.response?.status;
	return status !== undefined && status >= 400 && status < 500 && !IMPORT_REPLAY_UNSETTLED.has(apiErrorCode(answer.error) ?? "");
}

/** Cloud answered and refused; a 5xx or no answer may still have created the session. */
function cloudRefused(error: unknown): boolean {
	return error instanceof CloudCpError && error.status >= 400 && error.status < 500;
}

function errorCode(error: unknown): string | undefined {
	return error instanceof CloudCpError ? error.code : undefined;
}
