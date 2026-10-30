import { describe, expect, it, vi } from "vitest";
import { cachedArtifactPage } from "./artifactPageCache";

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
});
