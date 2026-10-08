import { describe, expect, it } from "vitest";
import { agentPageRequestAllowed, isAgentPageUrl } from "./render-frame-network";

const daemon = 3042;
const resolveTo = (address: string, family = 4) => async () => [{ address, family }];

describe("isAgentPageUrl", () => {
	it("knows the frames that hold agent pages", () => {
		expect(isAgentPageUrl("http://127.0.0.1:3042/api/v1/sessions/p-1/renders/r1")).toBe(true);
		expect(isAgentPageUrl("http://localhost:3042/api/v1/sessions/p-1/artifact-files/q3/report.html")).toBe(true);
		expect(isAgentPageUrl("http://ao-inline-artifact.x.localhost:3042/q3/report.html")).toBe(true);
	});

	it("leaves the app and everything else alone", () => {
		expect(isAgentPageUrl("http://localhost:5173/")).toBe(false);
		expect(isAgentPageUrl("app://renderer/index.html")).toBe(false);
		expect(isAgentPageUrl("http://127.0.0.1:3042/api/v1/sessions")).toBe(false);
		expect(isAgentPageUrl("http://ao-preview-artifact.x.localhost:3042/q3/report.html")).toBe(false);
		expect(isAgentPageUrl("not a url")).toBe(false);
	});
});

describe("agentPageRequestAllowed", () => {
	it("lets a page load public addresses, data and blob URLs", async () => {
		expect(await agentPageRequestAllowed("https://cdn.example/chart.js", daemon, resolveTo("93.184.216.34"))).toBe(true);
		expect(await agentPageRequestAllowed("https://93.184.216.34/x.png", daemon)).toBe(true);
		expect(await agentPageRequestAllowed("data:image/png;base64,iVBORw0KGgo=", daemon)).toBe(true);
		expect(await agentPageRequestAllowed("blob:http://ao-inline-artifact.x.localhost:3042/1", daemon)).toBe(true);
	});

	it("lets a page reach the daemon, its own files included", async () => {
		expect(await agentPageRequestAllowed("http://127.0.0.1:3042/api/v1/sessions/p-1/artifact-files/q3/chart.png", daemon)).toBe(true);
		expect(await agentPageRequestAllowed("http://ao-inline-artifact.x.localhost:3042/q3/data.json", daemon)).toBe(true);
	});

	it("refuses this computer's other ports and the local network", async () => {
		expect(await agentPageRequestAllowed("http://127.0.0.1:8888/api", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("http://localhost:5173/", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("http://ao-preview.x.localhost:8080/", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("http://192.168.1.1/cgi-bin/admin", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("http://[::1]:22/", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("ws://10.0.0.5:9000/", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("http://169.254.169.254/latest/meta-data/", daemon)).toBe(false);
	});

	it("refuses a name that resolves to the local network, even on the daemon's port", async () => {
		expect(await agentPageRequestAllowed("http://router.lan/", daemon, resolveTo("192.168.1.1"))).toBe(false);
		expect(await agentPageRequestAllowed("http://nas.example:3042/", daemon, resolveTo("10.0.0.2"))).toBe(false);
	});

	it("refuses loopback when the daemon's port is not known, and other schemes", async () => {
		expect(await agentPageRequestAllowed("http://127.0.0.1:3042/api/v1/sessions/p-1/renders/r1", undefined)).toBe(false);
		expect(await agentPageRequestAllowed("file:///etc/passwd", daemon)).toBe(false);
		expect(await agentPageRequestAllowed("not a url", daemon)).toBe(false);
	});
});
