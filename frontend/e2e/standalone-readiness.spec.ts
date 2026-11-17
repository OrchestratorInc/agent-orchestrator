import { expect, test } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

test("renderer: standalone preserves a draft until Codex startup readiness recovers @T0", async ({ page }) => {
	test.setTimeout(120_000);
	await installFakeAgent(page, { projectId: "readiness", projectName: "Readiness", workers: [] });
	await page.addInitScript(() => {
		localStorage.setItem("ao.taskComposer.preferences.v1", JSON.stringify({ __standalone__: {
			lastAgent: "codex", agents: { codex: { model: "gpt-6", effort: "high" } },
		} }));
	});
	let ready = false;
	let creates = 0;
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.startsWith("/api/v1/agents/readiness")) {
			return route.fulfill({ json: { agents: [agentReadiness("codex", "Codex", { authentication: ready ? "authorized" : "unknown" }), agentReadiness("claude-code", "Claude Code")] } });
		}
		if (path === "/api/v1/settings") return route.fulfill({ json: { defaultSessionMode: "chat", chatHarnesses: ["codex", "claude-code"] } });
		if (path === "/api/v1/agents/chat/permission-modes") return route.fulfill({ json: { agents: [{ agent: "codex", modes: ["default"] }] } });
		if (path.endsWith("/models")) return route.fulfill({ json: { agent: "codex", selectionMode: "text", models: [{ id: "gpt-6", label: "GPT-6", isDefault: true }], allowCustom: true } });
		if (path === "/api/v1/agents/accounts") return route.fulfill({ json: { accounts: [] } });
		if (path === "/api/v1/sessions" && route.request().method() === "POST") {
			creates++;
			// A final refusal leaves the draft here, so the test can inspect it.
			return route.fulfill({ status: 409, json: { error: "conflict", code: "TEST_REFUSAL", message: "Test admission received", requestId: "readiness-test" } });
		}
		return route.fallback();
	});
	await page.goto("/#/new", { waitUntil: "domcontentloaded" });
	const prompt = page.getByRole("combobox", { name: "Message the agent" });
	await expect(prompt).toBeVisible({ timeout: 60_000 });
	await expect(page.getByRole("status").getByText("Checking Codex…", { exact: true })).toBeVisible();
	await prompt.fill("Keep this draft during startup");
	await page.locator('input[type="file"]').setInputFiles({
		name: "startup.png", mimeType: "image/png",
		buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=", "base64"),
	});
	await expect(page.getByLabel("Remove startup.png")).toBeVisible();
	await page.keyboard.press("Enter");
	expect(creates).toBe(0);
	await expect(prompt).toHaveText("Keep this draft during startup");
	ready = true;
	await expect(page.getByRole("status").getByText("Checking Codex…", { exact: true })).toBeHidden({ timeout: 10_000 });
	await expect(prompt).toHaveText("Keep this draft during startup");
	expect(creates).toBe(0);
	await expect(page.getByLabel("Remove startup.png")).toBeVisible();
	await prompt.press("Enter");
	await expect.poll(() => creates).toBe(1);
	await expect(page.getByText("Test admission received", { exact: true })).toBeVisible();
});
