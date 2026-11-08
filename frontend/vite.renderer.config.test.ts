// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { contentSecurityPolicy } from "./vite.renderer.config";

describe("packaged renderer CSP", () => {
	it.each(["build", "serve"] as const)("allows media from the app, loopback preview and control-plane origins in %s", (mode) => {
		expect(contentSecurityPolicy(mode).split("; ").filter((directive) => directive.startsWith("media-src")))
			.toEqual(["media-src 'self' http://127.0.0.1:* http://*.localhost:* https://staging-api.aoagents.dev https://api.aoagents.dev"]);
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
