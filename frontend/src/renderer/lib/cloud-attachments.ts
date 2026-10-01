import type { CloudCpClient } from "./cloud-cp";
import type { FileAttachment } from "../hooks/useFileAttachments";

export const CLOUD_IMAGE_LIMITS = {
	count: 8,
	fileBytes: 10 * 1024 * 1024,
	totalBytes: 25 * 1024 * 1024,
	imagesOnly: true,
};

// A selection keeps its idempotency key and pending ID across retries. Mutating
// its descriptor before each await also preserves partial success on failure.
// Only storage receives bytes. Drafts contain IDs, never File objects or grants.
export async function uploadCloudAttachments(
	client: CloudCpClient,
	baseUrl: string,
	orgId: string,
	projectId: string,
	sessionId: string | undefined,
	files: FileAttachment[],
): Promise<FileAttachment[]> {
	const results = await Promise.allSettled(
		files.map(async (selection) => {
			if (selection.attachmentId && !selection.pendingUpload) return selection;
			if (selection.attachmentId) {
				try {
					const { attachment } = await client.completeAttachment(orgId, selection.attachmentId);
					Object.assign(selection, {
						attachmentId: attachment.id,
						pendingUpload: false,
						file: undefined,
						uploadError: undefined,
					});
					return selection;
				} catch (error) {
					// An interrupted upload needs its source File. A lost completion response
					// does not, so reopening the composer can still finish that upload.
					if (!selection.file) throw error;
				}
			}
			if (!selection.file) throw new Error("Reselect the image to retry its upload.");
			const digest = await crypto.subtle.digest("SHA-256", await selection.file.arrayBuffer());
			const sha256 = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
			const prepared = await client.prepareAttachment(
				orgId,
				{
					projectId,
					sessionId,
					filename: selection.name,
					size: selection.bytes,
					mimeType: selection.mimeType === "image/jpg" ? "image/jpeg" : selection.mimeType,
					sha256,
				},
				{ idempotencyKey: selection.id },
			);
			selection.attachmentId = prepared.attachment.id;
			selection.pendingUpload = true;
			if (prepared.attachment.status !== "ready") {
				const body = new FormData();
				for (const [key, value] of Object.entries(prepared.upload.fields)) body.append(key, value);
				body.append("file", selection.file);
				const response = await fetch(new URL(prepared.upload.url, baseUrl).toString(), {
					method: "POST",
					body,
					credentials: "omit",
					redirect: "error",
				});
				if (!response.ok) throw new Error("Image upload failed. Retry sending to refresh the upload grant.");
			}
			const { attachment } = await client.completeAttachment(orgId, prepared.attachment.id);
			Object.assign(selection, {
				attachmentId: attachment.id,
				pendingUpload: false,
				file: undefined,
				uploadError: undefined,
			});
			return selection;
		}),
	);
	const failed = results.find((result) => result.status === "rejected");
	if (failed?.status === "rejected") throw failed.reason;
	return files;
}

const draftPrefix = "ao.cloud-image-draft:";

// Task and terminal selections have no Chat draft owner. Keep only attachment
// descriptors here so they survive a renderer restart without persisting bytes.
export function readCloudImageDraft(key: string): FileAttachment[] {
	try {
		const value: unknown = JSON.parse(localStorage.getItem(draftPrefix + key) ?? "[]");
		if (!Array.isArray(value) || value.length > CLOUD_IMAGE_LIMITS.count) return [];
		return value
			.filter(
				(a): a is FileAttachment =>
					a !== null &&
					typeof a === "object" &&
					typeof a.id === "string" &&
					typeof a.attachmentId === "string" &&
					/^[0-9a-f-]{36}$/i.test(a.attachmentId) &&
					typeof a.name === "string" &&
					typeof a.mimeType === "string" &&
					typeof a.bytes === "number" &&
					a.bytes > 0 &&
					a.bytes <= CLOUD_IMAGE_LIMITS.fileBytes &&
					(a.pendingUpload === undefined || typeof a.pendingUpload === "boolean"),
			)
			.map(({ id, attachmentId, name, mimeType, bytes, pendingUpload }) => ({
				id,
				attachmentId,
				name,
				mimeType,
				bytes,
				pendingUpload,
			}));
	} catch {
		return [];
	}
}

export function writeCloudImageDraft(key: string, files: FileAttachment[]): void {
	const descriptors = files
		.filter((a) => a.attachmentId)
		.map(({ id, attachmentId, name, mimeType, bytes, pendingUpload }) => ({
			id,
			attachmentId,
			name,
			mimeType,
			bytes,
			pendingUpload,
		}));
	if (descriptors.length) localStorage.setItem(draftPrefix + key, JSON.stringify(descriptors));
	else localStorage.removeItem(draftPrefix + key);
}

export function clearCloudImageDrafts(): void {
	for (let i = localStorage.length - 1; i >= 0; i--) {
		const key = localStorage.key(i);
		if (key?.startsWith(draftPrefix)) localStorage.removeItem(key);
	}
}
