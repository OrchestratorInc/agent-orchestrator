import { beforeEach, describe, expect, it, vi } from "vitest";

const disk = vi.hoisted(() => ({ files: new Set<string>(), deleted: [] as string[] }));

vi.mock("expo-file-system", () => {
	class Directory {
		uri: string;
		constructor(...parts: Array<string | { uri: string }>) {
			this.uri = parts.map((part) => typeof part === "string" ? part : part.uri).join("/");
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
		delete() {
			disk.files.delete(this.uri);
			disk.deleted.push(this.uri);
		}
	}
	return { Directory, File, Paths: { cache: { uri: "cache" } } };
});

import { discardArtifact } from "./artifactDownload";
import { artifactCacheEntry } from "./artifacts";

beforeEach(() => {
	disk.files.clear();
	disk.deleted.length = 0;
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
