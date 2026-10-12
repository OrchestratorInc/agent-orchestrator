import path from "node:path";
import { _electron, expect, test as base, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import { installFakeTerminalMux } from "./support/fake-terminal-mux";

const test = process.env.AO_E2E_ELECTRON ? base.extend({
	page: async ({}, use) => {
		const app = await _electron.launch({
			executablePath: path.join(process.cwd(), "node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"),
			args: [path.join(process.cwd(), "e2e/support/electron-shell.cjs")],
		});
		try { await use(await app.firstWindow()); } finally { await app.close(); }
	},
}) : base;
test.setTimeout(90_000);
const origin = `http://127.0.0.1:${process.env.AO_E2E_PORT ?? 5173}`;
const hostId = "remote-grid-fixture";
const projectId = "grid-project";
const orch = `${projectId}-orchestrator`;
const proxy = "http://127.0.0.1:45318";
const panel = (page: Page, id: string) => page.locator(`[data-testid="grid-panel"][data-session-id="${id}"]`);

async function setup(page: Page, count: number, mixed = false) {
	await installFakeAgent(page, { projectId, projectName: "Local project must not appear", platform: "darwin" });
	await page.addInitScript(({ hostId, proxy }) => {
		localStorage.setItem("ao.developerMode", "true");
		localStorage.setItem("ao.remoteHosts", "true");
		let current = window.ao;
		const patch = (bridge: typeof window.ao) => {
			if (!bridge) return bridge;
			bridge.cloud.getSession = async () => ({ authProvider: "workos", user: { id: "test-account", email: "test@example.invalid", displayName: "Test" }, storedAt: "2026-10-11T00:00:00Z" });
			bridge.remotes.list = async () => [{ hostId, label: "Remote Grid fixture", url: "http://192.0.2.1:9999" }];
			bridge.remotes.connect = async (url) => ({ hostId, label: "Remote Grid fixture", url, base: proxy });
			return bridge;
		};
		// Playwright does not promise ordering between separate init scripts.
		Object.defineProperty(window, "ao", { configurable: true, get: () => current, set: (bridge) => { current = patch(bridge); } });
		current = patch(current);
	}, { hostId, proxy });
	const session = (id: string, index: number) => ({
		id, projectId, kind: index === 0 ? "orchestrator" : "worker", harness: "codex",
		mode: mixed && index > 0 && index < 3 ? "chat" : "tui",
		displayName: `Remote task ${index}`, status: "working", kanbanColumn: "working",
		activity: { state: "active" }, isTerminated: false, terminalHandleId: `${id}/terminal_0`,
		terminalGeneration: "fixture-generation", createdAt: `2026-10-11T00:00:${String(index).padStart(2, "0")}Z`, updatedAt: "2026-10-11T00:00:00Z", prs: [],
	});
	const sessions = Array.from({ length: count }, (_, index) => session(index === 0 ? orch : `worker-${index}`, index));
	const mutations: string[] = [];
	const sent: Record<string, string[]> = {};
	const texts: Record<string, string> = {};
	let offline = false;
	await installFakeTerminalMux(page, Object.fromEntries(sessions.map((s) => [`${s.id}/terminal_0`, `${s.id}: remote live\r\n`])));
	// Intercept every daemon request, including local endpoints. No installed or
	// commercial daemon can be contacted or mutated by this test.
	await page.route(/\/api\/v1\//, async (route) => {
		const request = route.request(); const url = new URL(request.url());
		if (request.method() !== "GET") mutations.push(`${url.origin}${url.pathname}`);
		if (url.origin !== proxy) return route.fulfill({ status: 404, json: { error: { message: "No local fixture API" } } });
		if (offline) return route.fulfill({ status: 503, json: { error: { message: "Fixture disconnected" } } });
		const id = decodeURIComponent(url.pathname.match(/\/sessions\/([^/]+)/)?.[1] ?? "");
		if (url.pathname.endsWith("/projects")) return route.fulfill({ json: { projects: [{ id: projectId, name: "Remote Grid project", kind: "single_repo", path: "/isolated/remote-fixture" }] } });
		if (url.pathname.endsWith("/sessions")) return route.fulfill({ json: { sessions } });
		if (url.pathname.endsWith("/conversation")) return route.fulfill({ json: {
			conversationId: `conversation-${id}`, sessionId: id, harness: "codex", mode: "chat", controller: "ready", latestSequence: 1, oldestSequence: 1, hasMoreBefore: false,
			turns: [], activities: [], settings: { model: "remote-test-model" }, messages: [{ kind: "message", id: `message-${id}`, sequence: 1, revision: texts[id] ? 2 : 1, role: "assistant", origin: "agent", text: texts[id] ?? `Remote Chat ${id}`, streaming: false, createdAt: "2026-10-11T00:00:00Z" }],
		} });
		if (url.pathname.endsWith("/conversation/messages")) { (sent[id] ??= []).push(request.postDataJSON().text); return route.fulfill({ status: 202, json: { turnId: `turn-${id}`, state: "queued" } }); }
		if (url.pathname.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
		if (url.pathname.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
		if (url.pathname.endsWith("/workspace/files")) return route.fulfill({ json: { files: [], truncated: false } });
		return route.fulfill({ status: 404, json: { error: { message: "Isolated test endpoint" } } });
	});
	await page.goto(`${origin}/#/host/${hostId}/project/${projectId}/grid`);
	await expect(page.getByTestId("grid-panel")).toHaveCount(count, { timeout: 30_000 });
	await expect(page.getByTestId("grid-panel").first()).toHaveAttribute("data-session-id", orch);
	return { sessions, session, mutations, sent, texts, setOffline: (value: boolean) => { offline = value; } };
}

for (const count of [1, 2, 4, 6, 9, 12]) test(`remote TUI Grid shares one connection with ${count} sessions @T0`, async ({ page }) => {
	const { mutations } = await setup(page, count);
	await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().writers)).toBe(count);
	await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().sockets)).toBe(1);
	await expect(page.getByTestId("live-grid")).toHaveAttribute("data-host-id", hostId);
	await expect(page.getByTestId("live-grid")).toContainText("Remote Grid fixture");
	expect(mutations).toEqual([]);
});

test("remote Chat/TUI input, streaming, CDC and Kanban retain their owning sessions @T0", async ({ page }) => {
	const { sessions, session, mutations, sent, texts } = await setup(page, 4, true);
	const a = panel(page, "worker-1"), b = panel(page, "worker-2");
	await expect(a.getByRole("log", { name: "Conversation" })).toContainText("Remote Chat worker-1");
	await expect(b.getByRole("log", { name: "Conversation" })).toContainText("Remote Chat worker-2");
	const composer = a.getByRole("combobox", { name: "Message the agent" });
	await composer.fill("remote draft");
	texts["worker-1"] = "Concurrent remote stream one"; texts["worker-2"] = "Concurrent remote stream two";
	await page.evaluate(() => { window.__aoFakeAgent!.conversationChanged("worker-1"); window.__aoFakeAgent!.conversationChanged("worker-2"); window.__aoFakeTerminalMux!.emit("worker-3/terminal_0", "remote stream\r\n"); });
	await expect(a.getByRole("log", { name: "Conversation" })).toContainText(texts["worker-1"]);
	await expect(b.getByRole("log", { name: "Conversation" })).toContainText(texts["worker-2"]);
	await panel(page, "worker-3").locator(".xterm-helper-textarea").focus();
	await page.keyboard.type("remote-tui-input");
	await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().inputs["worker-3/terminal_0"]?.join(""))).toBe("remote-tui-input");
	sessions.push(session("worker-new", 4));
	await page.evaluate(() => window.__aoFakeAgent!.createWorker({ id: "worker-new", title: "CDC", mode: "tui" }));
	await expect(page.getByTestId("grid-panel")).toHaveCount(5);
	sessions[4].isTerminated = true; sessions[4].status = "terminated";
	await page.evaluate(() => window.__aoFakeAgent!.setStatus("worker-new", "terminated", "exited"));
	await expect(page.getByTestId("grid-panel")).toHaveCount(4);
	await page.getByRole("button", { name: "Open Kanban", exact: true }).last().click();
	await expect(page).toHaveURL(new RegExp(`/host/${hostId}/project/${projectId}$`));
	await page.getByRole("button", { name: "Open Grid", exact: true }).last().click();
	await expect(composer).toHaveText("remote draft");
	await composer.press("Enter");
	await expect.poll(() => sent["worker-1"]).toEqual(["remote draft"]);
	expect(sent["worker-2"]).toBeUndefined();
	expect(mutations).toEqual([`${proxy}/api/v1/sessions/worker-1/conversation/messages`]);
	expect(await page.evaluate(() => window.__aoFakeTerminalMux!.stats().opens["worker-3/terminal_0"])).toBe(1);
});

test("remote disconnect preserves panels, drafts and xterm identity and reconnects without launches @T0", async ({ page }) => {
	const { mutations, setOffline } = await setup(page, 4, true);
	const composer = panel(page, "worker-1").getByRole("combobox", { name: "Message the agent" });
	await composer.fill("keep offline draft");
	await panel(page, "worker-3").locator(".terminal-xterm-host").evaluate((node) => { node.setAttribute("data-retained-proof", "yes"); });
	setOffline(true);
	await page.evaluate(async (hostId) => { const modulePath = "/src/renderer/lib/host-clients.ts"; const hosts = await import(modulePath); await hosts.disconnectHost(hostId); }, hostId);
	await expect(page.getByTestId("live-grid").getByRole("alert")).toContainText("Host is offline");
	await expect(page.getByTestId("grid-panel")).toHaveCount(4);
	await expect(composer).toHaveText("keep offline draft");
	setOffline(false);
	await page.evaluate(async (hostId) => { const modulePath = "/src/renderer/lib/host-clients.ts"; const hosts = await import(modulePath); await hosts.connectHost("http://192.0.2.1:9999", hostId); }, hostId);
	await expect(page.getByTestId("live-grid").getByRole("alert")).toHaveCount(0);
	await expect(panel(page, "worker-3").locator(".terminal-xterm-host")).toHaveAttribute("data-retained-proof", "yes");
	await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().writers)).toBe(2);
	await page.evaluate(() => window.__aoFakeTerminalMux!.disconnect("worker-3/terminal_0"));
	await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().sockets)).toBe(1);
	await expect(composer).toHaveText("keep offline draft");
	expect(mutations).toEqual([]);
});
