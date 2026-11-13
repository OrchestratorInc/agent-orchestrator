import { beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ download: vi.fn() }));

vi.mock("expo-crypto", () => ({ digest: vi.fn(), CryptoDigestAlgorithm: { SHA256: "SHA-256" } }));
vi.mock("expo-file-system", () => {
	class Directory {
		exists = true;
		create() {}
	}
	class File {
		exists = false;
		size = 0;
		uri: string;
		constructor(_parent: unknown, name: string) { this.uri = `file:///cache/${name}`; }
		static downloadFileAsync = fixtures.download;
	}
	return { Directory, File, Paths: { cache: "cache" } };
});

import { MessageFileGoneError } from "../chat/messageFiles";
import { cloudAttachmentUri, downloadStatus } from "./attachmentFiles";

const client = { attachmentRequest: vi.fn(async () => ({ url: "https://api.example/a", headers: { Authorization: "Bearer t" } })) };
const file = (id: string) => ({ id, name: "shot.png", mimeType: "image/png", size: 10 });

beforeEach(() => {
	fixtures.download.mockReset();
	client.attachmentRequest.mockClear();
});

describe("a cloud message's file on the phone", () => {
	it("reads the status a failed download reports", () => {
		expect(downloadStatus(new Error("UnableToDownloadException: Unable to download a file: response has status 404"))).toBe(404);
		expect(downloadStatus(new Error("Unable to download a file: response has status: 404"))).toBe(404);
		expect(downloadStatus(new Error("The Internet connection appears to be offline."))).toBeUndefined();
	});

	// Archiving a cloud session deletes its attachments, and restoring it does not
	// bring them back: the control plane answers 404 from then on.
	it("says a file the control plane no longer has is gone, and does not ask for it again", async () => {
		fixtures.download.mockRejectedValue(new Error("Unable to download a file: response has status 404"));
		await expect(cloudAttachmentUri(client, "org", "s1", file("gone"))).rejects.toBeInstanceOf(MessageFileGoneError);
		await expect(cloudAttachmentUri(client, "org", "s1", file("gone"))).rejects.toBeInstanceOf(MessageFileGoneError);
		expect(fixtures.download).toHaveBeenCalledTimes(1);
	});

	it("tries any other failure again the next time the file is shown", async () => {
		fixtures.download.mockRejectedValueOnce(new Error("The Internet connection appears to be offline."));
		await expect(cloudAttachmentUri(client, "org", "s1", file("flaky"))).rejects.not.toBeInstanceOf(MessageFileGoneError);
		fixtures.download.mockResolvedValueOnce({ uri: "file:///cache/flaky-shot.png" });
		await expect(cloudAttachmentUri(client, "org", "s1", file("flaky"))).resolves.toBe("file:///cache/flaky-shot.png");
		expect(fixtures.download).toHaveBeenCalledTimes(2);
	});
});
