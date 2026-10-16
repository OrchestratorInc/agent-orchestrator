// @vitest-environment node
import { describe, expect, it } from "vitest";
import { contentSecurityPolicy } from "./vite.renderer.config";

describe("packaged renderer CSP", () => {
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
