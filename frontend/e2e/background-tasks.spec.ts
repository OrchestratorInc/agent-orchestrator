import { expect, test } from "@playwright/test";
import type { components } from "../src/api/schema";
import { installFakeAgent } from "./support/fake-bridge";

// Renderer coverage uses native-shaped task fixtures. Adapter/service tests
// separately exercise the provider boundary; this does not launch Claude.
test("idle session exposes native background work and keeps process diagnostics secondary @T0", async ({ page }, testInfo) => {
	const projectId = "background-work";
	const sessionId = "background-shell-worker";
	let tasks: components["schemas"]["BackgroundTaskResponse"][] = [
		{ id: "shell-1", kind: "shell", description: "Start the settings dev server", state: "running" },
	];
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: "Background work",
		workers: [{ id: sessionId, provider: "claude-code", title: "Add project settings", mode: "chat", status: "idle", activity: "idle" }],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const path = new URL(route.request().url()).pathname;
		const reading = { sessionId, availability: "ready", controllerGeneration: "g1", observedAt: new Date().toISOString(), tasks };
		if (path === "/api/v1/sessions/background-tasks") {
			await route.fulfill({ json: { sessions: [reading] } });
		} else if (path === `/api/v1/sessions/${sessionId}/background-tasks`) {
			await route.fulfill({ json: reading });
		} else {
			// Unused APIs must fail rather than return a success body with the
			// wrong shape (which can crash unrelated inventory consumers).
			await route.fulfill({ status: 503, json: { error: "unavailable", message: "Not part of this renderer fixture" } });
		}
	});
	await page.goto(`/#/projects/${projectId}`);
	await page.getByRole("button", { name: "1 shell still running" }).click();
	const dialog = page.getByRole("dialog", { name: "Background tasks" });
	await expect(dialog).toBeVisible();
	await expect(dialog.getByRole("button", { name: /Start the settings dev server/ })).toBeVisible();
	await expect(dialog.getByText("OS process details · Diagnostics")).toBeVisible();
	await expect(dialog.locator("details")).not.toHaveAttribute("open");
	const screenshot = testInfo.outputPath("background-tasks.png");
	await page.screenshot({ path: screenshot });
	await testInfo.attach("background-tasks", { path: screenshot, contentType: "image/png" });
	await dialog.getByRole("button", { name: /Start the settings dev server/ }).click();
	tasks = [{ ...tasks[0], state: "completed", endedAt: new Date().toISOString() }];
	await dialog.getByRole("button", { name: "Refresh", exact: true }).click();
	await expect(dialog.getByText("No background tasks reported.")).toBeVisible();
	await expect(dialog.getByText("Recently finished")).toBeVisible();
	await expect(dialog.getByText("Completed", { exact: true })).toHaveCount(2);
	await page.keyboard.press("Escape");
	await expect(dialog).not.toBeVisible();
 await expect(page.getByRole("button", { name: "1 shell still running" })).not.toBeVisible();
 await expect(page.getByRole("button", { name: "Background tasks", exact: true })).not.toBeVisible();
});
