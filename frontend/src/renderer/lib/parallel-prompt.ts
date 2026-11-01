import type { components } from "../../api/schema";
import { captureRendererEvent } from "./telemetry";

// "Run more at once": after a user's first worker starts, the board offers two
// or three follow-up tasks to run beside it. Users who stop at one worker on
// day one rarely come back, so this nudges the first parallel run.

/**
 * The experiment's single switch. On by default; a first-run cohort (or QA)
 * turns it off by writing "off" to this key, and back on with "on". Every
 * surface of the prompt reads this one function.
 */
export const PARALLEL_PROMPT_FLAG_KEY = "ao.experiment.parallelPrompt";

export function parallelPromptEnabled(): boolean {
	try {
		return window.localStorage.getItem(PARALLEL_PROMPT_FLAG_KEY) !== "off";
	} catch {
		return true;
	}
}

/** Once an install has run this many workers the prompt has done its job. */
export const PARALLEL_PROMPT_GRADUATED_WORKERS = 3;

const STATE_KEY = "ao.parallelPrompt";

type StoredState = { dismissed?: boolean; graduated?: boolean };

function readState(): StoredState {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(STATE_KEY) ?? "{}");
		return parsed && typeof parsed === "object" ? (parsed as StoredState) : {};
	} catch {
		return {};
	}
}

function writeState(patch: StoredState): void {
	try {
		window.localStorage.setItem(STATE_KEY, JSON.stringify({ ...readState(), ...patch }));
	} catch {
		// Without storage the prompt simply shows again next launch.
	}
}

/** Never show the prompt again on this install. */
export function dismissParallelPrompt(): void {
	writeState({ dismissed: true });
}

/**
 * Whether the board should offer the prompt, given how many workers this
 * install has run (archived ones included). Reaching the graduation count is
 * remembered, so archiving or deleting workers later does not bring it back.
 */
export function shouldOfferParallelPrompt(workerCount: number): boolean {
	if (!parallelPromptEnabled()) return false;
	const state = readState();
	if (state.dismissed || state.graduated) return false;
	if (workerCount >= PARALLEL_PROMPT_GRADUATED_WORKERS) {
		writeState({ graduated: true });
		return false;
	}
	return workerCount >= 1;
}

/**
 * A start whose answer never arrived: the daemon may already have created the
 * worker. A retry sends exactly this request again, so the daemon replays the
 * first result instead of starting a second worker.
 */
export type PendingParallelStart = { requestId: string; brief: string };

const PENDING_KEY = "ao.parallelPrompt.pending.v1";

type PendingStarts = Record<string, Record<string, PendingParallelStart>>;

function readPendingStarts(): PendingStarts {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(PENDING_KEY) ?? "{}");
		return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as PendingStarts) : {};
	} catch {
		return {};
	}
}

function writePendingStarts(all: PendingStarts): void {
	try {
		window.localStorage.setItem(PENDING_KEY, JSON.stringify(all));
	} catch {
		// Without storage the request survives only while the card stays mounted.
	}
}

export function readPendingParallelStart(projectId: string, suggestionId: string): PendingParallelStart | undefined {
	const pending = readPendingStarts()[projectId]?.[suggestionId];
	return typeof pending?.requestId === "string" && typeof pending.brief === "string" ? pending : undefined;
}

export function savePendingParallelStart(projectId: string, suggestionId: string, pending: PendingParallelStart): void {
	const all = readPendingStarts();
	all[projectId] = { ...all[projectId], [suggestionId]: pending };
	writePendingStarts(all);
}

/** Forgets a start once the daemon has answered it, either way. */
export function clearPendingParallelStart(projectId: string, suggestionId: string): void {
	const all = readPendingStarts();
	if (!all[projectId]?.[suggestionId]) return;
	delete all[projectId][suggestionId];
	if (Object.keys(all[projectId]).length === 0) delete all[projectId];
	writePendingStarts(all);
}

export type TaskSuggestionView = components["schemas"]["TaskSuggestionView"];

/** Built-in tasks that suit almost any repository, used to fill the list. */
export const STARTER_TASKS = ["fixBug", "docs", "tests"] as const;
export type StarterTask = (typeof STARTER_TASKS)[number];

export type ParallelSuggestion =
	| { id: string; source: "issue"; issueNumber: number; title: string; url?: string }
	| { id: string; source: "todo"; path: string; line: number; text: string }
	| { id: string; source: "starter"; starter: StarterTask };

export type ParallelSuggestionSource = ParallelSuggestion["source"];

export const MAX_PARALLEL_SUGGESTIONS = 3;

/** Issues first, then TODO/FIXME notes, then starter tasks, three in all. */
export function rankParallelSuggestions(found: readonly TaskSuggestionView[]): ParallelSuggestion[] {
	const out: ParallelSuggestion[] = [];
	for (const item of found) {
		if (item.source === "issue" && item.issueNumber && item.title) {
			out.push({ id: `issue:${item.issueNumber}`, source: "issue", issueNumber: item.issueNumber, title: item.title, url: item.url });
		}
	}
	for (const item of found) {
		if (item.source === "todo" && item.path && item.line && item.text) {
			out.push({ id: `todo:${item.path}:${item.line}`, source: "todo", path: item.path, line: item.line, text: item.text });
		}
	}
	for (const starter of STARTER_TASKS) out.push({ id: `starter:${starter}`, source: "starter", starter });
	return out.slice(0, MAX_PARALLEL_SUGGESTIONS);
}

/** One source when every suggestion shares it, else "mixed". */
export function suggestionSetSource(suggestions: readonly ParallelSuggestion[]): ParallelSuggestionSource | "mixed" {
	const sources = new Set(suggestions.map((suggestion) => suggestion.source));
	return sources.size === 1 ? [...sources][0] : "mixed";
}

export type ParallelPromptAction = "spawn_one" | "spawn_all" | "edit" | "dismiss";

// One "shown" per project per launch: re-renders and revisits are not new views.
const shownThisLaunch = new Set<string>();

/** Enum and count properties only; nothing the user or repository wrote. */
export function reportParallelPromptShown(projectKey: string, suggestions: readonly ParallelSuggestion[]): void {
	if (shownThisLaunch.has(projectKey)) return;
	shownThisLaunch.add(projectKey);
	void captureRendererEvent("ao.onboarding.parallel_prompt_shown", {
		suggestion_count: suggestions.length,
		source: suggestionSetSource(suggestions),
	});
}

export function reportParallelPromptAction(action: ParallelPromptAction, count: number): void {
	void captureRendererEvent("ao.onboarding.parallel_prompt_action", { action, count });
}

export function resetParallelPromptForTest(): void {
	shownThisLaunch.clear();
}
