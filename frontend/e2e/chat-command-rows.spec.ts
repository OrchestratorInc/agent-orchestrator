import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Evidence rig for the command-row affordances: the collapsed row names what
// ran instead of a generic "Ran command", the expanded body wraps and is
// selectable instead of scrolling sideways, and Escape collapses the focused
// row. Also re-captures the mcp_tool arguments row from chat-tool-call-null
// with the body open, so the fixed rendering is visible in one place.
//
// Run with AO_COMMAND_EVIDENCE_DIR set to a directory to capture screenshots;
// `video: "on"` (below) leaves a screencast of the flows in test-results.

test.use({ video: "on" });

const sessionId = "chat-command-rows";
const now = "2026-10-23T12:00:00Z";

const longCommand =
	"docker buildx build --platform linux/amd64,linux/arm64 --tag registry.internal.example.com/ao/daemon:2026.10 --push .";

const commandSeeds = [
	{
		id: "cmd-test",
		command: "go test ./internal/...",
		status: "completed",
		detail: { command: "go test ./internal/...", output: "ok  \tagent-orchestrator/internal/service\t0.412s" },
	},
	{
		id: "cmd-long",
		command: longCommand,
		status: "completed",
		detail: { command: longCommand, output: "sha256:9f2c1e… pushed" },
	},
	{
		id: "cmd-fail",
		command: "cargo build --release",
		status: "failed",
		detail: { command: "cargo build --release", exitCode: 101, output: "error: could not compile `ao` (bin \"ao\")" },
	},
];

function snapshot() {
	return {
		conversationId: `conversation-${sessionId}`,
		sessionId,
		harness: "codex",
		mode: "chat",
		controller: "ready",
		latestSequence: commandSeeds.length + 3,
		oldestSequence: 1,
		hasMoreBefore: false,
		turns: [
			{
				id: "turn-1",
				state: "completed",
				requestedAt: now,
				startedAt: now,
				completedAt: now,
			},
		],
		messages: [
			{
				kind: "message",
				id: "user-1",
				turnId: "turn-1",
				sequence: 1,
				revision: 0,
				role: "user",
				origin: "human",
				text: "Run the tests, push the image, and try the release build",
				streaming: false,
				createdAt: now,
			},
		],
		activities: [
			...commandSeeds.map((seed, index) => ({
				kind: "activity",
				id: seed.id,
				turnId: "turn-1",
				sequence: 2 + index,
				revision: 1,
				activityKind: "command",
				status: seed.status,
				summary: seed.command,
				detail: seed.detail,
				createdAt: now,
			})),
			{
				kind: "activity",
				id: "agent-args",
				turnId: "turn-1",
				sequence: 2 + commandSeeds.length,
				revision: 1,
				activityKind: "mcp_tool",
				status: "completed",
				summary: "Agent tool",
				detail: { arguments: { task: "fix the flaky login test" } },
				createdAt: now,
			},
		],
		settings: {},
	};
}

async function installConversation(page: Page) {
	await installFakeAgent(page, {
		workers: [{ id: sessionId, title: "Command rows evidence", mode: "chat" }],
	});
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation") && route.request().method() === "GET") {
			await route.fulfill({ json: snapshot() });
			return;
		}
		if (path.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
			return;
		}
		if (path.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
			return;
		}
		if (path.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
			return;
		}
		if (path.endsWith("/interface-transition")) {
			await route.fulfill({ json: { supported: true, targetMode: "tui" } });
			return;
		}
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible();
	// A completed turn folds its work behind the duration summary; open the
	// turn, then every run and subgroup, so all rows are on screen.
	await page.getByRole("button", { name: /^Worked for/ }).click();
	await expandAll(page);
}

async function expandAll(page: Page) {
	for (let pass = 0; pass < 5; pass += 1) {
		const collapsed = page.getByRole("log").locator("[aria-expanded='false'].activity-run-toggle, [aria-expanded='false'].activity-subgroup-toggle");
		if ((await collapsed.count()) === 0) break;
		await collapsed.first().click();
	}
	await expect(page.getByRole("log").locator("[aria-expanded='false'].activity-run-toggle, [aria-expanded='false'].activity-subgroup-toggle")).toHaveCount(0);
	// A remount (grouping settles, store notifies) resets local overrides; let
	// one land, then re-apply so the captures show settled, expanded rows.
	await page.waitForTimeout(700);
	await expandCollapsed(page);
}

async function expandCollapsed(page: Page) {
	for (let pass = 0; pass < 5; pass += 1) {
		const collapsed = page.getByRole("log").locator("[aria-expanded='false'].activity-run-toggle, [aria-expanded='false'].activity-subgroup-toggle");
		if ((await collapsed.count()) === 0) break;
		await collapsed.first().click();
	}
	await expect(page.getByRole("log").locator("[aria-expanded='false'].activity-run-toggle, [aria-expanded='false'].activity-subgroup-toggle")).toHaveCount(0);
}

async function capture(page: Page, name: string) {
	await expandCollapsed(page);
	const directory = process.env.AO_COMMAND_EVIDENCE_DIR;
	if (directory) await page.screenshot({ path: `${directory}/${name}.png`, fullPage: true });
}

test("collapsed command rows name the command instead of `Ran command` @T0", async ({ page }) => {
	await installConversation(page);

	await expect(page.getByRole("log").getByText("go test ./internal/...", { exact: true })).toBeVisible();
	await expect(page.getByRole("log").getByText("Ran command", { exact: true })).toHaveCount(0);
	// The long command truncates to one line; the full text is on hover.
	const longLabel = page.getByRole("log").getByText(/…/);
	await expect(longLabel).toBeVisible();
	await expect(longLabel).toHaveAttribute("title", longCommand);

	await capture(page, "command-rows-collapsed");
});

test("the expanded command body wraps and is selectable instead of scrolling @T0", async ({ page }) => {
	await installConversation(page);

	const longRow = page.getByRole("button", { name: new RegExp(longCommand.slice(0, 30)) });
	await longRow.click();
	const body = page.getByRole("log").locator("pre.cursor-chat-explore-command", { hasText: "docker buildx build" });
	await expect(body).toBeVisible();
	await expect(body).toContainText("--tag registry.internal.example.com/ao/daemon:2026.10");
	expect(await body.getAttribute("class")).toContain("whitespace-pre-wrap");
	expect(await body.getAttribute("class")).toContain("select-text");
	expect(await body.getAttribute("class")).not.toContain("overflow-x-auto");

	await capture(page, "command-row-expanded-wrap");
});

test("Escape collapses the expanded command row under focus @T0", async ({ page }) => {
	await installConversation(page);

	const row = page.getByRole("button", { name: /go test \.\/internal/ });
	await row.click();
	await expect(row).toHaveAttribute("aria-expanded", "true");
	await row.focus();
	await row.press("Escape");
	await expect(row).toHaveAttribute("aria-expanded", "false");
	// The exit animation unmounts the body a frame later.
	await expect(page.getByText("ok  \tagent-orchestrator/internal/service")).toHaveCount(0);

	await capture(page, "command-row-esc-collapsed");
});

test("an mcp_tool row opens onto its stored arguments @T0", async ({ page }) => {
	await installConversation(page);

	const argsRow = page.getByRole("button", { name: /Agent tool/ });
	await argsRow.click();
	await expect(page.getByText("Arguments")).toBeVisible();
	await expect(page.getByText(/fix the flaky login test/)).toBeVisible();

	await capture(page, "tool-call-arguments-open");
});
