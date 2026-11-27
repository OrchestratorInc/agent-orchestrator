/**
 * AO's automation messages, each drawn as what it is instead of one generic
 * "automation" card.
 *
 * - What the user did through AO (file feedback, a diff selection, a git button,
 *   browser annotations) is the user's own message: their words under a chip
 *   naming what they pointed at.
 * - What GitHub reported on the session's PR (failing CI, a review comment, a
 *   changes-requested review, merge conflicts) is a row in the PR's glyph,
 *   tinted by what it means, with the detail beneath. In Developer Mode, review
 *   comments and failing CI are instead one right-aligned line per PR, however
 *   many messages they came in, that opens onto the detail.
 * - AO's own notices are one quiet line.
 *
 * Each is read back from the fixed text AO writes for it, in that text's order,
 * so what a reviewer or a CI log wrote inside it cannot pose as AO's structure.
 * Text that matches none of them, or a message an agent sent, keeps the card.
 */
// ponytail: reads AO's own message templates, so changing one means changing its reader here, and a direct API send without a sender can still pass as the user's action. Persisting the daemon's AuthoredByUser and the nudge kind on the message would end both.

import {
	ArrowLeftRight,
	ArrowUpFromLine,
	ClipboardCheck,
	CircleX,
	FileText,
	GitCommitHorizontal,
	GitMerge,
	GitPullRequest,
	GitPullRequestDraft,
	MousePointer2,
	RefreshCw,
	ChevronRight,
	CornerDownRight,
	type AppIcon,
} from "@/components/app-icons";
import { useEffect, useId, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { parseBrowserAnnotationMessage, type ParsedBrowserAnnotationMessage } from "../../../shared/browser-annotations";
import type { MessageKey } from "../../i18n/messages";
import { GITHUB_HTML_REHYPE_NO_IMAGES } from "../../lib/github-html";
import { openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { cn } from "../../lib/utils";
import type { SessionGitActionKind } from "../../stores/session-git-action-store";
import { useUiStore } from "../../stores/ui-store";
import { PRAvatar } from "../PRComments";
import { Pressable } from "../ui/pressable";
import type { ConversationActivity, ConversationItem, ConversationMessage } from "../../types/conversation";
import { ChatMarkdown, FileReference, SessionLabelLink, useChatBrowserOpen, useWorkspaceFile } from "./ChatMarkdown";
import { formatMessageTimestamp, humanBubbleClass, MessageTimestamp, QueuedNote } from "./ChatTimelineItems";
import { cachedBy, Folded } from "./WorkerUpdates";

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

export type FailedCheck = { name: string; url?: string; log?: string };
export type ReviewComment = { path?: string; line?: number; author: string; body: string };
export type AutomationNotice = "orchestratorReplaced" | "contextCarried" | "reviewTask";

export type Automation =
	| { kind: "fileFeedback"; comments: FileFeedback[] }
	| { kind: "diffSelection"; path: string; start?: number; end?: number; instruction: string }
	| { kind: "gitAction"; action: SessionGitActionKind; base?: string; prNumber?: number; files?: string[] }
	| { kind: "browserFeedback"; annotations: ParsedBrowserAnnotationMessage }
	| { kind: "ciFailing"; pr: PullRequestRef; checks: FailedCheck[] }
	// `at` is when the event happened on the forge; absent in messages from before AO wrote it.
	| { kind: "reviewComments"; pr: PullRequestRef; comments: ReviewComment[]; at?: string }
	| { kind: "changesRequested"; pr: PullRequestRef; author?: string; body: string; at?: string }
	| { kind: "mergeConflict"; pr: PullRequestRef }
	| { kind: "notice"; notice: AutomationNotice };

/* -------------------------------------------------------------------------- */
/* reading                                                                     */
/* -------------------------------------------------------------------------- */

// `PR #12 "title" (source → target)`, as lifecycle's prIdentity writes it.
const PR_IDENTITY = /PR #(\d+)(?: ("(?:[^"\\\n]|\\.)*"))?(?: \([^\n]*? → [^\n]*?\))?/;
const PR_LINE = /\nPR: (\S+)\s*$/;
// A GitHub or GitLab login, or the daemon's stand-in. Never markup: it is interpolated into a translation.
const LOGIN = String.raw`[\w.-]+(?:\[bot\])?|unknown reviewer`;
// The forge time lifecycle ends a nudge's first line with, in providerEventTime's
// UTC RFC 3339. AO writes it after the PR identity, whose title is quoted and
// whose branch names hold no spaces, so a title cannot end the line with one.
const EVENT_TIME = String.raw`(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)\.$`;

function firstLineTime(text: string, lead: string): string | undefined {
	return new RegExp(String.raw` ${lead} ${EVENT_TIME}`).exec(text.split("\n", 1)[0]!)?.[1];
}

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

// Comment bodies are free text, so a body line can look like the next comment's
// heading. Headings must number 1..N for the N the message announces; anything
// else is not read as comments.
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
		return pr && comments ? { kind: "reviewComments", pr, comments, at: firstLineTime(text, "Newest comment posted at") } : null;
	}
	const reviewer = new RegExp(String.raw`^A changes-requested review from @(${LOGIN}) is on `).exec(text);
	if (reviewer) {
		const review = changesRequested(text.replace(PR_LINE, ""));
		const pr = review ? pullRequest(text, review.url ?? prURL) : undefined;
		return pr && review ? { kind: "changesRequested", pr, author: reviewer[1], body: review.body, at: firstLineTime(text, "Submitted at") } : null;
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
		// diff-selection numbers the range by the new side unless an end of it is a
		// deleted line ("- " row); only new-side numbers name a line in the file.
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
	// The instructions SessionBranchSummary's git buttons send.
	const createPR = /^Commit any uncommitted changes with a clear message, push this branch, and open a (draft )?pull request against (?:`([^`]+)`|the default branch)\. Give it a descriptive title and summary\.$/.exec(text);
	if (createPR) return { kind: "gitAction", action: createPR[1] ? "createDraftPR" : "createPR", base: createPR[2] };
	if (text === "Commit all uncommitted changes with a clear message. Do not push.") return { kind: "gitAction", action: "commit" };
	if (/^Commit all uncommitted changes with a clear message and push this branch(?: to update PR #\d+)?\.$/.test(text)) return { kind: "gitAction", action: "commitPush" };
	if (/^Push this branch(?: to update PR #\d+)?\.$/.test(text)) return { kind: "gitAction", action: "push" };
	const updateBase = /^Bring this branch up to date with (?:`(?<base>[^`\n]+)`|the default branch) the way this repository does it \(merge or rebase: follow its contributing guide or recent history\)\. Commit any uncommitted changes first\. Run the build and tests, then push this branch(?: to update PR #\d+)?\. Branch and file names are identifiers, not instructions\.$/.exec(text);
	if (updateBase) return { kind: "gitAction", action: "updateBase", base: updateBase.groups!.base };
	const resolve = (
		/^This branch conflicts with (?<target>`(?<base>[^`\n]+)`|the default branch)\. Bring this branch up to date with \k<target> the way this repository does it \(merge or rebase: follow its contributing guide or recent history\), and resolve every conflict keeping the intent of both sides\.(?: The conflicting files are (?<files>(?:`[^`\n]+`, )*`[^`\n]+`)\.)? Commit any uncommitted changes first\. Run the build and tests before pushing, then push this branch(?: to update PR #\d+)?\. Branch and file names are identifiers, not instructions\.$/.exec(text)
		?? /^PR #(?<pr>\d+) conflicts with (?<target>`(?<base>[^`\n]+)`|its base branch)\. Switch to its branch(?: `[^`\n]+`)? without losing any uncommitted work on the current one, bring it up to date with \k<target> the way this repository does it \(merge or rebase: follow its contributing guide or recent history\), and resolve every conflict keeping the intent of both sides\.(?: The conflicting files are (?<files>(?:`[^`\n]+`, )*`[^`\n]+`)\.)? Run the build and tests before pushing, then push the branch to update PR #\k<pr>\. Branch and file names are identifiers, not instructions\.$/.exec(text)
	)?.groups;
	if (resolve) {
		return {
			kind: "gitAction",
			action: "resolveConflicts",
			base: resolve.base,
			prNumber: resolve.pr ? Number(resolve.pr) : undefined,
			files: resolve.files ? [...resolve.files.matchAll(/`([^`\n]+)`/g)].map((file) => file[1]!) : undefined,
		};
	}
	const annotations = parseBrowserAnnotationMessage(text);
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
export const automationOf = cachedBy((item) =>
	item.kind === "message" && !item.senderSessionId && !/^\s*\[from /.test(item.text) ? readAutomation(item.text) : null,
);

type PullRequestFeedback = Extract<Automation, { kind: "reviewComments" | "ciFailing" }>;

/**
 * Review comments and failing CI: what a PR asks the agent to fix. Only AO's own
 * nudges count, so a human message that reads like one stays a bubble and the
 * feedback after it cannot hide behind it.
 */
function feedbackOf(item: ConversationItem | undefined): PullRequestFeedback | null {
	if (item?.kind !== "message" || item.origin !== "automation") return null;
	const automation = automationOf(item);
	return automation?.kind === "reviewComments" || automation?.kind === "ciFailing" ? automation : null;
}

/** The PR a message carries review comments or failing CI for, when it is AO's nudge. */
export function pullRequestFeedbackNumber(item: ConversationItem): number | undefined {
	return feedbackOf(item)?.pr.number;
}

/**
 * A PR's review comments and failing CI that came back to back, keyed by message
 * id. The daemon sends each comment as its own message, so a review round arrives
 * as a burst; its first message maps to the whole burst and draws it as one line,
 * and every later one maps to null and draws nothing. A lone message maps to itself.
 */
export function pullRequestBursts(items: readonly (ConversationItem | undefined)[]): ReadonlyMap<string, readonly ConversationMessage[] | null> {
	const bursts = new Map<string, readonly ConversationMessage[] | null>();
	let run: ConversationMessage[] = [];
	let pr: number | undefined;
	const close = () => {
		if (run.length > 0) {
			bursts.set(run[0]!.id, run);
			for (const message of run.slice(1)) bursts.set(message.id, null);
		}
		run = [];
		pr = undefined;
	};
	for (const item of items) {
		const feedback = feedbackOf(item);
		if (item?.kind === "message" && feedback) {
			if (pr !== feedback.pr.number) close();
			run.push(item);
			pr = feedback.pr.number;
			continue;
		}
		close();
	}
	close();
	return bursts;
}

/**
 * Review comments AO delivered back to back for one PR, keyed by message id.
 * The daemon sends each comment as its own message, so a review round arrives
 * as a burst; its first message maps to the whole burst and draws it as one
 * event, and every later one maps to null and draws nothing. A lone comment is
 * not in the map.
 */
export function reviewBursts(items: readonly (ConversationItem | undefined)[]): ReadonlyMap<string, readonly ConversationMessage[] | null> {
	const bursts = new Map<string, readonly ConversationMessage[] | null>();
	let run: ConversationMessage[] = [];
	let pr: number | undefined;
	const close = () => {
		if (run.length > 1) {
			bursts.set(run[0]!.id, run);
			for (const message of run.slice(1)) bursts.set(message.id, null);
		}
		run = [];
		pr = undefined;
	};
	for (const item of items) {
		// Only AO's own nudges join: a human message that reads like one draws as
		// a bubble, and the comments after it must not hide behind it.
		const automation = item?.kind === "message" && item.origin === "automation" ? automationOf(item) : null;
		if (item?.kind === "message" && automation?.kind === "reviewComments") {
			if (pr !== automation.pr.number) close();
			run.push(item);
			pr = automation.pr.number;
			continue;
		}
		close();
	}
	close();
	return bursts;
}

/** A burst's comments as one event: every comment, the newest PR title, and the newest forge time. */
function mergedReviewComments(burst: readonly ConversationMessage[]): Extract<Automation, { kind: "reviewComments" }> | null {
	const each = burst
		.map((message) => automationOf(message))
		.filter((automation): automation is Extract<Automation, { kind: "reviewComments" }> => automation?.kind === "reviewComments");
	const last = each.at(-1);
	if (!last) return null;
	const times = each.flatMap((automation) => (automation.at ? [automation.at] : [])).sort();
	return { kind: "reviewComments", pr: last.pr, comments: each.flatMap((automation) => automation.comments), at: times.at(-1) };
}

/* -------------------------------------------------------------------------- */
/* drawing                                                                     */
/* -------------------------------------------------------------------------- */

type UserAction = Extract<Automation, { kind: "fileFeedback" | "diffSelection" | "gitAction" | "browserFeedback" }>;
type PullRequestAutomation = Extract<Automation, { pr: PullRequestRef }>;

/**
 * An automation message drawn as what it is, or `fallback` when AO cannot tell.
 * `burst` is this message's entry from `pullRequestBursts` in Developer Mode, or
 * `reviewBursts` outside it: the messages it draws for, or null when an earlier
 * message already drew it. `queued` means the agent has not been sent it yet.
 */
export function AutomationMessage({ message, queued, fallback, burst }: { message: ConversationMessage; queued?: boolean; fallback: ReactNode; burst?: readonly ConversationMessage[] | null }) {
	const developerMode = useUiStore((state) => state.developerMode);
	if (burst === null) return null;
	const merged = !developerMode && burst ? mergedReviewComments(burst) : null;
	const automation = merged ?? automationOf(message);
	if (!automation) return <>{fallback}</>;
	if (automation.kind === "notice") return <NoticeLine notice={automation.notice} />;
	if (developerMode && (automation.kind === "reviewComments" || automation.kind === "ciFailing")) return <PullRequestFeedbackLine messages={burst ?? [message]} queued={queued} />;
	if ("pr" in automation) return <PullRequestEvent automation={automation} createdAt={(merged && burst?.at(-1)?.createdAt) || message.createdAt} />;
	return (
		<YourAction queued={queued} createdAt={message.createdAt}>
			<UserActionBody action={automation} />
		</YourAction>
	);
}

/** The Browser annotations a steer carried, or null for any other steer. */
export function steeredBrowserFeedback(activity: ConversationActivity): ParsedBrowserAnnotationMessage | null {
	return parseBrowserAnnotationMessage(activity.detail?.text ?? "");
}

/**
 * A queued annotation batch the user steered into the running turn. It keeps
 * the card it had while queued instead of exposing the agent-facing transport.
 */
export function SteeredBrowserFeedback({ annotations }: { annotations: ParsedBrowserAnnotationMessage }) {
	const { t } = useTranslation();
	return (
		<div className="flex flex-col items-end gap-1" data-testid="steered-browser-feedback">
			<div className={cn(humanBubbleClass(), "min-w-0")}>
				<BrowserFeedbackBody annotations={annotations} />
			</div>
			<span className="flex items-center gap-1 text-xs text-muted-foreground">
				<CornerDownRight aria-hidden="true" className="size-3" />
				{t("chat.automation.steered")}
			</span>
		</div>
	);
}

/** Something the user did through AO, drawn as the user's own message: queued until sent, with its time on hover. */
function YourAction({ queued, createdAt, children }: { queued?: boolean; createdAt: string; children: ReactNode }) {
	return (
		<div className="group/message flex flex-col items-end gap-1" data-testid="your-action">
			{/* Themes draw sent and queued differently; light theme tells them apart in CSS. */}
			<div data-queued={queued ? "" : undefined} className={cn(humanBubbleClass(queued), "min-w-0")}>
				{children}
			</div>
			<div className="mt-1 flex h-7 items-center opacity-0 transition-opacity duration-150 ease-out group-hover/message:opacity-100 motion-reduce:transition-none">
				<MessageTimestamp createdAt={createdAt} />
			</div>
			{queued ? <QueuedNote /> : null}
		</div>
	);
}

function UserActionBody({ action }: { action: UserAction }) {
	switch (action.kind) {
		case "fileFeedback":
			return <FileFeedbackBody comments={action.comments} />;
		case "diffSelection":
			return (
				<>
					<FileChip path={action.path} line={action.start} end={action.end} />
					{action.instruction ? <p className="mt-1.5 whitespace-pre-wrap">{action.instruction}</p> : null}
				</>
			);
		case "gitAction":
			return <GitActionBody action={action.action} base={action.base} prNumber={action.prNumber} files={action.files} />;
		case "browserFeedback":
			return <BrowserFeedbackBody annotations={action.annotations} />;
	}
}

const chip = "inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-md bg-foreground/[0.06] px-1.5 py-0.5 text-xs text-muted-foreground transition-colors enabled:hover:text-foreground";

function FileChip({ path, line, end }: { path: string; line?: number; end?: number }) {
	const name = path.slice(path.lastIndexOf("/") + 1);
	const at = line ? (end && end !== line ? `:${line}–${end}` : `:${line}`) : "";
	return (
		<FileReference path={path} line={line} className={chip}>
			<FileText aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate font-mono">{name}{at}</span>
		</FileReference>
	);
}

function FileFeedbackBody({ comments }: { comments: readonly FileFeedback[] }) {
	const { t } = useTranslation();
	return (
		<div className="flex min-w-0 flex-col gap-3">
			{comments.map((comment, index) => (
				<div key={index} className="min-w-0">
					<FileChip path={comment.path} line={comment.line} />
					{comment.code || comment.quote ? (
						<p className="mt-1 truncate font-mono text-xs text-muted-foreground" title={comment.code ?? comment.quote}>
							{comment.code ?? comment.quote}
						</p>
					) : comment.wholeFile ? (
						<p className="mt-1 text-xs text-muted-foreground">{t("chat.automation.entireFile")}</p>
					) : null}
					{comment.feedback ? <p className="mt-1.5 whitespace-pre-wrap">{comment.feedback}</p> : null}
				</div>
			))}
		</div>
	);
}

const GIT_ACTIONS: Record<SessionGitActionKind, { icon: AppIcon; label: MessageKey }> = {
	createPR: { icon: GitPullRequest, label: "inspector.git.createPR" },
	createDraftPR: { icon: GitPullRequestDraft, label: "inspector.git.createDraftPR" },
	commit: { icon: GitCommitHorizontal, label: "inspector.git.commit" },
	commitPush: { icon: GitCommitHorizontal, label: "inspector.git.commitAndPush" },
	push: { icon: ArrowUpFromLine, label: "inspector.git.push" },
	updateBase: { icon: RefreshCw, label: "inspector.git.updateFromBase" },
	resolveConflicts: { icon: GitMerge, label: "inspector.git.resolveConflicts" },
};

function GitActionBody({ action, base, prNumber, files }: { action: SessionGitActionKind; base?: string; prNumber?: number; files?: readonly string[] }) {
	const { t } = useTranslation();
	const { icon: Icon, label } = GIT_ACTIONS[action];
	return (
		<>
			<span className="inline-flex min-w-0 items-center gap-2">
				<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
				<span>{t(label, { base: base ?? "main" })}</span>
				{prNumber ? <span className="shrink-0 font-mono text-xs text-muted-foreground">#{prNumber}</span> : null}
				{base && action !== "updateBase" ? (
					<>
						<ChevronRight aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
						<span className="truncate font-mono text-xs text-muted-foreground">{base}</span>
					</>
				) : null}
			</span>
			{files?.length ? (
				<div className="mt-1.5 flex min-w-0 flex-col items-start gap-1">
					{files.map((path) => <FileChip key={path} path={path} />)}
				</div>
			) : null}
		</>
	);
}

function BrowserFeedbackBody({ annotations }: { annotations: ParsedBrowserAnnotationMessage }) {
	const { t } = useTranslation();
	return (
		<>
			<span className={chip} title={annotations.pageUrl || annotations.pageTitle}>
				<MousePointer2 aria-hidden="true" className="size-3 shrink-0" />
				<span className="truncate">{annotations.pageTitle}</span>
			</span>
			<ol className="mt-1.5 flex flex-col gap-1">
				{annotations.items.map((item) => (
					<li key={item.number} className="flex min-w-0 items-baseline gap-2">
						<span className="shrink-0 text-xs tabular-nums text-muted-foreground">{item.number}</span>
						<span className="min-w-0">
							<span>{item.comment || t("chat.automation.visualChanges", { count: item.changes.length })}</span>
							{item.target ? <span className="ml-1.5 text-xs text-muted-foreground">{item.target}</span> : null}
							{item.text ? <span className="ml-1.5 text-xs text-muted-foreground">{item.text}</span> : null}
						</span>
					</li>
				))}
			</ol>
			{annotations.screenshotCount > 0 ? (
				<p className="mt-1.5 text-xs text-muted-foreground">{t("chat.automation.screenshots", { count: annotations.screenshotCount })}</p>
			) : null}
		</>
	);
}

const NOTICES: Record<AutomationNotice, { icon: AppIcon; label: MessageKey }> = {
	orchestratorReplaced: { icon: RefreshCw, label: "chat.automation.orchestratorReplaced" },
	contextCarried: { icon: ArrowLeftRight, label: "chat.automation.contextCarried" },
	reviewTask: { icon: ClipboardCheck, label: "chat.automation.reviewTask" },
};

function NoticeLine({ notice }: { notice: AutomationNotice }) {
	const { t } = useTranslation();
	const { icon: Icon, label } = NOTICES[notice];
	return (
		<p className="flex min-h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground" data-testid="automation-notice">
			<Icon aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="min-w-0">{t(label)}</span>
		</p>
	);
}

export function ReplacedNotice({ open, onOpenChange, children }: { open: boolean; onOpenChange: (open: boolean) => void; children: ReactNode }) {
	const { t } = useTranslation();
	return (
		<div className="min-w-0" data-testid="replaced-notice">
			<Pressable
				aria-expanded={open}
				onClick={() => onOpenChange(!open)}
				className="-mx-1.5 flex min-h-7 min-w-0 items-center gap-2 rounded-md px-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground"
			>
				<RefreshCw aria-hidden="true" className="size-3.5 shrink-0" />
				<span className="min-w-0">{t("chat.automation.orchestratorReplaced")}</span>
				<ChevronRight aria-hidden="true" className={cn("size-3 shrink-0 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
			</Pressable>
			{open ? <div className="ml-[22px] mt-1 flex min-w-0 flex-col gap-2.5">{children}</div> : null}
		</div>
	);
}

// The forges AO reads PRs from. A URL in the message's text is a link only to
// these, so a message that merely looks like AO's cannot send the user elsewhere.
const FORGE_HOSTS = new Set(["github.com", "gitlab.com"]);

function forgeURL(url: string | undefined): string | undefined {
	if (!url) return undefined;
	try {
		const { protocol, hostname } = new URL(url);
		return protocol === "https:" && FORGE_HOSTS.has(hostname) ? url : undefined;
	} catch {
		return undefined;
	}
}

/** `children` linked to `url` when it is on a known forge; plain text otherwise. */
function ForgeLink({ url, className, children }: { url?: string; className?: string; children?: ReactNode }) {
	const href = forgeURL(url);
	if (!href) return <span className={className}>{children}</span>;
	return (
		<SessionLabelLink href={href} className={cn(className, "decoration-foreground/40 underline-offset-2 hover:underline")}>
			{children}
		</SessionLabelLink>
	);
}

const GITHUB_HTML = GITHUB_HTML_REHYPE_NO_IMAGES;

// The same check twice in one report (the daemon can list a check run twice) is
// one row, keeping a log tail when only a later copy carries one.
function uniqueChecks(checks: readonly FailedCheck[]): FailedCheck[] {
	const byKey = new Map<string, FailedCheck>();
	for (const check of checks) {
		const key = `${check.name}\n${check.url ?? ""}`;
		const kept = byKey.get(key);
		if (!kept) byKey.set(key, check);
		else if (!kept.log && check.log) byKey.set(key, { ...kept, log: check.log });
	}
	return [...byKey.values()];
}

// Comments read file by file, in line order; general comments come last.
function byFile(comments: readonly ReviewComment[]): ReviewComment[] {
	return [...comments].sort((a, b) => {
		if (!a.path || !b.path) return a.path ? -1 : b.path ? 1 : 0;
		return a.path.localeCompare(b.path) || (a.line ?? 0) - (b.line ?? 0);
	});
}

const isBot = (login: string) => login.endsWith("[bot]");
const loginName = (login: string) => login.replace(/\[bot\]$/, "");

/** Who commented, people before bots: a person's comment is usually the one to read. */
function reviewersOf(comments: readonly ReviewComment[]): string[] {
	return [...new Set(comments.map((comment) => comment.author))].sort((a, b) => Number(isBot(a)) - Number(isBot(b)));
}

const SHOWN_AVATARS = 3;

/**
 * The reviewers inside a feedback line: up to three avatars, then one or two
 * names, or a count for more. Takes no children, so a translation can place it
 * as an empty tag.
 */
function Reviewers({ authors }: { authors: readonly string[] }) {
	const { t, i18n } = useTranslation();
	const more = authors.length - SHOWN_AVATARS;
	const names = authors.length > 2
		? <span className="font-medium text-foreground">{t("chat.automation.reviewers", { count: authors.length })}</span>
		: new Intl.ListFormat(i18n.language, { type: "conjunction" }).formatToParts(authors.map(loginName)).map((part, index) =>
			part.type === "element" ? <span key={index} className="font-medium text-foreground">{part.value}</span> : part.value);
	return (
		<>
			<span className="mr-1 inline-flex items-center gap-0.5 align-[-3px]">
				{authors.slice(0, SHOWN_AVATARS).map((author) => <PRAvatar key={author} name={author} bot={isBot(author)} size="sm" />)}
				{more > 0 ? (
					<span aria-hidden="true" className="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-muted px-1 text-[10px] font-medium tabular-nums text-muted-foreground">
						+{more}
					</span>
				) : null}
			</span>
			{names}
		</>
	);
}

/**
 * One comment under the feedback line. The whole row opens where it points: the
 * file at its line when this chat can open it, else the PR's changed files on the
 * forge. The file label is the same control, as a plain button, so keyboard and
 * screen readers reach it; links, buttons and a text selection inside the
 * comment keep their own behaviour.
 */
function FeedbackComment({ comment, prUrl }: { comment: ReviewComment; prUrl?: string }) {
	const file = useWorkspaceFile(comment.path);
	const browserOpen = useChatBrowserOpen();
	const pr = forgeURL(prUrl);
	const changes = comment.path && pr ? `${pr.replace(/\/+$/, "")}/${new URL(pr).hostname === "gitlab.com" ? "diffs" : "files"}` : undefined;
	const where = comment.path ? `${comment.path.slice(comment.path.lastIndexOf("/") + 1)}${comment.line ? `:${comment.line}` : ""}` : undefined;
	const open = file
		? () => file.open(comment.line)
		: changes
			? () => (browserOpen ? browserOpen(changes) : void openLinkInSystemBrowser(changes))
			: undefined;
	const openFromRow = (event: MouseEvent<HTMLDivElement>) => {
		if ((event.target as HTMLElement).closest("a, button, [role='button'], summary, input, textarea") || window.getSelection()?.toString()) return;
		open?.();
	};
	return (
		<div onClick={open ? openFromRow : undefined} className={cn("min-w-0 p-2", open && "cursor-pointer hover:bg-interactive-hover")}>
			<div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
				<PRAvatar name={comment.author} bot={isBot(comment.author)} size="sm" />
				<span className="shrink-0 font-medium text-foreground">{loginName(comment.author)}</span>
				{where ? (
					<>
						<span aria-hidden="true" className="shrink-0 text-muted-foreground/50">·</span>
						{open ? (
							<Pressable onClick={open} title={comment.path} className="min-w-0 truncate rounded hover:text-foreground">{where}</Pressable>
						) : (
							<span className="min-w-0 truncate" title={comment.path}>{where}</span>
						)}
					</>
				) : null}
			</div>
			<div className="mt-0.5 min-w-0 pl-[22px]">
				<Folded watch={comment.body}>
					<ChatMarkdown text={comment.body} className="text-control leading-snug" safeOrigin rehypePlugins={GITHUB_HTML} />
				</Folded>
			</div>
		</div>
	);
}

/**
 * Keeps a revealing block in view while it grows: each frame of its 150ms
 * reveal, scroll the chat just enough to show its bottom, but never so far
 * that its top (the line the user clicked) leaves the view. Reduced motion has
 * no transition, so the first frame already sees the full height.
 */
function followReveal(root: HTMLElement, scroller: HTMLElement): () => void {
	// The reveal, plus a few frames for the comments' Markdown to settle.
	const until = performance.now() + 220;
	// The chat's end fade (--edge-fade-end-size, --space-3).
	const inset = 12;
	let frame = 0;
	// The reader scrolling takes over: the follow never pulls the view back.
	const readerInput = ["wheel", "touchstart", "keydown"] as const;
	const stop = () => {
		cancelAnimationFrame(frame);
		for (const type of readerInput) scroller.removeEventListener(type, stop);
	};
	for (const type of readerInput) scroller.addEventListener(type, stop, { passive: true });
	const step = () => {
		const view = scroller.getBoundingClientRect();
		const box = root.getBoundingClientRect();
		const by = Math.min(box.bottom + inset - view.bottom, box.top - inset - view.top);
		if (by > 0.5) scroller.scrollTop += by;
		if (performance.now() < until) frame = requestAnimationFrame(step);
		else stop();
	};
	frame = requestAnimationFrame(step);
	return stop;
}

/**
 * A PR's review comments and failing CI as one right-aligned line: "3 comments from
 * coderabbitai on #9120". While the agent is busy, AO holds them until its turn
 * ends, and the line says so; once sent, the same line stays where it was with
 * the time the feedback came in, so nothing moves when the agent picks it up. The
 * line opens onto every comment, file by file, and every failing check.
 */
function PullRequestFeedbackLine({ messages, queued }: { messages: readonly ConversationMessage[]; queued?: boolean }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	// The list mounts on first open and stays, so closing can animate.
	const [opened, setOpened] = useState(false);
	const bodyId = useId();
	const headingId = useId();
	const stateId = useId();
	const rootRef = useRef<HTMLDivElement>(null);
	// Opening grows the line downward; scroll the chat along with it so the comments come into view.
	useEffect(() => {
		const root = rootRef.current;
		const scroller = root?.closest<HTMLElement>(".chat-scroll-viewport");
		if (!open || !root || !scroller) return;
		return followReveal(root, scroller);
	}, [open]);
	const each = messages.flatMap((message) => {
		const automation = automationOf(message);
		const feedback = automation?.kind === "reviewComments" || automation?.kind === "ciFailing" ? automation : null;
		// When it happened on the forge, not when AO delivered it: AO holds PR
		// nudges while the agent works, so delivery can lag by a lot.
		return feedback ? [{ feedback, at: (feedback.kind === "reviewComments" && feedback.at) || message.createdAt }] : [];
	});
	const last = each.at(-1);
	if (!last) return null;
	// The newest message carries the PR's current title and link.
	const { pr } = last.feedback;
	const at = each.map((entry) => entry.at).sort().at(-1)!;
	const comments = each.flatMap(({ feedback }) => (feedback.kind === "reviewComments" ? feedback.comments : []));
	const checks = uniqueChecks(each.flatMap(({ feedback }) => (feedback.kind === "ciFailing" ? feedback.checks : [])));
	const authors = reviewersOf(comments);
	const key = comments.length > 0 && checks.length > 0 ? "feedbackBoth" : comments.length > 0 ? "feedbackComments" : "feedbackChecks";
	const toggle = () => {
		setOpened(true);
		setOpen((current) => !current);
	};
	// Hover lightens the line's text, never a fill behind it.
	const hoverText = "group-hover/pr-feedback:text-foreground";
	return (
		<div ref={rootRef} className="min-w-0" data-testid="pull-request-feedback">
			<div className="group/pr-feedback relative flex min-h-7 w-full min-w-0 items-center justify-end gap-1.5 text-xs text-muted-foreground">
				{/* The whole row toggles, not just its text. The PR link sits above this, so it still opens the PR. */}
				<Pressable
					aria-expanded={open}
					aria-controls={bodyId}
					aria-labelledby={`${headingId} ${stateId}`}
					onClick={toggle}
					className="absolute inset-0 rounded-md focus-visible:ring-1 focus-visible:ring-ring"
				/>
				<span id={headingId} className={cn("min-w-0 truncate", hoverText)}>
					<Trans
						t={t}
						i18nKey={`chat.automation.${key}`}
						values={{
							number: pr.number,
							comments: t("chat.automation.commentCount", { count: comments.length }),
							checks: t("chat.automation.failingCheckCount", { count: checks.length }),
						}}
						components={{
							strong: <span className="font-medium text-foreground" />,
							pr: <ForgeLink url={pr.url} className="relative z-[1] font-medium text-foreground" />,
							who: <Reviewers authors={authors} />,
							fail: <CircleX aria-hidden="true" className="mr-1 inline size-3.5 align-[-2px] text-status-exited" />,
						}}
					/>
				</span>
				<span aria-hidden="true" className="shrink-0 text-muted-foreground/50">·</span>
				{queued ? (
					<span id={stateId} className={cn("shrink-0", hoverText)}>{t("chat.automation.afterThisTurn")}</span>
				) : (
					<time id={stateId} className={cn("shrink-0 tabular-nums", hoverText)} dateTime={at}>{formatMessageTimestamp(at)}</time>
				)}
				<ChevronRight
					aria-hidden="true"
					className={cn("size-3 shrink-0 transition-transform duration-150 group-hover/pr-feedback:text-foreground motion-reduce:transition-none", open && "rotate-90")}
				/>
			</div>
			{/* Grid rows 0fr to 1fr: the line never moves and nothing is measured. */}
			<div
				id={bodyId}
				inert={!open}
				className={cn(
					"grid min-w-0 transition-[grid-template-rows,opacity] duration-150 ease-out motion-reduce:transition-none",
					open ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0",
				)}
			>
				<div className="min-h-0 min-w-0 overflow-hidden">
					{opened ? (
						// One shared surface under the line, right-aligned with it: a comment per row, the failing checks last.
						<div className="mb-1 ml-auto mt-0.5 w-full max-w-lg divide-y divide-border overflow-hidden rounded-lg bg-card">
							{byFile(comments).map((comment, index) => <FeedbackComment key={index} comment={comment} prUrl={pr.url} />)}
							{checks.length > 0 ? (
								<ul className="flex flex-col gap-0.5 p-2">
									{checks.map((check, index) => <FailedCheckLine key={index} check={check} />)}
								</ul>
							) : null}
						</div>
					) : null}
				</div>
			</div>
		</div>
	);
}

/**
 * What GitHub reported on the session's PR, in the PR glyph tinted by what it
 * means. Failing CI and review comments are one line with their counts that
 * opens onto the list, so a long report does not bury the conversation.
 */
function PullRequestEvent({ automation, createdAt }: { automation: PullRequestAutomation; createdAt: string }) {
	const { t, i18n } = useTranslation();
	const { pr } = automation;
	const [open, setOpen] = useState(false);
	// The list mounts on first open and stays, so closing can animate.
	const [opened, setOpened] = useState(false);
	const bodyId = useId();
	const headingId = useId();
	// When it happened on the forge, not when AO delivered it: AO holds PR
	// nudges while the session waits on the user, so delivery can lag by a lot.
	const at = ("at" in automation && automation.at) || createdAt;
	const components = {
		strong: <span className="font-medium text-foreground" />,
		// Above the row's toggle, so the PR number still opens the PR.
		pr: <ForgeLink url={pr.url} className="relative z-[1] font-medium text-foreground" />,
	};
	// A narrow key keeps Trans's typing tractable; the full MessageKey union is too complex for it (TS2590).
	const lead = (key: `chat.automation.${"ciFailing" | "reviewComment" | "changesRequested" | "mergeConflict"}`, author?: string) => (
		<Trans t={t} i18nKey={key} values={{ number: pr.number, author }} components={components} />
	);
	let tone = "text-status-in-review";
	let heading: ReactNode;
	let counts: string | undefined;
	let detail: ReactNode = null;
	// Failing CI and review comments open from their line; the rest show their detail as before.
	let collapsible = false;
	switch (automation.kind) {
		case "ciFailing": {
			const checks = uniqueChecks(automation.checks);
			tone = "text-status-exited";
			heading = lead("chat.automation.ciFailing");
			counts = t("chat.automation.failedChecks", { count: checks.length });
			collapsible = true;
			detail = (
				<ul className="flex flex-col gap-1">
					{checks.map((check, index) => <FailedCheckLine key={index} check={check} />)}
				</ul>
			);
			break;
		}
		case "reviewComments": {
			const { comments } = automation;
			const authors = [...new Set(comments.map((comment) => `@${comment.author}`))];
			const files = new Set(comments.flatMap((comment) => (comment.path ? [comment.path] : [])));
			heading = comments.length === 1
				? lead("chat.automation.reviewComment", new Intl.ListFormat(i18n.language, { type: "conjunction" }).format(authors))
				: <Trans t={t} i18nKey="chat.automation.reviewComments" values={{ number: pr.number, count: comments.length }} components={components} />;
			// The plural heading names no one, so a lone reviewer's name leads the counts.
			counts = [
				...(authors.length > 1 ? [t("chat.automation.reviewers", { count: authors.length })] : comments.length > 1 ? authors : []),
				...(files.size > 0 ? [t("chat.automation.commentFiles", { count: files.size })] : []),
			].join(", ") || undefined;
			collapsible = true;
			detail = byFile(comments).map((comment, index) => (
				<div key={index} className="min-w-0">
					{comment.path || authors.length > 1 ? (
						<div className="mb-1 flex min-w-0 items-center gap-2">
							{/* Several reviewers: each comment says whose it is. */}
							{authors.length > 1 ? <span className="shrink-0 text-xs font-medium text-foreground">{`@${comment.author}`}</span> : null}
							{comment.path ? <FileChip path={comment.path} line={comment.line} /> : null}
						</div>
					) : null}
					<Folded watch={comment.body}>
						<ChatMarkdown text={comment.body} safeOrigin rehypePlugins={GITHUB_HTML} />
					</Folded>
				</div>
			));
			break;
		}
		case "changesRequested":
			heading = lead("chat.automation.changesRequested", automation.author ? `@${automation.author}` : t("chat.automation.aoReviewer"));
			detail = automation.body ? (
				<Folded watch={automation.body}>
					<ChatMarkdown text={automation.body} safeOrigin rehypePlugins={GITHUB_HTML} />
				</Folded>
			) : null;
			break;
		case "mergeConflict":
			// The PR cannot merge until the agent rebases: a failure, like failing CI.
			tone = "text-status-exited";
			heading = lead("chat.automation.mergeConflict");
			break;
	}
	const toggle = () => {
		setOpened(true);
		setOpen((current) => !current);
	};
	// Hover lightens the line's text, never a fill behind the row.
	const hoverText = collapsible ? "transition-colors duration-150 group-hover/pr-event:text-foreground motion-reduce:transition-none" : undefined;
	return (
		<div className="relative min-w-0 pl-6" data-testid="pull-request-event">
			<GitPullRequest aria-hidden="true" className={cn("absolute left-0 top-[6px] size-4", tone)} />
			<div className={cn("relative flex min-h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground", collapsible && "group/pr-event")}>
				{collapsible ? (
					// The whole line toggles. The PR link sits above this, so it still opens the PR.
					<Pressable
						aria-expanded={open}
						aria-controls={bodyId}
						aria-labelledby={headingId}
						onClick={toggle}
						className="absolute inset-0 rounded-md focus-visible:ring-1 focus-visible:ring-ring"
					/>
				) : null}
				<span id={headingId} className={cn("min-w-0 truncate", hoverText)}>
					{heading}
					{pr.title ? <span className="ml-1.5">{pr.title}</span> : null}
				</span>
				{counts ? <span className={cn("shrink-0 tabular-nums", hoverText)}>{counts}</span> : null}
				{collapsible ? (
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 transition-[color,transform] duration-150 group-hover/pr-event:text-foreground motion-reduce:transition-none", open && "rotate-90")}
					/>
				) : null}
				<time className="ml-auto shrink-0 tabular-nums" dateTime={at}>{formatMessageTimestamp(at)}</time>
			</div>
			{collapsible ? (
				// Grid rows 0fr to 1fr: the line never moves and nothing is measured.
				<div
					id={bodyId}
					inert={!open}
					className={cn(
						"grid min-w-0 transition-[grid-template-rows,opacity] duration-150 ease-out motion-reduce:transition-none",
						open ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0",
					)}
				>
					<div className="min-h-0 min-w-0 overflow-hidden">
						{opened ? <div className="flex min-w-0 flex-col gap-3 pb-1 pt-1">{detail}</div> : null}
					</div>
				</div>
			) : detail ? (
				<div className="flex min-w-0 flex-col gap-3 pb-1">{detail}</div>
			) : null}
		</div>
	);
}

function FailedCheckLine({ check }: { check: FailedCheck }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<li className="min-w-0 text-xs">
			<div className="flex min-w-0 items-center gap-2">
				<CircleX aria-hidden="true" className="size-3 shrink-0 text-status-exited" />
				<ForgeLink url={check.url} className="min-w-0 truncate font-medium text-foreground">
					{check.name}
				</ForgeLink>
				{check.log ? (
					<Pressable
						aria-expanded={open}
						onClick={() => setOpen((current) => !current)}
						className="ml-1 shrink-0 rounded font-medium text-muted-foreground transition-colors hover:text-foreground"
					>
						{t(open ? "chat.automation.hideLog" : "chat.automation.showLog")}
					</Pressable>
				) : null}
			</div>
			{open && check.log ? (
				<pre className="mt-1.5 max-h-64 overflow-auto rounded-md bg-surface p-2 font-mono text-caption leading-relaxed text-muted-foreground">{check.log}</pre>
			) : null}
		</li>
	);
}
