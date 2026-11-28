import { AoError } from "../../../../packages/shared/telemetry/ao-error";
import { getApiBaseUrl, reportingFetch } from "./api-client";
import { baseUrlForHost } from "./host-clients";
import { LOCAL_HOST, type HostId } from "./hosts";

/**
 * Files up to this size travel inline, as base64 in a JSON body, which is also
 * what a thumbnail and a native chat image need (the native image cap is the
 * same 10 MiB). A larger file is never read into the renderer: its `File` is
 * streamed from disk straight to the daemon (uploadAttachmentFile), and the
 * request names the upload instead of carrying the bytes.
 */
export const INLINE_ATTACHMENT_BYTES = 10 * 1024 * 1024;

/** One attachment on its way to the daemon: inline `data`, or a `file` still to upload (`data` is then ""). */
export type OutgoingAttachment = { mimeType: string; data: string; name?: string; file?: Blob };

/** What a spawn, delegate or stage request carries for one file. */
export type WireAttachment = { mimeType: string; data?: string; uploadId?: string };

// The daemon keeps an upload for 24 hours; reuse one for half that, so a retry
// of the same request names the same upload instead of sending the file again.
const UPLOAD_REUSE_MS = 12 * 60 * 60 * 1000;
const uploads = new WeakMap<Blob, Map<string, { id: Promise<string>; at: number }>>();

function uploadBase(hostId?: HostId): string {
	if (hostId && hostId !== LOCAL_HOST) {
		const base = baseUrlForHost(hostId);
		if (base === undefined) throw new Error("This machine is not connected. Reconnect it and attach the file again.");
		return base;
	}
	return getApiBaseUrl();
}

async function postUpload(file: Blob, hostId: HostId | undefined, signal?: AbortSignal): Promise<string> {
	// The File is the body: Chromium streams it from disk, so its bytes never
	// enter JS memory. A raw fetch, because the local API client buffers bodies.
	const response = await reportingFetch(new Request(`${uploadBase(hostId)}/api/v1/attachment-uploads`, {
		method: "POST",
		headers: { "Content-Type": "application/octet-stream" },
		body: file,
		signal,
	}));
	const body = await response.json().catch(() => undefined) as { uploadId?: string; message?: string } | undefined;
	if (!response.ok || !body?.uploadId) {
		throw new AoError("ATTACHMENT_UPLOAD_FAILED", body?.message?.trim() || "The file couldn’t be uploaded.");
	}
	return body.uploadId;
}

/** Stream one file to the daemon that will use it and resolve with its upload id. */
export function uploadAttachmentFile(file: Blob, hostId?: HostId, signal?: AbortSignal): Promise<string> {
	const host = hostId ?? LOCAL_HOST;
	let byHost = uploads.get(file);
	if (!byHost) {
		byHost = new Map();
		uploads.set(file, byHost);
	}
	const cached = byHost.get(host);
	if (cached && Date.now() - cached.at < UPLOAD_REUSE_MS) return cached.id;
	const id = postUpload(file, hostId, signal);
	const entry = { id, at: Date.now() };
	byHost.set(host, entry);
	// A failed or cancelled upload is not reused.
	id.catch(() => {
		if (byHost.get(host) === entry) byHost.delete(host);
	});
	return id;
}

/** Turn outgoing attachments into request entries, uploading the ones too large to inline. */
export function toWireAttachments(attachments: readonly OutgoingAttachment[], hostId?: HostId, signal?: AbortSignal): Promise<WireAttachment[]> {
	return Promise.all(attachments.map(async ({ mimeType, data, file }) =>
		!data && file ? { mimeType, uploadId: await uploadAttachmentFile(file, hostId, signal) } : { mimeType, data }));
}

/** Read a file as base64 without its data: URL prefix, for a destination that only takes inline bytes. */
export function readBlobAsBase64(blob: Blob): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onerror = () => reject(reader.error ?? new Error("Failed to read file"));
		reader.onload = () => resolve(String(reader.result).split(",", 2)[1] ?? "");
		reader.readAsDataURL(blob);
	});
}
