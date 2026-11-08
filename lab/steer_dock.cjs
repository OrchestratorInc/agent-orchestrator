// Reproduce / verify the empty "0 Queued Messages" dock after Steer.
// Usage: node steer_dock.cjs <cdpPort> <aoPort> <projectId> <sessionId> <outDir> <label> <mode:before|after> [latencyMs]
// mode "before" refuses to run if the served ChatWorkspace.tsx contains FIX_MARKER; "after" requires it.
const { chromium } = require("/Users/dhruvsharma/.ao/data/worktrees/agent-orchestrator-1/agent-orchestrator-1-374/frontend/node_modules/playwright");
const fs = require("node:fs");
const path = require("node:path");

const [cdp, aoPort, projectId, sid, outRoot, label, mode, latencyArg] = process.argv.slice(2);
const latencyMs = Number(latencyArg ?? 0);
const FIX_MARKER = "steeringQueuedTurnIds";
const out = path.join(outRoot, label);
fs.mkdirSync(path.join(out, "frames"), { recursive: true });
const api = `http://127.0.0.1:${aoPort}/api/v1`;
const token = `zephyr${Date.now().toString(36)}`;
const log = (event, extra = {}) => console.log(JSON.stringify({ t: new Date().toISOString(), event, ...extra }));
const conv = async () => (await (await fetch(`${api}/sessions/${sid}/conversation`)).json());
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitFor(labelText, pred, ms) {
	const end = Date.now() + ms;
	while (Date.now() < end) {
		const c = await conv();
		if (pred(c)) return c;
		await sleep(250);
	}
	throw new Error(`timed out waiting for ${labelText}`);
}

(async () => {
	const browser = await chromium.connectOverCDP(`http://127.0.0.1:${cdp}`);
	const page = browser.contexts().flatMap((c) => c.pages()).find((p) => /localhost/.test(p.url()));

	// Prove which code this run exercises.
	const served = await page.evaluate(async () => (await fetch("/src/renderer/components/chat/ChatWorkspace.tsx")).text());
	const hasFix = served.includes("steeringQueuedTurnIds");
	if ((mode === "after") !== hasFix) throw new Error(`served ChatWorkspace.tsx hasFix=${hasFix} does not match mode ${mode}`);
	log("served_source", { mode, hasFix });

	// Start from an idle chat so the prompt runs instead of queueing.
	await waitFor("idle chat", (c) => (c.turns ?? []).every((t) => t.state !== "running" && t.state !== "queued"), 600000);

	// Dark theme, like the report's screenshot.
	await page.evaluate(() => localStorage.setItem("ao.theme", "dark"));
	await page.reload();
	await page.evaluate((r) => { window.location.hash = r; }, `/projects/${projectId}/sessions/${sid}`);
	const box = page.getByRole("combobox").first();
	await box.waitFor({ timeout: 30000 });

	const cdpSession = await page.context().newCDPSession(page);
	if (latencyMs > 0) {
		// Renderer <-> daemon only: this models a slow link, not the remote-host code path.
		await cdpSession.send("Network.enable");
		await cdpSession.send("Network.emulateNetworkConditions", {
			offline: false, latency: latencyMs, downloadThroughput: (4 * 1024 * 1024) / 8, uploadThroughput: (4 * 1024 * 1024) / 8,
		});
		log("network_throttled", { latencyMs, mbit: 4 });
	}

	// 1. A long streamed answer keeps the turn running.
	await box.click();
	await page.keyboard.type("Write a long, detailed essay of at least 1500 words about the history of lighthouses. Do not use any tools and do not create or edit any files.", { delay: 2 });
	await page.keyboard.press("Enter");
	log("prompt_sent");
	await waitFor("turn streaming", (c) =>
		(c.turns ?? []).some((t) => t.state === "running") &&
		(c.messages ?? []).some((m) => m.role === "assistant" && (m.text ?? "").length > 300), 180000);
	log("turn_streaming");

	// 2. Queue a human message behind the running turn.
	await box.click();
	await page.keyboard.type(`Also add one sentence that contains the word ${token}.`, { delay: 2 });
	await page.keyboard.press("Enter");
	const steerButton = page.getByRole("button", { name: "Steer this queued message into the running turn" }).first();
	await steerButton.waitFor({ timeout: 30000 });
	await page.waitForTimeout(500);
	log("queued", { token });

	// 3. Sample every DOM change to the dock and timeline from now on.
	await page.evaluate((tok) => {
		const w = window;
		w.__samples = [];
		let last = "";
		const snap = () => {
			const dock = document.querySelector('[data-testid="queued-message-dock"]');
			const labelText = dock?.querySelector(".queue-dock-label")?.textContent?.trim() ?? null;
			const rows = dock ? dock.querySelectorAll(".queue-dock-row").length : 0;
			const inDock = Boolean(dock?.textContent?.includes(tok));
			let steerRows = 0;
			const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
			for (let n = walker.nextNode(); n; n = walker.nextNode()) {
				if (n.nodeValue?.includes(`contains the word ${tok}`) && !n.parentElement?.closest('[data-testid="queued-message-dock"]')) steerRows += 1;
			}
			const inTimeline = steerRows > 0;
			const key = JSON.stringify([Boolean(dock), labelText, rows, inDock, steerRows]);
			if (key === last) return;
			last = key;
			w.__samples.push({ t: performance.timeOrigin + performance.now(), dock: Boolean(dock), label: labelText, rows, inDock, inTimeline, steerRows });
		};
		snap();
		new MutationObserver(snap).observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true });
		w.__sampleTimer = setInterval(snap, 8);
		document.addEventListener("pointerdown", () => { w.__clickAt ??= performance.timeOrigin + performance.now(); }, { capture: true });
		performance.clearResourceTimings();
		performance.setResourceTimingBufferSize(5000);
	}, token);

	// 4. Record the window while the click plays out.
	const frames = [];
	cdpSession.on("Page.screencastFrame", async ({ data, metadata, sessionId }) => {
		const file = path.join(out, "frames", `${String(frames.length).padStart(4, "0")}.jpg`);
		fs.writeFileSync(file, Buffer.from(data, "base64"));
		frames.push({ file, t: metadata.timestamp * 1000 });
		await cdpSession.send("Page.screencastFrameAck", { sessionId }).catch(() => {});
	});
	await cdpSession.send("Page.startScreencast", { format: "jpeg", quality: 85, everyNthFrame: 1 });
	await page.waitForTimeout(700);
	await page.screenshot({ path: path.join(out, "1-queued.png") });

	const daemonAnswers = [];
	let steeredTurnId;
	page.on("request", (req) => { const m = /\/turns\/([^/]+)\/steer$/.exec(req.url()); if (m) steeredTurnId = m[1]; });
	page.on("response", async (res) => {
		if (!/\/conversation\?/.test(res.url()) || res.request().method() !== "GET") return;
		const at = Date.now();
		try {
			const body = await res.json();
			const turn = (body.turns ?? []).find((t) => t.id === steeredTurnId);
			daemonAnswers.push({ at, steeredTurnState: turn?.state ?? null, hasSteerRow: (body.activities ?? []).some((a) => a.detail?.event === "steer" && a.detail?.sourceTurnId === steeredTurnId) });
		} catch {}
	});
	await steerButton.click();
	log("steer_clicked");

	// 5. Wait until the dock is gone and the steered text is in the timeline.
	const settleBy = Date.now() + 30000;
	while (Date.now() < settleBy) {
		const done = await page.evaluate(() => {
			const s = window.__samples.at(-1);
			return Boolean(s && !s.dock && s.inTimeline);
		});
		if (done) break;
		await page.waitForTimeout(25);
	}
	await page.waitForTimeout(1200);
	await cdpSession.send("Page.stopScreencast");
	await page.screenshot({ path: path.join(out, "3-settled.png") });

	const { samples, clickAt, resources } = await page.evaluate(() => {
		clearInterval(window.__sampleTimer);
		return {
			samples: window.__samples,
			clickAt: window.__clickAt,
			resources: performance.getEntriesByType("resource")
				.filter((e) => /\/conversation(\?|$)|\/steer$/.test(e.name))
				.map((e) => ({ url: e.name.replace(/^https?:\/\/[^/]+/, ""), start: performance.timeOrigin + e.startTime, end: performance.timeOrigin + e.responseEnd, bytes: e.encodedBodySize })),
		};
	});

	const rel = (t) => Math.round(t - clickAt);
	const after = samples.filter((s) => s.t >= clickAt);
	// Intervals after the click: dock shown with no rows; message neither in the dock nor the timeline.
	const spans = (pred) => {
		const list = [];
		let open = null;
		for (const s of after) {
			if (pred(s) && open === null) open = s.t;
			if (!pred(s) && open !== null) { list.push([rel(open), rel(s.t)]); open = null; }
		}
		if (open !== null) list.push([rel(open), null]);
		return list;
	};
	const total = (list) => list.reduce((sum, [a, b]) => sum + ((b ?? NaN) - a), 0);
	const emptyDock = spans((s) => s.dock && s.rows === 0);
	const nowhere = spans((s) => !s.inDock && !s.inTimeline);
	const summary = {
		label, mode, latencyMs, token,
		emptyDockSpansMs: emptyDock, emptyDockTotalMs: total(emptyDock),
		messageNowhereSpansMs: nowhere, messageNowhereTotalMs: total(nowhere),
		dockGoneAtMs: rel(after.find((s) => !s.dock)?.t ?? NaN),
		inTimelineAtMs: rel(after.find((s) => s.inTimeline)?.t ?? NaN),
		daemonAnswersAfterClick: daemonAnswers.filter((a) => a.at >= clickAt).map((a) => ({ ...a, at: rel(a.at) })),
		firstDaemonAnswerWithoutQueuedTurnMs: rel(daemonAnswers.find((a) => a.at >= clickAt && a.steeredTurnState !== "queued")?.at ?? NaN),
		steerRowCounts: [...new Set(after.map((s) => s.steerRows))],
		requestsAfterClick: resources.filter((r) => r.end >= clickAt).map((r) => ({ ...r, start: rel(r.start), end: rel(r.end) })),
		samples: after.map((s) => ({ ...s, t: rel(s.t) })),
		frames: frames.map((f) => ({ file: path.basename(f.file), t: rel(f.t) })),
	};
	fs.writeFileSync(path.join(out, "summary.json"), JSON.stringify(summary, null, 1));

	// The still that shows the worst moment: an empty dock if there is one, else the first frame after the click.
	const pick = (ms) => summary.frames.filter((f) => f.t <= ms).at(-1) ?? summary.frames.find((f) => f.t >= 0);
	const at = emptyDock.length ? emptyDock[0][0] + Math.min(150, ((emptyDock[0][1] ?? emptyDock[0][0] + 300) - emptyDock[0][0]) / 2) : 60;
	const still = summary.frames.filter((f) => f.t >= 0 && f.t <= at + 40).at(-1) ?? pick(at);
	if (still) fs.copyFileSync(path.join(out, "frames", still.file), path.join(out, "2-after-click.jpg"));
	log("summary", { ...summary, samples: undefined, frames: `${frames.length} frames`, stillAtMs: still?.t });
	await browser.close();
})().catch((e) => { log("error", { message: String(e?.stack ?? e) }); process.exit(1); });
