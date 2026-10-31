import path from "node:path";
import {
	_electron as electron,
	expect,
	test as base,
	type Page,
} from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import type { ChatExcerptSource } from "../src/shared/chat-excerpt";

const test = process.env.AO_E2E_ELECTRON
	? base.extend({
			page: async ({}, use) => {
				const app = await electron.launch({
					executablePath: require("electron") as string,
					args: [path.join(__dirname, "support/electron-shell.cjs")],
				});
				const page = await app.firstWindow();
				await page.bringToFront();
				await use(page);
				await app.close();
			},
		})
	: base;
const origin = `http://127.0.0.1:${process.env.AO_E2E_PORT ?? 5173}`;
const project = "mixed-selection",
	session = "mixed-worker";
const prose = "The Code result follows. Copy and Diagram are ordinary words.";
const mermaid = "flowchart LR\n A[Start] --> B[End]";

async function open(page: Page, mode: "main" | "existing" | "new" | "mermaid") {
	await installFakeAgent(page, {
		projectId: project,
		projectName: project,
		workers: [
			{
				id: session,
				provider: "codex",
				title: "Mixed selection",
				mode: "chat",
			},
		],
	});
	const sends: Array<{
		text: string;
		excerpts?: ChatExcerptSource[];
		references?: ChatExcerptSource[];
	}> = [];
	const creations: unknown[] = [];
	const makeSide = (id: string) => ({
		id,
		sessionId: session,
		mainConversationId: "mixed-main",
		generation: "g",
		label: "Side Chat",
		state: "ready",
		contextMode: "native",
		hasWork: false,
		createdAt: "2026-11-01T00:00:00Z",
		updatedAt: "2026-11-01T00:00:00Z",
	});
	const sides = mode === "existing" ? [makeSide("B")] : [];
	const drafts = new Map<string, string>();
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		if (route.request().headers().accept?.includes("text/event-stream"))
			return route.fulfill({
				status: 200,
				headers: { "content-type": "text/event-stream" },
				body: "retry: 600000\n: connected\n\n",
			});
		const url = new URL(route.request().url()),
			p = url.pathname,
			method = route.request().method();
		if (
			p === "/api/v1/agents/readiness" ||
			p === "/api/v1/agents/readiness/ensure"
		)
			return route.fulfill({
				json: { agents: [agentReadiness("codex", "Codex")] },
			});
		if (
			p === "/api/v1/agents/installers" ||
			p === "/api/v1/agents/install-jobs"
		)
			return route.fulfill({ json: { agents: [], jobs: [] } });
		if (p === "/api/v1/agents/auth-plans")
			return route.fulfill({ json: { plans: [] } });
		if (p === `/api/v1/projects/${project}`)
			return route.fulfill({
				json: {
					status: "ok",
					project: {
						id: project,
						agent: "codex",
						config: { worker: { agent: "codex" } },
					},
				},
			});
		if (p === `/api/v1/sessions/${session}/conversation`)
			return route.fulfill({
				json: {
					conversationId: "mixed-main",
					sessionId: session,
					harness: "codex",
					mode: "chat",
					controller: "ready",
					capabilities: ["resume"],
					oldestSequence: 1,
					latestSequence: 3,
					hasMoreBefore: false,
					settings: {},
					turns: [
						{
							id: "mixed-turn",
							sequence: 1,
							state: "completed",
							createdAt: "2026-11-01T00:00:00Z",
							completedAt: "2026-11-01T00:00:03Z",
						},
					],
					messages: [
						{
							id: "user",
							turnId: "mixed-turn",
							sequence: 1,
							revision: 1,
							role: "user",
							origin: "human",
							text: "Explain",
							streaming: false,
							createdAt: "2026-11-01T00:00:00Z",
						},
						{
							id: "reply",
							turnId: "mixed-turn",
							sequence: 3,
							revision: 2,
							role: "assistant",
							origin: "provider",
							text:
								mode === "mermaid"
									? `Before.\n\n\`\`\`powershell\nWrite-Output 'Code'\n\`\`\`\n\n\`\`\`mermaid\n${mermaid}\n\`\`\`\n\n${prose}`
									: prose,
							streaming: false,
							createdAt: "2026-11-01T00:00:03Z",
						},
					],
					activities:
						mode === "mermaid"
							? []
							: [
									{
										id: "command",
										turnId: "mixed-turn",
										sequence: 2,
										revision: 4,
										activityKind: "command",
										status: "completed",
										summary: "Ran powershell",
										detail: {
											command: "Write-Output 'Copy'",
											output: "Copied ✓\nDiagram",
										},
										createdAt: "2026-11-01T00:00:01Z",
									},
								],
				},
			});
		if (p.endsWith("/conversation/models"))
			return route.fulfill({ json: { models: [], selected: {} } });
		if (p.endsWith("/conversation/skills"))
			return route.fulfill({ json: { skills: [] } });
		if (p.endsWith("/config-options"))
			return route.fulfill({ json: { options: [] } });
		if (p.endsWith("/conversation/messages") && method === "POST") {
			sends.push(route.request().postDataJSON());
			return route.fulfill({ json: { turnId: "accepted" } });
		}
		if (p.endsWith("/side-chats")) {
			if (method === "POST") {
				creations.push(route.request().postDataJSON());
				const side = makeSide("A");
				sides.push(side);
				return route.fulfill({ json: { side } });
			}
			return route.fulfill({ json: { sides } });
		}
		const sideId = /\/side-chats\/([^/]+)/.exec(p)?.[1];
		if (sideId) {
			if (p.endsWith("/draft")) {
				if (method === "PUT")
					drafts.set(sideId, route.request().postDataJSON().contentJson);
				return route.fulfill({
					json: { contentJson: drafts.get(sideId) ?? "" },
				});
			}
			if (p.endsWith("/messages") && method === "POST") {
				sends.push(route.request().postDataJSON());
				return route.fulfill({
					json: { turn: { id: "accepted", state: "completed" } },
				});
			}
			return route.fulfill({
				json: {
					snapshot: {
						side: makeSide(sideId),
						turns: [],
						messages: [],
						activities: [],
						hasMore: false,
					},
				},
			});
		}
		return route.fulfill({ json: { status: "ok" } });
	});
	await page.goto(`${origin}/#/projects/${project}/sessions/${session}`, {
		waitUntil: "domcontentloaded",
		timeout: 90000,
	});
	await expect(
		page.getByRole("combobox", { name: "Message the agent" }),
	).toBeVisible({ timeout: 30000 });
	await page.evaluate(async () => {
		const modulePath = "/src/renderer/stores/ui-store.ts";
		const { useUiStore } = await import(modulePath);
		useUiStore.setState({ developerMode: true, sideChats: true });
	});
	return { sends, creations };
}

async function selectMixed(page: Page, reverse = false) {
	const worked = page.getByRole("button", { name: /Worked for/ });
	if (await worked.count()) await worked.click();
	const card = page.locator('[data-chat-activity-id="command"]');
	await expect(card).toBeVisible();
	const disclosure = card.locator('button[aria-expanded="false"]');
	if (await disclosure.count()) await disclosure.click();
	const command = card.locator('[data-chat-command-field="command"]'),
		answer = page.locator(
			'[data-chat-message-id="reply"] [data-chat-message-body]',
		);
	await expect(command).toBeVisible();
	await expect(answer).toBeVisible();
	let previous = "";
	await expect
		.poll(
			async () => {
				const current = JSON.stringify([
					await command.boundingBox(),
					await answer.boundingBox(),
				]);
				const stable = current === previous;
				previous = current;
				return stable;
			},
			{ intervals: [250, 250], timeout: 5000 },
		)
		.toBe(true);
	const a = (await command.boundingBox())!,
		b = (await answer.boundingBox())!;
	const points = [
		{ x: a.x + 2, y: a.y + a.height / 2 },
		{ x: b.x + Math.min(200, b.width - 3), y: b.y + b.height / 2 },
	];
	if (reverse) points.reverse();
	await page.mouse.move(points[0].x, points[0].y);
	await page.mouse.down();
	await page.mouse.move(points[1].x, points[1].y, { steps: 14 });
	await page.mouse.up();
	expect(
		await page.evaluate(() => window.getSelection()!.toString()),
	).toContain("The Code result");
	await expect(
		page.getByRole("button", { name: "Add to chat", exact: true }),
	).toBeVisible();
}

for (const destination of ["main", "existing", "new"] as const)
	test(`mixed selection delivers one annotation to ${destination} @T0`, async ({
		page,
	}) => {
		const { sends, creations } = await open(page, destination);
		await selectMixed(page, destination === "existing");
		await page.screenshot({
			path: test
				.info()
				.outputPath(`${destination}-selection-before-attachment.png`),
		});
		await page
			.getByRole("button", {
				name: destination === "main" ? "Add to chat" : "Add to side chat",
				exact: true,
			})
			.click();
		await expect(
			page.getByRole("button", { name: "1 annotation", exact: true }),
		).toBeVisible();
		const editor = page.getByRole("combobox", {
			name: destination === "main" ? "Message the agent" : "Side chat question",
		});
		await editor.fill("Explain this");
		await editor.press("Enter");
		await expect.poll(() => sends.length).toBe(1);
		const refs = sends[0].excerpts ?? sends[0].references!;
		expect(refs).toHaveLength(1);
		expect(refs[0].segments?.map((part) => part.kind)).toEqual([
			"command",
			"command",
			"message",
		]);
		expect(refs[0].text).toContain("Write-Output 'Copy'");
		expect(refs[0].text).toContain("Copied ✓\nDiagram");
		expect(creations).toHaveLength(destination === "new" ? 1 : 0);
		await page.screenshot({
			path: test.info().outputPath(`${destination}-mixed-selection.png`),
		});
	});

test("Mermaid controls never enter an excerpt; diagram content requests Code view @T0", async ({
	page,
}) => {
	const { sends } = await open(page, "mermaid");
	const body = page.locator(
		'[data-chat-message-id="reply"] [data-chat-message-body]',
	);
	await expect(body.locator('[data-testid="mermaid-diagram"]')).toBeVisible();
	await body.evaluate((element) => {
		window.getSelection()!.selectAllChildren(element);
	});
	await body.dispatchEvent("mouseup");
	await expect(
		page.getByText(
			"Switch the Mermaid diagram to Code view, then select its source.",
		),
	).toBeVisible();
	expect(await page.evaluate(() => window.getSelection()!.toString())).not.toBe(
		"",
	);
	await body.getByRole("button", { name: "Code", exact: true }).click();
	await body.evaluate((element) => {
		window.getSelection()!.selectAllChildren(element);
	});
	await body.dispatchEvent("mouseup");
	await expect(
		page.getByRole("button", { name: "Add to chat", exact: true }),
	).toBeVisible();
	await page.screenshot({
		path: test.info().outputPath("mermaid-code-selection.png"),
	});
	await page.getByRole("button", { name: "Add to chat", exact: true }).click();
	const editor = page.getByRole("combobox", { name: "Message the agent" });
	await editor.fill("Explain this");
	await editor.press("Enter");
	await expect.poll(() => sends.length).toBe(1);
	const text = sends[0].excerpts![0].text;
	expect(text).toContain(mermaid);
	expect(text).toContain(prose);
	expect(text).not.toContain("powershell");
	expect(text).not.toContain("mermaid");
});
