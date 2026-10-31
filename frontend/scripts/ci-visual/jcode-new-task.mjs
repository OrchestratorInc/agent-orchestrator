#!/usr/bin/env node
// CI-only New Task evidence for the jcode harness (PR #7519).
//
// Attaches over CDP to the real AO Electron dev app (`npm run dev` with its
// managed daemon) and walks the path a user takes on the home New Task page:
// open the Agent picker and choose jcode, open the Model picker and choose the
// model jcode itself lists, type a task and submit it, then watch the spawned
// Terminal (TUI) session. Every checkpoint is screenshotted whatever its
// outcome. Nothing in the app, daemon or jcode is mocked or pre-seeded: the
// prompt and the reply are confirmed from jcode's own session file, not from AO.
//
// Usage: node scripts/ci-visual/jcode-new-task.mjs --out DIR --jcode-home DIR --run-file FILE --model ID
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

const HARNESS = "jcode";
const MARKER = "NEW_TASK_OK";
// "/no_think" is Qwen3's documented switch for a direct answer without a
// reasoning phase; the reply must still be exactly the marker. It sits mid
// sentence: a leading slash is a jcode command and a trailing one opens AO's
// composer skill menu.
const PROMPT = `Reply with exactly ${MARKER} and nothing else. /no_think Do not run any tools.`;

// The text of one assistant message as jcode stored it: its text parts only,
// without Qwen's <think> block, whitespace-normalized. Tool calls, reasoning
// parts and the user's own words never count.
function assistantText(message) {
	const parts = typeof message.content === "string"
		? [message.content]
		: (Array.isArray(message.content) ? message.content : [])
			.filter((part) => part && part.type === "text" && typeof part.text === "string")
			.map((part) => part.text);
	return parts.join(" ").replace(/<think>[\s\S]*?<\/think>/g, " ").replace(/\s+/g, " ").trim();
}

function parseArgs(argv) {
	const options = { cdp: "http://127.0.0.1:9222", out: "", jcodeHome: "", runFile: "", model: "", replyTimeoutMs: 25 * 60_000 };
	for (let i = 0; i < argv.length; i += 1) {
		const next = () => argv[++i];
		if (argv[i] === "--cdp") options.cdp = next();
		else if (argv[i] === "--out") options.out = next();
		else if (argv[i] === "--jcode-home") options.jcodeHome = next();
		else if (argv[i] === "--run-file") options.runFile = next();
		else if (argv[i] === "--model") options.model = next();
		else if (argv[i] === "--reply-timeout-ms") options.replyTimeoutMs = Number(next());
	}
	for (const key of ["out", "jcodeHome", "runFile", "model"]) {
		if (!options[key]) throw new Error(`--${key.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`)} is required`);
	}
	options.out = resolve(options.out);
	return options;
}

const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

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

// The daemon's port comes from the run file the dev app wrote; reads only.
function daemonBase(runFile) {
	const run = JSON.parse(readFileSync(runFile, "utf8"));
	return `http://127.0.0.1:${run.port}/api/v1`;
}

async function api(base, path, init) {
	const response = await fetch(`${base}/${path}`, { ...init, headers: { "content-type": "application/json", ...(init?.headers ?? {}) } });
	const text = await response.text();
	try {
		return { status: response.status, data: JSON.parse(text) };
	} catch {
		return { status: response.status, data: text.slice(0, 500) };
	}
}

// jcode's own record of the conversation: the user turn AO typed and the
// model's reply. jcode keeps a snapshot, $JCODE_HOME/sessions/<id>.json, and
// appends later saves to <id>.journal.jsonl until a checkpoint folds them in,
// so both are read; message ids are unique per session.
function journalMessages(snapshotPath) {
	let text = "";
	try {
		text = readFileSync(snapshotPath.replace(/\.json$/, ".journal.jsonl"), "utf8");
	} catch {
		return [];
	}
	return text.split("\n").flatMap((line) => {
		try {
			const entry = JSON.parse(line);
			return Array.isArray(entry.append_messages) ? entry.append_messages : [];
		} catch {
			// The last line may still be being written.
			return [];
		}
	});
}

function nativeConversation(jcodeHome) {
	const dir = join(jcodeHome, "sessions");
	let files = [];
	try {
		files = readdirSync(dir).filter((name) => /^session_.*\.json$/.test(name));
	} catch {
		return null;
	}
	for (const name of files) {
		let session;
		try {
			session = JSON.parse(readFileSync(join(dir, name), "utf8"));
		} catch {
			continue;
		}
		const seen = new Set();
		const messages = [...(Array.isArray(session.messages) ? session.messages : []), ...journalMessages(join(dir, name))]
			.filter((message) => message && !seen.has(message.id) && seen.add(message.id));
		const text = (message) => JSON.stringify(message.content ?? "");
		const user = messages.find((message) => message.role === "user" && text(message).includes(MARKER));
		if (!user) continue;
		const replies = messages.filter((message) => message.role === "assistant").map(assistantText).filter(Boolean);
		return {
			id: session.id,
			workingDir: session.working_dir,
			model: session.model,
			provider: session.provider_key,
			promptDelivered: true,
			// The final assistant answer must be the requested output itself, not a
			// longer reply that merely mentions it.
			replyHasMarker: replies.at(-1) === MARKER,
			lastReply: (replies.at(-1) ?? "").slice(0, 400),
			tokenUsage: messages.filter((message) => message.token_usage).map((message) => message.token_usage),
		};
	}
	return null;
}

const MENU_ITEMS = '[role="menuitem"], [role="menuitemradio"], [role="menuitemcheckbox"], [role="option"]';

async function main() {
	const options = parseArgs(process.argv.slice(2));
	mkdirSync(options.out, { recursive: true });
	const results = [];
	let browser;
	for (let attempt = 0; attempt < 90 && !browser; attempt += 1) {
		try {
			browser = await chromium.connectOverCDP(options.cdp);
		} catch {
			await sleep(2_000);
		}
	}
	if (!browser) throw new Error(`could not attach to the Electron app at ${options.cdp}`);
	let page;
	for (let attempt = 0; attempt < 180 && !page; attempt += 1) {
		page = browser.contexts().flatMap((context) => context.pages()).find((candidate) => {
			const url = candidate.url();
			return url && !url.startsWith("devtools://") && url !== "about:blank";
		});
		if (!page) await sleep(1_000);
	}
	if (!page) throw new Error("no AO renderer page appeared over CDP");
	await page.setViewportSize({ width: 1440, height: 900 }).catch(() => {});

	const checkpoint = async (id, title, run) => {
		let outcome;
		try {
			outcome = await run();
		} catch (error) {
			outcome = { status: "FAIL", observed: `error: ${String(error.message).split("\n")[0]}` };
		}
		const file = `${String(results.length + 1).padStart(2, "0")}-${id}-${outcome.status}.png`;
		await page.screenshot({ path: join(options.out, file) }).catch((error) => {
			outcome.screenshotError = error.message;
		});
		if (outcome.status === "FAIL") {
			// The page's accessible text, to see what was on screen instead.
			writeFileSync(join(options.out, `${id}-page-text.txt`), `${await page.locator("body").innerText().catch(() => "")}\n`);
		}
		results.push({ id, title, file, url: page.url(), at: new Date().toISOString(), ...outcome });
		console.log(`${outcome.status.padEnd(5)} ${id}: ${outcome.observed}`);
		return outcome;
	};

	const ready = await checkpoint("app-ready", "Real AO Electron app is up with its daemon", async () => {
		const up = await visible(page.getByRole("button", { name: "Settings" }), 240_000);
		return { status: up ? "OK" : "FAIL", observed: up ? "Settings control visible" : "app never became ready" };
	});
	const base = daemonBase(options.runFile);

	await checkpoint("readiness", "AO's own readiness for jcode before New Task", async () => {
		let snapshot;
		for (let attempt = 0; attempt < 40; attempt += 1) {
			const response = await api(base, "agents/readiness/ensure", { method: "POST", body: JSON.stringify({ agentIds: [HARNESS], purpose: "display" }) });
			snapshot = response.data?.agents?.find((agent) => agent?.id === HARNESS);
			if (snapshot?.installation?.state === "installed" && snapshot?.authentication?.freshness !== "checking") break;
			await sleep(3_000);
		}
		const models = await api(base, `agents/${HARNESS}/models`);
		return {
			status: snapshot?.installation?.state === "installed" ? "OK" : "FAIL",
			observed: `installation=${snapshot?.installation?.state} authentication=${snapshot?.authentication?.state} (${snapshot?.authentication?.reason ?? ""}); models=${(models.data?.models ?? []).map((model) => model.id).join(",")}`,
			readiness: snapshot,
			modelCatalog: models.data,
		};
	});

	// The home page is AO's New Chat page. Its turn settings carry one picker
	// for agent, model and effort; it appears once AO has a ready agent to
	// default to, and its Agent and Model submenus stay open on selection.
	const trigger = page.getByRole("button", { name: "Agent, model and reasoning effort for the next message" });
	const subTrigger = (label) => page.locator('[role="menuitem"][aria-haspopup="menu"]').filter({ hasText: new RegExp(`^\\s*${label}`) }).first();
	const submenuWith = (row) => page.locator('[role="menu"]').filter({ has: row }).last();
	const agentRow = page.locator('[role="menuitemradio"]').filter({ hasText: /\bjcode\b/i });
	const modelRow = page.locator('[role="menuitemradio"]').filter({ hasText: options.model });

	const agentPicker = await checkpoint("new-task-agent-picker", "New Task: Agent picker open, jcode offered", async () => {
		await page.evaluate(() => { window.location.hash = "#/"; });
		// A fresh install may open an optional "Connect GitHub" sign-in terminal
		// on the home page; close it without signing in.
		const github = page.locator("div").filter({ has: page.getByText("Connect GitHub", { exact: true }) }).filter({ has: page.getByText("Complete sign-in in this terminal") }).last();
		if (await visible(github, 3_000)) await github.getByRole("button").last().click().catch(() => {});
		// A fresh install shows "Get started"; its "Start a standalone agent"
		// entry opens New Task, as a user would.
		if (!(await visible(trigger, 10_000))) {
			const start = page.getByRole("button", { name: /Start a standalone agent/ });
			if (await visible(start, 30_000)) await start.first().click();
		}
		if (!(await visible(trigger, 60_000))) {
			const alert = await textOf(page.locator('[role="alert"]'));
			return { status: "FAIL", observed: `no agent/model picker on the New Task page${alert ? `; alert: ${alert}` : ""}` };
		}
		await trigger.first().click();
		if (!(await visible(subTrigger("Agent"), 10_000))) return { status: "FAIL", observed: "the picker did not open an Agent menu" };
		await subTrigger("Agent").click();
		const offered = await visible(agentRow, 10_000);
		return { status: offered ? "PASS" : "FAIL", observed: `Agent menu: ${await textOf(offered ? submenuWith(agentRow) : page.locator('[role="menu"]').last())}`, jcodeOffered: offered };
	});

	const agentSelected = agentPicker.status === "PASS" && await checkpoint("new-task-agent-selected", "New Task: jcode selected", async () => {
		await agentRow.first().click();
		await sleep(1_500);
		const checked = await agentRow.first().getAttribute("aria-checked").catch(() => null);
		const label = await textOf(subTrigger("Agent"));
		return { status: checked === "true" && /jcode/i.test(label) ? "PASS" : "FAIL", observed: `jcode row aria-checked=${checked}; Agent row shows: ${label}` };
	});

	const modelPicker = agentSelected?.status === "PASS" && await checkpoint("new-task-model-picker", "New Task: Model picker open with jcode's models", async () => {
		if (!(await visible(subTrigger("Model"), 60_000))) return { status: "FAIL", observed: "no Model menu after choosing jcode" };
		await subTrigger("Model").click();
		const listed = await visible(modelRow, 30_000);
		return { status: listed ? "PASS" : "FAIL", observed: `Model menu: ${(await textOf(listed ? submenuWith(modelRow) : page.locator('[role="menu"]').last())).slice(0, 500)}`, modelListed: listed };
	});

	const modelSelected = modelPicker?.status === "PASS" && await checkpoint("new-task-model-selected", "New Task: model selected", async () => {
		await modelRow.first().click();
		await sleep(1_500);
		const checked = await modelRow.first().getAttribute("aria-checked").catch(() => null);
		const row = await textOf(subTrigger("Model"));
		await page.keyboard.press("Escape");
		await sleep(500);
		const label = await textOf(trigger);
		return { status: checked === "true" && label.includes(options.model) ? "PASS" : "FAIL", observed: `model row aria-checked=${checked}; Model row: ${row}; picker shows: ${label}` };
	});

	let submitted = false;
	if (modelSelected?.status === "PASS") {
		await checkpoint("new-task-ready-to-submit", "New Task: task typed with jcode and the model chosen", async () => {
			const field = page.getByRole("combobox", { name: "Message the agent" }).first();
			await field.click();
			await field.fill(PROMPT);
			const submit = page.getByRole("button", { name: "Send message", exact: true }).first();
			const enabled = await visible(submit, 5_000) && !(await submit.isDisabled());
			return { status: enabled ? "OK" : "FAIL", observed: `send ${enabled ? "enabled" : "not available"}; composer: ${await textOf(field)}; picker: ${await textOf(trigger)}` };
		});
		await page.getByRole("button", { name: "Send message", exact: true }).first().click();
		submitted = true;
		// A Chat-only start error offers the Terminal UI instead; jcode is TUI
		// only, so AO should have asked for the TUI itself. Record, never click.
		if (await visible(page.getByRole("button", { name: "Create as Terminal UI" }), 5_000)) {
			await checkpoint("new-task-tui-fallback", "New Task offered Create as Terminal UI", async () => ({ status: "FAIL", observed: `AO did not start jcode as a TUI: ${await textOf(page.locator('[role="alert"]'))}` }));
		}
	}

	const running = submitted && await checkpoint("running-tui", "Spawned jcode task running in the Terminal (TUI)", async () => {
		// AO keeps other terminal instances mounted but hidden; only the visible
		// one is this session's, and it must show native jcode with the task.
		const terminal = page.locator('[data-testid="session-terminal"]:visible');
		const shown = await visible(terminal, 120_000);
		const onSession = /#\/(?:projects\/[^/]+\/)?sessions\/[^/?]+/.test(page.url());
		let screen = "";
		for (let attempt = 0; attempt < 30 && !(/jcode/.test(screen) && screen.includes(MARKER)); attempt += 1) {
			screen = shown ? (await terminal.first().innerText().catch(() => "")).replace(/\s+/g, " ") : "";
			if (!(/jcode/.test(screen) && screen.includes(MARKER))) await sleep(2_000);
		}
		const nativeOnScreen = /jcode/.test(screen) && screen.includes(MARKER);
		let session;
		for (let attempt = 0; attempt < 60 && !session; attempt += 1) {
			const listed = await api(base, "sessions");
			const sessions = listed.data?.sessions ?? listed.data ?? [];
			session = (Array.isArray(sessions) ? sessions : []).find((candidate) => candidate?.harness === HARNESS);
			if (!session) await sleep(2_000);
		}
		let native = null;
		const deadline = Date.now() + 180_000;
		while (Date.now() < deadline && !native) {
			native = nativeConversation(options.jcodeHome);
			if (!native) await sleep(3_000);
		}
		const pass = shown && onSession && nativeOnScreen && session?.mode === "tui" && native?.promptDelivered === true;
		return {
			status: pass ? "PASS" : "FAIL",
			observed: `terminal ${shown ? "visible" : "missing"} on ${onSession ? "the session route" : `unexpected route ${page.url()}`}; jcode TUI with the task on screen: ${nativeOnScreen}; AO session ${session?.id ?? "none"} mode=${session?.mode} activity=${session?.activity?.state}; jcode session ${native?.id ?? "none"} has the task: ${Boolean(native?.promptDelivered)}`,
			session: session && { id: session.id, harness: session.harness, mode: session.mode, activity: session.activity, model: session.metadata?.model ?? session.model },
			native,
		};
	});

	if (running?.status === "PASS") {
		await checkpoint("reply", `jcode replied ${MARKER} in the Terminal`, async () => {
			const deadline = Date.now() + options.replyTimeoutMs;
			let native = nativeConversation(options.jcodeHome);
			while (Date.now() < deadline && !native?.replyHasMarker) {
				await sleep(5_000);
				native = nativeConversation(options.jcodeHome);
			}
			await sleep(2_000);
			return {
				status: native?.replyHasMarker ? "PASS" : "FAIL",
				observed: native?.replyHasMarker ? `final reply recorded by jcode: ${native.lastReply}` : `final reply is not exactly ${MARKER} within ${Math.round(options.replyTimeoutMs / 60_000)} min; last reply: ${native?.lastReply ?? "none"}`,
				native,
			};
		});
	}

	const summary = Object.fromEntries(["new-task-agent-picker", "new-task-model-picker", "running-tui", "reply"].map((id) => [id, results.find((item) => item.id === id)?.status ?? "NOT_RUN"]));
	writeFileSync(join(options.out, "results.json"), `${JSON.stringify({ harness: HARNESS, model: options.model, appReady: ready.status, summary, results }, null, 2)}\n`);
	console.log(JSON.stringify(summary));
	await browser.close().catch(() => {});
	if (["new-task-agent-picker", "new-task-model-picker", "running-tui", "reply"].some((id) => summary[id] !== "PASS")) process.exitCode = 1;
}

main().catch((error) => {
	console.error(error);
	process.exitCode = 1;
});
