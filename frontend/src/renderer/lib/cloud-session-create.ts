import { CloudCpError } from "./cloud-cp";
import type { CloudCpClient, CloudCpCreateSessionRequest, CloudCpInterfaceMode, CloudCpMutationOptions, CloudCpSessionResponse } from "./cloud-cp";
import type { Settings } from "../hooks/useSettings";
import { reportCloudSessionCreateRequested } from "./cloud-telemetry";

export function cloudDefaultInterfaceMode(
	settings: Pick<Settings, "defaultSessionMode"> | undefined,
): CloudCpInterfaceMode {
	return settings?.defaultSessionMode === "chat" ? "chat" : "tui";
}

export async function createCloudSession(
	client: Pick<CloudCpClient, "createSession">,
	orgId: string,
	body: CloudCpCreateSessionRequest,
	options?: CloudCpMutationOptions,
): Promise<CloudCpSessionResponse> {
	try {
		reportCloudSessionCreateRequested(body);
	} catch {
	}
	const send = (request: CloudCpCreateSessionRequest, sendOptions = options) =>
		sendOptions ? client.createSession(orgId, request, sendOptions) : client.createSession(orgId, request);
	const { interfaceMode, defaultInterfaceMode, ...withoutInterface } = body;
	if (interfaceMode === undefined && defaultInterfaceMode === undefined) return send(body);
	try {
		return await send(body);
	} catch (error) {
		if (!isUnknownInterfaceFieldRefusal(error)) throw error;
		return send(withoutInterface, options?.idempotencyKey ? { ...options, idempotencyKey: `${options.idempotencyKey}:tui` } : options);
	}
}

export function isUnknownInterfaceFieldRefusal(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 400 && error.code === "invalid_request";
}

export interface CloudTaskFile {
	mimeType: string;
	data: string;
	name?: string;
}

export class CloudTaskFilesNeedChatError extends Error {
	readonly code = "CLOUD_ATTACHMENTS_NEED_CHAT";
	constructor() {
		super("Orchestrator Cloud started this task in the terminal, where files can't go. Remove the files to start it.");
		this.name = "CloudTaskFilesNeedChatError";
	}
}

export class CloudTaskFilesUndeliveredError extends Error {
	readonly code = "CLOUD_TASK_FILES_UNDELIVERED";
	constructor(message: string) {
		super(message);
		this.name = "CloudTaskFilesUndeliveredError";
	}
}

export class CloudTaskFilesUncertainError extends Error {
	constructor(cause: unknown) {
		super(cause instanceof Error ? cause.message : String(cause), { cause });
		this.name = "CloudTaskFilesUncertainError";
	}
}

const unsettledFileStarts = new Map<string, { key: string; sessionId?: string; attachmentIds?: string[] }>();

export async function createCloudChatSessionWithFiles(
	client: Pick<CloudCpClient, "createSession" | "sendSessionMessage" | "deleteSession">,
	orgId: string,
	body: CloudCpCreateSessionRequest & { interfaceMode: "chat" },
	files: readonly CloudTaskFile[],
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
		if (error instanceof CloudCpError && error.status >= 400 && error.status < 500) settle();
		throw error;
	}
	const sessionId = created.session.id;
	try {
		if (created.session.interfaceMode !== "chat") throw new CloudTaskFilesNeedChatError();
		if (attempt.sessionId !== sessionId || !attempt.attachmentIds) {
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
