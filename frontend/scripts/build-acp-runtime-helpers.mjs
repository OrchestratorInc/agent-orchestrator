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
