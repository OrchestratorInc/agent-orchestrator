import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import type { CloudAccount } from "../src/shared/cloud-account";

const port = Number(process.env.AO_E2E_PORT ?? 5173);

for (const resolution of ["found", "missing", "error"] as const) {
	test(`renderer: a loaded list cannot resolve an unverified Cloud deep link (${resolution}) @P0`, async ({ page }, testInfo) => {
		test.setTimeout(90_000);
		await installFakeAgent(page, { daemonPort: port, projectName: "Local project", workers: [], platform: "MacIntel" });
		await page.route("**/api/v1/settings*", (route) => route.fulfill({ json: {
			defaultSessionMode: "tui", chatHarnesses: [], localEnabled: true,
			cloudOffering: true, cloudEnabled: true, cloudControlPlaneUrl: "https://cloud.test",
		} }));
		await page.addInitScript(() => {
			Object.defineProperty(navigator, "userAgentData", { value: { platform: "macOS" }, configurable: true });
			const account: CloudAccount = { authProvider: "workos", storedAt: "2026-10-24T00:00:00Z", user: { id: "test-user", email: "test@example.com", displayName: "Test user" } };
			const bridge = window.ao!;
			const listeners = new Set<(session: CloudAccount | null) => void>();
			let signedIn = true;
			bridge.cloud.getSession = async () => signedIn ? account : null;
			bridge.cloud.onSessionChanged = (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
			const offering = { kind: "b2c", providers: ["freestyle"], defaultProvider: "freestyle", billingMode: "off", features: [] };
			const org = { id: "test-org", displayName: "Test org", slug: "test-org", role: "owner", offering };
			const project = { id: "cloud-101", orgId: org.id, displayName: "Deep-linked Cloud project", repositoryUrl: "https://github.com/example/test", defaultBranch: "main", config: {}, createdAt: "2026-10-21T00:00:00Z", updatedAt: "2026-10-21T00:00:00Z" };
			let response: "found" | "missing" | "error" | undefined;
			let release!: () => void;
			const pending = new Promise<void>((resolve) => { release = resolve; });
			bridge.cloudCp.request = async ({ path }) => {
				if (path.endsWith("/projects/cloud-101")) {
					await pending;
					return { status: response === "found" ? 200 : response === "missing" ? 404 : 503, headers: { "content-type": "application/json" }, body: JSON.stringify(response === "found" ? { project } : { error: { code: "unavailable", message: "Fixture project unavailable" } }) };
				}
				const body = path.endsWith("/me") ? { user: account.user, organizations: [org], offering }
					: path.includes("/projects?") ? { items: Array.from({ length: 100 }, (_, index) => ({ ...project, id: `other-${index}`, displayName: `Other ${index}` })) }
						: { items: [] };
				return { status: 200, headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
			};
			Object.assign(window, { __cloudBoardReview: {
				resolve: (value: "found" | "missing" | "error") => { response = value; release(); },
				signOut: () => { signedIn = false; for (const listener of listeners) listener(null); },
			} });
		});
		await page.goto("/#/projects/fake-proj");
		const board = page.getByTestId("board");
		await expect(board).toHaveAttribute("data-project-id", "fake-proj", { timeout: 45_000 });
		await expect(page.getByRole("button", { name: "Other 0 Cloud", exact: true })).toBeVisible();
		await page.evaluate(() => { window.location.hash = "/projects/cloud-101"; });
		await expect(board).toHaveAttribute("data-project-id", "cloud-101");
		await expect(page.getByTestId("daemon-startup-loader")).toHaveCount(0);
		await expect(board.getByRole("status")).toHaveAttribute("aria-busy", "true");
		await expect(board.getByRole("button", { name: "Spawn Orchestrator", exact: true })).toHaveClass(/topbar-control--secondary/);
		await expect(page.getByTestId("board-column")).toHaveCount(0);
		await page.screenshot({ path: testInfo.outputPath(`deep-link-${resolution}-pending.png`) });
		await page.evaluate((value) => (window as unknown as { __cloudBoardReview: { resolve(value: string): void } }).__cloudBoardReview.resolve(value), resolution);
		if (resolution === "found") {
			await expect(page.getByTestId("board-column")).toHaveCount(4);
		} else if (resolution === "missing") {
			await expect(board.getByRole("alert")).toContainText("Session not found.");
		} else {
			await expect(board.getByRole("alert")).toHaveText("Could not load sessions.");
			await page.evaluate(() => (window as unknown as { __cloudBoardReview: { resolve(value: string): void } }).__cloudBoardReview.resolve("found"));
			await board.getByRole("button", { name: "Retry", exact: true }).click();
			await expect(page.getByTestId("board-column")).toHaveCount(4);
		}
		if (resolution === "missing") await expect(page.getByTestId("board-column")).toHaveCount(0);
		await page.evaluate(() => (window as unknown as { __cloudBoardReview: { signOut(): void } }).__cloudBoardReview.signOut());
		await expect(page.getByTestId("project-board-empty")).toHaveCount(0);
		await expect(page.getByRole("heading", { name: "Jump back right in" })).toBeVisible();
		await page.evaluate(() => { window.location.hash = "/projects/cloud-101"; });
		await expect(board).toHaveAttribute("data-project-id", "cloud-101");
		await expect(board.getByRole("alert")).toContainText("Session not found.");
		await expect(page.getByTestId("board-column")).toHaveCount(0);
		await page.screenshot({ path: testInfo.outputPath(`deep-link-${resolution}-signed-out.png`) });
		await page.getByText("Local project", { exact: true }).click();
		await expect(board).toHaveAttribute("data-project-id", "fake-proj");
	});
}

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
