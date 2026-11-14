// Files attached to a cloud session. The control plane keeps their bytes in
// its storage and lists them on the message they ride; the worker places them
// in the sandbox before the agent reads the message. Uploads and downloads are
// bytes, so they go through the main process's binary channel, which attaches
// the AO Cloud token like every other control-plane call.

import { formatCapturedFileReferences, type ChatFileReference } from "../../shared/chat-file-reference";
import { DEFAULT_CLOUD_CP_BINARY_TIMEOUT_MS, MAX_CLOUD_CP_BINARY_TIMEOUT_MS } from "../../shared/cloud-binary";
import type { CloudCpProxyBinaryRequestInit, CloudCpProxyBinaryResponse } from "../../main/cloud-cp-proxy";
import { aoBridge } from "./bridge";
import { CLOUD_ATTACHMENT_REFERENCE_PREFIX } from "./cloud-attachment-reference";
import { raceAbort } from "./cloud-cp/transport";

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

/** Options for one attachment request. */
export interface CloudAttachmentRequestOptions {
	/** Cancels the request: the caller stops waiting at once and main aborts it. */
	signal?: AbortSignal;
	/** How long the request may take; main ends it after that. */
	timeoutMs?: number;
}

// Main ends a request at its timeout; this side stops waiting a little after,
// so a main process that never answers cannot hold the caller either.
const RENDERER_TIMEOUT_GRACE_MS = 15_000;
// An upload's time grows with its size, from a minute at 128 KiB/s.
const UPLOAD_BYTES_PER_SECOND = 128 * 1024;

/** How long an upload of this many bytes may take. */
export function uploadTimeoutMs(bytes: number): number {
	return Math.min(MAX_CLOUD_CP_BINARY_TIMEOUT_MS, 60_000 + Math.ceil(bytes / UPLOAD_BYTES_PER_SECOND) * 1000);
}

function isAbort(error: unknown): boolean {
	return error instanceof DOMException && (error.name === "AbortError" || error.name === "TimeoutError");
}

/**
 * One binary request through main, which the caller can cancel and which
 * cannot outlive its time. Errors read as a person would: the IPC wrapper's
 * prefix is dropped, and an unreachable control plane says so.
 */
async function binaryRequest(
	init: Omit<CloudCpProxyBinaryRequestInit, "requestId" | "timeoutMs">,
	options: CloudAttachmentRequestOptions = {},
): Promise<CloudCpProxyBinaryResponse> {
	const timeoutMs = options.timeoutMs ?? DEFAULT_CLOUD_CP_BINARY_TIMEOUT_MS;
	const requestId = crypto.randomUUID();
	const guard = AbortSignal.timeout(timeoutMs + RENDERER_TIMEOUT_GRACE_MS);
	const signal = options.signal ? AbortSignal.any([options.signal, guard]) : guard;
	signal.throwIfAborted();
	const cancel = () => aoBridge.cloudCp.cancelBinary(requestId);
	signal.addEventListener("abort", cancel, { once: true });
	try {
		return await raceAbort(aoBridge.cloudCp.requestBinary({ ...init, requestId, timeoutMs }), signal);
	} catch (error) {
		if (options.signal?.aborted) throw options.signal.reason ?? error;
		if (guard.aborted || isAbort(error)) throw new Error("Orchestrator Cloud did not answer in time. Try again.");
		const message = (error instanceof Error ? error.message : String(error))
			.replace(/^Error invoking remote method '[^']*':\s*(?:[A-Za-z]*Error:\s*)?/, "").trim();
		if (/fetch failed|ECONNREFUSED|ECONNRESET|ENOTFOUND|ETIMEDOUT|network/i.test(message)) {
			throw new Error("Couldn’t reach Orchestrator Cloud. Check the connection and try again.");
		}
		throw new Error(message || "The attachment request failed.");
	} finally {
		signal.removeEventListener("abort", cancel);
	}
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

/**
 * Upload a file (base64 bytes, as the composer holds them) to a cloud session.
 * A refusal throws the control plane's own message (the file too large, the
 * session's quota, a worker that cannot take files yet, a deleted session, a
 * name it will not take).
 */
export async function uploadCloudAttachment(
	baseUrl: string,
	orgId: string,
	sessionId: string,
	file: { name: string; data: string } | { name: string; bytes: Uint8Array },
	options: Omit<CloudAttachmentRequestOptions, "timeoutMs"> = {},
): Promise<CloudAttachment> {
	const bytes = "bytes" in file ? file.bytes : base64Bytes(file.data);
	const response = await binaryRequest({
		baseUrl,
		path: `${sessionPath(orgId, sessionId)}?name=${encodeURIComponent(file.name)}`,
		method: "POST",
		headers: { "Content-Type": "application/octet-stream", [SHA256_HEADER]: await sha256Hex(bytes) },
		body: bytes,
	}, { signal: options.signal, timeoutMs: uploadTimeoutMs(bytes.byteLength) });
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
	options: CloudAttachmentRequestOptions = {},
): Promise<Blob> {
	const response = await binaryRequest({
		baseUrl,
		path: `${sessionPath(orgId, sessionId)}/${encodeURIComponent(attachment.id)}`,
		method: "GET",
	}, options);
	if (response.status !== 200) throw new Error(errorMessage(response.body, response.status));
	return new Blob([response.body as BlobPart], { type: attachment.contentType });
}

/**
 * Have the session's worker place an attachment in its workspace, for a file
 * dropped on the terminal, and answer with its path in the sandbox.
 */
export async function placeCloudAttachment(
	baseUrl: string,
	orgId: string,
	sessionId: string,
	attachmentId: string,
	options: CloudAttachmentRequestOptions = {},
): Promise<string> {
	const response = await binaryRequest({
		baseUrl,
		path: `${sessionPath(orgId, sessionId)}/${encodeURIComponent(attachmentId)}/place`,
		method: "POST",
	}, options);
	if (response.status !== 200) throw new Error(errorMessage(response.body, response.status));
	const parsed = JSON.parse(new TextDecoder().decode(response.body)) as { path?: unknown };
	if (typeof parsed.path !== "string" || parsed.path === "") throw new Error("The worker did not place the attachment.");
	return parsed.path;
}

const REFERENCE_PREFIX = CLOUD_ATTACHMENT_REFERENCE_PREFIX;
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

/**
 * A cloud message with its staged files taken out as attachment ids. The
 * composer lists them under "Attached files" at the end of the message it
 * was asked to send, and with text delivery appends the code captures after
 * that and hands the message over as `displayText`. So the references come
 * off the message before its captures: `text` keeps the captures for the
 * agent, `displayText` is the message as the transcript shows it, and
 * neither holds a reference. A caller that hands the captures over
 * separately gets them inlined here.
 */
export function cloudMessageAttachments(
	text: string,
	displayText: string | undefined,
	fileReferences?: readonly ChatFileReference[],
): { text: string; displayText: string; attachmentIds: string[] } {
	const { text: body, attachmentIds } = splitCloudAttachmentReferences(displayText ?? text);
	const captures = displayText !== undefined && text.startsWith(displayText)
		? text.slice(displayText.length)
		: formatCapturedFileReferences("", fileReferences);
	return { text: body + captures, displayText: body, attachmentIds };
}
