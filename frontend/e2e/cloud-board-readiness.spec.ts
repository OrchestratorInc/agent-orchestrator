import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import type { CloudAccount } from "../src/shared/cloud-account";

const port = Number(process.env.AO_E2E_PORT ?? 5173);

for (const pending of ["projects", "sessions"] as const) {
	test(`renderer: first sign-in with pending Cloud ${pending} keeps navigation usable @P0`, async ({ page, context }, testInfo) => {
		await installFakeAgent(page, { daemonPort: port, projectName: "Local project", workers: [] });
		await page.route("**/api/v1/settings*", (route) => route.fulfill({ json: {
			defaultSessionMode: "tui", chatHarnesses: [], localEnabled: true,
			cloudOffering: true, cloudEnabled: true, cloudControlPlaneUrl: "https://cloud.test",
		} }));
		await page.addInitScript(({ pending }) => {
			const account: CloudAccount = {
				authProvider: "workos", storedAt: "2026-10-21T00:00:00Z",
				user: { id: "test-user", email: "test@example.com", displayName: "Test user" },
			};
			const bridge = window.ao!;
			const listeners = new Set<(session: CloudAccount | null) => void>();
			let signedIn = false;
			bridge.cloud.getSession = async () => signedIn ? account : null;
			bridge.cloud.onSessionChanged = (listener) => {
				listeners.add(listener);
				return () => { listeners.delete(listener); };
			};
			const offering = { kind: "b2c", providers: ["freestyle"], defaultProvider: "freestyle", billingMode: "off", features: [] };
			const org = { id: "test-org", displayName: "Test org", slug: "test-org", role: "owner", offering };
			const project = {
				id: "cloud-project", orgId: org.id, displayName: "Pending Cloud project",
				repositoryUrl: "https://github.com/example/test", defaultBranch: "main", config: {},
				createdAt: "2026-10-21T00:00:00Z", updatedAt: "2026-10-21T00:00:00Z",
			};
			let pendingRequests = 0;
			bridge.cloudCp.request = async ({ path }) => {
				if (path.includes(`/orgs/${org.id}/${pending}?`)) {
					pendingRequests += 1;
					return new Promise(() => undefined);
				}
				const body = path.endsWith("/me") ? { user: account.user, organizations: [org], offering }
					: path.includes("/projects?") ? { items: [project] } : { items: [] };
				return { status: 200, headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
			};
			Object.assign(window, { __cloudBoardTest: {
				signIn: () => { signedIn = true; for (const listener of listeners) listener(account); },
				pendingRequests: () => pendingRequests,
			} });
		}, { pending });
		await page.goto("/#/projects/fake-proj");
		await expect(page.getByTestId("board")).toHaveAttribute("data-project-id", "fake-proj");
		await expect(page.getByTestId("daemon-startup-loader")).toHaveCount(0);
		await page.evaluate(() => (window as unknown as { __cloudBoardTest: { signIn(): void } }).__cloudBoardTest.signIn());
		await expect.poll(() => page.evaluate(() => (
			window as unknown as { __cloudBoardTest: { pendingRequests(): number } }
		).__cloudBoardTest.pendingRequests())).toBeGreaterThan(0);
		if (pending === "sessions") {
			await page.getByText("Pending Cloud project", { exact: true }).click();
		} else {
			await context.setOffline(true);
			await page.evaluate(() => { window.location.hash = "/projects/cloud-project"; });
		}
		await expect(page.getByTestId("board")).toHaveAttribute("data-project-id", "cloud-project");
		await expect(page.getByTestId("daemon-startup-loader")).toHaveCount(0);
		await expect(page.getByTestId("board-column")).toHaveCount(0);
		await expect(page.getByTestId("project-board-empty")).toHaveCount(0);
		await page.screenshot({ path: testInfo.outputPath(`cloud-${pending}-pending.png`) });
		await page.getByText("Local project", { exact: true }).click();
		await expect(page.getByTestId("board")).toHaveAttribute("data-project-id", "fake-proj");
	});
}
