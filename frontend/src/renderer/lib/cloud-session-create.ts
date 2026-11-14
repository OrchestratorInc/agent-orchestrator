import { CloudCpError } from "./cloud-cp";
import type { CloudCpClient, CloudCpCreateSessionRequest, CloudCpInterfaceMode, CloudCpMutationOptions, CloudCpSessionResponse } from "./cloud-cp";
import type { Settings } from "../hooks/useSettings";
import { reportCloudSessionCreateRequested } from "./cloud-telemetry";

/**
 * The default session interface a new cloud orchestrator is created with: the
 * daemon-owned "Default session interface" (Settings > General), which local
 * spawns resolve from too. The control plane resolves the orchestrator's
 * interface from it as the local daemon does (Chat where its harness can) and
 * keeps it for the workers the orchestrator spawns. A task sends the
 * interface it starts in instead (interfaceMode, from the composer). It is
 * always sent, "tui" included: the control plane starts a create without
 * either field (an older desktop's) in the terminal, an orchestrator too.
 */
export function cloudDefaultInterfaceMode(
	settings: Pick<Settings, "defaultSessionMode"> | undefined,
): CloudCpInterfaceMode {
	return settings?.defaultSessionMode === "chat" ? "chat" : "tui";
}

/**
 * Creates a cloud session, with the interface it starts in (interfaceMode)
 * or the user's default session interface (defaultInterfaceMode) when the body
 * carries one. A control plane that predates those fields rejects the whole
 * body with 400 invalid_request before creating anything, so the create is
 * sent again without them and the session starts in the terminal with its
 * prompt on the agent's command line, as every session did there. The user's
 * request is reported once, not once per call.
 */
export async function createCloudSession(
	client: Pick<CloudCpClient, "createSession">,
	orgId: string,
	body: CloudCpCreateSessionRequest,
	options?: CloudCpMutationOptions,
): Promise<CloudCpSessionResponse> {
	try {
		reportCloudSessionCreateRequested(body);
	} catch {
		// Telemetry must never break the create.
	}
	const send = (request: CloudCpCreateSessionRequest, sendOptions = options) =>
		sendOptions ? client.createSession(orgId, request, sendOptions) : client.createSession(orgId, request);
	const { interfaceMode, defaultInterfaceMode, ...withoutInterface } = body;
	if (interfaceMode === undefined && defaultInterfaceMode === undefined) return send(body);
	try {
		return await send(body);
	} catch (error) {
		if (!isUnknownInterfaceFieldRefusal(error)) throw error;
		// A different body, so its own key: a retry still replays it, but never collides with the refused one.
		return send(withoutInterface, options?.idempotencyKey ? { ...options, idempotencyKey: `${options.idempotencyKey}:tui` } : options);
	}
}

/** A control plane's refusal of a create body carrying a field it predates. */
export function isUnknownInterfaceFieldRefusal(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 400 && error.code === "invalid_request";
}

/** A file a new Cloud task starts with, as the composer holds it (base64 bytes). */
export interface CloudTaskFile {
	mimeType: string;
	data: string;
	name?: string;
}

/**
 * Why a task with files cannot start: they ride a Chat session's first
 * message, and the control plane started the session in the terminal.
 */
export class CloudTaskFilesNeedChatError extends Error {
	/** A refusal a retry cannot change: the composer keeps the task to change and send again. */
	readonly code = "CLOUD_ATTACHMENTS_NEED_CHAT";
	constructor() {
		super("Orchestrator Cloud started this task in the terminal, where files can't go. Remove the files to start it.");
		this.name = "CloudTaskFilesNeedChatError";
	}
}

/**
 * The files or the brief did not reach a new Cloud task, and its session was
 * deleted again. Nothing is left behind, so the composer keeps the task, with
 * the reason, to send again.
 */
export class CloudTaskFilesUndeliveredError extends Error {
	readonly code = "CLOUD_TASK_FILES_UNDELIVERED";
	constructor(message: string) {
		super(message);
		this.name = "CloudTaskFilesUndeliveredError";
	}
}

/**
 * The files or the brief did not reach a new Cloud task, and the cleanup could
 * not delete its session: it is still there, and may hold the brief. Nothing
 * about the start is final then, whatever the failure was (a control plane's
 * refusal included), so it carries no code or status and the page keeps the
 * send to retry with the same request id, which continues that session
 * (unsettledFileStarts). cause is the failure the cleanup followed.
 */
export class CloudTaskFilesUncertainError extends Error {
	constructor(cause: unknown) {
		super(cause instanceof Error ? cause.message : String(cause), { cause });
		this.name = "CloudTaskFilesUncertainError";
	}
}

/**
 * A start with files whose outcome is not known yet, by its request id: a
 * create that went unanswered may have made the session, and a session the
 * cleanup could not delete is still there. A retry with the same request id
 * continues that attempt under the same keys (with the files it already
 * uploaded to that session), so it reaches the session the attempt made
 * instead of starting a second one. It is forgotten once the attempt is
 * settled: started, refused before a session existed, or its session deleted.
 */
const unsettledFileStarts = new Map<string, { key: string; sessionId?: string; attachmentIds?: string[] }>();

/**
 * Creates a Chat session whose brief carries files. Files are uploaded to a
 * session that exists, and a session created with its prompt queues that
 * prompt as its first turn at once, without them. So the session is created
 * without a prompt (no turn), the files are uploaded to it, and the brief is
 * sent as its first message with them, with the task's model, effort and
 * permission. A session that did not start in Chat (a control plane that
 * predates interfaceMode starts it in the terminal), or that the files or the
 * brief did not reach, is deleted and the error thrown, so the composer keeps
 * the task to send again. Once the delete went through the failure is final
 * (a control plane's refusal keeps its own status); while the session may
 * still be there it is uncertain (CloudTaskFilesUncertainError), so a retry
 * can still reach it.
 * Each attempt creates under its own key, since a replay of the same create
 * would answer with the deleted session; a retry of an attempt not settled
 * yet keeps it (unsettledFileStarts).
 */
export async function createCloudChatSessionWithFiles(
	client: Pick<CloudCpClient, "createSession" | "sendSessionMessage" | "deleteSession">,
	orgId: string,
	body: CloudCpCreateSessionRequest & { interfaceMode: "chat" },
	files: readonly CloudTaskFile[],
	/** Uploads one file to the session; the signal stops it once another file failed. */
	upload: (sessionId: string, file: CloudTaskFile, signal: AbortSignal) => Promise<{ id: string }>,
	options: { idempotencyKey?: string } = {},
): Promise<CloudCpSessionResponse> {
	const requestId = options.idempotencyKey;
	const attempt = (requestId === undefined ? undefined : unsettledFileStarts.get(requestId))
		?? { key: `${requestId ?? crypto.randomUUID()}:files:${crypto.randomUUID()}` };
	if (requestId !== undefined) unsettledFileStarts.set(requestId, attempt);
	const settle = () => {
		if (requestId !== undefined) unsettledFileStarts.delete(requestId);
	};
	let created: CloudCpSessionResponse;
	try {
		created = await createCloudSession(client, orgId, { ...body, prompt: "" }, { idempotencyKey: attempt.key });
	} catch (error) {
		// A refusal made nothing. Without an answer the session may exist, so a
		// retry replays this create.
		if (error instanceof CloudCpError && error.status >= 400 && error.status < 500) settle();
		throw error;
	}
	const sessionId = created.session.id;
	try {
		if (created.session.interfaceMode !== "chat") throw new CloudTaskFilesNeedChatError();
		if (attempt.sessionId !== sessionId || !attempt.attachmentIds) {
			// One failed file stops the others, and they finish stopping before the
			// session is deleted, so none lands in it afterwards.
			const stop = new AbortController();
			const uploads = files.map((file) => upload(sessionId, file, stop.signal));
			const attachments = await Promise.all(uploads).catch(async (error: unknown) => {
				stop.abort();
				await Promise.allSettled(uploads);
				throw error;
			});
			Object.assign(attempt, { sessionId, attachmentIds: attachments.map((attachment) => attachment.id) });
		}
		await client.sendSessionMessage(orgId, sessionId, {
			text: body.prompt,
			attachmentIds: attempt.attachmentIds,
			...(body.model ? { model: body.model } : {}),
			...(body.reasoningEffort ? { reasoningEffort: body.reasoningEffort } : {}),
			...(body.approvalMode ? { approvalMode: body.approvalMode } : {}),
		}, { idempotencyKey: `${attempt.key}:brief` });
	} catch (error) {
		const deleted = await client.deleteSession(orgId, sessionId).then(() => true, () => false);
		if (!deleted) throw new CloudTaskFilesUncertainError(error);
		settle();
		if (error instanceof CloudCpError || error instanceof CloudTaskFilesNeedChatError) throw error;
		throw new CloudTaskFilesUndeliveredError(error instanceof Error ? error.message : String(error));
	}
	settle();
	return created;
}
