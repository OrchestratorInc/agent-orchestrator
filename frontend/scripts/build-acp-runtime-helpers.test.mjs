// @vitest-environment node
import {
	existsSync,
	mkdirSync,
	mkdtempSync,
	readFileSync,
	readdirSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
	archiveExtraction,
	createWorkDirectory,
	npmInvocation,
	patchClaudeContextUsage,
	patchClaudeFoldedPromptSettlement,
	patchClaudeHibernationCheck,
	patchClaudeRetryDetails,
	patchClaudeStaleIdleDebt,
	pruneNodeDistribution,
	runtimeSourceFiles,
} from "./build-acp-runtime-helpers.mjs";

const temporaryDirectories = [];

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) {
		rmSync(directory, { recursive: true, force: true });
	}
});

describe("createWorkDirectory", () => {
	it("places extraction on the output filesystem", () => {
		const outputRoot = temporaryDirectory();
		const workDirectory = createWorkDirectory(outputRoot);

		expect(dirname(workDirectory)).toBe(outputRoot);
		expect(existsSync(workDirectory)).toBe(true);
	});
});

describe("runtimeSourceFiles", () => {
	it("packages and fingerprints the ACP runtime manifest", () => {
		expect(runtimeSourceFiles()).toEqual([
			"package.json",
			"package-lock.json",
		]);
	});
});

describe("npmInvocation", () => {
	it("runs the parent npm CLI through Node on Windows", () => {
		expect(
			npmInvocation(["ci", "--omit=dev"], {
				platform: "win32",
				execPath: "C:\\node\\node.exe",
				npmExecPath: "C:\\node\\node_modules\\npm\\bin\\npm-cli.js",
				commandInterpreter: "C:\\Windows\\System32\\cmd.exe",
			}),
		).toEqual({
			command: "C:\\node\\node.exe",
			args: ["C:\\node\\node_modules\\npm\\bin\\npm-cli.js", "ci", "--omit=dev"],
		});
	});

	it("falls back to cmd.exe for a directly invoked Windows build script", () => {
		expect(
			npmInvocation(["ci"], {
				platform: "win32",
				npmExecPath: null,
				commandInterpreter: "C:\\Windows\\System32\\cmd.exe",
			}),
		).toEqual({
			command: "C:\\Windows\\System32\\cmd.exe",
			args: ["/d", "/s", "/c", "npm.cmd", "ci"],
		});
	});

	it("invokes npm directly on Unix when no parent npm CLI is available", () => {
		expect(npmInvocation(["ci"], { platform: "linux", npmExecPath: null })).toEqual({
			command: "npm",
			args: ["ci"],
		});
	});
});

describe("patchClaudeRetryDetails", () => {
	it("keeps Claude's retry delay in the published session failure", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
                            case "api_retry": {
                                const title = "retrying";
                                await publishSessionFailure(message.error_status === null
                                    ? "transport_lost"
                                    : providerFailureCategory(message.error), {
                                    title,
                                    severity: "warning",
                                });
                                break;
                            }
                            case "model_refusal_fallback": {
`);

		expect(patchClaudeRetryDetails(adapterPath)).toBe(true);
		expect(patchClaudeRetryDetails(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");
		expect(patched).toContain("message.retry_delay_ms / 1000");
		expect(patched).toContain("Trying again in ${retryDelay}.");
		expect(patched).toContain("details: retryDetails");
	});
});

describe("patchClaudeHibernationCheck", () => {
	it("keeps live native tasks awake and allows sleep after they settle", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
			// session.liveBackgroundTasks.set(message.task_id
			connection.onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params));
		`);
		expect(patchClaudeHibernationCheck(adapterPath)).toBe(true);
		expect(patchClaudeHibernationCheck(adapterPath)).toBe(false);
		const handlers = new Map();
		const connection = { onRequest: (method, parser, handler) => {
			expect(typeof parser.parse).toBe("function");
			expect(typeof handler).toBe("function");
			handlers.set(method, (ctx) => handler({ params: parser.parse(ctx.params) }));
			return connection;
		} };
		const tasks = new Map();
		const agent = { sessions: { native: { liveBackgroundTasks: tasks } } };
		new Function("connection", "agent", "GOAL_CONTROL_METHOD", "parseGoalRequest", "RequestError", readFileSync(adapterPath, "utf8"))(
			connection, agent, "goal", params => params, { invalidParams: () => new Error("invalid session") },
		);
		const check = handlers.get("_ao/session/can_hibernate");
		const ctx = { params: { sessionId: "native" } };
		expect(check(ctx)).toEqual({ canHibernate: true });
		tasks.set("server", { isSubagent: false });
		expect(check(ctx)).toEqual({ canHibernate: false });
		tasks.delete("server");
		expect(check(ctx)).toEqual({ canHibernate: true });
		tasks.set("ended", { isSubagent: true, endedPerLevel: "ended" });
		expect(check(ctx)).toEqual({ canHibernate: true });
		for (const sessionId of [undefined, "missing", "__proto__"]) {
			expect(() => check({ params: { sessionId } })).toThrow("invalid session");
		}
		agent.sessions.native.queryClosed = true;
		expect(() => check(ctx)).toThrow("invalid session");
	});

	it("fails packaging if the pinned native task registry changes", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, "// incompatible adapter");
		expect(() => patchClaudeHibernationCheck(adapterPath)).toThrow("native-task hibernation check");
	});
});

describe("patchClaudeContextUsage", () => {
	it("publishes the SDK context snapshot through ACP after a result", async () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
                            // Send usage_update notification
                            if (lastAssistantTotalUsage !== null) {
                                await sendUpdate({
                                    update: {
                                        used: lastAssistantTotalUsage,
                                        size: session.contextWindowSize,
                                    },
                                });
                            }
                            if (session.cancelled) {
`);

		expect(patchClaudeContextUsage(adapterPath)).toBe(true);
		expect(patchClaudeContextUsage(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");
		expect(patched).toContain("session.query.getContextUsage()");
		expect(patched).toContain("lastAssistantTotalUsage = contextUsage.totalTokens");
		expect(patched).toContain("session.contextWindowSize = contextUsage.rawMaxTokens");

		const start = patched.indexOf("// AO: use the SDK's context snapshot.");
		const end = patched.indexOf("if (session.cancelled) {", start);
		const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
		const run = new AsyncFunction("session", "sendUpdate", `
			let lastAssistantTotalUsage = 9;
			${patched.slice(start, end)}
		`);
		const updates = [];
		const session = {
			contextWindowSize: 100,
			contextWindowAuthoritative: false,
			query: { getContextUsage: async () => ({ totalTokens: 17, rawMaxTokens: 200 }) },
		};
		await run.call({ logger: { error: () => {} } }, session, (notification) => {
			updates.push(notification.update);
		});
		expect(updates.map(({ used, size }) => [used, size])).toEqual([[17, 200]]);
		expect(session.contextWindowAuthoritative).toBe(true);

		const fallback = {
			contextWindowSize: 100,
			contextWindowAuthoritative: false,
			query: { getContextUsage: async () => { throw new Error("unavailable"); } },
		};
		const fallbackUpdates = [];
		await run.call({ logger: { error: () => {} } }, fallback, (notification) => {
			fallbackUpdates.push(notification.update);
		});
		expect(fallbackUpdates.map(({ used, size }) => [used, size])).toEqual([[9, 100]]);
		expect(fallback.contextWindowAuthoritative).toBe(false);

		const zero = {
			contextWindowSize: 100,
			contextWindowAuthoritative: false,
			query: { getContextUsage: async () => ({ totalTokens: 0, rawMaxTokens: 200 }) },
		};
		const zeroUpdates = [];
		await run.call({ logger: { error: () => {} } }, zero, (notification) => {
			zeroUpdates.push(notification.update);
		});
		expect(zeroUpdates.map(({ used, size }) => [used, size])).toEqual([[9, 100]]);
		expect(zero.contextWindowAuthoritative).toBe(false);
	});
});

describe("patchClaudeFoldedPromptSettlement", () => {
	it("routes a task-notification result that names a pending prompt to the user lane", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
        const findUnsettledTurn = (uuid) => (session.turnQueue ?? []).find((t) => t.promptUuid === uuid && !t.settled);
                    case "result": {
                        const isAutonomousResult = message.origin != null && AUTONOMOUS_RESULT_ORIGINS.has(message.origin.kind);
                        try {
`);

		expect(patchClaudeFoldedPromptSettlement(adapterPath)).toBe(true);
		expect(patchClaudeFoldedPromptSettlement(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");

		const start = patched.indexOf("// AO: a result naming a pending prompt answers it.");
		const end = patched.indexOf("try {", start);
		const classify = new Function("message", "session", `
			const AUTONOMOUS_RESULT_ORIGINS = new Set(["task-notification", "peer"]);
			const isHeldOpen = (turn) => turn.deferredSettle !== undefined && !turn.settled;
			const findUnsettledTurn = (uuid) => (session.turnQueue ?? []).find((t) => t.promptUuid === uuid && !t.settled);
			${patched.slice(start, end)}
			return isAutonomousResult;
		`);
		const notification = { kind: "task-notification" };
		const session = {
			turnQueue: [
				{ promptUuid: "pending", settled: false },
				{ promptUuid: "settled", settled: true },
				{ promptUuid: "held", settled: false, deferredSettle: { stopReason: "end_turn" } },
			],
		};

		expect(classify({ origin: notification, user_message_uuids: ["pending"] }, session)).toBe(false);
		expect(classify({ origin: notification, user_message_uuid: "pending" }, session)).toBe(false);
		expect(classify({ origin: notification, user_message_uuids: ["settled", "held", "unknown"] }, session)).toBe(true);
		expect(classify({ origin: notification }, session)).toBe(true);
		expect(classify({ origin: { kind: "human" } }, session)).toBe(false);
		expect(classify({}, session)).toBe(false);
	});

	it("fails packaging when the adapter's classification changes", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, "const isAutonomousResult = isAutonomous(message);\n");

		expect(() => patchClaudeFoldedPromptSettlement(adapterPath)).toThrow(/folded-prompt patch/);
	});
});

describe("patchClaudeStaleIdleDebt", () => {
	it("drops unpaid idle debt when Claude starts running again", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
                            case "session_state_changed": {
                                session.lastSessionState = message.state;
                                if (message.state === "idle") {
                                    if (session.owedTrailingIdles > 0) {
                                        session.owedTrailingIdles--;
                                    }
                                }
                                break;
                            }
`);

		expect(patchClaudeStaleIdleDebt(adapterPath)).toBe(true);
		expect(patchClaudeStaleIdleDebt(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");

		const start = patched.indexOf("// AO: drop idle debt that can no longer be paid.");
		const end = patched.indexOf("break;", start);
		const onState = new Function("message", "session", `${patched.slice(start, end)}`);
		const session = { lastSessionState: "idle", owedTrailingIdles: 2 };

		onState({ state: "idle" }, session);
		expect(session.owedTrailingIdles).toBe(1);
		onState({ state: "running" }, session);
		expect(session.owedTrailingIdles).toBe(0);
		session.owedTrailingIdles = 1;
		onState({ state: "running" }, session);
		expect(session.owedTrailingIdles).toBe(1);
		onState({ state: "idle" }, session);
		expect(session.owedTrailingIdles).toBe(0);
		expect(session.lastSessionState).toBe("idle");
	});

	it("fails packaging when the adapter's state handling changes", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, 'case "session_state_changed": {\n');

		expect(() => patchClaudeStaleIdleDebt(adapterPath)).toThrow(/idle-debt patch/);
	});
});

describe("pruneNodeDistribution", () => {
	it("removes Unix package-manager links before deleting their targets", () => {
		const nodeRoot = temporaryDirectory();
		const bin = join(nodeRoot, "bin");
		const npmBin = join(nodeRoot, "lib", "node_modules", "npm", "bin");
		mkdirSync(bin, { recursive: true });
		mkdirSync(npmBin, { recursive: true });
		writeFileSync(join(bin, "node"), "node");
		writeFileSync(join(npmBin, "npm-cli.js"), "npm");
		writeFileSync(join(npmBin, "npx-cli.js"), "npx");
		symlinkSync("../lib/node_modules/npm/bin/npm-cli.js", join(bin, "npm"));
		symlinkSync("../lib/node_modules/npm/bin/npx-cli.js", join(bin, "npx"));
		symlinkSync("../lib/node_modules/corepack/dist/corepack.js", join(bin, "corepack"));

		pruneNodeDistribution(nodeRoot);

		expect(readdirSync(bin)).toEqual(["node"]);
		expect(existsSync(join(nodeRoot, "lib"))).toBe(false);
	});

	it("removes package-manager files and modules from a Windows distribution", () => {
		const nodeRoot = temporaryDirectory();
		writeFileSync(join(nodeRoot, "node.exe"), "node");
		writeFileSync(join(nodeRoot, "LICENSE"), "license");
		for (const name of ["corepack", "corepack.cmd", "npm", "npm.cmd", "npx", "npx.cmd"]) {
			writeFileSync(join(nodeRoot, name), name);
		}
		mkdirSync(join(nodeRoot, "node_modules", "npm"), { recursive: true });

		pruneNodeDistribution(nodeRoot);

		expect(readdirSync(nodeRoot).sort()).toEqual(["LICENSE", "node.exe"]);
	});
});

function temporaryDirectory() {
	const directory = mkdtempSync(join(tmpdir(), "ao-acp-runtime-test-"));
	temporaryDirectories.push(directory);
	return directory;
}

describe("archiveExtraction", () => {
	// Extraction must not go through PowerShell's Expand-Archive: it is bound by
	// MAX_PATH, and with LongPathsEnabled=0 a deep checkout pushes Node's bundled
	// npm tree past 260 characters, where it fails while still exiting zero.
	it("uses bsdtar for the Windows zip", () => {
		expect(archiveExtraction("C:\\w\\node.zip", "C:\\w", {
			platform: "win32",
			systemRoot: "C:\\Windows",
		})).toEqual({
			command: "C:\\Windows\\System32\\tar.exe",
			args: ["-xf", "C:\\w\\node.zip", "-C", "C:\\w"],
		});
	});

	it("keeps gzip handling on the other platforms", () => {
		for (const platform of ["darwin", "linux"]) {
			expect(archiveExtraction("/w/node.tar.gz", "/w", { platform })).toEqual({
				command: "tar",
				args: ["-xzf", "/w/node.tar.gz", "-C", "/w"],
			});
		}
	});

	it("never shells out to a command interpreter", () => {
		for (const platform of ["win32", "darwin", "linux"]) {
			const { command } = archiveExtraction("/w/a", "/w", { platform, systemRoot: "C:\\Windows" });
			expect(command).not.toMatch(/powershell|cmd\.exe|\bsh\b/i);
		}
	});
});
