import path from "node:path";
import { _electron as electron, expect, test as base, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import { installFakeTerminalMux } from "./support/fake-terminal-mux";

const test = process.env.AO_E2E_ELECTRON
	? base.extend({
			page: async ({}, use) => {
				const app = await electron.launch({
					executablePath: path.join(process.cwd(), "node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"),
					args: [path.join(process.cwd(), "e2e/support/electron-shell.cjs")],
				});
				try {
					await use(await app.firstWindow());
				} finally {
					await app.close();
				}
			},
		})
	: base;
const origin = `http://127.0.0.1:${process.env.AO_E2E_PORT ?? 5173}`;
test.setTimeout(90_000);
const project = "grid-project";
const orchestrator = `${project}-orchestrator`;
const panel = (page: Page, id: string) => page.locator(`[data-testid="grid-panel"][data-session-id="${id}"]`);
const panelIds = (page: Page) =>
	page
		.getByTestId("grid-panel")
		.evaluateAll((elements) => elements.map((element) => element.getAttribute("data-session-id")));
async function terminalText(page: Page, id: string) {
	return panel(page, id)
		.locator(".terminal-xterm-host")
		.evaluate((element) => {
			const term = (
				element as HTMLElement & {
					__aoXtermForTest?: {
						buffer: {
							active: {
								length: number;
								getLine: (index: number) => { translateToString: (trim: boolean) => string } | undefined;
							};
						};
					};
				}
			).__aoXtermForTest;
			return term
				? Array.from(
						{ length: term.buffer.active.length },
						(_, index) => term.buffer.active.getLine(index)?.translateToString(true) ?? "",
					).join("\n")
				: "";
		});
}

async function setup(page: Page, count: number, chat = false) {
	const sent: Record<string, string[]> = {};
	const texts: Record<string, string> = {};
	const mutations: string[] = [];
	const workers = Array.from({ length: count - 1 }, (_, i) => ({
		id: `worker-${i + 1}`,
		title: `Task ${i + 1}`,
		mode: chat && i < 2 ? ("chat" as const) : ("tui" as const),
	}));
	await installFakeAgent(page, {
		projectId: project,
		projectName: "Live Grid isolated project",
		workers,
		platform: "darwin",
	});
	await installFakeTerminalMux(
		page,
		Object.fromEntries(
			[orchestrator, ...workers.map((w) => w.id)].map((id) => [`${id}/terminal_0`, `${id}: live\r\n`]),
		),
	);
	// Every REST call is intercepted: this suite cannot contact an installed daemon.
	await page.route(/\/api\/v1\//, async (route) => {
		const request = route.request();
		const url = new URL(request.url());
		const id = url.pathname.match(/\/sessions\/([^/]+)/)?.[1] ?? "";
		if (request.method() !== "GET") mutations.push(url.pathname);
		if (url.pathname.endsWith("/conversation") && request.method() === "GET")
			return route.fulfill({
				json: {
					conversationId: `conversation-${id}`,
					sessionId: id,
					harness: "codex",
					mode: "chat",
					controller: "ready",
					latestSequence: 1,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [],
					activities: [],
					settings: { model: "test-model" },
					messages: [
						{
							kind: "message",
							id: `message-${id}`,
							sequence: 1,
							revision: texts[id] ? 2 : 1,
							role: "assistant",
							origin: "agent",
							text: texts[id] ?? `Live Chat ${id}`,
							streaming: false,
							createdAt: "2026-10-11T00:00:00Z",
						},
					],
				},
			});
		if (url.pathname.endsWith("/conversation/messages")) {
			(sent[id] ??= []).push(request.postDataJSON().text);
			return route.fulfill({ status: 202, json: { turnId: `turn-${id}`, state: "queued" } });
		}
		if (url.pathname.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
		if (url.pathname.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
		if (url.pathname.endsWith("/workspace/files")) return route.fulfill({ json: { files: [], truncated: false } });
		if (url.pathname.endsWith("/interface-transition"))
			return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "isolated test endpoint" } } });
	});
	await page.goto(`${origin}/#/projects/${project}/grid`);
	await expect(page.getByTestId("grid-panel")).toHaveCount(count, { timeout: 20_000 });
	return { sent, texts, mutations };
}

for (const count of [1, 2, 4, 6, 9, 12]) {
	test(`readable live panels and pinned orchestrator with ${count} sessions @T0`, async ({ page }) => {
		await setup(page, count);
		await expect(page.getByTestId("grid-panel").first()).toHaveAttribute("data-session-id", orchestrator);
		await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().writers)).toBe(count);
		await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().sockets)).toBe(1);
		for (const size of [
			{ width: 1440, height: 900 },
			{ width: 900, height: 620 },
		]) {
			await page.setViewportSize(size);
			await expect
				.poll(async () =>
					page.getByTestId("grid-panel").evaluateAll((nodes) =>
						nodes.every((node) => {
							const r = node.getBoundingClientRect();
							return r.width >= 359 && r.height >= 279;
						}),
					),
				)
				.toBe(true);
			const box = await panel(page, orchestrator).boundingBox();
			const layout = await page.getByTestId("live-grid-layout").boundingBox();
			expect(box?.x).toBe(layout?.x);
			expect(box?.y).toBe(layout?.y);
		}
		if (process.env.AO_GRID_EVIDENCE_DIR)
			await page.screenshot({ path: `${process.env.AO_GRID_EVIDENCE_DIR}/grid-${count}.png` });
	});
}

test("workers arrive, complete and reconnect without agent launches or order changes @T0", async ({ page }) => {
	const { mutations } = await setup(page, 2);
	await page.evaluate(() => window.__aoFakeAgent!.createWorker({ id: "worker-new", title: "New task", mode: "tui" }));
	await expect(page.getByTestId("grid-panel")).toHaveCount(3);
	await expect.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().writers)).toBe(3);
	expect(await panelIds(page)).toEqual([orchestrator, "worker-1", "worker-new"]);
	await page.evaluate(() => {
		window.__aoFakeTerminalMux!.emit("worker-1/terminal_0", "worker-one-stream\r\n");
		window.__aoFakeTerminalMux!.emit("worker-new/terminal_0", "worker-new-stream\r\n");
		window.__aoFakeAgent!.setStatus("worker-1", "nosignal", "idle");
	});
	await expect(page.getByTestId("grid-panel")).toHaveCount(3);
	await expect.poll(() => terminalText(page, "worker-1")).toContain("worker-one-stream");
	await expect.poll(() => terminalText(page, "worker-new")).toContain("worker-new-stream");
	await panel(page, "worker-new").locator(".xterm-helper-textarea").focus();
	await page.keyboard.type("worker-only-input");
	await expect
		.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().inputs["worker-new/terminal_0"]?.join("")))
		.toBe("worker-only-input");
	expect(await page.evaluate(() => window.__aoFakeTerminalMux!.stats().inputs["worker-1/terminal_0"])).toBeUndefined();
	await page.evaluate(() => window.__aoFakeTerminalMux!.disconnect("worker-new/terminal_0"));
	await expect
		.poll(() => page.evaluate(() => window.__aoFakeTerminalMux!.stats().opens["worker-new/terminal_0"]))
		.toBe(2);
	await expect(page.getByTestId("grid-panel")).toHaveCount(3);
	await page.evaluate(() => window.__aoFakeAgent!.setStatus("worker-1", "terminated", "exited"));
	await expect(page.getByTestId("grid-panel")).toHaveCount(2);
	await page.getByRole("checkbox", { name: "Show completed" }).check();
	await expect(page.getByTestId("grid-panel")).toHaveCount(3);
	expect(await panelIds(page)).toEqual([orchestrator, "worker-1", "worker-new"]);
	expect(mutations).toEqual([]);
});

test("split sizes, worker order and maximize survive Kanban switching @T0", async ({ page }) => {
	await page.setViewportSize({ width: 1500, height: 1000 });
	await setup(page, 4);
	const separator = page.getByRole("separator", { name: "Resize columns" }).first();
	await expect(separator).toBeVisible();
	const before = (await panel(page, orchestrator).boundingBox())!.width;
	const handle = (await separator.boundingBox())!;
	await page.mouse.move(handle.x + 3, handle.y + 40);
	await page.mouse.down();
	await page.mouse.move(handle.x + 65, handle.y + 40, { steps: 5 });
	await page.mouse.up();
	await expect.poll(async () => (await panel(page, orchestrator).boundingBox())!.width).toBeGreaterThan(before + 40);
	const resized = (await panel(page, orchestrator).boundingBox())!.width;
	await panel(page, "worker-3").getByRole("button", { name: "Move worker earlier" }).click();
	expect(await panelIds(page)).toEqual([orchestrator, "worker-1", "worker-3", "worker-2"]);
	await panel(page, "worker-1").getByTestId("grid-panel-header").dblclick();
	await expect(panel(page, "worker-2")).toBeHidden();
	await panel(page, "worker-1").getByRole("button", { name: "Restore grid" }).click();
	await page.getByRole("button", { name: "Open Kanban", exact: true }).last().click();
	await expect(page.getByTestId("live-grid")).toHaveCount(0);
	await page.getByRole("button", { name: "Open Grid", exact: true }).last().click();
	await expect(page.getByTestId("grid-panel")).toHaveCount(4);
	expect(await panelIds(page)).toEqual([orchestrator, "worker-1", "worker-3", "worker-2"]);
	expect((await panel(page, orchestrator).boundingBox())!.width).toBeCloseTo(resized, 0);
	expect(await page.evaluate(() => window.__aoFakeTerminalMux!.stats().opens)).toEqual(
		Object.fromEntries([orchestrator, "worker-1", "worker-2", "worker-3"].map((id) => [`${id}/terminal_0`, 1])),
	);
	await page.reload();
	await expect(page.getByTestId("grid-panel")).toHaveCount(4);
	expect(await panelIds(page)).toEqual([orchestrator, "worker-1", "worker-3", "worker-2"]);
	expect((await panel(page, orchestrator).boundingBox())!.width).toBeCloseTo(resized, 0);
});

test("Chat and TUI stream concurrently, keep drafts and send to their owning session @T0", async ({ page }) => {
	await page.setViewportSize({ width: 1500, height: 1000 });
	const { texts, sent, mutations } = await setup(page, 4, true);
	const a = panel(page, "worker-1");
	const b = panel(page, "worker-2");
	await expect(a.getByRole("log", { name: "Conversation" })).toContainText("Live Chat worker-1");
	await expect(b.getByRole("log", { name: "Conversation" })).toContainText("Live Chat worker-2");
	const composer = a.getByRole("combobox", { name: "Message the agent" });
	await composer.fill("Keep this draft");
	texts["worker-1"] = "Concurrent Chat stream one";
	texts["worker-2"] = "Concurrent Chat stream two";
	await page.evaluate(() => {
		window.__aoFakeAgent!.conversationChanged("worker-1");
		window.__aoFakeAgent!.conversationChanged("worker-2");
		window.__aoFakeTerminalMux!.emit("worker-3/terminal_0", "Concurrent TUI stream\r\n");
	});
	await expect(a.getByRole("log", { name: "Conversation" })).toContainText(texts["worker-1"]);
	await expect(b.getByRole("log", { name: "Conversation" })).toContainText(texts["worker-2"]);
	await expect.poll(() => terminalText(page, "worker-3")).toContain("Concurrent TUI stream");
	await page.getByRole("button", { name: "Open Kanban", exact: true }).last().click();
	await page.getByRole("button", { name: "Open Grid", exact: true }).last().click();
	await expect(composer).toHaveText("Keep this draft");
	expect(mutations).toEqual([]);
	await page.evaluate(({ project, id }) => { window.location.hash = `/projects/${project}/sessions/${id}`; }, { project, id: "worker-1" });
	const singleComposer = page.getByRole("combobox", { name: "Message the agent" });
	await expect(singleComposer).toHaveText("Keep this draft");
	await page.getByRole("button", { name: "Open Grid", exact: true }).last().click();
	await expect(composer).toHaveText("Keep this draft");
	await composer.press("Enter");
	await expect.poll(() => sent["worker-1"]).toEqual(["Keep this draft"]);
	expect(sent["worker-2"]).toBeUndefined();
	// The ordinary session view renews its existing Chat visibility lease.
	expect(mutations.filter((url) => !url.endsWith("/conversation/messages") && !url.endsWith("/chat-view"))).toEqual([]);
});
