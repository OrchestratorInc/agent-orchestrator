#!/usr/bin/env node
// CI-only visual evidence for the Coder Agents harness (PR #7520).
//
// Attaches to the real AO Electron dev app (started by `npm run dev` with its
// managed daemon and --remote-debugging-port) over CDP and walks the same path
// a user takes: Settings > Harness > Coder Agents (Account, Models, Health),
// then New Task with Coder Agents selected. Every checkpoint is screenshotted
// whatever its outcome, and the text the app actually shows is recorded next
// to it, so captions describe the real state (on a fresh runner: not installed
// and signed out). Nothing here mocks the app, the daemon or its responses.
//
// Usage: node scripts/ci-visual/coder-agents-harness.mjs --out DIR [--cdp URL]
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

const HARNESS = "coder-agents";
const LABEL = "Coder Agents";

function parseArgs(argv) {
	const options = { cdp: "http://127.0.0.1:9222", out: "" };
	for (let i = 0; i < argv.length; i += 1) {
		if (argv[i] === "--cdp") options.cdp = argv[++i];
		else if (argv[i] === "--out") options.out = argv[++i];
	}
	if (!options.out) throw new Error("--out is required");
	options.out = resolve(options.out);
	return options;
}

async function visible(locator, timeout = 5_000) {
	try {
		await locator.first().waitFor({ state: "visible", timeout });
		return true;
	} catch {
		return false;
	}
}

async function textOf(locator) {
	return ((await locator.first().innerText().catch(() => "")) ?? "").replace(/\s+/g, " ").trim();
}

// Only defer-style answers are clicked on first-run dialogs; nothing decides
// for the user.
const DEFER_BUTTON = /^(Skip|Not now|Later|Maybe later|Dismiss)$/;
async function dismissDialogs(page, dismissed) {
	for (let attempt = 0; attempt < 4; attempt += 1) {
		const dialog = page
			.locator('[role="dialog"], [role="alertdialog"]')
			.filter({ has: page.getByRole("button", { name: DEFER_BUTTON }) })
			.last();
		if (!(await visible(dialog, 500))) return;
		const defer = dialog.getByRole("button", { name: DEFER_BUTTON }).first();
		dismissed.push(((await defer.textContent()) ?? "").trim());
		await defer.click();
		await page.waitForTimeout(500);
	}
}

async function appPage(browser) {
	const deadline = Date.now() + 180_000;
	while (Date.now() < deadline) {
		for (const context of browser.contexts()) {
			for (const page of context.pages()) {
				const url = page.url();
				if (url && !url.startsWith("devtools://") && url !== "about:blank") return page;
			}
		}
		await new Promise((done) => setTimeout(done, 1_000));
	}
	throw new Error("no AO renderer page appeared over CDP");
}

async function main() {
	const options = parseArgs(process.argv.slice(2));
	mkdirSync(options.out, { recursive: true });
	const results = [];
	const dismissed = [];
	let browser;
	for (let attempt = 0; attempt < 60 && !browser; attempt += 1) {
		try {
			browser = await chromium.connectOverCDP(options.cdp);
		} catch {
			await new Promise((done) => setTimeout(done, 2_000));
		}
	}
	if (!browser) throw new Error(`could not attach to the Electron app at ${options.cdp}`);
	const page = await appPage(browser);
	await page.setViewportSize({ width: 1440, height: 900 }).catch(() => {});

	const checkpoint = async (id, title, run) => {
		await dismissDialogs(page, dismissed);
		let outcome;
		try {
			outcome = await run();
		} catch (error) {
			outcome = { status: "FAIL", observed: `error: ${String(error.message).split("\n")[0]}` };
		}
		const file = `${String(results.length + 1).padStart(2, "0")}-${id}.png`;
		await page.screenshot({ path: join(options.out, file) }).catch((error) => {
			outcome.screenshotError = error.message;
		});
		results.push({ id, title, file, url: page.url(), ...outcome });
		console.log(`${outcome.status.padEnd(5)} ${id}: ${outcome.observed}`);
	};

	const settingsButton = page.getByRole("button", { name: "Settings" });
	await checkpoint("app-ready", "Real AO Electron app is up with its daemon", async () => {
		const ready = await visible(settingsButton, 240_000);
		return { status: ready ? "OK" : "FAIL", observed: ready ? "Settings control visible" : "app never became ready" };
	});

	const section = page.locator('[data-testid="settings-section"][data-section="harness"]');
	const detail = page.locator(`[data-harness-detail] div[data-agent="${HARNESS}"]`);
	await checkpoint("harness-listed", "Settings > Harness lists Coder Agents", async () => {
		await settingsButton.first().click();
		await page.locator('[data-testid="settings-page"]').waitFor({ timeout: 15_000 });
		await page.getByRole("button", { name: "Harness" }).first().click();
		await section.waitFor({ timeout: 15_000 });
		const item = section.locator(`button[data-agent="${HARNESS}"]`);
		if (!(await visible(item, 20_000))) return { status: "FAIL", observed: "Coder Agents row not found" };
		await item.first().scrollIntoViewIfNeeded();
		await item.first().click();
		const opened = await visible(detail, 15_000);
		return { status: opened ? "OK" : "FAIL", observed: opened ? `row text: ${await textOf(item)}` : "detail did not open" };
	});

	const openTab = async (name) => {
		await detail.getByRole("tab", { name }).first().click();
		// Let readiness probes settle so the screenshot is not a spinner.
		for (let i = 0; i < 20; i += 1) {
			if (!/Checking/i.test(await textOf(detail))) break;
			await page.waitForTimeout(1_500);
		}
	};

	await checkpoint("account", "Coder Agents Account tab: install/login actions", async () => {
		await openTab("Account");
		const text = await textOf(detail);
		const login = await visible(detail.getByRole("button", { name: /Log in|Login|Set up/ }), 1_000);
		const install = await visible(detail.getByRole("button", { name: /Install/ }), 1_000);
		return { status: "OK", observed: text.slice(0, 600), loginOffered: login, installOffered: install };
	});

	await checkpoint("models", "Coder Agents Models tab", async () => {
		await openTab("Models");
		return { status: "OK", observed: (await textOf(detail)).slice(0, 600) };
	});

	await checkpoint("health", "Coder Agents Health tab", async () => {
		await openTab("Health");
		return { status: "OK", observed: (await textOf(detail)).slice(0, 600) };
	});

	await checkpoint("new-task-chat-only", "New Task with Coder Agents (Chat-only)", async () => {
		await page.getByRole("button", { name: "Close settings" }).first().click().catch(() => page.keyboard.press("Escape"));
		await page.waitForTimeout(800);
		const start = page.getByRole("button", { name: /Start a standalone agent|New agent|New task/i });
		if (!(await visible(start, 10_000))) return { status: "SKIP", observed: "no New Task entry point reachable on a fresh install" };
		await start.first().click();
		const picker = page.getByLabel("Agent");
		if (!(await visible(picker, 10_000))) return { status: "SKIP", observed: "New Task opened without an agent picker" };
		await picker.first().click();
		const option = page.getByRole("menuitem", { name: new RegExp(`^${LABEL}`) }).or(page.getByRole("option", { name: new RegExp(`^${LABEL}`) }));
		if (!(await visible(option, 5_000))) return { status: "FAIL", observed: "Coder Agents is not offered in the agent picker" };
		await option.first().click();
		await page.waitForTimeout(800);
		const dialog = page.locator('[role="dialog"]').last();
		return { status: "OK", observed: (await textOf(dialog)).slice(0, 600) };
	});

	writeFileSync(join(options.out, "results.json"), `${JSON.stringify({ harness: HARNESS, dismissed, results }, null, 2)}\n`);
	await browser.close().catch(() => {});
	if (!results.some((item) => item.id === "account" && !item.screenshotError)) process.exitCode = 1;
}

main().catch((error) => {
	console.error(error);
	process.exitCode = 1;
});
