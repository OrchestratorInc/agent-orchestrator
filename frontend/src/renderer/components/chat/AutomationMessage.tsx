/**
 * AO's automation messages, each drawn as what it is instead of one generic
 * "automation" card.
 *
 * - What the user did through AO (file feedback, a diff selection, a git button,
 *   browser annotations) is the user's own message: their words under a chip
 *   naming what they pointed at.
 * - What GitHub reported on the session's PR (failing CI, a review comment, a
 *   changes-requested review, merge conflicts) is a row in the PR's glyph,
 *   tinted by what it means, with the detail beneath.
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
	GitPullRequest,
	GitPullRequestDraft,
	MousePointer2,
	RefreshCw,
	ChevronRight,
	type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { parseBrowserAnnotationMessage, type ParsedBrowserAnnotationMessage } from "../../../shared/browser-annotations";
import type { MessageKey } from "../../i18n/messages";
import { cn } from "../../lib/utils";
import type { ConversationMessage } from "../../types/conversation";
import { ChatMarkdown, FileReference, SessionLabelLink } from "./ChatMarkdown";
import { formatTime, humanBubbleClass, MessageTimestamp, QueuedNote } from "./ChatTimelineItems";
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
export type GitAction = "createPR" | "createDraftPR" | "commit" | "commitPush" | "push";
export type AutomationNotice = "orchestratorReplaced" | "contextCarried" | "reviewTask";

export type Automation =
	| { kind: "fileFeedback"; comments: FileFeedback[] }
	| { kind: "diffSelection"; path: string; start?: number; end?: number; instruction: string }
	| { kind: "gitAction"; action: GitAction; base?: string }
	| { kind: "browserFeedback"; annotations: ParsedBrowserAnnotationMessage }
	| { kind: "ciFailing"; pr: PullRequestRef; checks: FailedCheck[] }
	| { kind: "reviewComments"; pr: PullRequestRef; comments: ReviewComment[] }
	| { kind: "changesRequested"; pr: PullRequestRef; author?: string; body: string }
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
const automationOf = cachedBy((item) =>
	item.kind === "message" && !item.senderSessionId && !/^\s*\[from /.test(item.text) ? readAutomation(item.text) : null,
);

/* -------------------------------------------------------------------------- */
/* drawing                                                                     */
/* -------------------------------------------------------------------------- */

type UserAction = Extract<Automation, { kind: "fileFeedback" | "diffSelection" | "gitAction" | "browserFeedback" }>;
type PullRequestAutomation = Extract<Automation, { pr: PullRequestRef }>;

/** An automation message drawn as what it is, or `fallback` when AO cannot tell. */
export function AutomationMessage({ message, queued, fallback }: { message: ConversationMessage; queued?: boolean; fallback: ReactNode }) {
	const automation = automationOf(message);
	if (!automation) return <>{fallback}</>;
	if (automation.kind === "notice") return <NoticeLine notice={automation.notice} />;
	if ("pr" in automation) return <PullRequestEvent automation={automation} createdAt={message.createdAt} />;
	return (
		<YourAction queued={queued} createdAt={message.createdAt}>
			<UserActionBody action={automation} />
		</YourAction>
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
			return <GitActionBody action={action.action} base={action.base} />;
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

const GIT_ACTIONS: Record<GitAction, { icon: LucideIcon; label: MessageKey }> = {
	createPR: { icon: GitPullRequest, label: "inspector.git.createPR" },
	createDraftPR: { icon: GitPullRequestDraft, label: "inspector.git.createDraftPR" },
	commit: { icon: GitCommitHorizontal, label: "inspector.git.commit" },
	commitPush: { icon: GitCommitHorizontal, label: "inspector.git.commitAndPush" },
	push: { icon: ArrowUpFromLine, label: "inspector.git.push" },
};

function GitActionBody({ action, base }: { action: GitAction; base?: string }) {
	const { t } = useTranslation();
	const { icon: Icon, label } = GIT_ACTIONS[action];
	return (
		<span className="inline-flex min-w-0 items-center gap-2">
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<span>{t(label)}</span>
			{base ? (
				<>
					<ChevronRight aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
					<span className="truncate font-mono text-xs text-muted-foreground">{base}</span>
				</>
			) : null}
		</span>
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

const NOTICES: Record<AutomationNotice, { icon: LucideIcon; label: MessageKey }> = {
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

/** What GitHub reported on the session's PR, in the PR glyph tinted by what it means. */
function PullRequestEvent({ automation, createdAt }: { automation: PullRequestAutomation; createdAt: string }) {
	const { t, i18n } = useTranslation();
	const { pr } = automation;
	const components = {
		strong: <span className="font-medium text-foreground" />,
		pr: <ForgeLink url={pr.url} className="font-medium text-foreground" />,
	};
	// A narrow key keeps Trans's typing tractable; the full MessageKey union is too complex for it (TS2590).
	const lead = (key: `chat.automation.${"ciFailing" | "reviewComment" | "changesRequested" | "mergeConflict"}`, author?: string) => (
		<Trans t={t} i18nKey={key} values={{ number: pr.number, author }} components={components} />
	);
	let tone = "text-status-in-review";
	let heading: ReactNode;
	let detail: ReactNode = null;
	switch (automation.kind) {
		case "ciFailing":
			tone = "text-status-exited";
			heading = lead("chat.automation.ciFailing");
			detail = (
				<ul className="flex flex-col gap-1">
					{automation.checks.map((check, index) => <FailedCheckLine key={index} check={check} />)}
				</ul>
			);
			break;
		case "reviewComments": {
			const authors = [...new Set(automation.comments.map((comment) => `@${comment.author}`))];
			heading = lead("chat.automation.reviewComment", new Intl.ListFormat(i18n.language, { type: "conjunction" }).format(authors));
			detail = automation.comments.map((comment, index) => (
				<div key={index} className="min-w-0">
					{comment.path || authors.length > 1 ? (
						<div className="mb-1 flex min-w-0 items-center gap-2">
							{/* Several reviewers: each comment says whose it is. */}
							{authors.length > 1 ? <span className="shrink-0 text-xs font-medium text-foreground">{`@${comment.author}`}</span> : null}
							{comment.path ? <FileChip path={comment.path} line={comment.line} /> : null}
						</div>
					) : null}
					<Folded watch={comment.body}>
						<ChatMarkdown text={comment.body} safeOrigin />
					</Folded>
				</div>
			));
			break;
		}
		case "changesRequested":
			heading = lead("chat.automation.changesRequested", automation.author ? `@${automation.author}` : t("chat.automation.aoReviewer"));
			detail = automation.body ? (
				<Folded watch={automation.body}>
					<ChatMarkdown text={automation.body} safeOrigin />
				</Folded>
			) : null;
			break;
		case "mergeConflict":
			// The PR cannot merge until the agent rebases: a failure, like failing CI.
			tone = "text-status-exited";
			heading = lead("chat.automation.mergeConflict");
			break;
	}
	return (
		<div className="relative min-w-0 pl-6" data-testid="pull-request-event">
			<GitPullRequest aria-hidden="true" className={cn("absolute left-0 top-[6px] size-4", tone)} />
			<div className="flex min-h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground">
				<span className="min-w-0 truncate">
					{heading}
					{pr.title ? <span className="ml-1.5">{pr.title}</span> : null}
				</span>
				<span className="ml-auto shrink-0 tabular-nums">{formatTime(createdAt)}</span>
			</div>
			{detail ? <div className="flex min-w-0 flex-col gap-3 pb-1">{detail}</div> : null}
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
					<button
						type="button"
						aria-expanded={open}
						onClick={() => setOpen((current) => !current)}
						className="ml-1 shrink-0 rounded font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
					>
						{t(open ? "chat.automation.hideLog" : "chat.automation.showLog")}
					</button>
				) : null}
			</div>
			{open && check.log ? (
				<pre className="mt-1.5 max-h-64 overflow-auto rounded-md bg-surface p-2 font-mono text-caption leading-relaxed text-muted-foreground">{check.log}</pre>
			) : null}
		</li>
	);
}
