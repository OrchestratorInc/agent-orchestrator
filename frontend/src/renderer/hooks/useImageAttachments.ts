import { useCallback, useState } from "react";

export const MAX_ATTACHMENTS = 8;
export const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024;
export const MAX_ATTACHMENTS_BYTES = 100 * 1024 * 1024;

const mb = (bytes: number) => Math.round(bytes / (1024 * 1024));

export type ImageAttachment = {
	id: string;
	mimeType: string;
	bytes: number;
	dataUrl: string;
	data: string;
};

export type ImageAttachmentPayload = {
	mimeType: string;
	data: string;
};

const SUPPORTED_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/bmp"]);

const isImageType = (type: string) => SUPPORTED_IMAGE_TYPES.has(type.toLowerCase().trim());

const readImageFile = (file: File): Promise<ImageAttachment> =>
	new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onerror = () => reject(reader.error ?? new Error("Failed to read image"));
		reader.onload = () => {
			const dataUrl = typeof reader.result === "string" ? reader.result : "";
			const comma = dataUrl.indexOf(",");
			if (!dataUrl || comma < 0) {
				reject(new Error("Unreadable image data"));
				return;
			}
			resolve({
				id:
					typeof crypto !== "undefined" && "randomUUID" in crypto
						? crypto.randomUUID()
						: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
				mimeType: file.type || "image/png",
				bytes: file.size,
				dataUrl,
				data: dataUrl.slice(comma + 1),
			});
		};
		reader.readAsDataURL(file);
	});

export function useImageAttachments() {
	const [attachments, setAttachments] = useState<ImageAttachment[]>([]);
	const [error, setError] = useState<string | null>(null);

	const addFiles = useCallback(async (files: Iterable<File>) => {
		const images = Array.from(files).filter((file) => file.type.toLowerCase().startsWith("image/"));
		if (images.length === 0) return;

		const errors = new Set<string>();
		const supported = images.filter((file) => {
			if (!isImageType(file.type)) {
				errors.add("Only PNG, JPEG, GIF, WebP, and BMP images are supported.");
				return false;
			}
			return true;
		});
		const readable = supported.filter((file) => {
			if (file.size > MAX_ATTACHMENT_BYTES) {
				errors.add(`Each image must be under ${mb(MAX_ATTACHMENT_BYTES)} MB.`);
				return false;
			}
			return true;
		});

		const results = await Promise.all(readable.map((file) => readImageFile(file).catch(() => null)));
		const fresh = results.filter((a): a is ImageAttachment => a !== null);
		if (fresh.length < results.length) {
			errors.add("Some images couldn't be read and were skipped.");
		}

		setAttachments((prev) => {
			const accepted = [...prev];
			let total = accepted.reduce((sum, a) => sum + a.bytes, 0);
			for (const a of fresh) {
				if (accepted.length >= MAX_ATTACHMENTS) {
					errors.add(`You can attach up to ${MAX_ATTACHMENTS} images.`);
					break;
				}
				if (total + a.bytes > MAX_ATTACHMENTS_BYTES) {
					errors.add(`Attachments must total under ${mb(MAX_ATTACHMENTS_BYTES)} MB.`);
					break;
				}
				accepted.push(a);
				total += a.bytes;
			}
			return accepted.length === prev.length ? prev : accepted;
		});
		setError(errors.size > 0 ? Array.from(errors).join(" ") : null);
	}, []);

	const remove = useCallback((id: string) => {
		setAttachments((prev) => prev.filter((a) => a.id !== id));
		setError(null);
	}, []);

	const clear = useCallback(() => {
		setAttachments([]);
		setError(null);
	}, []);

	const toPayload = useCallback(
		(): ImageAttachmentPayload[] => attachments.map(({ mimeType, data }) => ({ mimeType, data })),
		[attachments],
	);

	return { attachments, error, addFiles, remove, clear, toPayload };
}
