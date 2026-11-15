import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

test("renderer: notification center shows the correct unread count @T0 @NTF", async ({ page }) => {
	await installFakeAgent(page, { platform: "Linux x86_64" });

	await page.route(/\/api\/v1\/notifications\?/, (route) =>
		route.fulfill({
			json: {
				notifications: [
					{
						id: "n1",
						type: "needs_input",
						title: "Worker needs your input",
						body: "",
						status: "unread",
						sessionId: "fake-a",
						projectId: "fake-proj",
						target: { kind: "session", sessionId: "fake-a" },
						createdAt: new Date().toISOString(),
					},
					{
						id: "n2",
						type: "ready_to_merge",
						title: "PR ready to merge",
						body: "",
						status: "unread",
						sessionId: "fake-b",
						projectId: "fake-proj",
						target: { kind: "session", sessionId: "fake-b" },
						createdAt: new Date().toISOString(),
					},
				],
			},
		}),
	);

	await page.goto("/#/projects/ao-demo");
	const bell = page.getByRole("button", { name: "2 unread notifications" });
	await expect(bell).toBeVisible();

	await page.evaluate(() =>
		window.__aoFakeAgent!.notify({
			id: "n3",
			type: "needs_input",
			title: "Second worker needs input",
			sessionId: "fake-c",
		}),
	);
	await expect(page.getByRole("button", { name: "3 unread notifications" })).toBeVisible();
});
