import { apiErrorCode, apiErrorDetails, apiErrorMessage } from "./api-client";
import { CloudCpError, type CloudCpClient, type CloudCpCreateProjectRequest } from "./cloud-cp";
import type { clientForSessionHost } from "./host-clients";
import { repoKey } from "./repo-key";

type DaemonClient = ReturnType<typeof clientForSessionHost>;

/** The AO Cloud project a session moves to; an empty projectId has the move create one. */
export interface MoveTarget {
	orgId: string;
	projectId: string;
	harness: string;
}

export type MoveStep = "project" | "clone" | "stop" | "export" | "import" | "complete";

export interface MoveResult {
	projectId: string;
	sessionId: string;
	/** The Cloud agent resumes the conversation, or starts from the handoff note. */
	conversation: "transcript" | "note";
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
	) {
		super(message);
	}
}

/** The body AO Cloud's createSession takes. */
export type CloudSessionRequest = Parameters<CloudCpClient["createSession"]>[1];

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
	cloudRequest?: { current?: CloudSessionRequest; pushed?: MovePushed; createUnsettled?: boolean };
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
			);
		}
		const resumed = await deps.source.POST("/api/v1/sessions/{sessionId}/resume-agent", path);
		throw new MoveError(apiErrorMessage(error, fallback), step, apiErrorCode(error), !resumed.error, pushed);
	};

	// A create whose answer was lost may still have made the session, and asking
	// again under the same key returns it. The agent here resumes only when
	// Cloud refuses and no create of this move went unanswered; a later refusal
	// (an expired sign-in, say) does not say the earlier one made nothing.
	const startInCloud = async (request: CloudSessionRequest, pushed?: MovePushed): Promise<MoveResult> => {
		let cloudSessionId: string | undefined;
		let failure: unknown;
		for (let attempt = 0; attempt < 2 && cloudSessionId === undefined; attempt++) {
			try {
				cloudSessionId = (await deps.cloud.createSession(target.orgId, request, { idempotencyKey: moveId })).session.id;
			} catch (error) {
				failure = error;
				if (cloudRefused(error)) break;
				state.createUnsettled = true;
			}
		}
		if (cloudSessionId === undefined) {
			if (cloudRefused(failure) && !state.createUnsettled) return rollback("import", failure, "Could not start the session in Orchestrator Cloud", pushed);
			throw new MoveError(
				cloudRefused(failure)
					? `${apiErrorMessage(failure)} An earlier attempt may already have started the session in Orchestrator Cloud. ${settle}`
					: `Orchestrator Cloud did not answer, so the session may already be starting there. ${settle}`,
				"import",
				errorCode(failure),
				false,
				pushed,
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
	const stopped = await deps.source.POST("/api/v1/sessions/{sessionId}/exit-agent", path);
	if (stopped.error && apiErrorCode(stopped.error) !== "AGENT_EXITED") {
		throw new MoveError(apiErrorMessage(stopped.error, "Could not stop the agent"), "stop", apiErrorCode(stopped.error), false);
	}

	deps.onStep?.("export");
	const exported = await deps.source.POST("/api/v1/sessions/{sessionId}/move/export", {
		...path,
		body: { moveId, ...(input.sourceLabel ? { sourceLabel: input.sourceLabel } : {}) },
	});
	if (exported.error || !exported.data) return rollback("export", exported.error, "Could not push the session");

	deps.onStep?.("import");
	const m = exported.data.manifest;
	const sameAgent = target.harness === m.harness;
	// Cloud resumes a conversation only in the agent that wrote it.
	const transcriptImportId = state.current || !sameAgent ? undefined : await carryTranscript(deps, path, moveId, m.harness);
	const request: CloudSessionRequest = state.current ?? {
		projectId,
		kind: "worker",
		harness: target.harness,
		displayName: (m.displayName || target.harness).slice(0, 100),
		prompt: exported.data.handoffNote,
		branch: m.branch,
		...(sameAgent && m.model ? { model: m.model } : {}),
		...(sameAgent && m.effort ? { reasoningEffort: m.effort } : {}),
		...(transcriptImportId ? { transcriptImportId } : {}),
	};
	state.current = request;
	state.pushed = { branch: m.branch, tip: m.tip, wip: m.wip };
	return startInCloud(request, state.pushed);
}

/**
 * Uploads the stopped agent's conversation for the Cloud session to resume
 * and returns the id createSession names it by. Any failure returns
 * undefined: the session then starts from the handoff note.
 */
async function carryTranscript(deps: MoveDeps, path: { params: { path: { sessionId: string } } }, moveId: string, harness: string): Promise<string | undefined> {
	if (!deps.uploadTranscript) return undefined;
	try {
		const transcript = await deps.source.GET("/api/v1/sessions/{sessionId}/move/transcript", path);
		if (!transcript.data) return undefined;
		const gzip = Uint8Array.from(atob(transcript.data.transcriptGzip), (c) => c.charCodeAt(0));
		await deps.uploadTranscript(moveId, harness, transcript.data.agentSessionId, gzip);
		const sidecar = transcript.data.sidecarGzip;
		if (sidecar && deps.uploadTranscriptSidecar) {
			// The transcript moves even when its sidecar folder cannot.
			await deps.uploadTranscriptSidecar(moveId, Uint8Array.from(atob(sidecar), (c) => c.charCodeAt(0))).catch(() => undefined);
		}
		return moveId;
	} catch {
		return undefined;
	}
}

export interface MoveHereDeps {
	daemon: DaemonClient;
	cloud: Pick<CloudCpClient, "exportSessionMove" | "restartSessionAgent" | "deleteSession">;
	onStep?: (step: MoveStep) => void;
	/** Called with the project a clone made, so Try again moves into it instead of cloning again. */
	onCloned?: (projectId: string) => void;
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
	input: { orgId: string; cloudSessionId: string; projectId?: string; clone?: MoveHereClone; moveId: string; settings: MoveHereSettings },
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
	const restart = () => deps.cloud.restartSessionAgent(orgId, cloudSessionId).then(() => true, () => false);

	// One Cloud call stops the agent and pushes; a failure is shown on the push.
	deps.onStep?.("stop");
	let exported: Awaited<ReturnType<MoveHereDeps["cloud"]["exportSessionMove"]>>;
	try {
		exported = await deps.cloud.exportSessionMove(orgId, cloudSessionId);
	} catch (error) {
		deps.onStep?.("export");
		const reason = error instanceof CloudCpError && error.status !== 0 ? error : "Could not reach Orchestrator Cloud.";
		throw new MoveError(apiErrorMessage(reason, "Could not push the session"), "export", errorCode(error), await restart());
	}

	deps.onStep?.("import");
	const m = exported.manifest;
	const pushed = { branch: m.branch, tip: m.tip, wip: m.wip };
	const importing = deps.daemon.POST("/api/v1/sessions/move/import", {
		body: {
			projectId,
			moveId: input.moveId,
			manifest: { ...m, ...input.settings },
			handoffNote: exported.handoffNote,
			...(exported.transcript ? { transcript: exported.transcript } : {}),
		},
	});
	// No answer may still mean the session started here, and Try again returns
	// it under the same move id, so Cloud's agent stays stopped.
	const imported = await importing.catch(() => {
		throw new MoveError(
			"This computer's Orchestrator did not answer, so the session may already be starting here. The Cloud agent stays stopped; Try again finishes the move.",
			"import",
			undefined,
			false,
			pushed,
		);
	});
	if (imported.error || !imported.data) {
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
	} as const;
	if (imported.data.conversationIncomplete) return { ...moved, keptInCloud: true };

	deps.onStep?.("complete");
	let deleteError: string | undefined;
	try {
		await deps.cloud.deleteSession(orgId, cloudSessionId);
	} catch (error) {
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

/** Cloud answered and refused; a 5xx or no answer may still have created the session. */
function cloudRefused(error: unknown): boolean {
	return error instanceof CloudCpError && error.status >= 400 && error.status < 500;
}

function errorCode(error: unknown): string | undefined {
	return error instanceof CloudCpError ? error.code : undefined;
}
