/**
 * Subagents, read back out of the activities their parent recorded.
 *
 * Claude records a subagent launch as one tool call (the `Agent` tool, `Task` before
 * it was renamed) and stamps every step the child takes with that call's id. ACP files
 * the launch as reasoning, which the timeline drops, so the child's commands used to
 * surface as loose tool calls in the parent's turn: interleaved with everything else
 * the parent did, with nothing saying which agent ran them or whether it finished.
 *
 * This module regroups them. A launch keeps its place in the turn and the steps that
 * point back to it move under it. Nothing is dropped: a step whose launch is not in
 * the same list stays where it was.
 */

import { fileChangeFiles, type ConversationActivity, type ConversationItem } from "../../types/conversation";

/** Tool names a provider uses for "start a subagent". `Task` is Claude's older name. */
const LAUNCH_TOOLS = new Set(["Agent", "Task"]);

/** The child's report back to its caller; its payload is the report, not a step. */
const HANDBACK_TOOL = "SubagentHandback";

/**
 * What Claude returns from a launch when the report is not in the result. Both are
 * notes to the model ("never quote this"), so they are never shown as a report.
 */
const ASYNC_RESULT = /^Async agent launched/;
const DELIVERED_ELSEWHERE = /^This agent's report was delivered/;

export type SubagentState = "working" | "done" | "failed" | "stopped" | "unknown";

export type Subagent = {
	launch: ConversationActivity;
	/** The short task name the parent gave the child. */
	title: string;
	/** The provider's agent type, such as `Explore`. */
	agentType?: string;
	prompt?: string;
	/** Everything the child did, in order, without its hand-back. */
	steps: ConversationActivity[];
	state: SubagentState;
	report?: string;
	/** Why a launch failed, as the provider said it. */
	error?: string;
	startedAt: number;
	/** Unset while the child works, so the elapsed time can keep counting. */
	endedAt?: number;
};

export function isSubagentLaunch(item: ConversationItem | undefined): item is ConversationActivity {
	if (item?.kind !== "activity") return false;
	const tool = item.detail?.providerToolName;
	if (typeof tool === "string" && LAUNCH_TOOLS.has(tool)) return true;
	return typeof launchInput(item).subagent_type === "string";
}

/**
 * Move each launch's steps out of `items` and under the launch.
 *
 * A step belongs to the outermost launch above it, so a child that started its own
 * child keeps the whole chain together instead of stranding the grandchild under a
 * launch that is itself folded away.
 */
export function partitionSubagentSteps(items: ConversationItem[]): {
	items: ConversationItem[];
	steps: ReadonlyMap<string, ConversationActivity[]>;
} {
	const launches = new Map<string, ConversationActivity>();
	for (const item of items) {
		if (isSubagentLaunch(item) && item.providerItemId) launches.set(item.providerItemId, item);
	}
	if (launches.size === 0) return { items, steps: new Map() };

	const byProvider = new Map<string, ConversationActivity>();
	for (const item of items) {
		if (item.kind === "activity" && item.providerItemId) byProvider.set(item.providerItemId, item);
	}
	const steps = new Map<string, ConversationActivity[]>();
	const kept: ConversationItem[] = [];
	for (const item of items) {
		const launch = item.kind === "activity" ? outermostLaunch(item, launches, byProvider) : undefined;
		if (!launch) {
			kept.push(item);
			continue;
		}
		const list = steps.get(launch.id);
		if (list) list.push(item as ConversationActivity);
		else steps.set(launch.id, [item as ConversationActivity]);
	}
	return { items: kept, steps };
}

function outermostLaunch(
	item: ConversationActivity,
	launches: ReadonlyMap<string, ConversationActivity>,
	byProvider: ReadonlyMap<string, ConversationActivity>,
): ConversationActivity | undefined {
	let found: ConversationActivity | undefined;
	let parentId = item.detail?.parentProviderItemId;
	// Provider ids can form a cycle in a malformed transcript; each id is visited once.
	const seen = new Set<string>(item.providerItemId ? [item.providerItemId] : []);
	while (parentId && !seen.has(parentId)) {
		seen.add(parentId);
		found = launches.get(parentId) ?? found;
		parentId = byProvider.get(parentId)?.detail?.parentProviderItemId;
	}
	return found;
}

/**
 * Derive one subagent's state from the facts recorded about it.
 *
 * A background launch completes as soon as the child starts, so the launch's own
 * status says nothing about the child. The hand-back is the fact that it finished; a
 * running step, or a turn that is still waiting on it, is the fact that it has not.
 * Anything else is reported as unknown rather than guessed in either direction.
 */
export function readSubagent(
	launch: ConversationActivity,
	allSteps: readonly ConversationActivity[],
	live: boolean,
): Subagent {
	const input = launchInput(launch);
	const handback = allSteps.find(isHandback);
	const steps = allSteps.filter((step) => !isHandback(step));
	const result = launchResultText(launch);
	const background = ASYNC_RESULT.test(result);
	const startedAt = timeOf(launch.createdAt);
	const lastStepAt = steps.reduce((latest, step) => Math.max(latest, timeOf(step.createdAt)), startedAt);
	const base = {
		launch,
		title: stringOf(input.description) ?? launch.summary,
		agentType: stringOf(input.subagent_type) ?? stringOf(launch.detail?.subagentType),
		prompt: stringOf(input.prompt),
		steps,
		startedAt,
	};

	if (launch.status === "failed") {
		return { ...base, state: "failed", error: firstParagraph(result), endedAt: lastStepAt };
	}
	if (launch.status === "cancelled") return { ...base, state: "stopped", endedAt: lastStepAt };
	if (handback && handback.status === "completed") {
		return {
			...base,
			state: "done",
			report: stringOf(launchInput(handback).message),
			endedAt: timeOf(handback.createdAt),
		};
	}
	const working =
		launch.status === "running" ||
		launch.status === "pending" ||
		steps.some((step) => step.status === "running" || step.status === "pending") ||
		(background && live);
	if (working) return { ...base, state: "working" };
	if (launch.status === "completed" && !background) {
		const report = DELIVERED_ELSEWHERE.test(result) ? undefined : result || undefined;
		return { ...base, state: "done", report, endedAt: lastStepAt };
	}
	return { ...base, state: "unknown", endedAt: lastStepAt };
}

/** What the child said or did last, for the one line under its name. */
export type SubagentLine =
	| { kind: "starting" }
	| { kind: "said"; text: string }
	| { kind: "command"; running: boolean; command: string }
	| { kind: "edit"; running: boolean; path: string }
	| { kind: "tool"; running: boolean; name: string };

export function latestSubagentLine(steps: readonly ConversationActivity[]): SubagentLine {
	const step = steps.at(-1);
	if (!step) return { kind: "starting" };
	const running = step.status === "running" || step.status === "pending";
	if (isChildMessage(step)) {
		const text = lastLine(childMessageText(step));
		if (text) return { kind: "said", text };
	}
	if (step.activityKind === "command") {
		return { kind: "command", running, command: displayCommand(step.detail?.command ?? step.summary) };
	}
	if (step.activityKind === "file_change") {
		const path = fileChangeFiles(step)[0]?.path;
		if (path) return { kind: "edit", running, path: basename(path) };
	}
	return { kind: "tool", running, name: step.detail?.providerToolName ?? step.summary };
}

/** A message the child wrote while it worked: prose, not a tool call. */
export function isChildMessage(step: ConversationActivity): boolean {
	return (
		step.activityKind === "mcp_tool" &&
		step.detail?.nestedAgent === true &&
		!step.detail?.providerToolName
	);
}

export function childMessageText(step: ConversationActivity): string {
	return (step.detail?.progress ?? step.detail?.text ?? "").trim();
}

function isHandback(step: ConversationActivity): boolean {
	return step.detail?.providerToolName === HANDBACK_TOOL;
}

function launchInput(activity: ConversationActivity): Record<string, unknown> {
	const input = (activity.detail as { input?: unknown } | undefined)?.input;
	return input && typeof input === "object" && !Array.isArray(input)
		? (input as Record<string, unknown>)
		: {};
}

/**
 * The launch's result as text. ACP hands it over as content blocks, and earlier rows
 * stored the same blocks as a JSON string in `output`. An error arrives fenced as a
 * code block; the fence is presentation, not part of the reason.
 */
function launchResultText(launch: ConversationActivity): string {
	return unfence(rawLaunchResult(launch));
}

function rawLaunchResult(launch: ConversationActivity): string {
	const fromContent = blockText(launch.detail?.content);
	if (fromContent) return fromContent;
	const output = launch.detail?.output;
	if (typeof output !== "string") return "";
	const trimmed = output.trim();
	if (trimmed.startsWith("[")) {
		try {
			const parsed = blockText(JSON.parse(trimmed));
			if (parsed) return parsed;
		} catch {
			// Not JSON after all: plain text that happens to start with a bracket.
		}
	}
	return trimmed;
}

function blockText(value: unknown): string {
	if (!Array.isArray(value)) return "";
	return value
		.map((block) => {
			if (!block || typeof block !== "object") return "";
			const record = block as Record<string, unknown>;
			if (typeof record.text === "string") return record.text;
			const inner = record.content as Record<string, unknown> | undefined;
			return inner && typeof inner.text === "string" ? inner.text : "";
		})
		.join("\n")
		.trim();
}

function unfence(text: string): string {
	const fenced = /^```[^\n]*\n([\s\S]*?)\n?```$/.exec(text.trim());
	return fenced ? fenced[1]!.trim() : text;
}

/** Drop the `cd <worktree>;` prefix agents put in front of every command. */
function displayCommand(command: string): string {
	return command.trim().replace(/^cd\s+(?:"[^"]*"|'[^']*'|\S+)\s*(?:;|&&)\s*/, "");
}

function firstParagraph(text: string): string | undefined {
	const paragraph = text.split(/\n\s*\n/)[0]?.trim();
	return paragraph || undefined;
}

function lastLine(text: string): string {
	return text.split("\n").map((line) => line.trim()).filter(Boolean).at(-1) ?? "";
}

function basename(path: string): string {
	return path.split("/").filter(Boolean).at(-1) ?? path;
}

function stringOf(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function timeOf(iso: string): number {
	const parsed = Date.parse(iso);
	return Number.isFinite(parsed) ? parsed : 0;
}
