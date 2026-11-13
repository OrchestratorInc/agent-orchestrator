import type { ChatImage, ChatResource } from "../chat/types";
import type { CloudClient } from "./client";
import type { CloudAttachment, CloudOrganization } from "./types";

// Files a cloud Chat message carries, as the desktop sends them
// (frontend/src/renderer/lib/cloud-attachments.ts): each file is uploaded to
// the session as soon as it is picked, and the message names the uploads by
// id. The worker places them in the workspace before the agent reads it.

/** How many files one message may carry (the control plane's limit). */
export const MAX_CLOUD_ATTACHMENTS = 10;
/**
 * Characters the control plane refuses in a file name: path separators, C0
 * and C1 controls, zero-width characters, line and paragraph separators, and
 * direction overrides and isolates.
 */
// eslint-disable-next-line no-control-regex
const HIDDEN_NAME_CHARACTERS = /[/\\\u0000-\u001f\u007f-\u009f\u200b-\u200f\ufeff\u2028\u2029\u202a-\u202e\u2066-\u2069]/g;
/** The longest file name the control plane keeps, in UTF-8 bytes. */
const MAX_NAME_BYTES = 238;

/**
 * The largest file the session's organization takes, or undefined where the
 * control plane takes none: it lists the limit only with attachments on.
 */
export function cloudAttachmentLimit(org: Pick<CloudOrganization, "offering"> | null | undefined): number | undefined {
	const limit = org?.offering?.maxAttachmentBytes;
	return typeof limit === "number" && limit > 0 ? limit : undefined;
}

/** "25 MB" for a limit, as a person reads file sizes. */
export function attachmentSizeLabel(bytes: number): string {
	if (bytes >= 1 << 20) return `${Math.round(bytes / (1 << 20))} MB`;
	if (bytes >= 1 << 10) return `${Math.round(bytes / (1 << 10))} KB`;
	return `${bytes} bytes`;
}

/**
 * A name the control plane keeps as given: a plain file name, with no
 * directory, control, zero-width or direction-changing characters (which
 * could make it read as another name), within its length.
 */
export function cloudAttachmentName(name: string): string {
	let safe = name.replace(HIDDEN_NAME_CHARACTERS, "_").trim();
	if (!safe || safe === "." || safe === "..") safe = "attachment";
	const encoder = new TextEncoder();
	if (encoder.encode(safe).byteLength <= MAX_NAME_BYTES) return safe;
	// Keep the extension: it is what the agent and the viewer go by.
	const dot = safe.lastIndexOf(".");
	const extension = dot > 0 && safe.length - dot <= 16 ? safe.slice(dot) : "";
	let stem = extension ? safe.slice(0, dot) : safe;
	while (stem && encoder.encode(stem + extension).byteLength > MAX_NAME_BYTES) stem = Array.from(stem).slice(0, -1).join("");
	return stem + extension;
}

export function hexDigest(buffer: ArrayBuffer): string {
	return Array.from(new Uint8Array(buffer), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** A file picked on the phone. */
export type PickedCloudFile = { uri: string; name: string; mimeType?: string; size?: number };

/** Reading a picked file and hashing it are the platform's; injected so this stays testable. */
export type CloudUploadDeps = {
	readBytes(uri: string): Promise<Uint8Array>;
	sha256(bytes: Uint8Array): Promise<string>;
};

/** Something to upload: its name and type, its size when known, and how to read it. */
export type CloudUploadSource = { name: string; mimeType?: string; size?: number; read(): Promise<Uint8Array> };

/** Uploads one file to the session, refusing one over the organization's limit before reading it when its size is known. */
export async function uploadCloudSource(
	client: Pick<CloudClient, "uploadAttachment">,
	target: { orgId: string; sessionId: string },
	source: CloudUploadSource,
	maxBytes: number,
	sha256: CloudUploadDeps["sha256"],
	signal?: AbortSignal,
): Promise<CloudAttachment> {
	const tooLarge = () => new Error(`${source.name} is larger than ${attachmentSizeLabel(maxBytes)}, the most a cloud session takes.`);
	if (source.size !== undefined && source.size > maxBytes) throw tooLarge();
	const bytes = await source.read();
	if (bytes.byteLength > maxBytes) throw tooLarge();
	return client.uploadAttachment(target.orgId, target.sessionId, {
		name: cloudAttachmentName(source.name),
		bytes,
		sha256: await sha256(bytes),
		contentType: source.mimeType || "application/octet-stream",
		signal,
	});
}

/** Uploads one picked file to the session, refusing one over the organization's limit before reading it. */
export function uploadCloudFile(
	client: Pick<CloudClient, "uploadAttachment">,
	target: { orgId: string; sessionId: string },
	file: PickedCloudFile,
	maxBytes: number,
	deps: CloudUploadDeps,
	signal?: AbortSignal,
): Promise<CloudAttachment> {
	return uploadCloudSource(client, target, { name: file.name, mimeType: file.mimeType, size: file.size, read: () => deps.readBytes(file.uri) }, maxBytes, deps.sha256, signal);
}

/** A photo's extension from its type, for the name it is uploaded under. */
function imageExtension(mimeType: string): string {
	const subtype = mimeType.split("/")[1]?.split(/[+;]/)[0]?.toLowerCase();
	return subtype === "jpeg" ? "jpg" : subtype || "img";
}

/** Bytes from base64, as the photo picker hands an image over. */
export function base64Bytes(data: string): Uint8Array {
	const binary = atob(data.replace(/\s/g, ""));
	const bytes = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
	return bytes;
}

/**
 * What the shared chat composer hands a send, as uploads: photos come as
 * base64, a text file as its text, and any other file as a local copy to read.
 * Photos are named in the order they were picked, as the phone has no name for them.
 */
export function composerUploadSources(
	images: readonly ChatImage[] | undefined,
	resources: readonly ChatResource[] | undefined,
	readBytes: CloudUploadDeps["readBytes"],
): CloudUploadSource[] {
	const photos = (images ?? []).map((image, index): CloudUploadSource => {
		const bytes = base64Bytes(image.data);
		return { name: `photo-${index + 1}.${imageExtension(image.mimeType)}`, mimeType: image.mimeType, size: bytes.byteLength, read: async () => bytes };
	});
	const files = (resources ?? []).map((resource): CloudUploadSource => {
		if (resource.text !== undefined) {
			const bytes = new TextEncoder().encode(resource.text);
			return { name: resource.name, mimeType: resource.mimeType || "text/plain", size: bytes.byteLength, read: async () => bytes };
		}
		return { name: resource.name, mimeType: resource.mimeType, read: () => readBytes(resource.uri) };
	});
	return [...photos, ...files];
}
