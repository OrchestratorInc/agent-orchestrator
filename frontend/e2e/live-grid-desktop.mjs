// Full packaged Electron verification: real preload, daemon, storage, PTY and ACP.
// The provider is a deterministic fixture; no paid AI CLI or user project is used.
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import net from "node:net";
import { spawnSync } from "node:child_process";
import { _electron, expect } from "playwright/test";

const bundle = process.env.AO_GRID_APP_PATH;
const sessionMode = process.env.AO_GRID_SESSION_MODE ?? "mixed";
assert.ok(["mixed", "chat", "tui"].includes(sessionMode));
if (!bundle || !["Agent Orchestrator.app", "Agent Orchestrator Live Grid.app"].some((name) => path.basename(bundle) === name))
	throw new Error("Provide AO_GRID_APP_PATH pointing to the packaged AO bundle to verify with an isolated profile.");
const root = fs.mkdtempSync(path.join(os.tmpdir(), "ao-grid-desktop-"));
console.log(`Isolated desktop profile: ${root}`);
const port = 32191;
const bin = path.join(root, "bin");
const repo = path.join(root, "project");
fs.mkdirSync(bin);
fs.mkdirSync(repo);
fs.mkdirSync(path.join(root, "evidence"));
const providerLog = path.join(root, "providers.jsonl");
fs.writeFileSync(providerLog, "");
const provider = path.resolve("e2e/support/live-grid-agent.cjs");
const quote = (text) => "'" + text.replaceAll("'", "'\\''") + "'";
const shim = path.join(bin, "opencode");
fs.writeFileSync(shim, `#!/bin/sh\nexec ${quote(process.execPath)} ${quote(provider)} "$@"\n`, {
	mode: 0o700,
});
const shell = path.join(bin, "fixture-shell");
fs.writeFileSync(
	shell,
	'#!/bin/sh\nif [ "$1" = "-ilc" ]; then printf __AO_SHELL_ENV__; exec /usr/bin/env -0; fi\nexec /bin/sh "$@"\n',
	{ mode: 0o700 },
);
const env = {
	...process.env,
	PATH: `${bin}:${process.env.PATH}`,
	SHELL: shell,
	AO_DESKTOP_TEST_ROOT: root,
	AO_DESKTOP_TEST_PORT: String(port),
	AO_LIVE_GRID_LAB_ROOT: root,
	AO_LIVE_GRID_LAB_PORT: String(port),
	AO_GRID_PROVIDER_LOG: providerLog,
	AO_KEEP_DAEMON: "0",
	OPENCODE_DATA_DIR: path.join(root, "opencode"),
	OPENCODE_CONFIG_CONTENT: "",
	AO_TELEMETRY_EVENTS: "off",
	AO_TELEMETRY_REMOTE: "off",
	AO_SENTRY_DSN: "",
};
const resolved = spawnSync(shell, ["-c", "command -v opencode"], { env, encoding: "utf8" });
assert.equal(
	resolved.stdout.trim().split("\n").at(-1),
	shim,
	"Login shell must select the fixture provider before any session is created",
);
await new Promise((resolve, reject) => {
	const server = net.createServer();
	server.once("error", reject);
	server.listen(port, "127.0.0.1", () => server.close(resolve));
});
const git = (...args) => {
	const result = spawnSync("git", args, {
		cwd: repo,
		encoding: "utf8",
		env: {
			...env,
			GIT_AUTHOR_NAME: "Live Grid fixture",
			GIT_AUTHOR_EMAIL: "fixture@example.invalid",
			GIT_COMMITTER_NAME: "Live Grid fixture",
			GIT_COMMITTER_EMAIL: "fixture@example.invalid",
		},
	});
	assert.equal(result.status, 0, result.stderr);
};
git("init", "-b", "main");
fs.writeFileSync(path.join(repo, "README.md"), "# Isolated Live Grid fixture\n");
git("add", "README.md");
git("commit", "-m", "fixture");
git("remote", "add", "origin", repo);
git("fetch", "origin", "main");
git("remote", "set-head", "origin", "main");
const app = await _electron.launch({
	executablePath: path.join(bundle, "Contents/MacOS/agent-orchestrator"),
	args: [],
	env,
	timeout: 60_000,
});
const logs = [];
app.process().stdout?.on("data", (data) => logs.push(data.toString()));
app.process().stderr?.on("data", (data) => logs.push(data.toString()));
const sessions = [];
const errors = [];
const checkpoints = [];
const api = async (method, endpoint, body) => {
	const response = await fetch(`http://127.0.0.1:${port}/api/v1${endpoint}`, {
		method,
		headers: { "content-type": "application/json" },
		body: body ? JSON.stringify(body) : undefined,
	});
	const value = await response.json();
	if (!response.ok) throw new Error(`${method} ${endpoint}: ${response.status} ${JSON.stringify(value)}`);
	return value;
};
const records = () =>
	fs
		.readFileSync(providerLog, "utf8")
		.trim()
		.split("\n")
		.filter(Boolean)
		.map((line) => JSON.parse(line));
const launches = () => records().filter((entry) => entry.event === "launch");
try {
	const page = await app.firstWindow();
	page.on("pageerror", (error) => errors.push(error.message));
	await page.evaluate(() => {
		const NativeSocket = window.WebSocket;
		window.__gridSockets = [];
		window.WebSocket = class extends NativeSocket {
			constructor(...args) {
				super(...args);
				if (new URL(this.url).pathname.endsWith("/mux")) window.__gridSockets.push(this);
			}
		};
	});
	await expect
		.poll(
			async () => {
				try {
					return fs.existsSync(path.join(root, "running.json"))
						? ((await api("GET", "/projects")).projects ?? []).length
						: -1;
				} catch {
					return -1;
				}
			},
			{ timeout: 60_000 },
		)
		.toBe(0);
	assert.equal(await app.evaluate(({ app }) => app.getPath("userData")), path.join(root, "electron"));
	await app.evaluate(({ BaseWindow }) => {
		const window = BaseWindow.getAllWindows()[0];
		window.setBounds({ ...window.getBounds(), width: 1500, height: 1000 });
	});
	const project = (await api("POST", "/projects", { path: repo })).project.id;
	const spawn = async (kind, mode, index) => {
		const value = await api("POST", "/sessions", {
			projectId: project,
			kind,
			mode,
			harness: "opencode",
			displayName: kind === "orchestrator" ? "Grid test orchestrator" : `Grid task ${index}`,
			prompt: `Isolated ${kind} ${index}`,
		});
		sessions.push(value.session.id);
		return value.session.id;
	};
	const orchestrator = await spawn("orchestrator", sessionMode === "tui" ? "tui" : "chat", 0);
	await page.evaluate((id) => {
		window.location.hash = `/projects/${id}/grid`;
	}, project);
	const panels = page.getByTestId("grid-panel");
	for (const count of [1, 2, 4, 6, 9, 12]) {
		while (sessions.length < count)
			await spawn(
				"worker",
				sessionMode === "mixed" ? (sessions.length < 3 ? "chat" : "tui") : sessionMode,
				sessions.length,
			);
		await expect(panels).toHaveCount(count, { timeout: 30_000 });
		await expect(panels.first()).toHaveAttribute("data-session-id", orchestrator);
		await expect
			.poll(() =>
				panels.evaluateAll((elements) =>
					Math.min(...elements.map((element) => element.getBoundingClientRect().height)),
				),
			)
			.toBeGreaterThanOrEqual(sessionMode === "tui" ? 280 : 360);
		await expect.poll(() => launches().length, { timeout: 30_000 }).toBe(count);
		await page.screenshot({ path: path.join(root, "evidence", `desktop-${count}.png`) });
		checkpoints.push({ count, metrics: await app.evaluate(({ app }) => app.getAppMetrics().map(({ pid, type, cpu, memory }) => ({ pid, type, cpu, memory }))) });
		console.log(`Verified ${count} existing sessions in packaged Electron`);
	}
	const baseline = launches();
	const worker = (id) => page.locator(`[data-testid="grid-panel"][data-session-id="${id}"]`);
	const chat = worker(sessions[1]);
	const draft = chat.getByRole("combobox", { name: "Message the agent" });
	if (sessionMode !== "tui") {
		await draft.fill("isolated-grid-chat-input");
		await draft.press("Enter");
		await expect(chat.getByRole("log", { name: "Conversation" })).toContainText("isolated-grid-chat-input", {
			timeout: 20_000,
		});
		await expect
			.poll(
				() =>
					records().some(
						(entry) =>
							entry.event === "input" &&
							entry.session === sessions[1] &&
							entry.text.includes("isolated-grid-chat-input"),
					),
				{ timeout: 20_000 },
			)
			.toBe(true);
		await expect
			.poll(async () => (await api("GET", `/sessions/${sessions[1]}/conversation`)).turns.at(-1)?.state, {
				timeout: 30_000,
			})
			.toBe("completed");
		await expect(draft).toHaveText("");
		await draft.pressSequentially("persistent grid draft");
		await expect(draft).toHaveText("persistent grid draft");
	}
	const tui = worker(sessions[3]);
	if (sessionMode !== "chat") {
		assert.equal(
			await page.evaluate(() => window.__gridSockets.length),
			1,
			"All local terminals share one mux socket",
		);
		await tui.locator(".xterm-helper-textarea").focus();
		await page.keyboard.type("isolated-grid-tui-input");
		await expect
			.poll(
				() =>
					records()
						.filter((entry) => entry.event === "input" && entry.session === sessions[3])
						.map((entry) => entry.text)
						.join(""),
				{ timeout: 10_000 },
			)
			.toContain("isolated-grid-tui-input");
		await page.evaluate(() => window.__gridSockets[0].close());
		await expect
			.poll(
				() =>
					page.evaluate(
						() => window.__gridSockets.filter((socket) => socket.readyState === WebSocket.OPEN).length,
					),
				{ timeout: 20_000 },
			)
			.toBe(1);
		await tui.locator(".xterm-helper-textarea").focus();
		await page.keyboard.type("reconnected-grid-input");
		await expect
			.poll(
				() =>
					records()
						.filter((entry) => entry.event === "input" && entry.session === sessions[3])
						.map((entry) => entry.text)
						.join(""),
				{ timeout: 10_000 },
			)
			.toContain("reconnected-grid-input");
	}
	await chat.getByTestId("grid-panel-header").dblclick();
	await expect(tui).toBeHidden();
	await page.screenshot({ path: path.join(root, "evidence", "desktop-maximized.png") });
	await chat.getByRole("button", { name: "Restore grid" }).click();
	const separator = page.getByRole("separator", { name: "Resize columns" }).first();
	await separator.focus();
	await separator.press("Shift+ArrowRight");
	const width = (await panels.first().boundingBox()).width;
	await page.getByRole("button", { name: "Open Kanban", exact: true }).last().click();
	await expect(page.getByTestId("live-grid")).toHaveCount(0);
	await page.getByRole("button", { name: "Open Grid", exact: true }).last().click();
	await expect(panels).toHaveCount(12);
	if (sessionMode !== "tui") await expect(draft).toHaveText("persistent grid draft");
	assert.ok(Math.abs((await panels.first().boundingBox()).width - width) < 2);
	assert.deepEqual(launches(), baseline, "Changing presentation must not launch provider processes");
	for (const entry of baseline) process.kill(entry.pid, 0);
	// Deliver the real desktop settings command to this app's renderer. CDP key
	// injection does not reliably dispatch macOS menu shortcuts to BaseWindow.
	await app.evaluate(({ webContents }, url) => {
		webContents
			.getAllWebContents()
			.find((contents) => contents.getURL() === url)
			?.send("app:open-settings");
	}, page.url());
	await page.getByRole("button", { name: "Theme", exact: true }).click();
	await page.getByRole("menuitem", { name: "Dark", exact: true }).click();
	await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe("dark");
	await page.getByRole("button", { name: "Close settings", exact: true }).click();
	await expect(page.getByRole("dialog")).toHaveCount(0);
	await page.screenshot({ path: path.join(root, "evidence", "desktop-dark.png") });
	await api("POST", `/sessions/${sessions.at(-1)}/kill`, {});
	await expect(panels).toHaveCount(11, { timeout: 20_000 });
	await page.getByRole("checkbox", { name: "Show completed" }).check();
	await expect(panels).toHaveCount(12);
	await page.screenshot({ path: path.join(root, "evidence", "desktop-final.png") });
	const metrics = await app.evaluate(({ app }) =>
		app.getAppMetrics().map(({ pid, type, cpu, memory }) => ({ pid, type, cpu, memory })),
	);
	assert.deepEqual(errors, [], "Renderer errors");
	fs.writeFileSync(
		path.join(root, "verification.json"),
		JSON.stringify(
			{
				bundle,
				root,
				sessionMode,
				project,
				sessions,
				providerPids: baseline.map((entry) => entry.pid),
				metrics,
				checkpoints,
				savedLayout: await page.evaluate(
					(id) => localStorage.getItem(`ao.live-grid.v1.${encodeURIComponent(id)}`),
					project,
				),
				errors,
				passed: true,
			},
			null,
			2,
		),
	);
	console.log(`PASS: native verification artifacts at ${root}`);
} finally {
	// Only this fixture's explicit IDs, through this isolated daemon.
	for (const id of sessions) {
		try {
			await api("POST", `/sessions/${id}/kill`, {});
		} catch {}
	}
	fs.writeFileSync(path.join(root, "electron.log"), logs.join(""));
	// AO's macOS Cmd+Q normally closes the window into its tray. End only this
	// launched test process after all fixture sessions have been terminated.
	await app.evaluate(({ app }) => app.exit(0)).catch(() => {});
	await app.close().catch(() => {});
}

// Re-open the same isolated Chromium profile and database with stopped fixtures.
// This checks durable layout without asking the daemon to restore any provider.
if (sessionMode === "mixed") {
	const proofPath = path.join(root, "verification.json");
	const proof = JSON.parse(fs.readFileSync(proofPath, "utf8"));
	await expect
		.poll(
			async () => {
				try {
					await api("GET", "/projects");
					return false;
				} catch {
					return true;
				}
			},
			{ timeout: 20_000 },
		)
		.toBe(true);
	const reopened = await _electron.launch({
		executablePath: path.join(bundle, "Contents/MacOS/agent-orchestrator"),
		args: [],
		env,
		timeout: 60_000,
	});
	try {
		const page = await reopened.firstWindow();
		await expect
			.poll(
				async () => {
					try {
						return ((await api("GET", "/projects")).projects ?? []).length;
					} catch {
						return 0;
					}
				},
				{ timeout: 60_000 },
			)
			.toBe(1);
		await page.evaluate((id) => {
			window.location.hash = `/projects/${id}/grid`;
		}, proof.project);
		await expect(page.getByTestId("grid-panel")).toHaveCount(12, { timeout: 30_000 });
		assert.equal(
			await page.evaluate(
				(id) => localStorage.getItem(`ao.live-grid.v1.${encodeURIComponent(id)}`),
				proof.project,
			),
			proof.savedLayout,
		);
		assert.equal(launches().length, 12, "Reopening stopped Grid history must not start providers");
		await page.screenshot({ path: path.join(root, "evidence", "desktop-restarted.png") });
		proof.restartPassed = true;
		fs.writeFileSync(proofPath, JSON.stringify(proof, null, 2));
		console.log("PASS: layout survived a real Electron restart without provider launches");
	} finally {
		await reopened.evaluate(({ app }) => app.exit(0)).catch(() => {});
		await reopened.close().catch(() => {});
	}
}

// The supervisor closes its owned daemon asynchronously after Electron exits.
// Wait for that graceful exit before another isolated run reserves this port.
await expect
	.poll(
		async () => {
			try {
				await api("GET", "/projects");
				return false;
			} catch {
				return true;
			}
		},
		{ timeout: 20_000 },
	)
	.toBe(true);
