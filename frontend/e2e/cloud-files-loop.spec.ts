import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import { openInspector } from "./support/open-inspector";

test("@P0 Cloud Chat settles after opening, switching and closing full files (fixture)", async ({ page }, testInfo) => {
	testInfo.setTimeout(60_000);
	await page.setViewportSize({ width: 1440, height: 960 });
	await installFakeAgent(page, {
		projectId: "cloud-files-fixture",
		workers: [{ id: "cloud-files-worker", title: "Cloud files fixture", provider: "codex", mode: "chat", status: "idle" }],
	});
	await page.addInitScript(() => {
		const snapshot = window.__aoFakeAgent!.snapshot;
		window.__aoFakeAgent!.snapshot = () => (snapshot() as Array<{ sessions: Array<Record<string, unknown>> }>).map((workspace) => ({
			...workspace, sessions: workspace.sessions.map((session) => ({ ...session, runtimeConnected: true, cloud: { orgId: "fixture-org", sandboxProvider: "docker" } })),
		}));
		window.ao!.cloud.getSession = async () => ({
			authProvider: "local", user: { id: "fixture-user", email: "fixture@example.test", displayName: "Fixture" }, storedAt: "2026-10-24T00:00:00Z",
		});
		window.ao!.cloudCp.request = async ({ path }) => {
			const file = (path: string) => ({ path, status: "modified", additions: 1, deletions: 1, size: 20, binary: false, editable: false });
			const files = [file("src/panel.tsx"), file("docs/notes.txt")];
			const url = new URL(path, "https://fixture.test");
			let body: unknown;
			if (url.pathname.endsWith("/workspace/review")) body = {
				workspaceRoot: "/home/coder/repo", workspaceVersion: "fixture-1", files, truncated: false,
				sections: { unstaged: files, staged: [], committed: [], untracked: [] }, commits: [], summary: { files: 2, additions: 2, deletions: 2 },
			};
			else if (url.pathname.endsWith("/workspace/review/diffs")) body = { workspaceVersion: "fixture-1", groups: [] };
			else if (url.pathname.endsWith("/workspace/review/file")) body = {
				...file(url.searchParams.get("path")!), content: "fixture first line\nfixture last line\n", deleted: false, contentTruncated: false, diff: "", diffTruncated: false, workspaceVersion: "fixture-1",
			};
			else if (url.pathname.endsWith("/chat-events")) body = { events: [], hasMore: false, nextAfter: 0 };
			else if (url.pathname.endsWith("/chat-models")) body = { models: [] };
			else if (url.pathname.endsWith("/reviews")) body = { reviews: [], runs: [] };
			else return { status: 404, headers: { "content-type": "application/json" }, body: JSON.stringify({ error: { code: "FIXTURE_UNSUPPORTED", message: "Not provided by this fixture" } }) };
			return { status: 200, headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
		};
		const state = window as unknown as { fixtureCommits: number; __REACT_DEVTOOLS_GLOBAL_HOOK__: unknown };
		state.fixtureCommits = 0;
		const renderers = new Map();
		state.__REACT_DEVTOOLS_GLOBAL_HOOK__ = {
			supportsFiber: true, renderers,
			inject: (renderer: unknown) => { renderers.set(1, renderer); return 1; },
			onScheduleFiberRoot: () => {},
			onCommitFiberRoot: () => { state.fixtureCommits++; },
			onCommitFiberUnmount: () => {},
		};
	});
	await page.route("http://127.0.0.1:8080/api/v1/settings", (route) => route.fulfill({ json: {
		cloudEnabled: true, localEnabled: true, cloudControlPlaneUrl: "https://fixture.test", chatHarnesses: ["codex"],
	} }));
	const errors: string[] = [];
	page.on("pageerror", (error) => errors.push(error.message));
	await page.goto("/#/projects/cloud-files-fixture/sessions/cloud-files-worker");
	const inspector = await openInspector(page);
	await inspector.getByRole("tab", { name: /Files?$/ }).click();
	await inspector.getByRole("button", { name: "Open full file" }).first().click();
	await expect(page.getByRole("tab", { name: "panel.tsx" })).toHaveAttribute("aria-selected", "true");
	await expect.poll(() => page.locator("#terminal diffs-container").evaluateAll((nodes) => nodes.some((node) => node.shadowRoot?.textContent?.includes("fixture first line")))).toBe(true);
	let settledCommits = 0;
	await expect.poll(async () => {
		const before = await page.evaluate(() => (window as unknown as { fixtureCommits: number }).fixtureCommits);
		await page.waitForTimeout(400);
		settledCommits = await page.evaluate(() => (window as unknown as { fixtureCommits: number }).fixtureCommits);
		return settledCommits - before;
	}).toBe(0);
	expect(settledCommits).toBeGreaterThan(0);
	await testInfo.attach("settled-render-count", { body: JSON.stringify({ settledCommits, unchangedForMs: 400 }), contentType: "application/json" });
	await page.screenshot({ path: testInfo.outputPath("cloud-session-browser.png") });
	await page.getByRole("tab", { name: "panel.tsx" }).hover();
	await page.getByRole("button", { name: "Close panel.tsx" }).click();
	await expect(page.getByRole("tab", { name: "panel.tsx" })).toHaveCount(0);
	await inspector.getByRole("button", { name: "Open full file" }).last().click();
	await expect(page.getByRole("tab", { name: "notes.txt" })).toHaveAttribute("aria-selected", "true");
	await page.getByRole("tab", { name: "notes.txt" }).hover();
	await page.getByRole("button", { name: "Close notes.txt" }).click();
	await expect(page.getByTestId("session-topbar-host")).toBeVisible();
	expect(errors).toEqual([]);
});
