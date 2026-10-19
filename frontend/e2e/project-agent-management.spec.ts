import { expect, test } from "@playwright/test";
import type { AoBridge } from "../src/preload";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

for (const role of ["Worker agent", "Orchestrator agent"] as const) {
	test(`renderer: ${role} management releases the clone backdrop and preserves setup @T0`, async ({ page }) => {
		await installFakeAgent(page);
		const cleanupRequests: unknown[] = [];
		await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
			const pathname = new URL(route.request().url()).pathname;
			if (pathname.startsWith("/api/v1/agents/readiness")) {
				await route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
			} else if (pathname === "/api/v1/agents/installers") {
				await route.fulfill({ json: { agents: [] } });
			} else if (pathname === "/api/v1/agents/install-jobs") {
				await route.fulfill({ json: { jobs: [] } });
			} else if (pathname === "/api/v1/agents/auth-plans") {
				await route.fulfill({ json: { plans: [] } });
			} else if (pathname === "/api/v1/projects/clone/prepare") {
				await route.fulfill({ json: {
					path: "/repos/example", remoteUrl: "https://example.com/example.git", preparationId: "prep-management",
				} });
			} else if (pathname === "/api/v1/projects/clone/cleanup") {
				cleanupRequests.push(route.request().postDataJSON());
				await route.fulfill({ status: 204 });
			} else if (pathname === "/api/v1/imports/validate") {
				await route.fulfill({ json: {
					importKind: "project", isValid: true, blockingErrors: [], nextStep: "continue",
					root: { repoPath: "/repos/example", isRepo: true, hasCommit: true, hasOrigin: true, isEmptyFolder: false, needsGitInit: false, requiredActions: [], blockingErrors: [] },
				} });
			} else {
				await route.fulfill({ json: {} });
			}
		});
		await page.goto("/#/");
		await page.evaluate(() => {
			const bridge = (window as unknown as { ao: AoBridge }).ao;
			bridge.app.chooseDirectory = async () => "/repos";
		});
		await page.getByRole("button", { name: "Clone a repo", exact: true }).click();
		await page.getByRole("textbox", { name: "Repository URL" }).fill("https://example.com/example.git");
		await page.getByRole("textbox", { name: "Clone into" }).fill("/repos");
		await page.getByRole("button", { name: "Continue", exact: true }).click();
		await expect(page.getByRole("combobox", { name: role, exact: true })).toHaveText(/Codex/);
		await page.getByRole("combobox", { name: role, exact: true }).click();
		await page.getByRole("option", { name: /Manage agents/ }).click();
		await expect(page.getByTestId("settings-page")).toBeVisible();
		const search = page.getByRole("textbox", { name: "Search harnesses", exact: true });
		// A real pointer click catches a scrim that a programmatic fill could bypass.
		await search.click({ timeout: 5_000 });
		await search.fill("Codex");
		await expect(search).toHaveValue("Codex");
		await expect(page.locator(".dialog-overlay")).toHaveCount(0);
		await page.getByRole("button", { name: "Back", exact: true }).click();
		await expect(page.getByRole("combobox", { name: "Worker agent", exact: true })).toHaveText(/Codex/);
		await expect(page.getByRole("combobox", { name: "Orchestrator agent", exact: true })).toHaveText(/Codex/);
		await expect(page.getByRole("dialog", { name: "Set up project" })).toBeVisible();
		expect(cleanupRequests).toEqual([]);
		await page.getByRole("button", { name: "Back to clone details" }).click();
		await expect.poll(() => cleanupRequests).toEqual([{ path: "/repos/example", preparationId: "prep-management" }]);
	});
}
