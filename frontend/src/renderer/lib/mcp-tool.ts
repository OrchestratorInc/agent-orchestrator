import type { ConversationActivity } from "../types/conversation";

export interface McpToolName {
	tool: string;
	server?: string;
}

const CLAUDE_MCP_NAME = /^mcp__(.+?)__(.+)$/;
const CODEX_MCP_SUMMARY = /^Called (\S+?)\/(\S+)$/;

export function mcpToolName(activity: ConversationActivity): McpToolName {
	const detail = activity.detail;
	const server = detail?.server ?? detail?.namespace;
	if (detail?.toolName && server) return { tool: detail.toolName, server };
	for (const name of [detail?.providerToolName, detail?.toolName, activity.summary]) {
		const match = name ? CLAUDE_MCP_NAME.exec(name.trim()) : null;
		if (match) return { server: match[1], tool: match[2] };
	}
	const called = CODEX_MCP_SUMMARY.exec(activity.summary.trim());
	if (called) return { server: called[1], tool: called[2] };
	return { tool: detail?.toolName ?? activity.summary, server };
}

export function mcpToolArguments(activity: ConversationActivity): unknown {
	return activity.detail?.arguments ?? activity.detail?.input;
}

export function mcpResultTexts(activity: ConversationActivity): string[] {
	const detail = activity.detail;
	const texts: string[] = [];
	const collect = (block: unknown) => {
		if (!block || typeof block !== "object") return;
		const record = block as Record<string, unknown>;
		if (record.type === "content") return collect(record.content);
		if (record.type === "text" && typeof record.text === "string") texts.push(record.text);
	};
	const resultContent = asRecord(detail?.result).content;
	if (Array.isArray(resultContent)) resultContent.forEach(collect);
	const content: unknown = detail?.content;
	if (Array.isArray(content)) content.forEach(collect);
	if (texts.length === 0 && detail?.output) texts.push(detail.output);
	return texts
		.map((text) => stripCodeFence(text).trim())
		.filter((text) => text && !/^\[Image: source: [^\]]*\]$/.test(text));
}

function stripCodeFence(text: string): string {
	const match = /^```[^\n]*\n([\s\S]*?)\n?```\s*$/.exec(text.trim());
	return match ? match[1] : text;
}

export type AOToolState = "running" | "completed" | "failed" | "unknown";

export interface AOToolLine {
	tone: "error" | "muted";
	text: string;
}

export interface AOToolView {
	label: string;
	note?: string;
	state: AOToolState;
	hidden?: boolean;
	error?: string;
	lines?: AOToolLine[];
	text?: string;
}

interface Copy {
	running: string;
	done: string;
	failed: string;
}

export function aoToolView(activity: ConversationActivity): AOToolView | undefined {
	if (activity.activityKind !== "mcp_tool") return undefined;
	const { tool, server } = mcpToolName(activity);
	if (server !== "ao") return undefined;
	const args = asRecord(mcpToolArguments(activity));
	const texts = mcpResultTexts(activity);
	const detail = activity.detail;

	let state: AOToolState;
	if (activity.status === "failed" || detail?.success === false || detail?.error) state = "failed";
	else if (activity.status === "running" || activity.status === "pending") state = "running";
	else if (activity.status === "completed") state = "completed";
	else state = "unknown";
	const error = readableError(detail?.error || texts.join("\n"));

	if (tool === "html_preview") return pageCheckView(state, texts, error);
	if (tool === "html_render") return pageShowView(state, texts, error);

	const copy = computerUseCopy(tool, args);
	if (!copy) return undefined;
	if (tool === "run_steps") return runStepsView(copy, state, args, texts, error);
	const view: AOToolView = { label: labelFor(copy, state), state };
	if (state === "failed") view.error = error;
	else if (tool === "type_text" && typeof args.text === "string" && shorten(args.text) !== args.text) view.text = args.text;
	return view;
}

function runStepsView(
	copy: Copy,
	state: AOToolState,
	args: Record<string, unknown>,
	texts: string[],
	error: string | undefined,
): AOToolView {
	const steps = Array.isArray(args.steps) ? args.steps.map(stepLabel) : [];
	const counts = /^run_steps: (\d+)\/(\d+) steps ok/m.exec(texts.join("\n"));
	const ok = counts ? Number(counts[1]) : undefined;
	const stopped = ok !== undefined && ok < Number(counts?.[2]);
	const lines: AOToolLine[] = steps.map((text, index) => ({
		tone: stopped && index === ok ? "error" : "muted",
		text: `${index + 1}. ${text}`,
	}));
	if (stopped) {
		return { label: `${copy.failed} at step ${(ok ?? 0) + 1} of ${steps.length || Number(counts?.[2])}`, state: "failed", lines };
	}
	return {
		label: labelFor(copy, state),
		state,
		error: state === "failed" ? error : undefined,
		lines: lines.length > 0 ? lines : undefined,
	};
}

const STEP_VERBS: Record<string, string> = {
	click: "Clicked", double_click: "Double-clicked", right_click: "Right-clicked", set_value: "Set",
	type_text: "Typed", press_key: "Pressed", hotkey: "Pressed", scroll: "Scrolled", drag: "Dragged",
	move_cursor: "Moved the pointer",
};

function stepLabel(value: unknown): string {
	const step = asRecord(value);
	let tool = text(step.tool);
	let args = asRecord(step.args);
	if (!tool) {
		const key = Object.keys(step).find((name) => name in STEP_VERBS);
		if (key) {
			tool = key;
			args = asRecord(step[key]);
		}
	}
	if (!tool) return "Waited for the window";
	const verb = STEP_VERBS[tool] ?? tool;
	const name = text(args.name);
	switch (tool) {
		case "type_text":
			return text(args.text) ? `Typed "${shorten(String(args.text))}"` : "Typed";
		case "press_key":
			return `Pressed ${keyLabel(args.key, args.modifiers) ?? "a key"}`;
		case "hotkey": {
			const keys = Array.isArray(args.keys) ? args.keys.map(String) : [];
			return `Pressed ${keyLabel(keys.at(-1), keys.slice(0, -1)) ?? "a shortcut"}`;
		}
		case "set_value": {
			const value = text(args.value);
			return `Set ${name ? `"${name}"` : "a field"}${value ? ` to "${shorten(value)}"` : ""}`;
		}
		case "scroll": {
			const direction = text(args.direction);
			return direction ? `Scrolled ${direction}` : verb;
		}
		default:
			return name ? `${verb} "${name}"` : verb;
	}
}

function readableError(value: string | undefined): string | undefined {
	const text = value
		?.trim()
		.replace(/\s*\[request [^\]]*\]\s*$/, "")
		.replace(/\s*\([A-Z][A-Z0-9_]*\)\s*$/, "")
		.trim();
	if (!text) return undefined;
	return /^[a-z]+[\s:,]/.test(text) ? text.charAt(0).toUpperCase() + text.slice(1) : text;
}

function labelFor(copy: Copy, state: AOToolState): string {
	if (state === "running") return copy.running;
	if (state === "failed") return copy.failed;
	return copy.done;
}

function pageCheckView(state: AOToolState, texts: string[], error: string | undefined): AOToolView {
	const copy = { running: "Previewing the page", done: "Previewed the page", failed: "Couldn't preview the page" };
	if (state !== "completed") {
		return { label: labelFor(copy, state), state, error: state === "failed" ? error : undefined };
	}
	const report = texts.map(parsePageCheckReport).find(Boolean);
	if (!report) {
		return texts.length > 0
			? { label: copy.failed, state: "failed", error }
			: { label: copy.done, state };
	}
	const errors = report.consoleMessages.filter((message) => message.level === "error");
	const notes = [
		errors.length > 0 ? plural(errors.length, "console error") : "",
		report.missingImages.length > 0 ? plural(report.missingImages.length, "missing image") : "",
	].filter(Boolean);
	const lines: AOToolLine[] = [
		...errors.map((message) => ({ tone: "error" as const, text: message.text })),
		...report.missingImages.map((path) => ({ tone: "error" as const, text: `Missing image: ${path}` })),
	];
	return {
		label: copy.done,
		note: notes.join(" · ") || undefined,
		state,
		lines: lines.length > 0 ? lines : undefined,
	};
}

function pageShowView(state: AOToolState, texts: string[], error: string | undefined): AOToolView {
	const copy = { running: "Showing the page", done: "Showed the page", failed: "Couldn't show the page" };
	if (state === "completed" && texts.length > 0 && !texts.some((text) => /\(render [^)\s]+\)/.test(text))) {
		return { label: copy.failed, state: "failed", error };
	}
	return {
		label: labelFor(copy, state),
		state,
		hidden: state === "completed",
		error: state === "failed" ? error : undefined,
	};
}

export function isShownAORender(activity: ConversationActivity): boolean {
	return aoToolView(activity)?.hidden === true;
}

function computerUseCopy(tool: string, args: Record<string, unknown>): Copy | undefined {
	switch (tool) {
		case "list_windows": {
			const app = text(args.app);
			return app
				? { running: `Looking at ${app} windows`, done: `Looked at ${app} windows`, failed: `Couldn't list ${app} windows` }
				: { running: "Looking at open windows", done: "Looked at open windows", failed: "Couldn't list windows" };
		}
		case "get_window_state":
			return { running: "Looking at a window", done: "Looked at a window", failed: "Couldn't read the window" };
		case "launch_app": {
			const app = appName(args) ?? "an app";
			return { running: `Opening ${app}`, done: `Opened ${app}`, failed: `Couldn't open ${app}` };
		}
		case "bring_to_front":
			return {
				running: "Bringing an app to the front",
				done: "Brought an app to the front",
				failed: "Couldn't bring the app to the front",
			};
		case "click": {
			const verb = args.button === "right" ? "Right-click" : Number(args.count) === 2 ? "Double-click" : "Click";
			const keys = modifierSymbols(args.modifier);
			const done = keys ? `${keys}-${verb.toLowerCase()}ed` : `${verb}ed`;
			return { running: `${done.replace(/ed$/, "ing")}`, done, failed: "Couldn't click" };
		}
		case "type_text": {
			const typed = text(args.text);
			const quoted = typed ? ` "${shorten(typed)}"` : "";
			return { running: `Typing${quoted}`, done: `Typed${quoted}`, failed: "Couldn't type" };
		}
		case "press_key": {
			const key = keyLabel(args.key, args.modifiers);
			return key
				? { running: `Pressing ${key}`, done: `Pressed ${key}`, failed: `Couldn't press ${key}` }
				: { running: "Pressing a key", done: "Pressed a key", failed: "Couldn't press the key" };
		}
		case "run_steps": {
			const count = Array.isArray(args.steps) ? args.steps.length : 0;
			const app = text(args.app);
			const actions = count > 0 ? plural(count, "action") : "actions";
			const where = app ? ` in ${app}` : "";
			return { running: `Taking ${actions}${where}`, done: `Took ${actions}${where}`, failed: `Stopped${where}` };
		}
		default:
			return undefined;
	}
}

function appName(args: Record<string, unknown>): string | undefined {
	const name = text(args.name);
	if (name) return name.replace(/\/+$/, "").split("/").pop()?.replace(/\.app$/i, "") || name;
	return text(args.bundle_id);
}

const MODIFIER_SYMBOLS: Record<string, string> = {
	cmd: "⌘", command: "⌘", shift: "⇧", option: "⌥", alt: "⌥", ctrl: "⌃", control: "⌃", fn: "fn ",
};

function modifierSymbols(value: unknown): string {
	if (!Array.isArray(value)) return "";
	return value.map((key) => MODIFIER_SYMBOLS[String(key).toLowerCase()] ?? "").join("").trim();
}

const KEY_NAMES: Record<string, string> = {
	return: "Return", enter: "Return", tab: "Tab", escape: "Esc", esc: "Esc", space: "Space",
	delete: "Delete", backspace: "Delete", home: "Home", end: "End", pageup: "Page Up", pagedown: "Page Down",
	up: "↑", down: "↓", left: "←", right: "→",
};

function keyLabel(key: unknown, modifiers: unknown): string | undefined {
	const name = text(key);
	if (!name) return undefined;
	const lower = name.toLowerCase();
	const shown = KEY_NAMES[lower] ?? (name.length === 1 ? name.toUpperCase() : /^f\d{1,2}$/.test(lower) ? name.toUpperCase() : name);
	return `${modifierSymbols(modifiers)}${shown}`;
}

function shorten(value: string): string {
	const line = value.replace(/\s+/g, " ").trim();
	return line.length > 40 ? `${line.slice(0, 39)}…` : line;
}

function plural(count: number, noun: string): string {
	return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

function text(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function asRecord(value: unknown): Record<string, unknown> {
	return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

interface PageCheckReport {
	consoleMessages: { level: string; text: string }[];
	missingImages: string[];
}

function parsePageCheckReport(value: string): PageCheckReport | undefined {
	if (!value.startsWith("{")) return undefined;
	let parsed: unknown;
	try {
		parsed = JSON.parse(value);
	} catch {
		return undefined;
	}
	const record = asRecord(parsed);
	if (typeof record.width !== "number" || typeof record.contentHeight !== "number") return undefined;
	const consoleMessages = Array.isArray(record.consoleMessages)
		? record.consoleMessages.flatMap((message) => {
				const entry = asRecord(message);
				return typeof entry.text === "string"
					? [{ level: typeof entry.level === "string" ? entry.level : "log", text: entry.text }]
					: [];
			})
		: [];
	const missingImages = Array.isArray(record.missingImages)
		? record.missingImages.filter((path): path is string => typeof path === "string")
		: [];
	return { consoleMessages, missingImages };
}
