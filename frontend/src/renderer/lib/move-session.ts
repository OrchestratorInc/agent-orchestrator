import { apiErrorCode, apiErrorMessage } from "./api-client";
import type { CloudCpClient } from "./cloud-cp";
import type { clientForSessionHost } from "./host-clients";

type DaemonClient = ReturnType<typeof clientForSessionHost>;

/** The AO Cloud project a session moves to. */
export interface MoveTarget {
	orgId: string;
	projectId: string;
	harness: string;
}

export type MoveStep = "stop" | "export" | "import" | "complete";

export interface MoveResult {
	sessionId: string;
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
	cloud: Pick<CloudCpClient, "createSession">;
	onStep?: (step: MoveStep) => void;
	/**
	 * Holds the Cloud request of the move's first export. AO Cloud accepts a
	 * reused idempotency key only with the same body, and a retry's export
	 * differs (a new note time, a branch already pushed), so a retry under the
	 * same moveId resends this one.
	 */
	cloudRequest?: { current?: CloudSessionRequest };
}

/**
 * Moves a worker session to AO Cloud: stop its agent, push its branch, start
 * a Cloud session on that branch, then mark it moved here. Until the Cloud
 * session exists a failure resumes the source agent; after the push the error
 * says what was pushed. moveId makes each call safe to retry.
 */
export async function moveSession(
	input: { sessionId: string; sourceLabel?: string; target: MoveTarget; moveId?: string },
	deps: MoveDeps,
): Promise<MoveResult> {
	const { sessionId, target } = input;
	const moveId = input.moveId ?? crypto.randomUUID();
	const path = { params: { path: { sessionId } } };

	deps.onStep?.("stop");
	const stopped = await deps.source.POST("/api/v1/sessions/{sessionId}/exit-agent", path);
	if (stopped.error && apiErrorCode(stopped.error) !== "AGENT_EXITED") {
		throw new MoveError(apiErrorMessage(stopped.error, "Could not stop the agent"), "stop", apiErrorCode(stopped.error), false);
	}

	const rollback = async (step: MoveStep, error: unknown, fallback: string, pushed?: MovePushed): Promise<never> => {
		const resumed = await deps.source.POST("/api/v1/sessions/{sessionId}/resume-agent", path);
		throw new MoveError(apiErrorMessage(error, fallback), step, apiErrorCode(error), !resumed.error, pushed);
	};

	deps.onStep?.("export");
	const exported = await deps.source.POST("/api/v1/sessions/{sessionId}/move/export", {
		...path,
		body: { moveId, ...(input.sourceLabel ? { sourceLabel: input.sourceLabel } : {}) },
	});
	if (exported.error || !exported.data) return rollback("export", exported.error, "Could not push the session");

	deps.onStep?.("import");
	const m = exported.data.manifest;
	const sameAgent = target.harness === m.harness;
	const request: CloudSessionRequest = deps.cloudRequest?.current ?? {
		projectId: target.projectId,
		kind: "worker",
		harness: target.harness,
		displayName: (m.displayName || target.harness).slice(0, 100),
		prompt: exported.data.handoffNote,
		branch: m.branch,
		...(sameAgent && m.model ? { model: m.model } : {}),
		...(sameAgent && m.effort ? { reasoningEffort: m.effort } : {}),
	};
	if (deps.cloudRequest) deps.cloudRequest.current = request;
	let cloudSessionId: string;
	try {
		const { session } = await deps.cloud.createSession(target.orgId, request, { idempotencyKey: moveId });
		cloudSessionId = session.id;
	} catch (error) {
		return rollback("import", error, "Could not start the session in AO Cloud", { branch: m.branch, tip: m.tip, wip: m.wip });
	}

	deps.onStep?.("complete");
	const completed = await deps.source.POST("/api/v1/sessions/{sessionId}/move/complete", {
		...path,
		body: { moveId, orgId: target.orgId, sessionId: cloudSessionId },
	});
	return {
		sessionId: cloudSessionId,
		...(completed.error ? { completeError: apiErrorMessage(completed.error, "Could not mark the session as moved") } : {}),
	};
}
