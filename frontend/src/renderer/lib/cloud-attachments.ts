// Files attached to a cloud session. The control plane keeps their bytes in
// its storage and lists them on the message they ride; the worker places them
// in the sandbox before the agent reads the message. Uploads and downloads are
// bytes, so they go through the main process's binary channel, which attaches
// the AO Cloud token like every other control-plane call.

import { aoBridge } from "./bridge";

/** An attachment as the control plane describes it. */
export interface CloudAttachment {
	id: string;
	fileName: string;
	contentType: string;
	size: number;
}

const SHA256_HEADER = "X-AO-Artifact-SHA256";

function sessionPath(orgId: string, sessionId: string): string {
	return `/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/sessions/${encodeURIComponent(sessionId)}/attachments`;
}

function errorMessage(body: Uint8Array, status: number): string {
	try {
		const parsed = JSON.parse(new TextDecoder().decode(body)) as { message?: unknown };
		if (typeof parsed.message === "string" && parsed.message !== "") return parsed.message;
	} catch {
		// Not an error envelope; fall back to the status.
	}
	return `The attachment request failed (${status}).`;
}

function base64Bytes(data: string): Uint8Array {
	const binary = atob(data);
	const bytes = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
	return bytes;
}

async function sha256Hex(bytes: Uint8Array): Promise<string> {
	const digest = await crypto.subtle.digest("SHA-256", bytes as BufferSource);
	return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** Upload a file (base64 bytes, as the composer holds them) to a cloud session. */
export async function uploadCloudAttachment(
	baseUrl: string,
	orgId: string,
	sessionId: string,
	file: { name: string; data: string } | { name: string; bytes: Uint8Array },
): Promise<CloudAttachment> {
	const bytes = "bytes" in file ? file.bytes : base64Bytes(file.data);
	const response = await aoBridge.cloudCp.requestBinary({
		baseUrl,
		path: `${sessionPath(orgId, sessionId)}?name=${encodeURIComponent(file.name)}`,
		method: "POST",
		headers: { "Content-Type": "application/octet-stream", [SHA256_HEADER]: await sha256Hex(bytes) },
		body: bytes,
	});
	if (response.status !== 201) throw new Error(errorMessage(response.body, response.status));
	const parsed = JSON.parse(new TextDecoder().decode(response.body)) as { attachment: CloudAttachment };
	return parsed.attachment;
}

/** Read an attachment's bytes as a Blob of its content type. */
export async function fetchCloudAttachment(
	baseUrl: string,
	orgId: string,
	sessionId: string,
	attachment: Pick<CloudAttachment, "id" | "contentType">,
): Promise<Blob> {
	const response = await aoBridge.cloudCp.requestBinary({
		baseUrl,
		path: `${sessionPath(orgId, sessionId)}/${encodeURIComponent(attachment.id)}`,
		method: "GET",
	});
	if (response.status !== 200) throw new Error(errorMessage(response.body, response.status));
	return new Blob([response.body as BlobPart], { type: attachment.contentType });
}

/**
 * Have the session's worker place an attachment in its workspace, for a file
 * dropped on the terminal, and answer with its path in the sandbox.
 */
export async function placeCloudAttachment(baseUrl: string, orgId: string, sessionId: string, attachmentId: string): Promise<string> {
	const response = await aoBridge.cloudCp.requestBinary({
		baseUrl,
		path: `${sessionPath(orgId, sessionId)}/${encodeURIComponent(attachmentId)}/place`,
		method: "POST",
	});
	if (response.status !== 200) throw new Error(errorMessage(response.body, response.status));
	const parsed = JSON.parse(new TextDecoder().decode(response.body)) as { path?: unknown };
	if (typeof parsed.path !== "string" || parsed.path === "") throw new Error("The worker did not place the attachment.");
	return parsed.path;
}

// The composer treats a staged file as a path it lists under "Attached
// files". A cloud file is staged by uploading it, and its "path" is this
// reference, which the cloud send turns back into the message's attachment ids.
const REFERENCE_PREFIX = "ao-cloud-attachment:";
const ATTACHED_FILES_HEADING = "Attached files (read these files in the workspace):";

/** The staged "path" of an uploaded cloud attachment. */
export function cloudAttachmentReference(attachment: Pick<CloudAttachment, "id" | "fileName">): string {
	return `${REFERENCE_PREFIX}${attachment.id}/${encodeURIComponent(attachment.fileName)}`;
}

/**
 * Split the composer's message into the text the person wrote and the cloud
 * attachments it lists. A message without cloud references comes back as is.
 */
export function splitCloudAttachmentReferences(message: string): { text: string; attachmentIds: string[] } {
	const headingIndex = message.lastIndexOf(ATTACHED_FILES_HEADING);
	if (headingIndex === -1) return { text: message, attachmentIds: [] };
	const lines = message.slice(headingIndex + ATTACHED_FILES_HEADING.length).split("\n").filter((line) => line.trim() !== "");
	const attachmentIds: string[] = [];
	for (const line of lines) {
		const match = /^- ao-cloud-attachment:([^/\s]+)\//.exec(line.trim());
		if (!match) return { text: message, attachmentIds: [] };
		attachmentIds.push(match[1]);
	}
	return { text: message.slice(0, headingIndex).replace(/\s+$/, ""), attachmentIds };
}
