import * as Crypto from "expo-crypto";
import { Directory, File, Paths } from "expo-file-system";
import { MessageFileGoneError } from "../chat/messageFiles";
import type { MessageFile } from "../chat/types";
import { cloudAttachmentName, hexDigest, type CloudUploadDeps } from "./attachments";
import type { CloudClient } from "./client";

// The phone's side of cloud attachments: reading a picked file to upload it,
// and keeping a sent one on the phone to show it. An attachment's bytes never
// change once uploaded, so a downloaded copy is kept in the cache for good.

export const cloudUploadDeps: CloudUploadDeps = {
	readBytes: async (uri) => new Uint8Array(await new File(uri).arrayBuffer()),
	sha256: async (bytes) => hexDigest(await Crypto.digest(Crypto.CryptoDigestAlgorithm.SHA256, bytes as Uint8Array<ArrayBuffer>)),
};

const downloads = new Map<string, Promise<string>>();

/**
 * A local file:// copy of a message's file, downloaded once with the account's
 * token: an image source cannot refresh a token, and a file is immutable.
 */
export function cloudAttachmentUri(client: Pick<CloudClient, "attachmentRequest">, orgId: string, sessionId: string, file: MessageFile): Promise<string> {
	const key = `${orgId}/${sessionId}/${file.id}`;
	const known = downloads.get(key);
	if (known) return known;
	const download = (async () => {
		const directory = new Directory(Paths.cache, "cloud-attachments");
		if (!directory.exists) directory.create({ intermediates: true, idempotent: true });
		const destination = new File(directory, `${file.id}-${cloudAttachmentName(file.name)}`);
		if (destination.exists && destination.size === file.size) return destination.uri;
		const { url, headers } = await client.attachmentRequest(orgId, sessionId, file.id);
		try {
			const saved = await File.downloadFileAsync(url, destination, { headers, idempotent: true });
			return saved.uri;
		} catch (error) {
			throw downloadStatus(error) === 404 ? new MessageFileGoneError(file.name) : error;
		}
	})();
	downloads.set(key, download);
	// A failed download is tried again the next time the file is shown; one
	// that is gone is not asked for again.
	download.catch((error: unknown) => { if (!(error instanceof MessageFileGoneError)) downloads.delete(key); });
	return download;
}

/**
 * The HTTP status a failed download reports, when it says one: expo-file-system
 * words it "response has status 404" on iOS and "response has status: 404" on
 * Android.
 */
export function downloadStatus(error: unknown): number | undefined {
	const match = /status:?\s*(\d{3})\b/.exec(error instanceof Error ? error.message : String(error));
	return match ? Number(match[1]) : undefined;
}
