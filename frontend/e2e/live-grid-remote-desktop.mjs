// View-only integration against an explicitly selected paired daemon, using the
// ordinary packaged app, real preload, authenticated proxy, SSE, mux and xterm.
// Input/creation/termination tests belong to disposable fixtures instead.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { _electron, expect } from "playwright/test";

const bundle = process.env.AO_GRID_APP_PATH;
const root = process.env.AO_GRID_REMOTE_ROOT;
const hostLabel = process.env.AO_GRID_REMOTE_HOST_LABEL;
const projectId = process.env.AO_GRID_REMOTE_PROJECT_ID;
assert.ok(bundle && root && hostLabel && projectId, "Provide the app, isolated profile, paired host label and project ID explicitly");
assert.ok(fs.existsSync(path.join(root, "cloud-auth.bin")), "Prepare an isolated authenticated profile with exclusive ownership of its credential refresh");
const evidence = path.join(root, "remote-evidence");
fs.mkdirSync(evidence, { recursive: true, mode: 0o700 });
console.log("Launching ordinary AO with an isolated remote verification profile");
const app = await _electron.launch({
	executablePath: path.join(bundle, "Contents/MacOS/agent-orchestrator"), args: [],
	env: { ...process.env, AO_DESKTOP_TEST_ROOT: root, AO_DESKTOP_TEST_PORT: "32192", AO_KEEP_DAEMON: "0", AO_TELEMETRY_REMOTE: "off", AO_SENTRY_DSN: "" }, timeout: 60_000,
});
const errors = [], mutations = [], streams = {};
let before, after;
try {
	const page = await app.firstWindow();
	page.on("pageerror", (error) => errors.push(error.message));
	page.on("websocket", (socket) => {
		if (!new URL(socket.url()).pathname.endsWith("/mux")) return;
		socket.on("framereceived", ({ payload }) => {
			try { const frame = JSON.parse(String(payload)); if (frame.ch === "terminal" && frame.id && frame.type === "data") streams[frame.id] = (streams[frame.id] ?? 0) + String(frame.data ?? "").length; } catch {}
		});
	});
	await expect.poll(() => page.evaluate(() => window.ao.daemon.getStatus()).then((state) => state.state), { timeout: 60_000 }).toBe("ready");
	console.log("Isolated local daemon ready");
	assert.equal(await app.evaluate(({ app }) => app.getPath("userData")), path.join(root, "electron"));
	const selected = await page.evaluate(async (label) => (await window.ao.remotes.list()).find((host) => host.label === label), hostLabel);
	assert.ok(selected, "The existing paired host must be available to the ordinary preload");
	console.log(`Existing paired host available: ${hostLabel}`);
	const connection = await page.evaluate(({ url, hostId }) => window.ao.remotes.connect(url, hostId), selected);
	const read = async (endpoint) => {
		const response = await fetch(`${connection.base}${endpoint}`, { signal: AbortSignal.timeout(20_000) }); assert.ok(response.ok, `Read-only remote ${endpoint}: ${response.status}`); return response.json();
	};
	const snapshot = async () => ({ health: await read("/healthz"), sessions: (await read("/api/v1/sessions")).sessions });
	before = await snapshot();
	console.log(`Remote daemon ${before.health.pid}; ${before.sessions.length} existing session records`);
	// Enforce read-only HTTP at the renderer/proxy boundary. OPTIONS is CORS.
	await page.route(/\/api\/v1\//, async (route) => {
		const request = route.request();
		if (["GET", "OPTIONS"].includes(request.method()) || new URL(request.url()).origin !== new URL(connection.base).origin) return route.continue();
		mutations.push({ method: request.method(), path: new URL(request.url()).pathname.replace(new URL(connection.base).pathname, "") });
		return route.abort("blockedbyclient");
	});
	await page.evaluate(({ hostId, projectId }) => {
		localStorage.setItem("ao.developerMode", "true"); localStorage.setItem("ao.remoteHosts", "true");
		window.location.hash = `/host/${hostId}/project/${projectId}/grid`;
	}, { hostId: selected.hostId, projectId });
	await page.reload();
	await app.evaluate(({ BaseWindow }) => { const window = BaseWindow.getAllWindows()[0]; window.setBounds({ ...window.getBounds(), width: 1500, height: 1000 }); });
	const grid = page.getByTestId("live-grid"), panels = page.getByTestId("grid-panel");
	await expect(grid).toHaveAttribute("data-host-id", selected.hostId, { timeout: 60_000 });
	await expect(panels.first()).toHaveAttribute("data-session-id", before.sessions.filter((session) => session.projectId === projectId && session.kind === "orchestrator" && !session.isTerminated).at(-1).id, { timeout: 60_000 });
	await expect.poll(() => panels.count(), { timeout: 60_000 }).toBeGreaterThan(1);
	await expect.poll(() => Object.keys(streams).length, { timeout: 60_000 }).toBeGreaterThan(1);
	await expect.poll(() => page.getByRole("log", { name: "Conversation" }).count(), { timeout: 60_000 }).toBeGreaterThan(0);
	const ids = await panels.evaluateAll((nodes) => nodes.map((node) => node.dataset.sessionId));
	console.log(`Real Grid visible: ${ids.length} panels, ${Object.keys(streams).length} live TUI streams`);
	const expected = before.sessions.filter((session) => session.projectId === projectId && session.kind !== "orchestrator" && !session.isTerminated && session.status !== "merged" && session.kanbanColumn !== "archive").map((session) => session.id);
	for (const id of expected) assert.ok(ids.includes(id), `Existing active worker must appear: ${id}`);
	// Offscreen panes settle their first fit when brought into view. Visit every
	// existing TUI without sending input or requesting an agent lifecycle action.
	for (const session of before.sessions.filter((session) => session.mode === "tui" && ids.includes(session.id))) {
		const pane = page.locator(`[data-testid="grid-panel"][data-session-id="${session.id}"]`);
		await pane.getByRole("button", { name: "Maximize panel", exact: true }).click();
		await expect.poll(() => streams[session.terminalHandleId] ?? 0, { timeout: 30_000 }).toBeGreaterThan(0);
		await pane.getByRole("button", { name: "Restore grid", exact: true }).click();
	}
	await grid.locator('[data-testid="live-grid-layout"]').evaluate((node) => { node.parentElement.scrollTop = 0; });
	const metrics = await app.evaluate(({ app }) => app.getAppMetrics().map(({ pid, type, cpu, memory }) => ({ pid, type, cpu, memory })));
	await page.screenshot({ path: path.join(evidence, "remote-grid.png") });
	await page.getByRole("button", { name: "Open Kanban", exact: true }).last().click();
	await expect(grid).toHaveCount(0);
	await page.getByRole("button", { name: "Open Grid", exact: true }).last().click();
	await expect(panels).toHaveCount(ids.length);
	assert.deepEqual(await panels.evaluateAll((nodes) => nodes.map((node) => node.dataset.sessionId)), ids);
	after = await snapshot();
	assert.equal(after.health.pid, before.health.pid, "Remote daemon must remain the same process");
	const sessionsAfter = new Map(after.sessions.map((session) => [session.id, session]));
	for (const session of before.sessions) {
		const current = sessionsAfter.get(session.id); assert.ok(current, "Existing remote sessions must not disappear");
		assert.equal(current.terminalGeneration, session.terminalGeneration, `Existing terminal generation: ${session.id}`);
	}
	assert.deepEqual(mutations, [], "Viewing Grid must not request any remote HTTP mutations");
	assert.deepEqual(errors, [], "No renderer errors");
	fs.writeFileSync(path.join(evidence, "verification.json"), JSON.stringify({ passed: true, version: await app.evaluate(({ app }) => app.getVersion()), hostLabel, projectId, ids, terminalOutputBytes: streams, metrics, remoteDaemonPid: after.health.pid, unchangedExistingGenerations: true, mutations, errors }, null, 2), { mode: 0o600 });
	console.log(`PASS: real paired remote Grid, ${ids.length} panels; evidence at ${evidence}`);
} finally {
	// End only this client; do not send shutdown, kill or restore to the host.
	await app.evaluate(({ app }) => app.exit(0)).catch(() => {});
	await app.close().catch(() => {});
}
