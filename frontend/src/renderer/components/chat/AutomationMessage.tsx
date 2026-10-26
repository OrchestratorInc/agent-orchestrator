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
 * Each is read back from the fixed text AO writes for it. Text that matches none
 * of them keeps the card.
 */
// ponytail: reads AO's own message templates, so changing one means changing its reader here; structured metadata on the message would end the parsing.

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
import { useMemo, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { parseBrowserAnnotationMessage, type ParsedBrowserAnnotationMessage } from "../../../shared/browser-annotations";
import type { MessageKey } from "../../i18n/messages";
import { cn } from "../../lib/utils";
import type { ConversationMessage } from "../../types/conversation";
import { ChatMarkdown, FileReference, SessionLabelLink } from "./ChatMarkdown";
import { formatTime } from "./ChatTimelineItems";
import { Folded } from "./WorkerUpdates";

export type PullRequestRef = { number: number; title?: string; url?: string };

export type FileFeedback = {
	path: string;
	line?: number;
	/** The commented line's code, when the comment was on one. */
	code?: string;
	/** Text selected in a rendered view, when the comment was on a selection. */
	quote?: string;
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

function readPullRequestNudge(text: string): Automation | null {
	if (text.startsWith("There are merge conflicts on ")) {
		const pr = pullRequest(text, PR_LINE.exec(text)?.[1]);
		return pr ? { kind: "mergeConflict", pr } : null;
	}
	if (text.startsWith("CI is failing on ")) {
		const pr = pullRequest(text, PR_LINE.exec(text)?.[1]);
		if (!pr) return null;
		const headers = [...text.matchAll(/^Failed: (.+) \(([^()\n]*)\)$/gm)];
		const checks = headers.map((header, index) => {
			const end = headers[index + 1]?.index ?? text.indexOf("\nUse the included log tail");
			const section = text.slice(header.index!, end < 0 ? undefined : end);
			const log = /Log tail \(last \d+ lines?\):\n(`{3,})\n([\s\S]*?)\n\1/.exec(section)?.[2];
			return { name: header[1]!, url: /^Failure URL: (\S+)$/m.exec(section)?.[1], log };
		});
		return checks.length > 0 ? { kind: "ciFailing", pr, checks } : null;
	}
	if (/^The following \d+ unresolved review comment\(s\) are on /.test(text)) {
		const pr = pullRequest(text, PR_LINE.exec(text)?.[1]);
		if (!pr) return null;
		const closing = text.indexOf("\nAddress each comment and push fixes.");
		const items = [...text.matchAll(/^\d+\. (.+) \(@([^)\n]*)\):$/gm)];
		const comments = items.map((item, index) => {
			const end = items[index + 1]?.index ?? (closing < 0 ? text.length : closing);
			const lines = text.slice(item.index! + item[0].length + 1, end).replace(/\s+$/, "").split("\n");
			while (lines.length > 0 && /^ {3}(?:https?:\/\/\S+|Thread ID: .*)$/.test(lines.at(-1)!)) lines.pop();
			const location = item[1]!;
			const at = location === "(general)" ? undefined : /^(.*?)(?::(\d+))?$/.exec(location);
			return { path: at?.[1] || undefined, line: at?.[2] ? Number(at[2]) : undefined, author: item[2]!, body: lines.join("\n").trim() };
		});
		return comments.length > 0 ? { kind: "reviewComments", pr, comments } : null;
	}
	const changes = /^A changes-requested review from @(.+?) is on /.exec(text);
	if (changes) {
		const url = /\n\nReview URL: (\S+)/.exec(text)?.[1] ?? PR_LINE.exec(text)?.[1];
		const pr = pullRequest(text, url);
		if (!pr) return null;
		const body = /\n\nReview body:\n([\s\S]*?)(?=\n\nReview URL: |\nReview ID: |\n\nAddress the requested changes)/.exec(text)?.[1] ?? "";
		return { kind: "changesRequested", pr, author: changes[1], body: body.trim() };
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
	const line = Number(fields.get("New line") ?? fields.get("Old line") ?? /line (\d+)$/.exec(fields.get("Location") ?? "")?.[1]);
	const code = fields.get("Code");
	return {
		path,
		line: Number.isSafeInteger(line) && line > 0 ? line : undefined,
		code: code && code !== "(blank line)" ? code : undefined,
		quote: fields.get("Selected text"),
		feedback: feedback[1]!.trim() === "(empty)" ? "" : feedback[1]!.trim(),
	};
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
	if (text.startsWith("The user selected lines in the diff viewer and asked for a change.\n")) {
		const instruction = /\nInstruction:\n([\s\S]*?)\n\nFile: /.exec(text)?.[1];
		const path = /^File: (.+)$/m.exec(text)?.[1];
		if (instruction === undefined || !path) return null;
		const range = /^Selected lines (\d+)-(\d+):$/m.exec(text);
		return {
			kind: "diffSelection",
			path,
			start: range ? Number(range[1]) : undefined,
			end: range ? Number(range[2]) : undefined,
			instruction: instruction.trim() === "(empty)" ? "" : instruction.trim(),
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

/* -------------------------------------------------------------------------- */
/* drawing                                                                     */
/* -------------------------------------------------------------------------- */

/** An automation message drawn as what it is, or `fallback` when AO cannot tell. */
export function AutomationMessage({ message, fallback }: { message: ConversationMessage; fallback: ReactNode }) {
	const automation = useMemo(() => readAutomation(message.text), [message.text]);
	if (!automation) return <>{fallback}</>;
	switch (automation.kind) {
		case "fileFeedback":
			return <FileFeedbackMessage comments={automation.comments} />;
		case "diffSelection":
			return (
				<YourAction>
					<FileChip path={automation.path} line={automation.start} end={automation.end} />
					{automation.instruction ? <p className="mt-1.5 whitespace-pre-wrap">{automation.instruction}</p> : null}
				</YourAction>
			);
		case "gitAction":
			return <GitActionMessage action={automation.action} base={automation.base} />;
		case "browserFeedback":
			return <BrowserFeedbackMessage annotations={automation.annotations} />;
		case "notice":
			return <NoticeLine notice={automation.notice} />;
		default:
			return <PullRequestEvent automation={automation} createdAt={message.createdAt} />;
	}
}

/** Something the user did through AO, drawn as the user's own message. */
function YourAction({ children }: { children: ReactNode }) {
	return (
		<div className="flex flex-col items-end" data-testid="your-action">
			<div className="cursor-chat-human-message w-fit min-w-0 max-w-[min(78%,560px)] rounded-[10px] bg-raised px-3 py-2.5 text-sm leading-[1.55] text-foreground">
				{children}
			</div>
		</div>
	);
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

function FileFeedbackMessage({ comments }: { comments: readonly FileFeedback[] }) {
	const { t } = useTranslation();
	return (
		<YourAction>
			<div className="flex min-w-0 flex-col gap-3">
				{comments.map((comment, index) => (
					<div key={index} className="min-w-0">
						<FileChip path={comment.path} line={comment.line} />
						{comment.code || comment.quote ? (
							<p className="mt-1 truncate font-mono text-xs text-muted-foreground" title={comment.code ?? comment.quote}>
								{comment.code ?? comment.quote}
							</p>
						) : !comment.line ? (
							<p className="mt-1 text-xs text-muted-foreground">{t("chat.automation.entireFile")}</p>
						) : null}
						{comment.feedback ? <p className="mt-1.5 whitespace-pre-wrap">{comment.feedback}</p> : null}
					</div>
				))}
			</div>
		</YourAction>
	);
}

const GIT_ACTIONS: Record<GitAction, { icon: LucideIcon; label: MessageKey }> = {
	createPR: { icon: GitPullRequest, label: "inspector.git.createPR" },
	createDraftPR: { icon: GitPullRequestDraft, label: "inspector.git.createDraftPR" },
	commit: { icon: GitCommitHorizontal, label: "inspector.git.commit" },
	commitPush: { icon: GitCommitHorizontal, label: "inspector.git.commitAndPush" },
	push: { icon: ArrowUpFromLine, label: "inspector.git.push" },
};

function GitActionMessage({ action, base }: { action: GitAction; base?: string }) {
	const { t } = useTranslation();
	const { icon: Icon, label } = GIT_ACTIONS[action];
	return (
		<YourAction>
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
		</YourAction>
	);
}

function BrowserFeedbackMessage({ annotations }: { annotations: ParsedBrowserAnnotationMessage }) {
	const { t } = useTranslation();
	return (
		<YourAction>
			<span className={cn(chip, "max-w-full")} title={annotations.pageUrl || annotations.pageTitle}>
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
		</YourAction>
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

type PullRequestAutomation = Extract<Automation, { pr: PullRequestRef }>;

/** What GitHub reported on the session's PR, in the PR glyph tinted by what it means. */
function PullRequestEvent({ automation, createdAt }: { automation: PullRequestAutomation; createdAt: string }) {
	const { t } = useTranslation();
	const { pr } = automation;
	const components = {
		strong: <span className="font-medium text-foreground" />,
		pr: pr.url && /^https?:\/\//.test(pr.url)
			? <SessionLabelLink href={pr.url} className="font-medium text-foreground decoration-foreground/40 underline-offset-2 hover:underline">{null}</SessionLabelLink>
			: <span className="font-medium text-foreground" />,
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
		case "reviewComments":
			heading = lead("chat.automation.reviewComment", `@${automation.comments[0]!.author}`);
			detail = automation.comments.map((comment, index) => (
				<div key={index} className="min-w-0">
					{comment.path ? <FileChip path={comment.path} line={comment.line} /> : null}
					<div className={cn(comment.path && "mt-1")}>
						<Folded watch={comment.body}>
							<ChatMarkdown text={comment.body} safeOrigin />
						</Folded>
					</div>
				</div>
			));
			break;
		case "changesRequested":
			heading = lead("chat.automation.changesRequested", automation.author ? `@${automation.author}` : t("chat.automation.aoReviewer"));
			detail = automation.body ? (
				<Folded watch={automation.body}>
					<ChatMarkdown text={automation.body} safeOrigin />
				</Folded>
			) : null;
			break;
		case "mergeConflict":
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
				{check.url && /^https?:\/\//.test(check.url) ? (
					<SessionLabelLink href={check.url} className="min-w-0 truncate font-medium text-foreground decoration-foreground/40 underline-offset-2 hover:underline">
						{check.name}
					</SessionLabelLink>
				) : (
					<span className="min-w-0 truncate font-medium text-foreground">{check.name}</span>
				)}
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
