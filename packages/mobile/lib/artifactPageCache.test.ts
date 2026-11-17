import { describe, expect, it, vi } from "vitest";
import { cachedArtifactPage, MAX_CACHED_CHARS } from "./artifactPageCache";

describe("cachedArtifactPage", () => {
	it("reads a page once per version of the file", async () => {
		const build = vi.fn(async () => "page");
		await cachedArtifactPage("s|a.html", "t1", build);
		await cachedArtifactPage("s|a.html", "t1", build);
		expect(build).toHaveBeenCalledTimes(1);
		await cachedArtifactPage("s|a.html", "t2", build);
		expect(build).toHaveBeenCalledTimes(2);
	});

	it("reads a page with no known version afresh every time, so a rewrite shows", async () => {
		let copy = 0;
		const build = vi.fn(async () => `copy ${++copy}`);
		expect(await cachedArtifactPage("s|report.html", undefined, build)).toBe("copy 1");
		expect(await cachedArtifactPage("s|report.html", "", build)).toBe("copy 2");
	});

	it("does not keep a failed read", async () => {
		const failing = vi.fn(async () => { throw new Error("offline"); });
		await expect(cachedArtifactPage("s|b.html", "t1", failing)).rejects.toThrow("offline");
		await Promise.resolve();
		const build = vi.fn(async () => "page");
		expect(await cachedArtifactPage("s|b.html", "t1", build)).toBe("page");
	});

	it("holds the kept pages to a total size, dropping the oldest first", async () => {
		const half = "x".repeat(MAX_CACHED_CHARS / 2 + 1);
		const first = vi.fn(async () => half);
		await cachedArtifactPage("s|first.html", "t1", first);
		await cachedArtifactPage("s|second.html", "t1", async () => half);
		await Promise.resolve();
		// The two together are over the budget, so the first was let go.
		await cachedArtifactPage("s|first.html", "t1", first);
		expect(first).toHaveBeenCalledTimes(2);
	});

	it("does not keep a page larger than the whole budget", async () => {
		const huge = vi.fn(async () => "x".repeat(MAX_CACHED_CHARS + 1));
		await cachedArtifactPage("s|huge.html", "t1", huge);
		await Promise.resolve();
		await cachedArtifactPage("s|huge.html", "t1", huge);
		expect(huge).toHaveBeenCalledTimes(2);
	});
});
