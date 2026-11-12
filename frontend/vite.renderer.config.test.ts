// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ARTIFACT_SNAPSHOT_SCHEME } from "./src/shared/artifact-snapshot";
import { contentSecurityPolicy } from "./vite.renderer.config";

describe("packaged renderer CSP", () => {
	it.each(["build", "serve"] as const)("allows media from the app, loopback preview and control-plane origins in %s", (mode) => {
		expect(contentSecurityPolicy(mode).split("; ").filter((directive) => directive.startsWith("media-src")))
			.toEqual(["media-src 'self' blob: http://127.0.0.1:* http://*.localhost:* https://staging-api.aoagents.dev https://api.aoagents.dev"]);
	});

	// A just-sent attachment's tile draws from the composer's object URL. Without
	// blob: the packaged app blocked it and the tile showed only its alt text.
	it.each(["img-src", "media-src", "connect-src"])("lets just-sent attachments load from object URLs in %s", (name) => {
		const directive = contentSecurityPolicy("build")
			.split("; ")
			.find((candidate) => candidate.startsWith(`${name} `));
		expect(directive?.split(" ")).toContain("blob:");
	});

	it("keeps images to the app, data:, blob:, loopback, https and the cached-preview scheme", () => {
		expect(contentSecurityPolicy("build").split("; ").filter((directive) => directive.startsWith("img-src")))
			.toEqual(["img-src 'self' data: blob: http://127.0.0.1:* http://*.localhost:* https: ao-artifact-preview:"]);
	});

	it("lets gallery cards load cached artifact previews, in the build and in serve mode", () => {
		// The dev app has no CSP at all, so only this test notices the scheme being dropped.
		for (const mode of ["build", "serve"] as const) {
			const imgSrc = contentSecurityPolicy(mode).split("; ").find((directive) => directive.startsWith("img-src "));
			expect(imgSrc?.split(" ")).toContain(`${ARTIFACT_SNAPSHOT_SCHEME}:`);
		}
	});

	it("frames agent renders from the loopback daemon and cloud artifacts from the control planes", () => {
		const frameSrc = contentSecurityPolicy("build")
			.split("; ")
			.filter((directive) => directive.startsWith("frame-src"));
		expect(frameSrc).toEqual([
			"frame-src 'self' http://127.0.0.1:* http://*.localhost:* https://staging-api.aoagents.dev https://api.aoagents.dev",
		]);
	});

	it("keeps the control-plane websocket origins", () => {
		const connectSrc = contentSecurityPolicy("build")
			.split("; ")
			.find((directive) => directive.startsWith("connect-src"));
		expect(connectSrc).toContain("wss://staging-api.aoagents.dev");
		expect(connectSrc).toContain("wss://api.aoagents.dev");
	});

	it("lets chat image copy and save fetch data: images", () => {
		const connectSrc = contentSecurityPolicy("build")
			.split("; ")
			.find((directive) => directive.startsWith("connect-src"));
		expect(connectSrc?.split(" ")).toContain("data:");
	});
});

describe("VITE_AO_TEAMS at build time", () => {
	afterEach(() => {
		vi.unstubAllEnvs();
		vi.resetModules();
	});

	const loadConfig = async () => (await import("./vite.renderer.config")).default as { define?: Record<string, string> };

	it.each([
		[undefined, ""],
		["", ""],
		["on", "on"],
		[" ON ", "on"],
		["off", "off"],
	])("bakes %j into the renderer as %j", async (raw, baked) => {
		vi.resetModules();
		if (raw === undefined) vi.stubEnv("VITE_AO_TEAMS", undefined as unknown as string);
		else vi.stubEnv("VITE_AO_TEAMS", raw);
		expect((await loadConfig()).define?.["import.meta.env.VITE_AO_TEAMS"]).toBe(JSON.stringify(baked));
	});

	it("stops the build on any other value", async () => {
		vi.resetModules();
		vi.stubEnv("VITE_AO_TEAMS", "true");
		await expect(loadConfig()).rejects.toThrow(/VITE_AO_TEAMS must be "on" or "off"/);
	});
});
