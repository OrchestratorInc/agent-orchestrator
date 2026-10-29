import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Renderer/protocol smoke with a real opaque iframe. Backend authorization and
// persistence have separate SQLite-backed service tests.
test("MCP apps preserve selections, confirm writes and export server resources @P0", async ({ page }) => {
	const projectId = "mcp-apps", sessionId = "mcp-apps-1", conversationId = "c", activityId = "a";
	const now = "2026-10-29T12:00:00Z";
	const html = readFileSync("e2e/mcp-apps/app.html", "utf8");
	const policy = "sandbox allow-scripts allow-forms; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'";
	const requests: Array<{ method: string; params: Record<string, unknown>; approvalToken?: string }> = [];
	await installFakeAgent(page, { projectId, projectName: "MCP Apps", workers: [{ id: sessionId, title: "Explore revenue", provider: "codex", mode: "chat", status: "idle", activity: "idle" }] });
	await page.route("http://127.0.0.1:8080/api/v1/**", async route => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/mcp-apps/a/document")) return route.fulfill({ contentType: "text/html", headers: { "Content-Security-Policy": policy }, body: html });
		if (path.endsWith("/mcp-apps/a/actions")) {
			const request = route.request().postDataJSON(); requests.push(request);
			if (request.method === "tools/call" && !request.approvalToken) return route.fulfill({ json: { approvalToken: "once", approvalTitle: "Save analytics selection" } });
			if (request.method === "resources/read") return route.fulfill({ json: { result: { contents: [{ uri: request.params.uri, mimeType: "text/csv", text: "region,total\nEurope,3800000\n" }] } } });
			return route.fulfill({ json: { result: { content: [] } } });
		}
		if (path.endsWith("/mcp-apps/a")) return route.fulfill({ json: { server: "analytics", tool: "revenue", arguments: { year: 2026 }, result: { content: [] }, ready: true, live: true, policy } });
		if (path.endsWith("/conversation")) return route.fulfill({ json: {
			conversationId, sessionId, harness: "codex", mode: "chat", controller: "ready", latestSequence: 2, oldestSequence: 1, hasMoreBefore: false, settings: {},
			turns: [{ id: "turn", state: "completed", requestedAt: now, startedAt: now, completedAt: now }],
			messages: [{ kind: "message", id: "user", turnId: "turn", sequence: 1, revision: 0, role: "user", origin: "human", text: "Show quarterly revenue", streaming: false, createdAt: now }],
			activities: [{ kind: "activity", id: activityId, turnId: "turn", sequence: 2, revision: 0, activityKind: "mcp_tool", status: "completed", summary: "Called analytics/revenue", createdAt: now, detail: { server: "analytics", toolName: "revenue", mcpApp: { conversationId, resourceUri: "ui://revenue" }, arguments: { year: 2026 }, result: { content: [] } } }],
		} });
		return route.fulfill({ json: { status: "ok", agents: [], models: [], skills: [] } });
	});
	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	const app = page.frameLocator('iframe[title="analytics interactive app"]');
	await app.getByRole("button", { name: "Europe", exact: true }).click();
	await expect.poll(() => requests.some(r => r.method === "ui/update-model-context" && (r.params.structuredContent as { region: string }).region === "Europe")).toBe(true);
	await page.getByRole("button", { name: "Expand app", exact: true }).click();
	await expect(app.getByRole("button", { name: "Europe", exact: true })).toHaveAttribute("aria-pressed", "true");
	await page.getByRole("button", { name: "Collapse app", exact: true }).click();
	await expect(app.getByRole("button", { name: "Europe", exact: true })).toHaveAttribute("aria-pressed", "true");
	await app.getByRole("button", { name: "Save selection", exact: true }).click();
	await expect(page.getByRole("alertdialog", { name: "Save analytics selection" })).toBeVisible();
	expect(requests.filter(r => r.method === "tools/call")).toHaveLength(1);
	await page.getByRole("button", { name: "Allow once", exact: true }).click();
	await expect.poll(() => requests.filter(r => r.method === "tools/call" && r.approvalToken === "once").length).toBe(1);
	await app.getByRole("button", { name: "Export data", exact: true }).click();
	await expect(page.getByRole("alertdialog", { name: "Save files from this app?" })).toBeVisible();
	const download = page.waitForEvent("download");
	await page.getByRole("button", { name: "Allow once", exact: true }).click();
	expect((await download).suggestedFilename()).toBe("revenue.csv");
	expect(requests.some(r => r.method === "resources/read" && r.params.uri === "mcp://analytics/revenue.csv")).toBe(true);
});
