import { expect, test, type Locator, type Page } from "@playwright/test";
import type { components } from "../src/api/schema";
import { installFakeAgent } from "./support/fake-bridge";

type Task = components["schemas"]["BackgroundTaskResponse"];

// Renderer coverage uses native-shaped task fixtures. Adapter/service tests
// separately exercise the provider boundary; this does not launch Claude.
const projectId = "background-work";
const sessionId = "background-shell-worker";
const now = "2026-11-13T10:00:00Z";
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
const queued = [
	"commit when the suite is green",
	"skip the screenshot step for now",
	"keep the dev server up, I'm testing in the window",
	"then rerun the composer tests with --repeat-each=5",
	"also check the ru catalog has the new keys",
	"and update the changelog",
];

type Fixture = { tasks: Task[]; output?: (taskId: string) => { output: string; truncated?: boolean } | undefined };

async function openWorker(page: Page, fixture: Fixture, stops: string[] = []) {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: "Background work",
		workers: [{ id: sessionId, provider: "claude-code", title: "Add project settings", mode: "chat", status: "working", activity: "active" }],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const path = new URL(route.request().url()).pathname;
		const reading = { sessionId, availability: "ready", controllerGeneration: "g1", observedAt: new Date().toISOString(), tasks: fixture.tasks };
		if (path === "/api/v1/sessions/background-tasks") {
			await route.fulfill({ json: { sessions: [reading] } });
		} else if (path === `/api/v1/sessions/${sessionId}/background-tasks`) {
			await route.fulfill({ json: reading });
		} else if (path.startsWith(`/api/v1/sessions/${sessionId}/background-tasks/`) && path.endsWith("/output")) {
			const taskId = decodeURIComponent(path.split("/").at(-2) ?? "");
			const output = fixture.output?.(taskId);
			await route.fulfill({ json: { sessionId, taskId, availability: output ? "ready" : "unavailable", output: output?.output ?? "", truncated: output?.truncated } });
		} else if (path.startsWith(`/api/v1/sessions/${sessionId}/background-tasks/`) && path.endsWith("/stop")) {
			const taskId = decodeURIComponent(path.split("/").at(-2) ?? "");
			fixture.tasks = fixture.tasks.map((task) => task.id === taskId
				? { ...task, state: "stopped", endedAt: new Date().toISOString(), stoppable: false }
				: task);
			stops.push(taskId);
			await route.fulfill({ status: 204 });
		} else if (path === `/api/v1/sessions/${sessionId}/conversation`) {
			await route.fulfill({
				json: {
					conversationId: "conversation-background",
					sessionId,
					harness: "claude-code",
					mode: "chat",
					controller: "busy",
					capabilities: ["steer"],
					latestSequence: queued.length + 1,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [
						{ id: "running-1", state: "running", providerTurnId: "provider-1", requestedAt: now, startedAt: now },
						...queued.map((_, index) => ({ id: `queued-${index}`, state: "queued", requestedAt: now })),
					],
					messages: queued.map((text, index) => ({
						kind: "message", id: `message-${index}`, turnId: `queued-${index}`, sequence: index + 2, revision: 0,
						role: "user", origin: "human", text, streaming: false, createdAt: now,
					})),
					activities: [],
					settings: {},
				},
			});
		} else if (path.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
		} else if (path.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
		} else if (path.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
		} else {
			// Unused APIs must fail rather than return a success body with the
			// wrong shape (which can crash unrelated inventory consumers).
			await route.fulfill({ status: 503, json: { error: "unavailable", message: "Not part of this renderer fixture" } });
		}
	});
}

async function box(locator: Locator) {
	const rect = await locator.boundingBox();
	if (!rect) throw new Error("not laid out");
	return { top: rect.y, bottom: rect.y + rect.height, left: rect.x, right: rect.x + rect.width };
}

/** The queue, the tab and the composer stack top to bottom and never cover each other. */
async function expectStacked(page: Page, tab: Locator) {
	const queue = await box(page.getByTestId("queued-composer-dock").locator(".queue-dock"));
	const strip = await box(tab);
	const composer = await box(page.locator("form.cursor-chat-composer"));
	expect(queue.bottom).toBeLessThanOrEqual(strip.top + 0.5);
	expect(strip.bottom).toBeLessThanOrEqual(composer.top + 0.5);
	// The tab is inset from the composer's sides, like the tray beneath New Task.
	expect(strip.left).toBeGreaterThan(composer.left);
	expect(strip.right).toBeLessThan(composer.right);
}

/** Sets the queue open or closed; a long queue may already start open. */
async function setQueue(page: Page, open: boolean) {
	const toggle = page.getByTestId("queued-message-toggle");
	await expect(toggle).toBeVisible();
	if ((await toggle.getAttribute("aria-expanded")) !== String(open)) await toggle.click();
	await expect(toggle).toHaveAttribute("aria-expanded", String(open));
}

test("the board shows background work as plain text @T0", async ({ page }) => {
	await openWorker(page, { tasks: [{ id: "shell-1", kind: "shell", description: "Start the settings dev server", state: "running", startedAt: ago(3) }] });
	await page.goto(`/#/projects/${projectId}`);
	await expect(page.getByTestId("board")).toBeVisible({ timeout: 15_000 });
	await expect(page.getByText("1 shell still running")).toBeVisible();
	await expect(page.getByRole("button", { name: /shell still running/ })).toHaveCount(0);
});

test("one task rides the composer with a stop, beside a queue it never covers @T0", async ({ page }, testInfo) => {
	const fixture: { tasks: Task[] } = { tasks: [{ id: "shell-1", kind: "shell", description: "Start the settings dev server", state: "running", startedAt: ago(12), stoppable: true }] };
	const stops: string[] = [];
	await openWorker(page, fixture, stops);
	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	const tab = page.getByRole("region", { name: "Background tasks" });
	await expect(tab).toContainText("Start the settings dev server");
	await expect(tab).toContainText("12m");
	await setQueue(page, false);
	await expectStacked(page, tab);

	// Opening the queue floats it upward: the tab and composer stay put below it.
	const tabBefore = await box(tab);
	const composerBefore = await box(page.locator("form.cursor-chat-composer"));
	await setQueue(page, true);
	await expectStacked(page, tab);
	expect(await box(tab)).toEqual(tabBefore);
	expect(await box(page.locator("form.cursor-chat-composer"))).toEqual(composerBefore);
	const screenshot = testInfo.outputPath("background-tasks-one.png");
	await page.screenshot({ path: screenshot });
	await testInfo.attach("one task beside an open queue", { path: screenshot, contentType: "image/png" });

	await tab.getByRole("button", { name: "Stop Start the settings dev server" }).click();
	await expect.poll(() => stops).toEqual(["shell-1"]);
	// The stop rereads at once; a stopped task needs no attention and leaves.
	await expect(tab).toHaveCount(0);
});

test("several tasks fold to a count and open in place without moving into the queue @T0", async ({ page }, testInfo) => {
	const tasks: Task[] = [
		{ id: "dev", kind: "shell", description: "Start the renderer dev server", state: "running", startedAt: ago(12), stoppable: true },
		{ id: "watch", kind: "shell", description: "Watch the composer tests", state: "running", startedAt: ago(9), stoppable: true },
		{ id: "lint", kind: "shell", description: "Run the Go linter", state: "failed", startedAt: ago(5), endedAt: ago(3) },
		{ id: "audit", kind: "agent", description: "Audit the catalogs", state: "running", startedAt: ago(1) },
	];
	await openWorker(page, { tasks });
	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	const tab = page.getByRole("region", { name: "Background tasks" });
	const toggle = tab.getByRole("button", { name: /2 running/ });
	await expect(toggle).toHaveAttribute("aria-expanded", "false");
	await expect(tab).toContainText("1 failed");
	// Agents belong to Subagents, and a folded tab offers no stop.
	await expect(tab).not.toContainText("Audit the catalogs");
	await expect(tab.getByRole("button", { name: /^Stop/ })).toHaveCount(0);
	await setQueue(page, false);
	await expectStacked(page, tab);

	const composerBefore = await box(page.locator("form.cursor-chat-composer"));
	await toggle.click();
	const rows = tab.getByRole("listitem");
	await expect(rows).toHaveCount(3);
	await expect(rows.nth(2)).toContainText("Run the Go linter");
	await expect(rows.nth(2).getByRole("button", { name: /^Stop/ })).toHaveCount(0);
	await expect(tab.getByRole("button", { name: /^Stop/ })).toHaveCount(2);
	// Grown in the flow: the queue's footprint moved up, the composer did not.
	await expectStacked(page, tab);
	expect(await box(page.locator("form.cursor-chat-composer"))).toEqual(composerBefore);
	await setQueue(page, true);
	await expectStacked(page, tab);
	const screenshot = testInfo.outputPath("background-tasks-open.png");
	await page.screenshot({ path: screenshot });
	await testInfo.attach("open task list beside an open queue", { path: screenshot, contentType: "image/png" });
});

test("a task opens in its own side panel tab that follows its output @T0", async ({ page }, testInfo) => {
	let ticks = 60;
	const lines = () => Array.from({ length: ticks }, (_, i) => `tick-${i + 1}`).join("\n");
	const fixture: Fixture = {
		tasks: [
			{ id: "dev", kind: "shell", description: "npm run dev -- --port 3000", command: "npm run dev -- --port 3000", state: "running", startedAt: ago(12), stoppable: true },
			{ id: "tests", kind: "shell", description: "npm test -- --watch", command: "npm test -- --watch", state: "running", startedAt: ago(9), stoppable: true },
		],
		output: (taskId) => (taskId === "dev" ? { output: lines() } : { output: "" }),
	};
	await openWorker(page, fixture);
	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	const strip = page.getByRole("region", { name: "Background tasks" });
	await strip.getByRole("button", { name: /2 running/ }).click();
	await strip.getByRole("button", { name: /^Dev server on port 3000/ }).click();

	const devTab = page.getByRole("tab", { name: "Dev server on port 3000" });
	await expect(devTab).toHaveAttribute("aria-selected", "true");
	const panel = page.getByRole("region", { name: "Dev server on port 3000" });
	await expect(panel.getByText("npm run dev -- --port 3000")).toBeVisible();
	const output = panel.getByRole("region", { name: "Output" });
	const atBottom = () => output.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight <= 8);
	await expect(output).toContainText("tick-60");
	await expect.poll(atBottom).toBe(true);

	// New lines arrive in view.
	ticks = 120;
	await expect(output).toContainText("tick-120", { timeout: 5_000 });
	await expect.poll(atBottom).toBe(true);
	const following = testInfo.outputPath("background-task-tab.png");
	await page.screenshot({ path: following });
	await testInfo.attach("task tab following its output", { path: following, contentType: "image/png" });

	// Reading back holds the text still while lines keep arriving.
	await output.evaluate((element) => { element.scrollTop = 0; });
	const jump = panel.getByRole("button", { name: "Jump to latest" });
	await expect(jump).toBeVisible();
	ticks = 150;
	await page.waitForTimeout(2_500);
	await expect(output).not.toContainText("tick-150");
	expect(await output.evaluate((element) => element.scrollTop)).toBe(0);
	const reading = testInfo.outputPath("background-task-tab-held.png");
	await page.screenshot({ path: reading });
	await testInfo.attach("task tab held while reading back", { path: reading, contentType: "image/png" });
	await jump.click();
	await expect(output).toContainText("tick-150");
	await expect.poll(atBottom).toBe(true);
	await expect(jump).toHaveCount(0);

	// A second task opens a second tab; closing tabs shows the neighbour, then leaves task tabs.
	await strip.getByRole("button", { name: /^Running tests/ }).click();
	await expect(page.getByRole("tab", { name: "Running tests" })).toHaveAttribute("aria-selected", "true");
	await expect(page.getByRole("region", { name: "Running tests" }).getByText("No output yet")).toBeVisible();
	await page.getByRole("button", { name: "Close Running tests" }).click();
	await expect(devTab).toHaveAttribute("aria-selected", "true");
	await page.getByRole("button", { name: "Close Dev server on port 3000" }).click();
	await expect(page.getByRole("tab", { name: /Dev server/ })).toHaveCount(0);
	await expect(page.getByRole("region", { name: "Dev server on port 3000" })).toHaveCount(0);
});
