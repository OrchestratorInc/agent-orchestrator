import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

const fixtureUrl = process.env.AO_GITHUB_FIXTURE_URL;

// Production screen, HTTP client, daemon and released gh binary. Only native
// desktop operations and GitHub's external OAuth/API server are controlled.
test.describe("GitHub onboarding through the real daemon @connected", () => {
	test.skip(!fixtureUrl, "Start TestGitHubDeviceBrowserFixture and set AO_GITHUB_FIXTURE_URL.");
	test.beforeEach(async ({ page, request }) => {
		expect((await request.post(`${fixtureUrl}/reset`)).ok()).toBe(true);
		const { baseUrl } = await (await request.get(fixtureUrl!)).json() as { baseUrl: string };
		await installFakeBridge(page, { daemonPort: Number(new URL(baseUrl).port) });
		await page.route("**/src/renderer/lib/preview-mode.ts*", (route) => route.fulfill({ contentType: "text/javascript", body: "export const usesPreviewWorkspaceData = false;" }));
		await page.goto("/#/onboarding");
		await page.getByRole("button", { name: "Continue", exact: true }).click();
		await expect(page.getByRole("heading", { name: "Connect a Git provider" })).toBeVisible();
	});

	test("reattaches on reload, copies the code, and continues only after verified credentials", async ({ page, request }) => {
		const next = page.getByRole("button", { name: "Continue", exact: true });
		await expect(next).toBeDisabled();
		await page.getByRole("button", { name: "Sign in with GitHub", exact: true }).click();
		await expect(page.getByTestId("github-device-code")).toHaveText("ABCD-1234");
		await page.getByRole("button", { name: "Copy code", exact: true }).click();
		await expect(page.getByRole("button", { name: "Copied", exact: true })).toBeVisible();
		const before = await (await request.get(fixtureUrl!)).json() as { starts: number };
		await page.reload();
		await page.getByRole("button", { name: "Continue", exact: true }).click();
		await expect(page.getByTestId("github-device-code")).toHaveText("ABCD-1234");
		expect((await (await request.get(fixtureUrl!)).json() as { starts: number }).starts).toBe(before.starts);
		await expect(next).toBeDisabled();
		expect((await request.post(`${fixtureUrl}/approve`)).ok()).toBe(true);
		await expect(next).toBeEnabled({ timeout: 15_000 });
		await next.click();
		await expect(page.getByRole("heading", { name: "Create your first project" })).toBeVisible();
	});

	for (const outcome of ["deny", "expire"] as const) {
		test(`can retry after ${outcome} and cancel an active attempt`, async ({ page, request }) => {
			await page.getByRole("button", { name: "Sign in with GitHub", exact: true }).click();
			await expect(page.getByTestId("github-device-code")).toBeVisible();
			await request.post(`${fixtureUrl}/${outcome}`);
			await expect(page.getByRole("alert")).toBeVisible({ timeout: 15_000 });
			await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeDisabled();
			await request.post(`${fixtureUrl}/reset`);
			await page.getByRole("button", { name: "Try again", exact: true }).click();
			await expect(page.getByTestId("github-device-code")).toBeVisible();
			await page.getByRole("button", { name: "Cancel", exact: true }).click();
			await expect(page.getByRole("button", { name: "Sign in with GitHub", exact: true })).toBeEnabled();
			await expect(page.getByTestId("github-device-code")).toHaveCount(0);
		});
	}

	test("cancels while waiting for the CLI to produce a code", async ({ page, request }) => {
		await request.post(`${fixtureUrl}/delay`);
		await page.getByRole("button", { name: "Sign in with GitHub", exact: true }).click();
		await page.getByRole("button", { name: "Cancel", exact: true }).click();
		await expect(page.getByRole("button", { name: "Sign in with GitHub", exact: true })).toBeEnabled();
		await expect(page.getByTestId("github-device-code")).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeDisabled();
	});

	test("requires an old CLI update before sign-in and rechecks after a manual update", async ({ page, request }) => {
		expect((await request.post(`${fixtureUrl}/old-cli`)).ok()).toBe(true);
		const update = page.getByRole("button", { name: "Update GitHub CLI", exact: true });
		await expect(update).toBeVisible();
		await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeDisabled();
		await expect(page.getByRole("button", { name: "Sign in with GitHub", exact: true })).toHaveCount(0);
		const response = page.waitForResponse((result) => result.request().method() === "POST" && result.url().endsWith("/system/install/gh"));
		await update.click();
		const job = await (await response).json() as { status: string };
		if (job.status === "unsupported") {
			// Linux deliberately hands root-only updates to the user. Simulate
			// completing that command, without updating any system packages.
			await expect(page.getByRole("button", { name: "Download GitHub CLI", exact: true })).toBeVisible();
			await expect(update).toBeVisible();
			expect((await request.post(`${fixtureUrl}/reset`)).ok()).toBe(true);
		}
		// macOS uses the fixture's fake Homebrew; it updates only the fixture
		// version banner, while login still runs the released gh binary.
		await expect(update).toHaveCount(0);
		const signIn = page.getByRole("button", { name: "Sign in with GitHub", exact: true });
		if (await signIn.isVisible()) await signIn.click();
		await expect(page.getByTestId("github-device-code")).toHaveText("ABCD-1234");
	});
});
