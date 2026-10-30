/**
 * AO's automation messages and worker reports, read back from the fixed text AO
 * writes for each, so the timeline can draw each as what it is instead of one
 * generic "automation" card.
 *
 * A port of the desktop's readers (frontend/src/renderer/components/chat/
 * AutomationMessage.tsx and WorkerUpdates.tsx). Keep the two in step: a change
 * to an AO message template needs its reader changed in both places.
 */
// ponytail: parses the daemon's prompt text, as the desktop does; structured rows on the message would end the guess on both clients.

import type { ConversationItem } from "./types";

/* -------------------------------------------------------------------------- */
/* worker reports                                                              */
/* -------------------------------------------------------------------------- */

export type WorkerUpdate = {
	key: string;
	/** The report state (`needs_input`, `stuck`, `done`, ...); absent for a direct message. */
	state?: string;
	name: string;
	sessionId?: string;
	projectId?: string;
	body: string;
	createdAt: string;
};

const REPORTS_HEADER = "Reports since your previous turn:";
// One entry per `[state] [name](ao://sessions/<project>/<session>)`, as report.PreparedBatch.Render writes it,
// or `[state] ao://sessions/<project>/<session>` with no name, as older daemons wrote it.
const REPORT_ENTRY = /\n\n\[([a-z_]+)\] (?:\[((?:\\.|[^\]\\])*)\]\(ao:\/\/sessions\/([^/)\s]+)\/([^)\s]+)\)|ao:\/\/sessions\/([^/\s]+)\/(\S+))(?: \(repeated \d+ times\))?/g;
const ARTIFACT_LINE = /^artifact: (\S+)$/;
// The `[from <session> "<name>"]` prefix `ao send` puts on a worker's message.
const SENDER_PREFIX = /^\[from (?:worker\s+)?([\w.-]+)(?:\s+"([^"]*)")?\]/;
const WORKER_REPORT_OPEN = "<ao-worker-reports>";
const WORKER_REPORT_CLOSE = "</ao-worker-reports>";

function decode(value: string): string {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}

export function parseReports(text: string, createdAt: string, keyPrefix: string): WorkerUpdate[] {
	if (!text.startsWith(REPORTS_HEADER)) return [];
	const entries = [...text.matchAll(REPORT_ENTRY)];
	return entries.map((entry, index) => {
		const sessionId = decode(entry[4] ?? entry[6]!);
		return {
			key: `${keyPrefix}:${index}`,
			state: entry[1],
			name: entry[2]?.replace(/\\(.)/g, "$1") ?? sessionId,
			projectId: decode(entry[3] ?? entry[5]!),
			sessionId,
			body: text.slice(entry.index! + entry[0].length, entries[index + 1]?.index ?? text.length).trim(),
			createdAt,
		};
	});
}

/** What the user typed, without the worker reports the daemon folded in because the orchestrator was busy. */
export function humanVisibleText(text: string): string {
	if (!text.endsWith(WORKER_REPORT_CLOSE)) return text;
	const reportStart = text.lastIndexOf(`\n\n${WORKER_REPORT_OPEN}\n`);
	return reportStart < 0 ? text : text.slice(0, reportStart);
}

export function stripSenderPrefix(text: string, senderSessionId: string): string {
	const escaped = senderSessionId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	return text.replace(new RegExp(`^\\[from (?:worker\\s+)?${escaped}(?:\\s+"[^"]*")?\\]\\s*`), "");
}

/** Items never change in place, so each is parsed once however often the timeline renders. */
function cachedBy<T>(read: (item: ConversationItem) => T): (item: ConversationItem) => T {
	const cache = new WeakMap<ConversationItem, T>();
	return (item) => {
		if (!cache.has(item)) cache.set(item, read(item));
		return cache.get(item)!;
	};
}

/** What a worker sent, when this message is one: a direct `ao send` or a batch of reports. */
export const workerUpdatesOf = cachedBy((item): WorkerUpdate[] => {
	// Another agent's message that arrived mid-turn is a steer; the daemon names its sender.
	if (item.kind === "activity") {
		const sender = item.detail?.event === "steer" ? item.detail.senderSessionId?.trim() : undefined;
		if (!sender) return [];
		return [{
			key: item.id,
			name: item.detail?.senderDisplayName?.trim() || sender,
			sessionId: sender,
			projectId: item.detail?.senderProjectId,
			body: stripSenderPrefix(item.detail?.text ?? item.summary, sender).trim(),
			createdAt: item.createdAt,
		}];
	}
	if (item.role !== "user" || item.origin === "human") return [];
	const prefix = SENDER_PREFIX.exec(item.text);
	const sender = item.senderSessionId?.trim() || prefix?.[1];
	if (sender) {
		return [{
			key: item.id,
			name: item.senderDisplayName?.trim() || prefix?.[2]?.trim() || sender,
			sessionId: sender,
			projectId: item.senderProjectId,
			body: stripSenderPrefix(item.text, sender).trim(),
			createdAt: item.createdAt,
		}];
	}
	return parseReports(item.text, item.createdAt, item.id);
});

/** Reports the daemon folded into a human message because the orchestrator was busy. */
export const piggybackedUpdates = cachedBy((item): WorkerUpdate[] => {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return [];
	const visible = humanVisibleText(item.text);
	if (visible === item.text) return [];
	const envelope = item.text.slice(visible.length).trim().replace(/^<ao-worker-reports>/, "").replace(/<\/ao-worker-reports>$/, "").trim();
	return parseReports(envelope, item.createdAt, `${item.id}:reports`);
});

/** Each worker's newest update key; an older "needs you" has been overtaken. */
export function latestWorkerUpdateKeys(items: readonly ConversationItem[]): ReadonlySet<string> {
	const latest = new Map<string, string>();
	for (const item of items) {
		for (const update of [...piggybackedUpdates(item), ...workerUpdatesOf(item)]) latest.set(update.sessionId ?? update.key, update.key);
	}
	return new Set(latest.values());
}

const plain = (text: string) =>
	text.replace(/\[([^\]]+)\]\([^)]*\)/g, "$1").replace(/`([^`]*)`/g, "$1").replace(/\*\*([^*]+)\*\*/g, "$1");

export function splitArtifacts(body: string): { text: string; artifacts: string[] } {
	const artifacts: string[] = [];
	const lines = body.split("\n").filter((line) => {
		const path = ARTIFACT_LINE.exec(line.trim())?.[1];
		if (path) artifacts.push(path);
		return !path;
	});
	return { text: lines.join("\n").trim(), artifacts };
}

/** A short first line followed by more is the worker's headline; otherwise its first sentence is. */
export function splitHeadline(body: string): { headline: string; detail: string } {
	const newline = body.indexOf("\n");
	const firstLine = (newline < 0 ? body : body.slice(0, newline)).trim();
	if (newline > 0 && firstLine.length <= 100) {
		return { headline: plain(firstLine), detail: body.slice(newline + 1).trim() };
	}
	const sentence = /^.+?[.;](?=\s)/.exec(firstLine)?.[0] ?? firstLine;
	return { headline: plain(sentence.replace(/[.;]$/, "")), detail: body };
}

/* -------------------------------------------------------------------------- */
/* automation messages                                                         */
/* -------------------------------------------------------------------------- */

export type PullRequestRef = { number: number; title?: string; url?: string };

export type FileFeedback = {
	path: string;
	line?: number;
	/** The commented line's code, when the comment was on one. */
	code?: string;
	/** Text selected in a rendered view, when the comment was on a selection. */
	quote?: string;
	/** The comment was on the whole file, not a line. */
	wholeFile?: boolean;
	feedback: string;
};

export type BrowserAnnotation = { number: number; target: string; text: string; comment: string; changes: string[] };
export type BrowserFeedback = { pageTitle: string; pageUrl: string; items: BrowserAnnotation[]; screenshotCount: number };
export type FailedCheck = { name: string; url?: string; log?: string };
export type ReviewComment = { path?: string; line?: number; author: string; body: string };
export type GitAction = "createPR" | "createDraftPR" | "commit" | "commitPush" | "push";
export type AutomationNotice = "orchestratorReplaced" | "contextCarried" | "reviewTask";

export type Automation =
	| { kind: "fileFeedback"; comments: FileFeedback[] }
	| { kind: "diffSelection"; path: string; start?: number; end?: number; instruction: string }
	| { kind: "gitAction"; action: GitAction; base?: string }
	| { kind: "browserFeedback"; annotations: BrowserFeedback }
	| { kind: "ciFailing"; pr: PullRequestRef; checks: FailedCheck[] }
	| { kind: "reviewComments"; pr: PullRequestRef; comments: ReviewComment[] }
	| { kind: "changesRequested"; pr: PullRequestRef; author?: string; body: string }
	| { kind: "mergeConflict"; pr: PullRequestRef }
	| { kind: "notice"; notice: AutomationNotice };

// `PR #12 "title" (source → target)`, as lifecycle's prIdentity writes it.
const PR_IDENTITY = /PR #(\d+)(?: ("(?:[^"\\\n]|\\.)*"))?(?: \([^\n]*? → [^\n]*?\))?/;
const PR_LINE = /\nPR: (\S+)\s*$/;
// A GitHub or GitLab login, or the daemon's stand-in. Never markup.
const LOGIN = String.raw`[\w.-]+(?:\[bot\])?|unknown reviewer`;

function pullRequest(text: string, url?: string): PullRequestRef | undefined {
	const match = PR_IDENTITY.exec(text.split("\n", 1)[0]!);
	if (!match) return undefined;
	let title: string | undefined;
	if (match[2]) {
		try {
			title = JSON.parse(match[2]) as string;
		} catch {
			title = match[2].slice(1, -1);
		}
	}
	return { number: Number(match[1]), title, url };
}

// One check as lifecycle's formatCIFailureMessage writes it. The log tail's fence is
// longer than any backtick run inside it, so a log line cannot end the section.
const FAILED_CHECK = /\nFailed: ([^\n]*) \(([^()\n]*)\)(?:\nFailure URL: (\S+))?(?:\n\nLog tail \(last \d+ lines?\):\n(`{3,})\n([\s\S]*?)\n\4)?\n/y;

function failedChecks(text: string): FailedCheck[] | null {
	const checks: FailedCheck[] = [];
	let at = text.indexOf("\n") + 1;
	while (at > 0) {
		FAILED_CHECK.lastIndex = at;
		const match = FAILED_CHECK.exec(text);
		if (!match) break;
		checks.push({ name: match[1]!, url: match[3], log: match[5] });
		at = FAILED_CHECK.lastIndex;
	}
	return checks.length > 0 && text.startsWith("\nUse the included log tail", at) ? checks : null;
}

// Headings must number 1..N for the N the message announces; a comment body that
// looks like a heading then cannot pose as the next comment.
function reviewComments(text: string, announced: number): ReviewComment[] | null {
	const closing = text.lastIndexOf("\n\nAddress each comment and push fixes.");
	const headings = [...text.matchAll(new RegExp(String.raw`\n\n(\d+)\. ([^\n]+) \(@(${LOGIN})\):\n`, "g"))];
	if (closing < 0 || headings.length !== announced || headings.some((heading, index) => Number(heading[1]) !== index + 1)) return null;
	return headings.map((heading, index) => {
		const end = headings[index + 1]?.index ?? closing;
		const lines = text.slice(heading.index! + heading[0].length, end).replace(/\s+$/, "").split("\n");
		while (lines.length > 0 && /^ {3}(?:https?:\/\/\S+|Thread ID: .*)$/.test(lines.at(-1)!)) lines.pop();
		const at = heading[2] === "(general)" ? undefined : /^(.*?)(?::(\d+))?$/.exec(heading[2]!);
		return { path: at?.[1] || undefined, line: at?.[2] ? Number(at[2]) : undefined, author: heading[3]!, body: lines.join("\n").trim() };
	});
}

// Read from the end: the review URL and ID follow the free-text body.
function changesRequested(text: string): { body: string; url?: string } | null {
	const closing = text.lastIndexOf("\n\nAddress the requested changes and push.");
	if (closing < 0) return null;
	let rest = text.slice(0, closing);
	const id = /\nReview ID: [^\n]*$/.exec(rest);
	if (id) rest = rest.slice(0, id.index);
	const url = /\n\nReview URL: (\S+)$/.exec(rest);
	if (url) rest = rest.slice(0, url.index);
	const bodyAt = rest.indexOf("\n\nReview body:\n");
	return { body: bodyAt < 0 ? "" : rest.slice(bodyAt + "\n\nReview body:\n".length).trim(), url: url?.[1] };
}

function readPullRequestNudge(text: string): Automation | null {
	const prURL = PR_LINE.exec(text)?.[1];
	if (text.startsWith("There are merge conflicts on ")) {
		const pr = pullRequest(text, prURL);
		return pr ? { kind: "mergeConflict", pr } : null;
	}
	if (text.startsWith("CI is failing on ")) {
		const pr = pullRequest(text, prURL);
		const checks = pr ? failedChecks(text) : null;
		return pr && checks ? { kind: "ciFailing", pr, checks } : null;
	}
	const announced = /^The following (\d+) unresolved review comment\(s\) are on /.exec(text);
	if (announced) {
		const pr = pullRequest(text, prURL);
		const comments = pr ? reviewComments(text, Number(announced[1])) : null;
		return pr && comments ? { kind: "reviewComments", pr, comments } : null;
	}
	const reviewer = new RegExp(String.raw`^A changes-requested review from @(${LOGIN}) is on `).exec(text);
	if (reviewer) {
		const review = changesRequested(text.replace(PR_LINE, ""));
		const pr = review ? pullRequest(text, review.url ?? prURL) : undefined;
		return pr && review ? { kind: "changesRequested", pr, author: reviewer[1], body: review.body } : null;
	}
	// AO's own reviewer, before its findings became GitHub reviews; only old history carries these.
	if (text.startsWith("[AO reviewer] ")) {
		const url = /^PR: (\S+)$/m.exec(text)?.[1];
		const number = Number(/\/pull\/(\d+)/.exec(url ?? "")?.[1]);
		if (!url || !number) return null;
		const body = /\n\nReview body:\n([\s\S]*?)(?=\n\nReview \d+\n|$)/.exec(text)?.[1] ?? "";
		return { kind: "changesRequested", pr: { number, url }, body: body.trim() };
	}
	return null;
}

// The "- Key: value" lines file-annotations' commentLines writes under "File context:".
function fileFeedback(block: string): FileFeedback | null {
	const feedback = /^Feedback:\n([\s\S]*?)\n\nFile context:\n/.exec(block.trim());
	if (!feedback) return null;
	const fields = new Map<string, string>();
	for (const line of block.slice(block.indexOf("File context:\n")).split("\n")) {
		const field = /^- ([A-Za-z ]+): (.*)$/.exec(line);
		if (field) fields.set(field[1]!, field[2]!);
	}
	const path = fields.get("Path");
	if (!path) return null;
	// Only a new-side line exists in the file Files opens; a deleted line has none.
	const location = fields.get("Location");
	const line = Number(fields.get("New line") ?? /^New side, line (\d+)$/.exec(location ?? "")?.[1]);
	const code = fields.get("Code");
	return {
		path,
		line: Number.isSafeInteger(line) && line > 0 ? line : undefined,
		code: code && code !== "(blank line)" ? code : undefined,
		quote: fields.get("Selected text"),
		wholeFile: location === "Entire file" || undefined,
		feedback: feedback[1]!.trim() === "(empty)" ? "" : feedback[1]!.trim(),
	};
}

// diff-selection's message; its instruction and path are each one line.
const DIFF_SELECTION = /^The user selected lines in the diff viewer and asked for a change\.\n\nInstruction:\n([^\n]*)\n\nFile: ([^\n]+)\n(?:(?:Comparison scope|Workspace version|File fingerprint): [^\n]*\n)*Selected lines (?:(\d+)-(\d+)|[^\n]*):\n\n/;

const BROWSER_ANNOTATIONS = /^<browser_annotations>\n([\s\S]*?)\n<\/browser_annotations>$/;

function quotedField(value: string): string {
	try {
		const parsed: unknown = JSON.parse(value);
		return typeof parsed === "string" ? parsed : value;
	} catch {
		return value;
	}
}

/** The desktop browser's annotation handoff, as formatBrowserAnnotationMessage writes it. */
export function parseBrowserFeedback(message: string): BrowserFeedback | null {
	const match = BROWSER_ANNOTATIONS.exec(message.trim());
	if (!match) return null;
	const lines = (match[1] ?? "").split("\n");
	const after = (prefix: string) => lines.find((line) => line.startsWith(prefix))?.slice(prefix.length).trim() ?? "";
	const items: BrowserAnnotation[] = [];
	let current: BrowserAnnotation | null = null;
	let readingChanges = false;
	let readingScreenshots = false;
	let screenshotCount = 0;
	for (const line of lines) {
		const heading = /^Annotation (\d+) \((comment|adjustment)\):$/.exec(line);
		if (heading) {
			current = { number: Number(heading[1]), target: "", text: "", comment: "", changes: [] };
			items.push(current);
			readingChanges = false;
			readingScreenshots = false;
			continue;
		}
		if (line === "Requested visual changes:") {
			readingChanges = true;
			continue;
		}
		if (line === "Reference screenshots:") {
			current = null;
			readingChanges = false;
			readingScreenshots = true;
			continue;
		}
		if (readingScreenshots && line.startsWith("- ")) {
			screenshotCount += 1;
			continue;
		}
		if (!current) continue;
		if (line.startsWith("Target: ")) current.target = line.slice("Target: ".length).trim();
		else if (line.startsWith("Element text: ")) current.text = quotedField(line.slice("Element text: ".length).trim());
		else if (line.startsWith("Comment: ")) current.comment = line.slice("Comment: ".length).trim();
		else if (readingChanges && line.startsWith("- ")) current.changes.push(line.slice(2).trim());
	}
	return { pageTitle: after("Page: ") || "Untitled page", pageUrl: after("URL: "), items, screenshotCount };
}

function readUserAction(text: string): Automation | null {
	if (text.startsWith("The user left inline feedback while reviewing a file in AO and asked for a change.\n")) {
		const comment = fileFeedback(text.slice(text.indexOf("\n\n") + 2).replace(/\n\nApply this feedback in the current workspace\.[^\n]*$/, ""));
		return comment ? { kind: "fileFeedback", comments: [comment] } : null;
	}
	if (/^The user left \d+ inline feedback comments? while reviewing files in AO and asked for changes\./.test(text)) {
		const blocks = text.replace(/\n\nApply every comment in the current workspace\.[^\n]*$/, "").split(/\n\nComment \d+:\n/).slice(1);
		const comments = blocks.map(fileFeedback);
		return comments.length > 0 && comments.every(Boolean) ? { kind: "fileFeedback", comments: comments as FileFeedback[] } : null;
	}
	const selection = DIFF_SELECTION.exec(text);
	if (selection) {
		const [, instruction, path, start, end] = selection;
		// The range is numbered by the new side unless an end of it is a deleted
		// line ("- " row); only new-side numbers name a line in the file.
		const rows = text.slice(selection[0].length).split("\n");
		const truncated = rows.at(-1) === "[truncated]";
		const newSide = rows.some((row) => !row.startsWith("-"));
		const startsNew = newSide && rows[0] !== undefined && !rows[0].startsWith("-");
		const endsNew = newSide && !truncated && !rows.at(-1)!.startsWith("-");
		return {
			kind: "diffSelection",
			path: path!,
			start: start && startsNew ? Number(start) : undefined,
			end: start && startsNew && endsNew ? Number(end) : undefined,
			instruction: instruction === "(empty)" ? "" : instruction!,
		};
	}
	// The instructions the git buttons send.
	const createPR = /^Commit any uncommitted changes with a clear message, push this branch, and open a (draft )?pull request against (?:`([^`]+)`|the default branch)\. Give it a descriptive title and summary\.$/.exec(text);
	if (createPR) return { kind: "gitAction", action: createPR[1] ? "createDraftPR" : "createPR", base: createPR[2] };
	if (text === "Commit all uncommitted changes with a clear message. Do not push.") return { kind: "gitAction", action: "commit" };
	if (/^Commit all uncommitted changes with a clear message and push this branch(?: to update PR #\d+)?\.$/.test(text)) return { kind: "gitAction", action: "commitPush" };
	if (/^Push this branch(?: to update PR #\d+)?\.$/.test(text)) return { kind: "gitAction", action: "push" };
	const annotations = parseBrowserFeedback(text);
	return annotations ? { kind: "browserFeedback", annotations } : null;
}

function readNotice(text: string): Automation | null {
	if (text.startsWith("AO is replacing this project orchestrator. Stop coordinating new work now")) return { kind: "notice", notice: "orchestratorReplaced" };
	if (text.startsWith("AO transferred the previous agent's context in hidden system instructions.")) return { kind: "notice", notice: "contextCarried" };
	if (/^Read and follow the AO review task in `[^`]+`\.?$/.test(text)) return { kind: "notice", notice: "reviewTask" };
	return null;
}

/** What an automation message is, read from the text AO wrote for it; null when it is none of the known kinds. */
export function readAutomation(text: string): Automation | null {
	const trimmed = text.trim();
	return readUserAction(trimmed) ?? readPullRequestNudge(trimmed) ?? readNotice(trimmed);
}

// A message an agent sent is the agent's, whatever its text says.
export const automationOf = cachedBy((item): Automation | null =>
	item.kind === "message" && item.role === "user" && item.origin !== "human" && !item.senderSessionId && !/^\s*\[from /.test(item.text)
		? readAutomation(item.text)
		: null,
);

/** The forges AO reads PRs from. A URL in the text links only to these, so a look-alike message cannot send the user elsewhere. */
export function forgeURL(url: string | undefined): string | undefined {
	if (!url) return undefined;
	try {
		const { protocol, hostname } = new URL(url);
		return protocol === "https:" && (hostname === "github.com" || hostname === "gitlab.com") ? url : undefined;
	} catch {
		return undefined;
	}
}

export const GIT_ACTION_LABELS: Record<GitAction, string> = {
	createPR: "Create PR",
	createDraftPR: "Create draft PR",
	commit: "Commit",
	commitPush: "Commit & push",
	push: "Push",
};

export const NOTICE_LABELS: Record<AutomationNotice, string> = {
	orchestratorReplaced: "AO is replacing this orchestrator. A fresh one takes over in a new workspace.",
	contextCarried: "Context carried over from the previous agent",
	reviewTask: "AO gave this reviewer its review task",
};

/**
 * Where a reported artifact sits in its session's artifact folder, the path the
 * daemon's artifact route serves. Reports name artifacts absolutely
 * (`…/artifacts/<session>/report.md`) or relative to that folder; a path outside
 * it has no entry and is not opened.
 */
export function artifactEntry(path: string, sessionId: string | undefined): string | undefined {
	const trimmed = path.trim().replace(/\\/g, "/");
	if (!trimmed || !sessionId) return undefined;
	let entry = trimmed;
	if (trimmed.startsWith("/") || /^[A-Za-z]:\//.test(trimmed)) {
		const marker = `/artifacts/${sessionId}/`;
		const at = trimmed.lastIndexOf(marker);
		if (at < 0) return undefined;
		entry = trimmed.slice(at + marker.length);
	}
	// The entry is joined into an authenticated daemon URL, so it may only name
	// something inside the folder: no parent steps, whichever form the path took.
	const segments = entry.split("/").filter((segment) => segment !== "" && segment !== ".");
	return segments.length > 0 && !segments.includes("..") ? segments.join("/") : undefined;
}

export type InlineRun = { text: string; href?: string; code?: boolean; strong?: boolean };

// A markdown link, a bare web or session link, inline code, or bold: the inline
// markup workers put in reports. Trailing punctuation stays out of a bare link.
const INLINE = /\[([^\]\n]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>()]*[^\s<>().,;:!?'"])|(ao:\/\/sessions\/[^\s<>()]*[^\s<>().,;:!?'"])|`([^`\n]+)`|\*\*([^*\n]+)\*\*/g;

/** A report's text as runs to draw, its links kept as links. */
export function inlineRuns(text: string): InlineRun[] {
	const runs: InlineRun[] = [];
	let at = 0;
	for (const match of text.matchAll(INLINE)) {
		if (match.index! > at) runs.push({ text: text.slice(at, match.index) });
		if (match[1] !== undefined) runs.push({ text: match[1], href: match[2] });
		else if (match[3] !== undefined || match[4] !== undefined) runs.push({ text: match[0], href: match[0] });
		else if (match[5] !== undefined) runs.push({ text: match[5], code: true });
		else runs.push({ text: match[6]!, strong: true });
		at = match.index! + match[0].length;
	}
	if (at < text.length) runs.push({ text: text.slice(at) });
	return runs;
}

/** The project and session an `ao://sessions/<project>/<session>` link names. */
export function sessionLink(href: string): { projectId: string; sessionId: string } | undefined {
	const match = /^ao:\/\/sessions\/([^/?#\s]+)\/([^/?#\s]+)\/?$/.exec(href);
	if (!match) return undefined;
	try {
		return { projectId: decodeURIComponent(match[1]!), sessionId: decodeURIComponent(match[2]!) };
	} catch {
		return undefined;
	}
}

/**
 * Where a link in text an agent or a reviewer wrote may go. That text can repeat
 * whatever the agent read, so a link opens only a session's chat or a page on
 * the forges AO reads PRs from; any other link is drawn as its words.
 */
export function safeLink(href: string): { kind: "session"; projectId: string; sessionId: string } | { kind: "web"; href: string; host: string } | undefined {
	const session = sessionLink(href);
	if (session) return { kind: "session", ...session };
	const web = forgeURL(href);
	return web ? { kind: "web", href: web, host: new URL(web).hostname } : undefined;
}

export type ReportBlock = { kind: "heading" | "paragraph" | "item" | "code"; text: string; marker?: string };

/** A report's blocks: the headings, list items and code fences workers write, and the paragraphs between. */
export function reportBlocks(text: string): ReportBlock[] {
	const blocks: ReportBlock[] = [];
	const lines = text.split("\n");
	let paragraph: string[] = [];
	const flush = () => {
		if (paragraph.length > 0) blocks.push({ kind: "paragraph", text: paragraph.join("\n") });
		paragraph = [];
	};
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index]!;
		const fence = /^\s*(`{3,}|~{3,})/.exec(line);
		if (fence) {
			flush();
			const code: string[] = [];
			for (index += 1; index < lines.length && !lines[index]!.trimStart().startsWith(fence[1]!); index += 1) code.push(lines[index]!);
			blocks.push({ kind: "code", text: code.join("\n") });
			continue;
		}
		const heading = /^#{1,6}\s+(.+)$/.exec(line);
		const item = /^\s*([-*+]|\d+[.)])\s+(.*)$/.exec(line);
		if (heading) {
			flush();
			blocks.push({ kind: "heading", text: heading[1]!.trim() });
		} else if (item) {
			flush();
			blocks.push({ kind: "item", marker: /\d/.test(item[1]!) ? item[1]! : "•", text: item[2]! });
		} else if (line.trim() === "") {
			flush();
		} else {
			paragraph.push(line);
		}
	}
	flush();
	return blocks;
}

const CODE_SPAN = /`[^`\n]*`/g;
// Innermost first: `[![a](x)](y)` loses its image before its link is read.
const IMAGE = /!\[([^\[\]\n]*)\]\(([^()\s]*)\)/g;
// Links, autolinks, and the bare web URLs ChatMarkdown would otherwise link.
const LINK = /\[([^\]\n]+)\]\(([^)\s]+)\)|<((?:https?|ao):\/\/[^\s>]+)>|(https?:\/\/[^\s<]+)/g;

/** One stretch of Markdown outside code fences, made safe. */
function safeInline(text: string): string {
	// Code spans are set aside, not split on, so a link whose label holds code stays one link.
	const spans: string[] = [];
	let out = text.replace(/\u0000/g, "").replace(CODE_SPAN, (span) => `\u0000${spans.push(span) - 1}\u0000`);
	for (let previous = ""; previous !== out;) {
		previous = out;
		out = out.replace(IMAGE, (_image, alt: string) => alt);
	}
	out = out.replace(LINK, (match, label: string | undefined, href: string | undefined, auto: string | undefined, bare: string | undefined) => {
		if (label !== undefined && href !== undefined) {
			const target = safeLink(href);
			if (!target) return label;
			return target.kind === "web" && label !== href ? `${match} (${target.host})` : match;
		}
		const url = auto ?? bare!;
		if (safeLink(url.replace(/[.,;:!?'")\]]+$/, ""))) return match;
		return url.includes("`") ? `\`\` ${url} \`\`` : `\`${url}\``;
	});
	return out.replace(/\u0000(\d+)\u0000/g, (_token, index: string) => spans[Number(index)]!);
}

/**
 * Markdown an agent or a reviewer wrote, made safe to hand to `ChatMarkdown`, as
 * the desktop's `safeOrigin` does: a link stays a link only where `safeLink`
 * allows, and then names its host when its label is something else; any other
 * link keeps its words, a bare URL becomes code so it cannot be linked, and an
 * image, even one inside a link, becomes its alt text so nothing loads from
 * where the text points. Code is left as written.
 */
export function safeMarkdown(text: string): string {
	// A fence runs to its closing fence, or to the end of the text when it has none.
	return text.split(/(^ {0,3}(?:`{3,}|~{3,})[^\n]*\n[\s\S]*?(?:\n {0,3}(?:`{3,}|~{3,})[ \t]*(?=\n|(?![\s\S]))|(?![\s\S])))/m)
		.map((part, index) => index % 2 === 1 ? part : safeInline(part))
		.join("");
}
