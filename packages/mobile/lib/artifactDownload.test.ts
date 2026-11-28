import { beforeEach, describe, expect, it, vi } from "vitest";

const disk = vi.hoisted(() => ({ files: new Set<string>(), deleted: [] as string[], downloadSize: 10 }));

vi.mock("expo-file-system", () => {
	class Directory {
		uri: string;
		constructor(...parts: Array<string | { uri: string }>) {
			this.uri = parts.map((part) => typeof part === "string" ? part : part.uri).join("/");
		}
		get exists() {
			return true;
		}
		create() {}
		list() {
			return [];
		}
	}
	class File {
		uri: string;
		constructor(directory: { uri: string }, name: string) {
			this.uri = `${directory.uri}/${name}`;
		}
		get exists() {
			return disk.files.has(this.uri);
		}
		get size() {
			return disk.downloadSize;
		}
		delete() {
			disk.files.delete(this.uri);
			disk.deleted.push(this.uri);
		}
		move(target: { uri: string }) {
			disk.files.delete(this.uri);
			disk.files.add(target.uri);
			this.uri = target.uri;
		}
		static async downloadFileAsync(_url: string, destination: File) {
			disk.files.add(destination.uri);
			return destination;
		}
	}
	return { Directory, File, Paths: { cache: { uri: "cache" } } };
});

import { ArtifactTooLargeError, cachedArtifactUri, discardArtifact, downloadArtifact } from "./artifactDownload";
import { artifactCacheEntry } from "./artifacts";

beforeEach(() => {
	disk.files.clear();
	disk.deleted.length = 0;
	disk.downloadSize = 10;
});

describe("downloadArtifact", () => {
	const source = { uri: "http://machine/spec.pdf", headers: {} };

	it("keeps a download within the limit", async () => {
		const entry = artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v1");
		await expect(downloadArtifact(source, entry, { maxBytes: 100 })).resolves.toBe(`cache/artifacts/${entry.name}`);
		expect(disk.files.has(`cache/artifacts/${entry.name}`)).toBe(true);
	});

	it("deletes a download over the limit and says it is too large", async () => {
		disk.downloadSize = 101;
		const entry = artifactCacheEntry("host.a", "ao-1", "big.pdf", "v1");
		await expect(downloadArtifact(source, entry, { maxBytes: 100 })).rejects.toBeInstanceOf(ArtifactTooLargeError);
		expect(disk.files.size).toBe(0);
		expect(disk.deleted).toEqual([`cache/artifacts/${entry.name}.part`]);
	});
});

describe("discardArtifact", () => {
	it("deletes the cached copy, so the next open downloads it again", () => {
		const entry = artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v1");
		disk.files.add(`cache/artifacts/${entry.name}`);
		discardArtifact(entry);
		expect(disk.deleted).toEqual([`cache/artifacts/${entry.name}`]);
		expect(disk.files.size).toBe(0);
	});

	it("does nothing when there is no cached copy", () => {
		expect(() => discardArtifact(artifactCacheEntry("host.a", "ao-1", "spec.pdf", "v1"))).not.toThrow();
		expect(disk.deleted).toEqual([]);
	});
});

describe("cachedArtifactUri", () => {
	const source = { uri: "http://machine/shot.png", headers: {} };

	it("finds a downloaded version at once, and nothing for one not downloaded", async () => {
		const v1 = artifactCacheEntry("host.a", "ao-1", "shot.png", "v1");
		const v2 = artifactCacheEntry("host.a", "ao-1", "shot.png", "v2");
		expect(cachedArtifactUri(v1)).toBeUndefined();
		await downloadArtifact(source, v1);
		expect(cachedArtifactUri(v1)).toBe(`cache/artifacts/${v1.name}`);
		// A rewritten file is another version: it is not served from the old copy.
		expect(cachedArtifactUri(v2)).toBeUndefined();
	});

	it("says nothing while the download is still running, nor for an empty file", async () => {
		const entry = artifactCacheEntry("host.a", "ao-1", "shot.png", "v1");
		const running = downloadArtifact(source, entry);
		expect(cachedArtifactUri(entry)).toBeUndefined();
		await running;
		disk.downloadSize = 0;
		expect(cachedArtifactUri(entry)).toBeUndefined();
	});
});
