/**
 * The orchestrator chat's view of its workers.
 *
 * A worker's message or report is drawn as one quiet row — who, state, a headline,
 * time — that opens to the full text, instead of a full-width card. When the
 * orchestrator's answer asks the user nothing, it hangs under the rows as one line
 * that opens to the work behind it, so the chat reads as the user's conversation
 * rather than a log of worker traffic. A turn that spawned workers lists them
 * with where each one is now.
 */

import { Bot, ChevronDown, ChevronRight, CornerDownRight, FileText, GitPullRequest, LoaderCircle, Split, Tag, Undo2 } from "lucide-react";
import { createContext, useContext, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { Options } from "react-markdown";
import { remarkPlainReferences } from "../../lib/session-links";
import { cn } from "../../lib/utils";
import { isSteer, type ConversationItem, type ConversationMessage } from "../../types/conversation";
import type { PullRequestFacts, WorkspaceSession } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { ArtifactPathCode, ChatMarkdown, SessionLabelLink, useChatLinkWorkspaces } from "./ChatMarkdown";
import { formatTime, humanVisibleText, stripSteerSenderPrefix } from "./ChatTimelineItems";
import { CopyButton } from "./CopyButton";

export interface WorkerUpdate {
	key: string;
	/** The report state (`needs_input`, `stuck`, `done`, ...); absent for a direct message. */
	state?: string;
	name: string;
	sessionId?: string;
	projectId?: string;
	body: string;
	createdAt: string;
}

const REPORTS_HEADER = "Reports since your previous turn:";
// One entry per `[state] [name](ao://sessions/<project>/<session>)`, as report.PreparedBatch.Render writes it,
// or `[state] ao://sessions/<project>/<session>` with no name, as older daemons wrote it.
// ponytail: parses the prompt text the daemon renders, so a worker's note can mimic an entry; structured report rows on the message would end the guess.
const REPORT_ENTRY = /\n\n\[([a-z_]+)\] (?:\[((?:\\.|[^\]\\])*)\]\(ao:\/\/sessions\/([^/)\s]+)\/([^)\s]+)\)|ao:\/\/sessions\/([^/\s]+)\/(\S+))(?: \(repeated \d+ times\))?/g;
// The `artifact: <path>` line a report gets per attached artifact, as report.PreparedBatch.Render writes it.
const ARTIFACT_LINE = /^artifact: (\S+)$/;
// The `[from <session> "<name>"]` prefix `ao send` puts on a worker's message. Messages from before AO
// stored the sender alongside carry only this.
const SENDER_PREFIX = /^\[from (?:worker\s+)?([\w.-]+)(?:\s+"([^"]*)")?\]/;
// The prompt AO used to send the orchestrator to name a new task, and the command it answered with.
const TITLE_UPDATE = "AO TASK TITLE UPDATE";
const RENAME_COMMAND = /\bao'?\s+session\s+rename\s+\S+\s+(["'])(.+?)\1/;

const STATES: Record<string, { label: string; attention?: boolean }> = {
	needs_input: { label: "needs you", attention: true },
	stuck: { label: "stuck", attention: true },
	done: { label: "done" },
};

function decode(value: string): string {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}

function parseReports(text: string, createdAt: string, keyPrefix: string): WorkerUpdate[] {
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

/** Items never change in place, so each is parsed once however often the timeline renders. */
export function cachedBy<T>(read: (item: ConversationItem) => T): (item: ConversationItem) => T {
	const cache = new WeakMap<ConversationItem, T>();
	return (item) => {
		if (!cache.has(item)) cache.set(item, read(item));
		return cache.get(item)!;
	};
}

/**
 * What a worker sent, when this item is one: a direct `ao send` (a message, or a
 * steer when it arrived mid-turn) or a batch of reports.
 */
export const workerUpdatesOf = cachedBy((item): WorkerUpdate[] => {
	if (item.kind === "activity") {
		const sender = isSteer(item) ? item.detail?.senderSessionId?.trim() : undefined;
		if (!sender) return [];
		return [{
			key: item.id,
			name: item.detail?.senderDisplayName?.trim() || sender,
			sessionId: sender,
			projectId: item.detail?.senderProjectId,
			body: stripSteerSenderPrefix(item.detail?.text ?? item.summary, sender).trim(),
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
			body: stripSteerSenderPrefix(item.text, sender).trim(),
			createdAt: item.createdAt,
		}];
	}
	return parseReports(item.text, item.createdAt, item.id);
});

export function isTaskTitleUpdate(item: ConversationItem): boolean {
	return item.kind === "message" && item.role === "user" && item.origin !== "human" && item.text.startsWith(TITLE_UPDATE);
}

/** The title the orchestrator gave a new task, read from its `ao session rename`. */
export function renamedTitle(items: readonly ConversationItem[]): string | undefined {
	for (const item of items) {
		if (item.kind !== "activity" || item.activityKind !== "command") continue;
		const match = RENAME_COMMAND.exec(item.detail?.command ?? item.summary);
		if (match) return match[2];
	}
	return undefined;
}

/** A turn a worker or AO opened rather than the user: worker traffic, or a task-naming prompt. */
export function isWorkerLead(item: ConversationItem | undefined): boolean {
	return item !== undefined && (workerUpdatesOf(item).length > 0 || isTaskTitleUpdate(item));
}

/** Reports the daemon folded into a human message because the orchestrator was busy. */
export const piggybackedUpdates = cachedBy((item): WorkerUpdate[] => {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return [];
	const visible = humanVisibleText(item.text);
	if (visible === item.text) return [];
	const envelope = item.text
		.slice(visible.length)
		.trim()
		.replace(/^<ao-worker-reports>/, "")
		.replace(/<\/ao-worker-reports>$/, "")
		.trim();
	return parseReports(envelope, item.createdAt, `${item.id}:reports`);
});

/** True when a reply asks the user something. A `?` in code or a link, or one running into more text (`a?.b`), is not a question. */
// ponytail: punctuation is a guess at intent; a structured ask from the orchestrator would replace it.
export function asksUser(text: string): boolean {
	const prose = text.replace(/```[\s\S]*?(?:```|$)|`[^`\n]*`|\]\([^)]*\)|\bhttps?:\/\/\S+/g, " ");
	return /[?？](?=[\s"'”’)\]*_]|$)/.test(prose);
}

function finalReply(items: readonly ConversationItem[]): ConversationMessage | undefined {
	for (let index = items.length - 1; index >= 0; index -= 1) {
		const item = items[index];
		if (item?.kind === "message" && item.role === "assistant" && item.text.trim() !== "") return item;
	}
	return undefined;
}

/**
 * The orchestrator's answer to a turn a worker opened, when it asks the user
 * nothing. That answer is drawn as one line under the worker's rows.
 */
export function quietWorkerReply(items: readonly ConversationItem[]): ConversationMessage | undefined {
	const lead = items[0];
	if (!isWorkerLead(lead)) return undefined;
	const reply = finalReply(items);
	return reply && !asksUser(reply.text) ? reply : undefined;
}

export interface WorkerUpdateIndex {
	/** Keys of each worker's newest update; an older "needs you" has been overtaken. */
	latestKeys: ReadonlySet<string>;
	/** Each worker's newest update, by session. */
	latest: ReadonlyMap<string, WorkerUpdate>;
}

export const WorkerUpdateIndexContext = createContext<WorkerUpdateIndex | null>(null);

export function indexWorkerUpdates(items: readonly ConversationItem[]): WorkerUpdateIndex {
	const latest = new Map<string, WorkerUpdate>();
	for (const item of items) {
		for (const update of [...piggybackedUpdates(item), ...workerUpdatesOf(item)]) {
			latest.set(update.sessionId ?? update.key, update);
		}
	}
	return { latest, latestKeys: new Set([...latest.values()].map((update) => update.key)) };
}

/**
 * The index for a timeline, keeping the same object until some worker's newest
 * update changes, so rows in settled turns stay idle while a reply streams.
 */
export function useWorkerUpdateIndex(items: readonly ConversationItem[] | undefined): WorkerUpdateIndex | null {
	const previous = useRef<WorkerUpdateIndex | null>(null);
	return useMemo(() => {
		const next = items ? indexWorkerUpdates(items) : null;
		const before = previous.current;
		const unchanged = before && next && before.latestKeys.size === next.latestKeys.size
			&& [...next.latestKeys].every((key) => before.latestKeys.has(key));
		previous.current = unchanged ? before : next;
		return previous.current;
	}, [items]);
}

function isCurrent(index: WorkerUpdateIndex | null, update: WorkerUpdate): boolean {
	return !index || index.latestKeys.has(update.key);
}

const plain = (text: string) =>
	text.replace(/\[([^\]]+)\]\([^)]*\)/g, "$1").replace(/`([^`]*)`/g, "$1").replace(/\*\*([^*]+)\*\*/g, "$1");

function splitArtifacts(body: string): { text: string; artifacts: string[] } {
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

const sessionHref = (update: Pick<WorkerUpdate, "sessionId" | "projectId">) =>
	update.sessionId && update.projectId
		? `ao://sessions/${encodeURIComponent(update.projectId)}/${encodeURIComponent(update.sessionId)}`
		: undefined;

/** The worker as this chat's links see it: the local daemon's, a remote host's, or Cloud's. */
function useWorkerSession(sessionId: string | undefined, projectId?: string): WorkspaceSession | undefined {
	const workspaces = useChatLinkWorkspaces();
	if (!sessionId) return undefined;
	const scope = projectId ? workspaces.filter((workspace) => workspace.id === projectId) : workspaces;
	for (const workspace of scope) {
		const session = workspace.sessions.find((candidate) => candidate.id === sessionId);
		if (session) return session;
	}
	return undefined;
}

// Keyed on which sessions exist, not on the workspace list: that list changes with every session
// event, and a new plugin list re-parses every message that renders it.
// ponytail: one cached session set; chats on two hosts at once rebuild in turn, key the cache by set if that shows up.
type References = { sessions: string; plugins: Options["remarkPlugins"] };
const referencesByWorkspaces = new WeakMap<object, References>();
let latestReferences: References | undefined;

/**
 * Agents message each other in plain prose; their paths read as code and the
 * sessions they name link to them. Built once per workspace list and shared by
 * every message, and the same list until a session is added or removed.
 */
export function usePlainReferences(): Options["remarkPlugins"] {
	const workspaces = useChatLinkWorkspaces();
	let references = referencesByWorkspaces.get(workspaces);
	if (!references) {
		const sessions = JSON.stringify(workspaces.flatMap((workspace) => workspace.sessions.map((entry) => [entry.id, workspace.id])));
		references = latestReferences?.sessions === sessions
			? latestReferences
			: { sessions, plugins: [[remarkPlainReferences, new Map(JSON.parse(sessions) as [string, string][])]] };
		referencesByWorkspaces.set(workspaces, references);
	}
	latestReferences = references;
	return references.plugins;
}

/** Prose one agent sent another, with its plain references highlighted. Its own component, so only drawn prose reads them. */
function AgentProse({ text, muted, className }: { text: string; muted?: boolean; className?: string }) {
	const references = usePlainReferences();
	return <ChatMarkdown text={text} muted={muted} safeOrigin remarkPlugins={references} className={className} />;
}

function WorkerUpdateRow({ update }: { update: WorkerUpdate }) {
	const [open, setOpen] = useState(false);
	const session = useWorkerSession(update.sessionId, update.projectId);
	const index = useContext(WorkerUpdateIndexContext);
	const { text, artifacts } = splitArtifacts(update.body);
	const { headline, detail } = splitHeadline(text);
	const state = update.state ? STATES[update.state] : undefined;
	const attention = state?.attention && isCurrent(index, update);
	// An older message names its sender only by session id; the session supplies its project and title.
	const href = sessionHref({ sessionId: update.sessionId, projectId: update.projectId ?? session?.workspaceId });
	const name = <span className="block truncate">{update.name === update.sessionId ? session?.title || update.name : update.name}</span>;
	return (
		<div className="min-w-0 text-xs" data-testid="worker-update">
			<div className="group -mx-1.5 flex h-7 w-[calc(100%+0.75rem)] min-w-0 items-center gap-2 rounded-md px-1.5 text-muted-foreground transition-colors hover:bg-interactive-hover">
				{session?.provider ? (
					<AgentAvatar provider={session.provider} decorative className="size-4 shrink-0" />
				) : (
					<Bot aria-hidden="true" className="size-3.5 shrink-0" />
				)}
				{/* The sender opens its session, as the "[from …]" label on a worker message does; the rest of the row expands. */}
				{href ? (
					<SessionLabelLink
						href={href}
						className="max-w-[40%] shrink-0 font-medium text-foreground decoration-foreground/40 underline-offset-2 hover:underline"
					>
						{name}
					</SessionLabelLink>
				) : (
					<span className="max-w-[40%] shrink-0 font-medium text-foreground">{name}</span>
				)}
				<button
					type="button"
					aria-expanded={open}
					onClick={() => setOpen((current) => !current)}
					className="flex h-full min-w-0 flex-1 items-center gap-2 rounded text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					{state ? (
						<span className={cn("shrink-0", attention && "font-medium text-status-needs-you")}>{state.label}</span>
					) : null}
					<span className="min-w-0 flex-1 truncate transition-colors group-hover:text-foreground">{headline}</span>
					<span className="shrink-0 tabular-nums">{formatTime(update.createdAt)}</span>
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 transition-transform motion-reduce:transition-none", open && "rotate-90")}
					/>
				</button>
			</div>
			{artifacts.map((path) => (
				<p key={path} className="ml-6 flex h-6 min-w-0 items-center gap-1.5 text-muted-foreground">
					<FileText aria-hidden="true" className="size-3.5 shrink-0" />
					{/* A listed artifact links to its preview; one the session no longer lists stays a name. */}
					<span className="min-w-0 truncate font-mono" title={path}>
						<ArtifactPathCode text={artifactReference(path, update.sessionId)}>{path.split("/").at(-1)}</ArtifactPathCode>
					</span>
				</p>
			))}
			{open ? (
				<div className="mb-2 ml-6 mt-1 min-w-0">
					<AgentProse text={detail} muted className="whitespace-pre-wrap text-xs leading-relaxed" />
				</div>
			) : null}
		</div>
	);
}

/** A report may name an artifact relative to its session's artifact folder; scope that to the session. */
function artifactReference(path: string, sessionId: string | undefined): string {
	const absolute = path.startsWith("/") || /^[A-Za-z]:[\\/]/.test(path);
	return absolute || !sessionId ? path : `${sessionId}/${path}`;
}

export function WorkerUpdateRows({ updates }: { updates: readonly WorkerUpdate[] }) {
	return (
		<div className="flex min-w-0 flex-col">
			{updates.map((update) => (
				<WorkerUpdateRow key={update.key} update={update} />
			))}
		</div>
	);
}

/**
 * In a worker's chat, a message another agent sent it (its orchestrator, or a
 * sibling worker), drawn the way the orchestrator chat draws a worker: avatar,
 * linked name and time, with what it sent open beneath at the row's indent.
 * Any other message draws as `children`.
 */
export function FromAgent({ item, children }: { item: ConversationItem; children: ReactNode }) {
	const { t } = useTranslation();
	const updates = workerUpdatesOf(item);
	const update = updates.length === 1 && !updates[0]!.state ? updates[0] : undefined;
	const session = useWorkerSession(update?.sessionId, update?.projectId);
	if (!update || !session) return <>{children}</>;
	const name = session.kind === "orchestrator" ? t("shell.orchestrator") : session.title || update.name;
	const href = sessionHref({ sessionId: update.sessionId, projectId: update.projectId ?? session.workspaceId });
	return (
		<div className="relative min-w-0 pl-6" data-testid="agent-message">
			<AgentAvatar provider={session.provider} decorative className="absolute left-0 top-[3px] size-4" />
			<Folded watch={update.body}>
				{/* The name and time float into the message's first line; the rest wraps under the name. */}
				<span className="float-right ml-3 text-xs leading-[calc(var(--chat-readable-font-size)*1.58)] tabular-nums text-muted-foreground">
					{formatTime(update.createdAt)}
				</span>
				{href ? (
					<SessionLabelLink
						href={href}
						className="float-left mr-2 text-[length:var(--chat-readable-font-size)] font-medium leading-[1.58] text-foreground decoration-foreground/40 underline-offset-2 hover:underline"
					>
						{name}
					</SessionLabelLink>
				) : null}
				<AgentProse text={update.body} />
			</Folded>
		</div>
	);
}

/**
 * Content folded after five lines, with "Show more" whenever the fold hides
 * something. Re-measured as the chat resizes, so hidden text always comes with a
 * way to open it.
 */
export function Folded({ watch, children }: { watch: unknown; children: ReactNode }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [folds, setFolds] = useState(false);
	const body = useRef<HTMLDivElement>(null);
	useLayoutEffect(() => {
		const element = body.current;
		if (!element || open) return;
		const measure = () => setFolds(element.scrollHeight > element.clientHeight + 1);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [open, watch]);
	return (
		<>
			<div ref={body} className={cn("min-w-0", !open && "line-clamp-5")}>
				<div>{children}</div>
			</div>
			{folds || open ? (
				<button
					type="button"
					aria-expanded={open}
					onClick={() => setOpen((current) => !current)}
					className="mt-1 inline-flex items-center gap-1 rounded text-xs font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					{t(open ? "shell.showLess" : "shell.showMore")}
					<ChevronDown aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-180")} />
				</button>
			) : null}
		</>
	);
}

/** A task-naming prompt and its rename, drawn as the one fact they produced. */
export function TaskNamedLine({ title }: { title: string }) {
	const { t } = useTranslation();
	return (
		<p className="flex h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground">
			<Tag aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="min-w-0 truncate">
				<Trans t={t} i18nKey="chat.taskNamed" values={{ title }} components={{ task: <span className="font-medium text-foreground" /> }} />
			</span>
		</p>
	);
}

/**
 * The orchestrator's answer under the rows that prompted it. It keeps what the
 * reply would have — copy, rollback, the work behind it — but on one quiet line.
 */
export function OrchestratorReply({
	message,
	work,
	workOpen,
	onWorkOpenChange,
	onRollback,
	rollbackDisabled,
}: {
	message: ConversationMessage;
	work?: ReactNode;
	workOpen: boolean;
	onWorkOpenChange: (open: boolean) => void;
	onRollback?: () => void;
	rollbackDisabled?: boolean;
}) {
	const { t } = useTranslation();
	const control = "flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground";
	return (
		<div
			className="group/message -mt-1.5 ml-6 min-w-0"
			data-testid="orchestrator-reply"
			data-chat-message-id={message.id}
			data-chat-message-revision={message.revision}
			data-chat-message-role={message.role}
		>
			<div className="flex min-w-0 items-start gap-2">
				<CornerDownRight aria-hidden="true" className="mt-[3px] size-3 shrink-0 text-muted-foreground" />
				{/* The timeline sizes chat markdown through these variables; a reply line sits at the rows' size. */}
				<div
					data-chat-message-body=""
					className="min-w-0 flex-1 [--chat-readable-code-font-size:var(--font-size-caption)] [--chat-readable-font-size:var(--font-size-sm)]"
				>
					<ChatMarkdown text={message.text} />
				</div>
				<div className="-my-1 flex shrink-0 items-center">
					{/* Revealed on hover or keyboard focus, so a list of quiet replies does not carry a toolbar each. */}
					<div className="flex items-center opacity-0 transition-opacity group-hover/message:opacity-100 group-focus-within/message:opacity-100 motion-reduce:transition-none">
						<CopyButton text={message.text} label="Copy message as markdown" compact className={cn(control, "px-0 py-0")} />
						{onRollback ? (
							<button
								type="button"
								onClick={onRollback}
								disabled={rollbackDisabled}
								aria-label={t("chat.rollBackToHere")}
								title={t("chat.rollBackToHere")}
								className={cn(control, "disabled:pointer-events-none disabled:opacity-40")}
							>
								<Undo2 aria-hidden="true" className="size-3" />
							</button>
						) : null}
					</div>
					{work ? (
						<button
							type="button"
							aria-expanded={workOpen}
							aria-label={t("chat.showReplyWork")}
							onClick={() => onWorkOpenChange(!workOpen)}
							className={control}
						>
							<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", workOpen && "rotate-90")} />
						</button>
					) : null}
				</div>
			</div>
			{workOpen && work ? <div className="mt-2 flex min-w-0 flex-col gap-2.5">{work}</div> : null}
		</div>
	);
}

const readyToMerge = (pr: PullRequestFacts | undefined) =>
	pr?.state === "open" && pr.ci === "passing" && pr.review === "approved" && pr.mergeability !== "conflicting";

// What `ao spawn` prints: `spawned session <id> "<name>" (<status>)`.
// ponytail: reads command output; #6507's session.spawned rows replace this once merged.
const SPAWNED = /\bspawned\s+session\s+([\w.-]+)/i;

export function spawnedSessionIds(items: readonly ConversationItem[]): string[] {
	const ids: string[] = [];
	for (const item of items) {
		if (item.kind !== "activity" || item.activityKind !== "command") continue;
		if (!/\bao'?\s+spawn\b/.test(item.detail?.command ?? item.summary)) continue;
		const match = SPAWNED.exec(item.detail?.output ?? "");
		if (match) ids.push(match[1]!);
	}
	return ids;
}

/** The workers a turn started, each showing where it is now. */
export function SpawnedAgents({ sessionIds }: { sessionIds: readonly string[] }) {
	const [open, setOpen] = useState(true);
	return (
		<div className="min-w-0 text-xs">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
				className="inline-flex items-center gap-1.5 text-muted-foreground transition-colors hover:text-foreground"
			>
				<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
				<Split aria-hidden="true" className="size-3.5" />
				{sessionIds.length === 1 ? "Spawned 1 agent" : `Spawned ${sessionIds.length} agents`}
			</button>
			{open ? (
				<div className="mt-1 flex flex-col">
					{sessionIds.map((id) => (
						<SpawnedAgentLine key={id} sessionId={id} />
					))}
				</div>
			) : null}
		</div>
	);
}

function SpawnedAgentLine({ sessionId }: { sessionId: string }) {
	const session = useWorkerSession(sessionId);
	const latest = useContext(WorkerUpdateIndexContext)?.latest.get(sessionId);
	const pr = session?.prs[0];
	const name = session?.title || latest?.name || sessionId;
	const href = session?.workspaceId ? sessionHref({ sessionId, projectId: session.workspaceId }) : latest ? sessionHref(latest) : undefined;
	let lead: ReactNode;
	let status: ReactNode;
	if (pr) {
		const ready = readyToMerge(pr);
		lead = <GitPullRequest aria-hidden="true" className={cn("size-3.5 shrink-0", ready ? "text-status-ready" : "text-muted-foreground")} />;
		status = ready ? `#${pr.number} · ready to merge` : `#${pr.number}`;
	} else if (latest?.state === "done") {
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-status-ready" />;
		status = "Done";
	} else if (latest?.state === "needs_input" || latest?.state === "stuck") {
		lead = <span aria-hidden="true" className="mx-[3.5px] size-[7px] shrink-0 rounded-full bg-status-needs-you" />;
		status = <span className="text-status-needs-you">{latest.state === "stuck" ? "Stuck" : "Needs input"}</span>;
	} else {
		lead = <LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-status-working motion-reduce:animate-none" />;
		status = latest ? splitHeadline(splitArtifacts(latest.body).text).headline : "Working";
	}
	return (
		<div className="flex min-w-0 items-center gap-2 py-0.5 pl-5">
			{lead}
			<span className="min-w-0 shrink truncate text-foreground">
				{href ? <SessionLabelLink href={href} className="decoration-foreground/40 underline-offset-2 hover:underline">{name}</SessionLabelLink> : name}
			</span>
			<span className="ml-auto max-w-[48%] shrink-0 truncate text-muted-foreground" title={typeof status === "string" ? status : undefined}>{status}</span>
		</div>
	);
}
