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

/** Why a task with files cannot start: they ride a Chat session's first message. */
export class CloudTaskFilesNeedChatError extends Error {
	constructor() {
		super("Files can start a Cloud task in Chat only. Start it in Chat, or remove the files.");
		this.name = "CloudTaskFilesNeedChatError";
	}
}

/**
 * Creates a Chat session whose brief carries files. Files are uploaded to a
 * session that exists, and a session created with its prompt queues that
 * prompt as its first turn at once, without them. So the session is created
 * without a prompt (no turn), the files are uploaded to it, and the brief is
 * sent as its first message with them, with the task's model, effort and
 * permission. A session that did not start in Chat (a control plane that
 * predates interfaceMode starts it in the terminal), or that the files or the
 * brief did not reach, is deleted and the error thrown, so the composer keeps
 * the task to send again. Each attempt creates under its own key, since a
 * replay of the same create would answer with the deleted session.
 */
export async function createCloudChatSessionWithFiles(
	client: Pick<CloudCpClient, "createSession" | "sendSessionMessage" | "deleteSession">,
	orgId: string,
	body: CloudCpCreateSessionRequest & { interfaceMode: "chat" },
	files: readonly CloudTaskFile[],
	upload: (sessionId: string, file: CloudTaskFile) => Promise<{ id: string }>,
	options: { idempotencyKey?: string } = {},
): Promise<CloudCpSessionResponse> {
	const key = `${options.idempotencyKey ?? crypto.randomUUID()}:files:${crypto.randomUUID()}`;
	const created = await createCloudSession(client, orgId, { ...body, prompt: "" }, { idempotencyKey: key });
	const sessionId = created.session.id;
	try {
		if (created.session.interfaceMode !== "chat") throw new CloudTaskFilesNeedChatError();
		const attachments = await Promise.all(files.map((file) => upload(sessionId, file)));
		await client.sendSessionMessage(orgId, sessionId, {
			text: body.prompt,
			attachmentIds: attachments.map((attachment) => attachment.id),
			...(body.model ? { model: body.model } : {}),
			...(body.reasoningEffort ? { reasoningEffort: body.reasoningEffort } : {}),
			...(body.approvalMode ? { approvalMode: body.approvalMode } : {}),
		}, { idempotencyKey: `${key}:brief` });
	} catch (error) {
		await client.deleteSession(orgId, sessionId).catch(() => undefined);
		throw error;
	}
	return created;
}
