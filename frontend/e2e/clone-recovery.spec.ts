import { expect, test, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const remoteUrl = "https://github.com/acme/example.git";
const checkout = "/clones/example";
const validation = {
	importKind: "project", isValid: true, blockingErrors: [], nextStep: "continue", childRepos: [],
	root: { repoPath: checkout, isRepo: true, hasCommit: true, hasOrigin: true, isEmptyFolder: false, needsGitInit: false, requiredActions: [], blockingErrors: [] },
};

async function openClone(page: Page) {
	await page.goto("/#/");
	await page.getByRole("button", { name: "New project", exact: true }).first().click();
	await page.getByRole("button", { name: "Clone a repo", exact: true }).click();
	await page.getByRole("textbox", { name: "Repository URL" }).fill("https://github.com/acme/example");
	await page.getByRole("textbox", { name: "Clone into", exact: true }).fill("/clones");
}

// Renderer integration with deterministic HTTP responses. Service tests cover
// Git, filesystem preservation, network retries, and cancellation cleanup.
test.beforeEach(async ({ page }) => {
	await installFakeAgent(page, { projectId: "existing", projectName: "existing", workers: [] });
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname.includes("/agents/readiness")) {
			await route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
		} else if (pathname === "/api/v1/agents/installers") {
			await route.fulfill({ json: { agents: [] } });
		} else if (pathname === "/api/v1/agents/auth-plans") {
			await route.fulfill({ json: { plans: [] } });
		} else if (pathname === "/api/v1/agents/install-jobs") {
			await route.fulfill({ json: { jobs: [] } });
		} else if (pathname === "/api/v1/projects/clone/check") {
			await route.fulfill({ json: { remoteUrl, path: checkout, exists: false, canUseExisting: false } });
		} else if (pathname === "/api/v1/imports/validate") {
			await route.fulfill({ json: validation });
		} else {
			await route.fulfill({ json: { status: "ok" } });
		}
	});
});

test("clone URL is normalized and failed registration keeps the checkout @T0", async ({ page }, testInfo) => {
	let clones = 0;
	await page.route("**/api/v1/projects/clone/prepare", async (route) => {
		clones++;
		expect(route.request().postDataJSON().remoteUrl).toBe(remoteUrl);
		await route.fulfill({ json: { path: checkout, remoteUrl, preparationId: "prepared" } });
	});
	await page.route("**/api/v1/projects", async (route) => {
		if (route.request().method() !== "POST") return route.fallback();
		await route.fulfill({ status: 400, json: { code: "INVALID_PROJECT_CONFIG", message: "Project settings need attention.", error: "invalid" } });
	});
	await openClone(page);
	await page.getByRole("button", { name: "Continue", exact: true }).click();
	await expect(page.getByText(`Repository cloned to ${checkout}. This step adds the project.`)).toBeVisible();
	await page.getByRole("button", { name: "Create and start", exact: true }).click();
	await expect(page.getByRole("button", { name: "Retry project setup", exact: true })).toBeVisible();
	await testInfo.attach("registration-recovery", { body: await page.screenshot(), contentType: "image/png" });
	await page.getByRole("button", { name: "Retry project setup", exact: true }).click();
	await expect(page.getByRole("button", { name: "Keep checkout and close", exact: true })).toBeVisible();
	await page.getByRole("button", { name: "Keep checkout and close", exact: true }).click();
	await expect(page.getByRole("dialog")).toHaveCount(0);
	expect(clones).toBe(1);
});

test("destination conflict offers reuse without cloning @T0", async ({ page }, testInfo) => {
	let clones = 0;
	await page.route("**/api/v1/projects/clone/check", (route) => route.fulfill({ json: { remoteUrl, path: checkout, exists: true, canUseExisting: true } }));
	await page.route("**/api/v1/projects/clone/prepare", async (route) => { clones++; await route.abort(); });
	await openClone(page);
	await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeDisabled();
	await expect(page.getByText(`A folder already exists at ${checkout}.`, { exact: false })).toBeVisible();
	await testInfo.attach("destination-conflict", { body: await page.screenshot(), contentType: "image/png" });
	await page.getByRole("button", { name: "Use existing checkout", exact: true }).click();
	await expect(page.getByRole("button", { name: "Create and start", exact: true })).toBeVisible();
	expect(clones).toBe(0);
});

test("reuse validation failure preserves inputs and offers retry @T0", async ({ page }) => {
	let validations = 0;
	let clones = 0;
	await page.route("**/api/v1/projects/clone/check", (route) => route.fulfill({ json: { remoteUrl, path: checkout, exists: true, canUseExisting: true } }));
	await page.route("**/api/v1/imports/validate", async (route) => {
		validations++;
		if (validations === 1) {
			await route.fulfill({ status: 503, json: { error: "unavailable", code: "SERVICE_UNAVAILABLE", message: "Validation is temporarily unavailable." } });
		} else {
			await route.fulfill({ json: validation });
		}
	});
	await page.route("**/api/v1/projects/clone/prepare", async (route) => { clones++; await route.abort(); });
	await openClone(page);
	await page.getByRole("button", { name: "Use existing checkout", exact: true }).click();
	const dialog = page.getByRole("dialog", { name: "Clone a Git repository" });
	await expect(dialog.getByText("Validation is temporarily unavailable.", { exact: true })).toBeVisible();
	await expect(dialog.getByRole("textbox", { name: "Repository URL" })).toHaveValue("https://github.com/acme/example");
	await expect(dialog.getByRole("textbox", { name: "Clone into", exact: true })).toHaveValue("/clones");
	await dialog.getByRole("button", { name: "Use existing checkout", exact: true }).click();
	await expect(page.getByRole("button", { name: "Create and start", exact: true })).toBeVisible();
	expect(validations).toBe(2);
	expect(clones).toBe(0);
});

test("cloning shows status and Cancel keeps inputs for retry @T0", async ({ page }, testInfo) => {
	let release: () => void = () => undefined;
	const pending = new Promise<void>((resolve) => { release = resolve; });
	await page.route("**/api/v1/projects/clone/prepare", async (route) => {
		await pending;
		await route.abort().catch(() => undefined);
	});
	await openClone(page);
	await page.getByRole("button", { name: "Continue", exact: true }).click();
	await expect(page.getByText("Cloning repository", { exact: false })).toBeVisible();
	await testInfo.attach("clone-progress", { body: await page.screenshot(), contentType: "image/png" });
	await page.getByRole("button", { name: "Cancel clone", exact: true }).click();
	release();
	await expect(page.getByText("Clone cancelled. You can retry or change the URL and destination.")).toBeVisible();
	await expect(page.getByRole("textbox", { name: "Repository URL" })).toHaveValue("https://github.com/acme/example");
	await expect(page.getByRole("textbox", { name: "Clone into", exact: true })).toHaveValue("/clones");
	await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeEnabled();
});
