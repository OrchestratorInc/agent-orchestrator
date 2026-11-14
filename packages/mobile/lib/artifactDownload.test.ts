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

import { ArtifactTooLargeError, discardArtifact, downloadArtifact } from "./artifactDownload";
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
