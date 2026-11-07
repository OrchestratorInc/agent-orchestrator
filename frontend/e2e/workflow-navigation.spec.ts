import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// 删除 Shell 的保活宿主或工作台队列导航，会丢失草稿或无法完成原 Chat 队列编辑。
// 此用例只验证真实 Renderer；可信 Main/Daemon 身份另由 HTTP 集成和隔离 Electron 验证。
test("工作台往返保留草稿，复用会话队列编辑取消并刷新回执 @T0", async ({ page }) => {
	test.setTimeout(90000);
	const sessionId = "workflow-session";
	const now = new Date().toISOString();
	const workflow = { id: "wf", title: "M5 队列验收", goal: "保留原会话", state: "active", revision: 1 };
	const task = { id: "task", title: "实现任务", state: "running", agentHarness: "codex", model: "fixture", brief: "已有执行任务" };
	const attempt = { id: "attempt", taskId: task.id, sessionId, attemptNo: 1, state: "running" };
	let queued = false;
	let cancelled = false;
	let queueText = "";
	let revision = 0;
	let messageId = "";
	const writes: string[] = [];
	const operatorWrites: unknown[] = [];
	await installFakeAgent(page, { workers: [{ id: sessionId, title: "原会话", mode: "chat" }] });
	page.on("request", request => { if (request.method() !== "GET") writes.push(new URL(request.url()).pathname); });
	await page.route("**/__m5_operator", async route => {
		const input = route.request().postDataJSON();
		let body: unknown = {};
		if (input.write) {
			operatorWrites.push(input);
			expect(input.verb).toBe("intervention");
			expect(input.operationPath).toBe("/api/v1/workflows/wf/attempts/attempt/interventions");
			const request = JSON.parse(input.body);
			expect(request.mode).toBe("queue");
			queued = true;
			queueText = request.text;
			messageId = request.clientMessageId;
		} else {
			switch (input.verb) {
				case "coordination": body = { coordination: { tasks: [], reservations: [] }, quality: {} }; break;
				case "messages": body = { messages: [] }; break;
				case "task-requests": body = { requests: [] }; break;
				case "binding": body = { binding: { controllerGeneration: "generation" } }; break;
				case "observe": body = { live: true, observation: { sessionId, controllerGeneration: "generation" } }; break;
				case "attempt-capabilities": body = { capabilities: [{ observedSessionId: sessionId, generation: "generation", expiresAt: new Date(Date.now() + 120000).toISOString(), readiness: "ready", authState: "authorized", queue: "supported", steer: "unsupported", interrupt: "supported" }] }; break;
				case "intervention-receipts": body = { interventions: queued ? [{ id: "receipt", mode: "queue", state: cancelled ? "cancelled" : "queued", clientMessageId: messageId }] : [] }; break;
			}
		}
		await route.fulfill({ json: { status: 200, body } });
	});
	await page.route("**/api/v1/workflows**", async route => {
		const path = new URL(route.request().url()).pathname;
		const json = path.endsWith("/workflows") ? { workflows: [workflow] }
			: path.endsWith("/scheduler") ? { revision: 1, paused: false }
			: path.endsWith("/tasks/task") ? { task, attempts: [attempt] }
			: { workflow, tasks: [task], edges: [], repositories: [{ repoKey: "repo", projectId: "fake-proj" }], taskRepositories: [{ taskId: "task", repoKey: "repo" }] };
		await route.fulfill({ json });
	});
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async route => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation")) {
			await route.fulfill({ json: { conversationId: "conversation", sessionId, harness: "codex", mode: "chat", controller: "busy", capabilities: [], latestSequence: queued ? 2 : 1, oldestSequence: 1, hasMoreBefore: false,
				turns: [{ id: "running", state: "running", providerTurnId: "provider", requestedAt: now, startedAt: now }, ...(queued ? [{ id: "queued", state: cancelled ? "cancelled" : "queued", requestedAt: now }] : [])],
				messages: [{ id: "existing", turnId: "running", sequence: 1, role: "assistant", text: "原对话仍在", createdAt: now, streaming: false }, ...(queued ? [{ id: "message", turnId: "queued", sequence: 2, revision, role: "user", origin: "human", text: queueText, createdAt: now, streaming: false, content: [] }] : [])], activities: [], settings: {} } });
		} else if (path.endsWith("/queue/edit")) {
			const edit = route.request().postDataJSON();
			expect(edit.expectedRevision).toBe(revision);
			queueText = edit.text;
			revision++;
			await route.fulfill({ status: 204 });
		} else if (path.endsWith("/cancel")) {
			cancelled = true;
			await route.fulfill({ status: 204 });
		} else if (path.endsWith("/models")) await route.fulfill({ json: { models: [], selected: {} } });
		else if (path.endsWith("/skills")) await route.fulfill({ json: { skills: [] } });
		else if (path.endsWith("/workspace/files")) await route.fulfill({ json: { files: [], truncated: false } });
		else await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "fixture endpoint unavailable" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	const field = page.getByRole("combobox", { name: "Message the agent" });
	await expect(field).toBeVisible({ timeout: 30000 });
	await field.fill("保留未发送草稿");
	await page.evaluate(() => {
		window.ao!.operator = {
			authorizeWorkflow: async workflowId => ({ workflowId, expiresAt: new Date(Date.now() + 60000).toISOString() }),
			readWorkflow: async input => (await fetch("/__m5_operator", { method: "POST", body: JSON.stringify(input) })).json(),
			runVerb: async input => (await fetch("/__m5_operator", { method: "POST", body: JSON.stringify({ ...input, write: true }) })).json(),
		};
	});
	await page.getByRole("button", { name: "Workflows", exact: true }).click();
	await page.getByRole("button", { name: /M5 队列验收/ }).click();
	const node = page.getByRole("button", { name: /实现任务/ });
	await node.focus();
	await page.keyboard.press("Enter");
	await page.getByRole("textbox", { name: "Guidance for the current agent" }).fill("等待派发的消息");
	await page.getByRole("button", { name: "Queue message", exact: true }).click();
	await page.getByRole("button", { name: "Edit or cancel in session queue" }).click();
	await expect(field).toHaveText("保留未发送草稿");
	await expect(page.getByText("原对话仍在", { exact: true })).toBeVisible();
	await page.getByRole("button", { name: "Edit queued message" }).click();
	await expect(field).toHaveText("等待派发的消息");
	await field.fill("修改后的队列消息");
	await page.getByRole("button", { name: "Send message", exact: true }).click();
	await expect.poll(() => queueText).toBe("修改后的队列消息");
	await expect(field).toHaveText("保留未发送草稿");
	await page.getByRole("button", { name: "Delete queued message" }).click();
	await expect.poll(() => cancelled).toBe(true);
	await page.getByRole("button", { name: "Workflows", exact: true }).click();
	await expect(page.getByRole("status").filter({ hasText: "queue: cancelled" })).toBeVisible();
	await page.evaluate(() => window.__aoFakeAgent!.reconnectEventsWithReplayBacklog(1));
	await expect(page.getByRole("status").filter({ hasText: "queue: cancelled" })).toBeVisible();
	await page.getByRole("button", { name: "Return to session" }).click();
	await expect(field).toHaveText("保留未发送草稿");
	expect(operatorWrites).toHaveLength(1);
	expect(writes.filter(path => /\/spawn$|\/bind$/.test(path))).toEqual([]);
	await page.screenshot({ path: test.info().outputPath("workflow-session-return.png") });
});
