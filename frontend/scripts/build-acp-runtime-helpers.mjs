import { mkdtempSync, readFileSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { join, win32 } from "node:path";

const ROOT_BUILD_TOOLS = ["corepack", "corepack.cmd", "npm", "npm.cmd", "npx", "npx.cmd"];
const BIN_BUILD_TOOLS = ["corepack", "npm", "npx"];
const BUILD_ONLY_CONTENT = ["include", "lib", "node_modules", "share", "CHANGELOG.md", "README.md"];

export function runtimeSourceFiles() {
	return ["package.json", "package-lock.json"];
}

export function createWorkDirectory(outputRoot) {
	// Windows runners commonly keep the checkout on D: and the OS temp directory
	// on C:. Keep extraction beside its destination so the final rename remains
	// an atomic, same-filesystem operation on every platform.
	return mkdtempSync(join(outputRoot, ".node-download-"));
}

export function npmInvocation(
	args,
	{
		platform = process.platform,
		execPath = process.execPath,
		npmExecPath = process.env.npm_execpath,
		commandInterpreter = process.env.ComSpec,
	} = {},
) {
	// npm exposes the JavaScript entry point of the npm instance running this
	// package script. Invoking it with Node avoids the npm.cmd shell boundary on
	// Windows and keeps the nested install on the same npm version as the build.
	if (npmExecPath) {
		return { command: execPath, args: [npmExecPath, ...args] };
	}
	if (platform === "win32") {
		return {
			command: commandInterpreter || "cmd.exe",
			args: ["/d", "/s", "/c", "npm.cmd", ...args],
		};
	}
	return { command: "npm", args };
}

/**
 * Preserve Claude's API-retry backoff in the ACP session-failure extension.
 *
 * claude-agent-acp 0.70 publishes retry count and category, but drops the
 * SDK's retry_delay_ms before the event reaches ACP clients. AO patches the
 * pinned compiled adapter during packaging so the extension's ordinary
 * `details` field carries the missing timing. The narrow block match is a
 * deliberate upgrade tripwire: if upstream changes this code, packaging fails
 * instead of silently returning to an unobservable retry loop.
 */
export function patchClaudeRetryDetails(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	const caseStart = source.indexOf('case "api_retry": {');
	const caseEnd = source.indexOf('case "model_refusal_fallback":', caseStart);
	if (caseStart < 0 || caseEnd < 0) {
		throw new Error("claude-agent-acp no longer contains the expected api_retry block");
	}

	let block = source.slice(caseStart, caseEnd);
	if (block.includes("const retryDetails =")) return false;

	const publishMarker = "await publishSessionFailure";
	const publishAt = block.indexOf(publishMarker);
	const severityMarker = '                                    severity: "warning",';
	if (publishAt < 0 || !block.includes(severityMarker)) {
		throw new Error("claude-agent-acp api_retry block no longer matches AO's retry patch");
	}

	const retryDetailLines = [
		"const retryDelay = message.retry_delay_ms >= 1000",
		"                                    ? `${Number((message.retry_delay_ms / 1000).toFixed(1))}s`",
		"                                    : `${message.retry_delay_ms}ms`;",
		'                                const retryDetails = `${message.error_status === null ? "Connection error." : `API error ${message.error_status}.`} Trying again in ${retryDelay}.`;',
		"                                ",
	].join("\n");
	block = block.slice(0, publishAt) + retryDetailLines + block.slice(publishAt);
	block = block.replace(severityMarker, `${severityMarker}\n                                    details: retryDetails,`);

	writeFileSync(adapterPath, source.slice(0, caseStart) + block + source.slice(caseEnd));
	return true;
}

/**
 * claude-agent-acp 0.70 reports the last assistant message's token usage as
 * context occupancy. The SDK's getContextUsage returns its current retained
 * context estimate. Publish that snapshot after each result so AO's existing
 * usage_update projection receives the provider's own estimate.
 *
 * The snapshot can take a second or more on a large conversation, so it is
 * published after the result instead of before it: the turn settles at once
 * and the usage update follows.
 */
export function patchClaudeContextUsage(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	if (source.includes(CLAUDE_CONTEXT_USAGE_MARKER)) return false;
	const start = source.indexOf("// Send usage_update notification");
	const end = source.indexOf("if (session.cancelled) {", start);
	if (
		start < 0 ||
		end < 0 ||
		source.indexOf("// Send usage_update notification", start + 1) >= 0 ||
		collapseWhitespace(source.slice(start, end)) !== collapseWhitespace(CLAUDE_RESULT_USAGE_BLOCK)
	) {
		throw new Error("claude-agent-acp result usage block no longer matches AO's context patch");
	}
	writeFileSync(adapterPath, source.slice(0, start) + CLAUDE_CONTEXT_USAGE_BLOCK + source.slice(end));
	return true;
}

const CLAUDE_CONTEXT_USAGE_MARKER = "// AO: publish the SDK's context snapshot without holding the turn open.";

const CLAUDE_RESULT_USAGE_BLOCK = `// Send usage_update notification
if (lastAssistantTotalUsage !== null) {
    await sendUpdate({
        sessionId: params.sessionId,
        update: {
            sessionUpdate: "usage_update",
            used: lastAssistantTotalUsage,
            size: session.contextWindowSize,
            cost: {
                amount: message.total_cost_usd,
                currency: "USD",
            },
            ...(message.origin && {
                _meta: { "_claude/origin": message.origin },
            }),
        },
    });
}
`;

const CLAUDE_CONTEXT_USAGE_BLOCK = `${CLAUDE_CONTEXT_USAGE_MARKER}
                            if (lastAssistantTotalUsage !== null) {
                                const messageUsage = lastAssistantTotalUsage;
                                const resultModelId = session.models.currentModelId;
                                void (async () => {
                                    let used = messageUsage;
                                    let contextUsageTimer;
                                    try {
                                        const contextUsage = await Promise.race([
                                            session.query.getContextUsage(),
                                            new Promise((_, reject) => {
                                                contextUsageTimer = setTimeout(() => reject(new Error('Claude SDK context usage timed out')), 2000);
                                            }),
                                        ]);
                                        if (Number.isFinite(contextUsage.totalTokens) && contextUsage.totalTokens > 0 &&
                                            Number.isFinite(contextUsage.rawMaxTokens) && contextUsage.rawMaxTokens > 0) {
                                            used = contextUsage.totalTokens;
                                            // Only while the turn's usage is still current; a newer turn resets it.
                                            if (lastAssistantTotalUsage === messageUsage) {
                                                lastAssistantTotalUsage = used;
                                            }
                                            // A model switch since this result already seeded its own window.
                                            if (session.models.currentModelId === resultModelId) {
                                                session.contextWindowSize = contextUsage.rawMaxTokens;
                                                session.contextWindowAuthoritative = true;
                                            }
                                        }
                                    } catch (error) {
                                        this.logger.error('Failed to fetch Claude SDK context usage:', error);
                                    } finally {
                                        clearTimeout(contextUsageTimer);
                                    }
                                    try {
                                        await sendUpdate({
                                            sessionId: params.sessionId,
                                            update: {
                                                sessionUpdate: "usage_update",
                                                used,
                                                size: session.contextWindowSize,
                                                cost: {
                                                    amount: message.total_cost_usd,
                                                    currency: "USD",
                                                },
                                                ...(message.origin && {
                                                    _meta: { "_claude/origin": message.origin },
                                                }),
                                            },
                                        });
                                    } catch (error) {
                                        this.logger.error('Failed to publish Claude context usage:', error);
                                    }
                                })();
                            }
                            `;

function collapseWhitespace(text) {
	return text.replace(/\s+/g, " ").trim();
}

/**
 * Learn the model's real context window during a fresh session's first turn.
 *
 * claude-agent-acp 0.70 starts a fresh session at a 200k default unless the
 * model's id, label or description carries a "1m" token or this adapter process
 * has already cached its window. It learns the real window only from the
 * turn's result. A worker that does its whole task in one turn streams
 * `size: 200000` throughout, so a 1M model such as claude-fable-5-1 reads as
 * full at 200k (issue #8005). The SDK services getContextUsage once a turn is
 * running, so ask it at the turn's first assistant message without holding the
 * stream, and publish the window when it answers: the turn may be waiting on a
 * long tool and send no other usage_update meanwhile. The pinned SDK cannot
 * cancel the request, so a late answer is still used. Drop this patch when the
 * adapter learns the window before the first result.
 */
export function patchClaudeContextWindowProbe(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	if (source.includes(CLAUDE_WINDOW_PROBE_MARKER)) return false;
	const anchor = "const inferred = inferContextWindowFromModel(model);";
	const anchorAt = source.indexOf(anchor);
	const start = source.lastIndexOf("if (message.parent_tool_use_id === null &&", anchorAt);
	const end = source.indexOf("for (const notification of streamEventToAcpNotifications(", anchorAt);
	// The probe chains onto the heuristic's closing brace, so only whitespace may separate them.
	const heuristic = /const inferred = inferContextWindowFromModel\(model\);\s*if \(inferred !== null\) \{\s*session\.contextWindowSize = inferred;\s*\}/y;
	heuristic.lastIndex = anchorAt;
	const at = heuristic.test(source) ? heuristic.lastIndex : -1;
	if (anchorAt < 0 || start < 0 || end < 0 || at < 0 || source.indexOf(anchor, anchorAt + 1) >= 0 ||
		!startsLiveStreamCode(source, start) ||
		codeOnly(source.slice(start, end)) !== codeOnly(CLAUDE_STREAM_USAGE_BRANCH)) {
		throw new Error("claude-agent-acp streamed window inference no longer matches AO's context window patch");
	}
	writeFileSync(adapterPath, source.slice(0, at) + CLAUDE_WINDOW_PROBE + source.slice(at));
	return true;
}

// The pinned branch has only whole-line comments; anything else must match exactly.
function codeOnly(text) {
	return collapseWhitespace(text.replace(/^\s*\/\/.*$/gm, ""));
}

// codeOnly keeps every "/*" and "*/", so check only what sits before the compared branch: nothing
// but indentation on its first line, no block comment open, and stream_event as the nearest case.
// shortcut: a text scan, not a parse. A "/*" in an earlier string fails packaging. A string around
// the branch, or an earlier statement that skips it (a brace-less if, a break), goes unseen; parse
// the adapter if upstream ever does either.
function startsLiveStreamCode(source, at) {
	return !/\S/.test(source.slice(source.lastIndexOf("\n", at) + 1, at)) &&
		source.lastIndexOf("/*", at) <= source.lastIndexOf("*/", at) &&
		source.startsWith('case "stream_event": {', source.lastIndexOf('case "', at));
}

const CLAUDE_WINDOW_PROBE_MARKER = "// AO: ask the SDK for the model's real window.";

// The whole top-level stream branch: the guards the probe sits behind and the
// usage publish whose counter, sender and session id it reuses.
const CLAUDE_STREAM_USAGE_BRANCH = `if (message.parent_tool_use_id === null &&
    (message.event.type === "message_start" || message.event.type === "message_delta")) {
    if (message.event.type === "message_start") {
        lastAssistantUsage = snapshotFromUsage(message.event.message.usage);
        const model = message.event.message.model;
        if (model && model !== "<synthetic>") {
            lastAssistantModel = model;
            if (!session.contextWindowAuthoritative &&
                session.contextWindowSize === DEFAULT_CONTEXT_WINDOW) {
                const inferred = inferContextWindowFromModel(model);
                if (inferred !== null) {
                    session.contextWindowSize = inferred;
                }
            }
        }
    }
    else {
        const usage = message.event.usage;
        const prev = lastAssistantUsage ?? ZERO_USAGE;
        lastAssistantUsage = {
            input_tokens: usage.input_tokens ?? prev.input_tokens,
            output_tokens: usage.output_tokens,
            cache_read_input_tokens: usage.cache_read_input_tokens ?? prev.cache_read_input_tokens,
            cache_creation_input_tokens: usage.cache_creation_input_tokens ?? prev.cache_creation_input_tokens,
        };
    }
    const nextUsage = totalTokens(lastAssistantUsage);
    if (nextUsage !== lastAssistantTotalUsage) {
        lastAssistantTotalUsage = nextUsage;
        await sendUpdate({
            sessionId: params.sessionId,
            update: {
                sessionUpdate: "usage_update",
                used: nextUsage,
                size: session.contextWindowSize,
            },
        });
    }
}`;

const CLAUDE_WINDOW_PROBE = `
                                        ${CLAUDE_WINDOW_PROBE_MARKER}
                                        else if (session.aoWindowProbe?.models !== session.models ||
                                            (session.aoWindowProbe.settled && session.aoWindowProbe.turn !== session.activeTurn)) {
                                            // One probe per model selection while it is pending. A failed one is retried when
                                            // session.activeTurn changes: once per turn, twice if the turn's echo lands after
                                            // its first message. Every model switch replaces session.models, even a switch back.
                                            const probe = { models: session.models, turn: session.activeTurn, settled: false };
                                            session.aoWindowProbe = probe;
                                            void (async () => {
                                                try {
                                                    const { rawMaxTokens } = await session.query.getContextUsage();
                                                    // A result or a model switch while the probe ran already set the window.
                                                    if (!Number.isFinite(rawMaxTokens) || rawMaxTokens <= 0 ||
                                                        session.contextWindowAuthoritative || session.models !== probe.models) {
                                                        return;
                                                    }
                                                    const changed = rawMaxTokens !== session.contextWindowSize;
                                                    session.contextWindowSize = rawMaxTokens;
                                                    session.contextWindowAuthoritative = true;
                                                    if (changed && lastAssistantTotalUsage !== null) {
                                                        await sendUpdate({
                                                            sessionId: params.sessionId,
                                                            update: { sessionUpdate: "usage_update", used: lastAssistantTotalUsage, size: rawMaxTokens },
                                                        });
                                                    }
                                                } catch (error) {
                                                    this.logger.error('Failed to learn Claude context window:', error);
                                                } finally {
                                                    probe.settled = true;
                                                }
                                            })();
                                        }`;

// A UI projection of Claude's complete background set. The bridge's own
// liveBackgroundTasks also contains foreground calls for permission attribution;
// it must not be used as a background-work counter or changed by this reader.
function aoObserveBackgroundTasks(session, message) {
	const now = Date.now();
	const live = session.aoBackgroundLive ??= new Map();
	const known = session.aoBackgroundKnown ??= new Map();
	const native = session.aoBackgroundNative ??= new Set();
	const recent = session.aoBackgroundRecent ??= new Map();
	for (const [id, task] of known) {
		if (!native.has(id) && now - task.observedAt > 30000) known.delete(id);
	}
	for (const [id, task] of recent) {
		if (now - Date.parse(task.endedAt) >= 30000) recent.delete(id);
	}
	if (message.subtype === "background_tasks_changed") {
		// A removal level can precede its completion event even after a task
		// has run for hours. Retain its label for the following bookend.
		for (const id of native) {
			const record = known.get(id);
			if (record) record.observedAt = now;
		}
		native.clear();
		live.clear();
		for (const task of message.tasks) {
			const previous = known.get(task.task_id);
			const sameRun = !task.run_id || !previous?.runId || task.run_id === previous.runId;
			const record = {
				id: task.task_id, runId: task.run_id ?? (sameRun ? previous?.runId : undefined),
				kind: task.task_type === "local_bash" ? "shell" : task.task_type === "local_agent" ? "agent" : "other",
				description: task.description ?? "", state: "running", observedAt: now, wasBackground: true,
				ambient: task.ambient === true || (sameRun && previous?.ambient === true),
				// The run's own start when its task_started was seen, else the first snapshot.
				startedAt: (sameRun && previous?.startedAt) || new Date(now).toISOString(),
			};
			native.add(record.id);
			known.set(record.id, record);
			if (!record.ambient) live.set(record.id, record);
			recent.delete(record.id);
		}
		session.aoBackgroundReady = true;
	} else if (message.subtype === "task_started") {
		// A start may describe foreground work. Only a full background snapshot
		// grants membership; starts can enrich or hide an already known task.
		let record = known.get(message.task_id);
		if (!record || (message.run_id && record.runId && message.run_id !== record.runId)) {
			record = { id: message.task_id, runId: message.run_id, observedAt: now, startedAt: new Date(now).toISOString() };
			known.set(message.task_id, record);
		}
		if (record && (!message.run_id || !record.runId || record.runId === message.run_id)) {
			record.ambient ||= message.ambient === true || message.skip_transcript === true;
			if (record.ambient) live.delete(record.id);
		}
	} else {
		const record = known.get(message.task_id);
		if (!record?.wasBackground || (message.run_id && record.runId && message.run_id !== record.runId)) return;
		const status = message.subtype === "task_notification" ? message.status : message.patch?.status;
		if (["completed", "failed", "killed", "stopped"].includes(status)) {
			live.delete(record.id);
			if (!record.ambient && !recent.has(record.id)) {
				recent.set(record.id, { ...record, state: status === "killed" ? "stopped" : status, endedAt: new Date(now).toISOString() });
			}
		}
	}
	// Bound transient history even if a provider emits an unusually large burst.
	for (const id of known.keys()) {
		if (known.size <= 512) break;
		if (!native.has(id)) known.delete(id);
	}
	while (recent.size > 128) recent.delete(recent.keys().next().value);
}

// Claude Code names a background shell's live output file only in the text of
// the result that launched it ("Output is being written to: <path>."), and its
// command only in that call's input. Record both by task id before the adapter
// drops the call from its tool-use cache.
function aoObserveBackgroundLaunch(session, message) {
	const taskId = message.type === "user" ? message.tool_use_result?.backgroundTaskId : undefined;
	if (typeof taskId !== "string" || !taskId || !Array.isArray(message.message?.content)) return;
	const results = message.message.content.filter(block => block?.type === "tool_result");
	const fileName = `${taskId}.output`;
	let launch;
	for (const block of results) {
		const text = typeof block.content === "string" ? block.content
			: Array.isArray(block.content) ? block.content.map(part => typeof part?.text === "string" ? part.text : "").join("\n") : "";
		const outputPath = aoBackgroundOutputPath(text, fileName);
		if (!outputPath && results.length > 1) continue;
		const command = session.toolUseCache?.[block.tool_use_id]?.input?.command;
		launch = { command: typeof command === "string" ? command : undefined, outputPath };
		break;
	}
	if (!launch) return;
	const launches = session.aoBackgroundLaunches ??= new Map();
	launches.delete(taskId);
	launches.set(taskId, launch);
	while (launches.size > 128) launches.delete(launches.keys().next().value);
}

// The output file a launch result names, which ends with the task's file name.
// The path may contain spaces (a Windows profile such as "C:\Users\Jane Doe"),
// so it runs from "written to:" to the first file name after it when the
// sentence has that phrase, else from the path root nearest the file name.
function aoBackgroundOutputPath(text, fileName) {
	const root = /^(?:\/|[A-Za-z]:[\\/]|\\\\)/;
	const endsAt = (line, from) => {
		for (let end = line.indexOf(fileName, from); end >= 0; end = line.indexOf(fileName, end + 1)) {
			if (end > 0 && (line[end - 1] === "/" || line[end - 1] === "\\")) return end + fileName.length;
		}
		return -1;
	};
	for (const line of text.split(/\r?\n/)) {
		const anchor = /written to:\s*/i.exec(line);
		if (anchor) {
			const start = anchor.index + anchor[0].length;
			const end = endsAt(line, start);
			const path = end < 0 ? "" : line.slice(start, end).replace(/^['"(]/, "");
			if (root.test(path)) return path;
			continue;
		}
		const end = endsAt(line, 0);
		if (end < 0) continue;
		const head = line.slice(0, end);
		const starts = [...head.matchAll(/(?:^|[\s'"(])(?=\/|[A-Za-z]:[\\/]|\\\\)/g)].map(m => m.index + m[0].length);
		for (const start of starts.reverse()) {
			const path = head.slice(start);
			if (root.test(path)) return path;
		}
	}
	return undefined;
}

// The tail of a task's output file, at most maxBytes, starting on a whole line.
// Null when the file is gone or not a regular file. The agent can write that
// directory, so a symlink is not followed and a FIFO never blocks the read.
async function aoReadBackgroundOutput(path, maxBytes) {
	const { lstat, open, realpath } = process.getBuiltinModule("node:fs/promises");
	const { constants } = process.getBuiltinModule("node:fs");
	const { basename, dirname, join } = process.getBuiltinModule("node:path");
	// Never a network share: a forged result line could point the read at one.
	if (/^(?:\\\\|\/\/)/.test(path)) return null;
	let file;
	try {
		if (!(await lstat(path)).isFile()) return null;
		file = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0));
	} catch {
		return null;
	}
	try {
		const stats = await file.stat();
		if (!stats.isFile()) return null;
		// Windows has no O_NOFOLLOW, so a link swapped in after the lstat is
		// caught here: the opened name must still be the file itself.
		if (await realpath(path) !== join(await realpath(dirname(path)), basename(path))) return null;
		const start = Math.max(0, stats.size - maxBytes);
		const buffer = Buffer.alloc(stats.size - start);
		const { bytesRead } = await file.read(buffer, 0, buffer.length, start);
		let output = buffer.subarray(0, bytesRead).toString("utf8");
		if (start > 0) {
			// Drop the partial first line only when a complete line follows it.
			const at = output.replace(/[\r\n]+$/, "").indexOf("\n");
			if (at >= 0) output = output.slice(at + 1);
		}
		return { output, truncated: start > 0 };
	} finally {
		await file.close();
	}
}

export function patchClaudeBackgroundTasks(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	const method = '"_ao/session/background_tasks"';
	if (source.includes(method)) return false;
	const marker = ".onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))";
	const cases = ["task_started", "task_updated", "task_notification", "background_tasks_changed"];
	const messages = /case "user":\s*case "assistant": \{/g;
	if (!source.includes(marker) || cases.some(name => source.split(`case "${name}":`).length !== 2) || source.match(messages)?.length !== 1) {
		throw new Error("claude-agent-acp no longer matches AO's background-task projection");
	}
	source = source.replace(messages, match => `${match}\n                        aoObserveBackgroundLaunch(session, message);`);
	for (const name of cases) {
		source = source.replace(`case "${name}":`, `case "${name}":\n                                aoObserveBackgroundTasks(session, message);`);
	}
	const registration = `.onRequest(${method}, { parse: params => params }, (ctx) => {
            const id = ctx.params?.sessionId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed) throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            // Until a native level arrives, absence of events does not prove empty.
            if (!session.aoBackgroundReady) throw RequestError.methodNotFound("Background task snapshot unavailable.");
            aoObserveBackgroundTasks(session, { subtype: "prune" });
            const tasks = [...session.aoBackgroundLive.values(), ...session.aoBackgroundRecent.values()];
            return { tasks: tasks.map(({ id, runId, kind, description, state, startedAt, endedAt }) => ({
                id, runId, kind, description, command: session.aoBackgroundLaunches?.get(id)?.command, state, startedAt, endedAt, stoppable: state === "running",
            })) };
        })
        .onRequest("_ao/session/background_task_output", { parse: params => params }, async (ctx) => {
            const id = ctx.params?.sessionId;
            const taskId = ctx.params?.taskId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed) throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            // Only the file Claude Code named for this task, never a path from the request.
            const path = typeof taskId === "string" ? session.aoBackgroundLaunches?.get(taskId)?.outputPath : undefined;
            if (!path) throw RequestError.invalidParams(undefined, "This background task has no output file.");
            const maxBytes = Math.min(Math.max(Number(ctx.params?.maxBytes) || 65536, 1024), 1048576);
            const tail = await aoReadBackgroundOutput(path, maxBytes);
            if (!tail) throw RequestError.invalidParams(undefined, "This background task's output file is gone.");
            return tail;
        })
        .onRequest("_ao/session/stop_background_task", { parse: params => params }, async (ctx) => {
            const id = ctx.params?.sessionId;
            const taskId = ctx.params?.taskId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed) throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            // Only work in the background projection above: never a foreground call.
            if (typeof taskId !== "string" || !session.aoBackgroundLive?.has(taskId)) throw RequestError.invalidParams(undefined, "Background task is not running.");
            // The SDK settles it with a task_notification "stopped", which the projection records.
            try {
                await session.query.stopTask(taskId);
            } catch (error) {
                // A rejection may precede native completion. Reconcile briefly,
                // but never acknowledge a failed stop while the task remains live.
                const settleUntil = Date.now() + 1000;
                while (session.aoBackgroundLive?.has(taskId) && !session.queryClosed && Date.now() < settleUntil) {
                    await new Promise(resolve => setTimeout(resolve, Math.min(25, settleUntil - Date.now())));
                }
                if (!session.aoBackgroundLive?.has(taskId)) throw RequestError.invalidParams(undefined, "Background task is not running.");
                if (session.queryClosed) throw error;
                throw RequestError.internalError({ retryable: true }, "Stop not acknowledged; try again.");
            }
            return {};
        })
        `;
	const helpers = [aoObserveBackgroundTasks, aoObserveBackgroundLaunch, aoBackgroundOutputPath, aoReadBackgroundOutput].map(String).join("\n");
	writeFileSync(adapterPath, `${helpers}\n${source.replace(marker, registration + marker)}`);
	return true;
}

// Reuse the pinned bridge's native task registry. Ending its process loses the
// task monitor even when a background shell's process group survives. Claude
// Code also starts cycles on its own after an AO turn, for example to answer a
// finished task's notification. AO has no turn for those, and the finished
// task has already left the registry, so the SDK's last reported state is the
// only signal that the agent is still working. A query that never reports idle
// again keeps its host awake, which is the safe side.
export function patchClaudeHibernationCheck(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	const method = '"_ao/session/can_hibernate"';
	if (source.includes(method)) return false;
	const marker = ".onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))";
	// task_updated and task_notification both drop a finished task.
	const ended = "session.liveBackgroundTasks.delete(message.task_id);";
	if (!source.includes(marker) || !source.includes("session.liveBackgroundTasks.set(message.task_id") ||
		!source.includes("session.lastSessionState = message.state;") || source.split(ended).length !== 3) {
		throw new Error("claude-agent-acp no longer matches AO's native-task hibernation check");
	}
	source = source.replaceAll(ended, `${ended} session.aoTaskEndedAt = Date.now();`);
	const registration = `.onRequest(${method}, { parse: params => params }, (ctx) => {
            const id = ctx.params?.sessionId;
            const session = typeof id === "string" && Object.hasOwn(agent.sessions, id) ? agent.sessions[id] : undefined;
            if (!session || session.queryClosed || !(session.liveBackgroundTasks instanceof Map)) {
                throw RequestError.invalidParams(undefined, "Live Claude session unavailable.");
            }
            const sdkIdle = session.lastSessionState === undefined || session.lastSessionState === "idle";
            // The SDK reports the notification cycle as running 79 ms after the
            // task leaves the registry (measured on Claude Code 2.1.296).
            // ponytail: a fixed hold; a slower CLI needs a pending-cycle signal.
            const cyclePending = Date.now() - (session.aoTaskEndedAt ?? 0) < 10000;
            return { canHibernate: sdkIdle && !cyclePending && !Array.from(session.liveBackgroundTasks.values()).some(task => !task.endedPerLevel) };
        })
        `;
	writeFileSync(adapterPath, source.replace(marker, registration + marker));
	return true;
}

/**
 * Settle a prompt that Claude Code folded into a task-notification cycle.
 *
 * When a prompt arrives while Claude Code runs a cycle it started itself (for
 * example after a background command finished), the CLI adds the prompt to
 * that cycle. The cycle's single result keeps its task-notification origin, so
 * claude-agent-acp 0.70 treats it as autonomous and never answers the prompt:
 * the session stays "Working" until the user sends something else. The result
 * names the prompts it answered in user_message_uuid(s); route it through the
 * user lane when it names a pending, not held-open turn. Port of upstream
 * agentclientprotocol/claude-agent-acp#1233 for issue #1145.
 */
export function patchClaudeFoldedPromptSettlement(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	if (source.includes("// AO: a result naming a pending prompt answers it.")) return false;
	const original = "const isAutonomousResult = message.origin != null && AUTONOMOUS_RESULT_ORIGINS.has(message.origin.kind);";
	const at = source.indexOf(original);
	if (at < 0 || source.indexOf(original, at + 1) >= 0 || !source.includes("const findUnsettledTurn = (uuid) =>")) {
		throw new Error("claude-agent-acp autonomous result classification no longer matches AO's folded-prompt patch");
	}
	const replacement = [
		"// AO: a result naming a pending prompt answers it.",
		"                        const answeredPromptUuids = Array.isArray(message.user_message_uuids)",
		"                            ? message.user_message_uuids",
		'                            : typeof message.user_message_uuid === "string" ? [message.user_message_uuid] : [];',
		"                        const answersPendingPrompt = answeredPromptUuids.some((uuid) => {",
		"                            const turn = findUnsettledTurn(uuid);",
		"                            return turn !== undefined && !isHeldOpen(turn);",
		"                        });",
		"                        const isAutonomousResult = message.origin != null && AUTONOMOUS_RESULT_ORIGINS.has(message.origin.kind) && !answersPendingPrompt;",
	].join("\n");
	writeFileSync(adapterPath, source.slice(0, at) + replacement + source.slice(at + original.length));
	return true;
}

/**
 * Drop trailing-idle debt that can no longer be paid.
 *
 * claude-agent-acp 0.70 counts one owed `idle` per result and absorbs that many
 * idles before treating one as a turn-over signal. Claude Code 2.1.270+ emits a
 * single idle for a turn plus the task-notification cycle that follows it, so
 * one unit is never paid. The leftover then swallows the idle a steered turn
 * settles on, leaving that turn "Working" forever. A transition into `running`
 * proves every earlier idle was already emitted, so reset the debt there. Port
 * of the sweep upstream shipped in claude-agent-acp 0.79.
 */
export function patchClaudeStaleIdleDebt(adapterPath) {
	const source = readFileSync(adapterPath, "utf8");
	if (source.includes("// AO: drop idle debt that can no longer be paid.")) return false;
	const original = [
		'case "session_state_changed": {',
		"                                session.lastSessionState = message.state;",
		'                                if (message.state === "idle") {',
	].join("\n");
	const at = source.indexOf(original);
	if (at < 0 || source.indexOf(original, at + 1) >= 0 || !source.includes("session.owedTrailingIdles--;")) {
		throw new Error("claude-agent-acp session state handling no longer matches AO's idle-debt patch");
	}
	const replacement = [
		'case "session_state_changed": {',
		"                                // AO: drop idle debt that can no longer be paid.",
		"                                const previousState = session.lastSessionState;",
		"                                session.lastSessionState = message.state;",
		'                                if (message.state === "running" && previousState !== "running") {',
		"                                    session.owedTrailingIdles = 0;",
		"                                }",
		'                                if (message.state === "idle") {',
	].join("\n");
	writeFileSync(adapterPath, source.slice(0, at) + replacement + source.slice(at + original.length));
	return true;
}

const CLAUDE_COMPACTION_STATUS_MARKER = "// AO: report compaction as status, not reply text.";

/**
 * Report Claude's compaction as a status update instead of reply text.
 *
 * claude-agent-acp 0.70 turns the SDK's compaction status into assistant text:
 * "Compacting..." for every status message while it runs, then "Compacting
 * completed." or "Compacting failed: <reason>". A compaction Claude starts on
 * its own lands in the reply as "Compacting...Compacting...Compacting...".
 * Send each step as a `session_info_update` carrying `_meta["ao/compaction"]`
 * instead, in the shape OpenCode uses for `opencode/compaction`, so AO can draw
 * one compaction row that runs and then settles. The banners still count as
 * delivered text, as they did, so the issue-#453 result fallback is unchanged.
 *
 * The SDK reports the result before the `compact_boundary` that carries its own
 * figures (pre_tokens, post_tokens). The boundary therefore sends a second
 * "completed" with tokensBefore/tokensAfter, on the scale of the context usage
 * it publishes right after: what the compaction freed, and how full the context
 * is now. It goes first, so the row settles on these figures and not on a
 * usage report alone.
 */
export function patchClaudeCompactionStatus(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	if (source.includes(CLAUDE_COMPACTION_STATUS_MARKER)) return false;
	const indent = (spaces, lines) => lines.map(line => " ".repeat(spaces) + line).join("\n");
	const banner = (content) => indent(36, [
		"await sendUpdate({",
		"    sessionId: message.session_id,",
		"    update: {",
		'        sessionUpdate: "agent_message_chunk",',
		`        ${content}`,
		"    },",
		"});",
	]);
	const status = (meta, marker = false) => indent(36, [
		...(marker ? [CLAUDE_COMPACTION_STATUS_MARKER] : []),
		"session.emittedAssistantText = true;",
		"await sendUpdate({",
		"    sessionId: message.session_id,",
		"    update: {",
		'        sessionUpdate: "session_info_update",',
		`        _meta: { "ao/compaction": ${meta} },`,
		"    },",
		"});",
	]);
	const boundary = "                                lastAssistantTotalUsage = usedTokens ?? 0;";
	const figures = [
		boundary,
		"                                {",
		"                                    // AO: what the compaction freed, from the SDK's own figures.",
		"                                    const pre = message.compact_metadata?.pre_tokens;",
		"                                    const post = message.compact_metadata?.post_tokens;",
		'                                    const known = typeof pre === "number" && typeof post === "number";',
		"                                    const freed = known && pre > post ? pre - post : undefined;",
		"                                    const figures = usedTokens != null && usedTokens > 0",
		"                                        ? { tokensAfter: usedTokens, ...(freed !== undefined ? { tokensBefore: usedTokens + freed } : {}) }",
		"                                        : known ? { tokensBefore: pre, tokensAfter: post } : undefined;",
		"                                    if (figures) {",
		"                                        await sendUpdate({",
		"                                            sessionId: message.session_id,",
		"                                            update: {",
		'                                                sessionUpdate: "session_info_update",',
		'                                                _meta: { "ao/compaction": { status: "completed", ...figures } },',
		"                                            },",
		"                                        });",
		"                                    }",
		"                                }",
	].join("\n");
	const patches = [
		[boundary, figures],
		[banner('content: { type: "text", text: "Compacting..." },'), status('{ status: "started" }', true)],
		[banner('content: { type: "text", text: "\\n\\nCompacting completed." },'), status('{ status: "completed" }')],
		[
			banner("content: { type: \"text\", text: `\\n\\nCompacting failed${reason}` },"),
			status('{ status: "failed", error: { message: message.compact_error ?? "" } }'),
		],
	];
	for (const [original, replacement] of patches) {
		const at = source.indexOf(original);
		if (at < 0 || source.indexOf(original, at + 1) >= 0) {
			throw new Error("claude-agent-acp compaction banners no longer match AO's compaction status patch");
		}
		source = source.slice(0, at) + replacement + source.slice(at + original.length);
	}
	writeFileSync(adapterPath, source);
	return true;
}

/**
 * Settle steered turns the way upstream claude-agent-acp#1166 does.
 *
 * A steer aborts the running cycle and runs the steered message as a second
 * cycle, so 0.70 parks a steered turn's results and settles it at an idle. An
 * owed idle can absorb that idle (the "Working forever" stall), or the turn
 * waits for an echo the CLI never replays. #1166 settles the turn at a result
 * that names the steer in user_message_uuid(s). That result still owes its
 * trailing idle. Unstamped results settle at the first idle after a result
 * that followed the steered echo (`steeredAwaitingResult`), ahead of idle
 * debt. AO adds one rule: a result that names a steer answers it, whatever its
 * origin. Claude Code folds a steer sent during a task-notification cycle into
 * that cycle's result. Drop this patch when AO moves to claude-agent-acp
 * 0.86 or later.
 */
export function patchClaudeSteerSettlement(adapterPath) {
	let source = readFileSync(adapterPath, "utf8");
	if (source.includes("// AO: port of claude-agent-acp#1166.")) return false;
	const patches = [
		[
			"function isSteering(turn) {\n    return turn != null && turn.steeredEchoes !== undefined && !turn.settled;\n}",
			[
				"function isSteering(turn) {",
				"    return turn != null && turn.steeredEchoes !== undefined && !turn.settled;",
				"}",
				"// AO: port of claude-agent-acp#1166.",
				"function isSteeredSettleReady(turn) {",
				"    return isSteering(turn) && turn.steeredEchoes.size === 0 && turn.steeredSettle !== undefined && !turn.steeredAwaitingResult;",
				"}",
				"function leaveSteerLane(turn) {",
				"    turn.steeredEchoes = undefined;",
				"    turn.steeredUuids = undefined;",
				"    turn.steeredAwaitingResult = undefined;",
				"    turn.steeredSettle = undefined;",
				"}",
			].join("\n"),
		],
		[
			"(turnInFlight.steeredEchoes ??= new Set()).add(steeredUuid);",
			"(turnInFlight.steeredEchoes ??= new Set()).add(steeredUuid);\n        (turnInFlight.steeredUuids ??= new Set()).add(steeredUuid);",
		],
		[
			"session.activeTurn.steeredSettle = outcome;",
			"session.activeTurn.steeredSettle = outcome;\n                session.activeTurn.steeredAwaitingResult = false;",
		],
		[
			"session.activeTurn.steeredEchoes.delete(message.uuid);",
			[
				"if (session.activeTurn.steeredEchoes.delete(message.uuid)) {",
				"                                        // The recorded outcome predates the steered cycle.",
				"                                        session.activeTurn.steeredAwaitingResult = true;",
				"                                    }",
			].join("\n"),
		],
		[
			[
				"// Plain Turn so the fields can be cleared below; isSteering",
				"                                        // narrows steeredEchoes to non-optional.",
				"                                        const steered = session.activeTurn;",
				"                                        if (steered.steeredEchoes?.size === 0 && steered.steeredSettle !== undefined) {",
				"                                            // Via the subagent gate, not settleActive: a steered turn",
				"                                            // can also have spawned background subagents, which own it",
				"                                            // from here (settles now if none is live, holds otherwise).",
				"                                            steered.deferredSettle = steered.steeredSettle;",
				"                                            steered.steeredEchoes = undefined;",
				"                                            steered.steeredSettle = undefined;",
				"                                            settleDeferredIfDrained();",
				"                                        }",
			].join("\n"),
			"// AO: the ready lane above settles a steered turn; this one swallows the idle.",
		],
		[
			"else if (session.owedTrailingIdles > 0) {",
			[
				"else if (isSteeredSettleReady(session.activeTurn)) {",
				"                                        // Ahead of idle debt: the outcome is final, and debt",
				"                                        // absorption could swallow the only idle this turn gets.",
				"                                        // Via the subagent gate, not settleActive: a steered turn",
				"                                        // can also have spawned background subagents.",
				"                                        const steered = session.activeTurn;",
				"                                        steered.deferredSettle = steered.steeredSettle;",
				"                                        leaveSteerLane(steered);",
				"                                        settleDeferredIfDrained();",
				"                                    }",
				"                                    else if (session.owedTrailingIdles > 0) {",
			].join("\n"),
		],
		[
			"const isAutonomousResult = message.origin != null && AUTONOMOUS_RESULT_ORIGINS.has(message.origin.kind)",
			[
				"const resultUuids = message.user_message_uuids ??",
				"                            (message.user_message_uuid !== undefined ? [message.user_message_uuid] : []);",
				"                        // AO: a result that names a steer answers it, whatever its origin.",
				"                        const namesSteer = !session.cancelled && resultUuids.some((uuid) =>",
				"                            (session.turnQueue ?? []).some((turn) => isSteering(turn) && turn.steeredUuids?.has(uuid)));",
				"                        const isAutonomousResult = message.origin != null && AUTONOMOUS_RESULT_ORIGINS.has(message.origin.kind) && !namesSteer",
			].join("\n"),
		],
		[
			"recordResultForOrphanCommands();\n                                ensureActiveTurn();",
			[
				"recordResultForOrphanCommands();",
				"                                ensureActiveTurn();",
				"                                // A result naming a steer consumed it: retire its echo and,",
				"                                // with no other steer outstanding, leave the steer lane so",
				"                                // this result settles the turn and owes its trailing idle.",
				"                                const steeredTurn = session.activeTurn;",
				"                                if (!session.cancelled && isSteering(steeredTurn)) {",
				"                                    let answersSteer = false;",
				"                                    for (const uuid of resultUuids) {",
				"                                        steeredTurn.steeredEchoes.delete(uuid);",
				"                                        if (steeredTurn.steeredUuids?.has(uuid)) answersSteer = true;",
				"                                    }",
				"                                    if (answersSteer && steeredTurn.steeredEchoes.size === 0) {",
				"                                        leaveSteerLane(steeredTurn);",
				"                                    }",
				"                                }",
			].join("\n"),
		],
	];
	for (const [original, replacement] of patches) {
		const at = source.indexOf(original);
		if (at < 0 || source.indexOf(original, at + 1) >= 0) {
			throw new Error("claude-agent-acp no longer matches AO's steer settlement patch");
		}
		source = source.slice(0, at) + replacement + source.slice(at + original.length);
	}
	writeFileSync(adapterPath, source);
	return true;
}

export function pruneNodeDistribution(nodeRoot) {
	// The Unix archives expose npm/corepack as bin/ symlinks into lib/. Remove
	// the entry points before their targets so packagers never see dangling
	// links. Windows keeps the launchers at the archive root instead.
	for (const name of BIN_BUILD_TOOLS) {
		removeFile(join(nodeRoot, "bin", name));
	}
	for (const name of ROOT_BUILD_TOOLS) {
		removeFile(join(nodeRoot, name));
	}
	for (const name of BUILD_ONLY_CONTENT) {
		rmSync(join(nodeRoot, name), { recursive: true, force: true });
	}
}

function removeFile(path) {
	try {
		// unlink removes a symlink itself even when its target is already absent.
		unlinkSync(path);
	} catch (error) {
		if (error?.code !== "ENOENT") throw error;
	}
}

export function archiveExtraction(
	archivePath,
	workDir,
	{ platform = process.platform, systemRoot = process.env.SystemRoot } = {},
) {
	// Windows ships bsdtar as System32\tar.exe and it reads zip. PowerShell's
	// Expand-Archive is the obvious alternative but is bound by MAX_PATH: with
	// LongPathsEnabled=0 and a deep checkout, Node's bundled npm tree exceeds
	// 260 characters and extraction fails without a non-zero exit, so the build
	// only discovers it later, as a missing directory. bsdtar handles the same
	// archive at the same depth.
	if (platform === "win32") {
		if (!systemRoot) throw new Error("SystemRoot is required for Windows archive extraction");
		// Git Bash can put GNU tar ahead of System32 on PATH.
		return { command: win32.join(systemRoot, "System32", "tar.exe"), args: ["-xf", archivePath, "-C", workDir] };
	}
	return { command: "tar", args: ["-xzf", archivePath, "-C", workDir] };
}
