/**
 * The orchestrator chat's view of its workers.
 *
 * A turn that spawned workers shows one box per worker while it runs: who, what it
 * is doing, and for how long. Once the worker answers, its box goes and the answer
 * reads in full under its name, as any message in the chat does. When the
 * orchestrator's answer to it asks the user nothing, that answer hangs under the
 * worker's message as one line that opens to the work behind it, so the chat reads
 * as the user's conversation rather than a log of worker traffic.
 */

import { Bot, ChevronDown, ChevronRight, CornerDownRight, FileText, Split, Tag, Undo2 } from "lucide-react";
import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { Options } from "react-markdown";
import { remarkPlainReferences } from "../../lib/session-links";
import { cn } from "../../lib/utils";
import { isSteer, type ConversationItem, type ConversationMessage } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { ArtifactPathCode, ChatMarkdown, SessionLabelLink, useChatLinkWorkspaces } from "./ChatMarkdown";
import { formatDuration, formatTime, humanVisibleText, QueuedNote, ResponseSpinner, stripSteerSenderPrefix } from "./ChatTimelineItems";
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
// The `[via <tool>]` or `[via <tool> #<tag>]` prefix an outside tool puts on what it sends through AO's API,
// as integrations/grok-bot does. The tag only lets the tool find its reply.
const VIA_PREFIX = /^\[via ([^\]#\n]{1,60}?)(?: #[^\]\n]*)?\]\s*/;
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

/** The outside tool a message or steer came through and what it said, when no AO session sent it. */
// ponytail: reads the tool's text prefix, so anyone can type one; a sender name on the send API would replace it.
export const relayedVia = cachedBy((item): { name: string; body: string } | null => {
	// A message's piggybacked reports draw as their own rows, as under a human message.
	const text = item.kind === "message"
		? item.role === "user" && !item.senderSessionId ? humanVisibleText(item.text) : undefined
		: isSteer(item) && !item.detail?.senderSessionId?.trim() ? item.detail?.text ?? item.summary : undefined;
	if (text === undefined) return null;
	const via = VIA_PREFIX.exec(text);
	const name = via?.[1]?.trim();
	return via && name ? { name, body: text.slice(via[0].length) } : null;
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

function splitArtifacts(body: string): { text: string; artifacts: string[] } {
	const artifacts: string[] = [];
	const lines = body.split("\n").filter((line) => {
		const path = ARTIFACT_LINE.exec(line.trim())?.[1];
		if (path) artifacts.push(path);
		return !path;
	});
	return { text: lines.join("\n").trim(), artifacts };
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

/** What a worker sent, in full under its name, as any message in the chat reads. */
function WorkerMessage({ update }: { update: WorkerUpdate }) {
	const session = useWorkerSession(update.sessionId, update.projectId);
	const index = useContext(WorkerUpdateIndexContext);
	const { text, artifacts } = splitArtifacts(update.body);
	const state = update.state ? STATES[update.state] : undefined;
	const attention = state?.attention && isCurrent(index, update);
	// An older message names its sender only by session id; the session supplies its project and title.
	const href = sessionHref({ sessionId: update.sessionId, projectId: update.projectId ?? session?.workspaceId });
	const name = update.name === update.sessionId ? session?.title || update.name : update.name;
	return (
		<div className="relative min-w-0 pl-6" data-testid="worker-update">
			{session?.provider ? (
				<AgentAvatar provider={session.provider} decorative className="absolute left-0 top-[3px] size-4" />
			) : (
				<Bot aria-hidden="true" className="absolute left-0 top-[3px] size-4 text-muted-foreground" />
			)}
			{/* A worker's name is its task title, often long, so it heads the message on its own line and truncates. */}
			<div className="flex min-w-0 items-baseline gap-3">
				<span
					className="min-w-0 flex-1 truncate text-[length:var(--chat-readable-font-size)] font-medium leading-[1.58] text-foreground"
					title={name}
				>
					{href ? (
						<SessionLabelLink href={href} className="decoration-foreground/40 underline-offset-2 hover:underline">
							{name}
						</SessionLabelLink>
					) : (
						name
					)}
				</span>
				<span className="shrink-0 text-xs tabular-nums text-muted-foreground">
					{state ? (
						<>
							<span className={cn(attention && "font-medium text-status-needs-you")}>{state.label}</span>
							{" · "}
						</>
					) : null}
					{formatTime(update.createdAt)}
				</span>
			</div>
			{text ? <AgentProse text={text} /> : null}
			{artifacts.map((path) => (
				<p key={path} className="flex h-6 min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
					<FileText aria-hidden="true" className="size-3.5 shrink-0" />
					{/* A listed artifact links to its preview; one the session no longer lists stays a name. */}
					<span className="min-w-0 truncate font-mono" title={path}>
						<ArtifactPathCode text={artifactReference(path, update.sessionId)}>{path.split("/").at(-1)}</ArtifactPathCode>
					</span>
				</p>
			))}
		</div>
	);
}

/** A report may name an artifact relative to its session's artifact folder; scope that to the session. */
function artifactReference(path: string, sessionId: string | undefined): string {
	const absolute = path.startsWith("/") || /^[A-Za-z]:[\\/]/.test(path);
	return absolute || !sessionId ? path : `${sessionId}/${path}`;
}

export function WorkerMessages({ updates }: { updates: readonly WorkerUpdate[] }) {
	return (
		<div className="flex min-w-0 flex-col gap-2.5">
			{updates.map((update) => (
				<WorkerMessage key={update.key} update={update} />
			))}
		</div>
	);
}

/**
 * In a worker's chat, a message another agent sent it (its orchestrator, or a
 * sibling worker), drawn the way the orchestrator chat draws a worker: avatar,
 * linked name and time, with what it sent open beneath at the row's indent.
 * What an outside tool relayed through AO's API (`[via Grok Bot] …`) draws the
 * same way under the tool's name, in any chat. Any other message draws as `children`.
 */
export function FromAgent({ item, queued, children }: { item: ConversationItem; queued?: boolean; children: ReactNode }) {
	const { t } = useTranslation();
	const updates = workerUpdatesOf(item);
	const update = updates.length === 1 && !updates[0]!.state ? updates[0] : undefined;
	const session = useWorkerSession(update?.sessionId, update?.projectId);
	const via = relayedVia(item);
	if (via) {
		return (
			<SenderRow
				avatar={<Bot aria-hidden="true" className="absolute left-0 top-[3px] size-4 text-muted-foreground" />}
				createdAt={item.createdAt}
				body={via.body}
				queued={queued}
			>
				<span className={SENDER_NAME}>{via.name}</span>
			</SenderRow>
		);
	}
	if (!update || !session) return <>{children}</>;
	const name = session.kind === "orchestrator" ? t("shell.orchestrator") : session.title || update.name;
	const href = sessionHref({ sessionId: update.sessionId, projectId: update.projectId ?? session.workspaceId });
	return (
		<SenderRow avatar={<AgentAvatar provider={session.provider} decorative className="absolute left-0 top-[3px] size-4" />} createdAt={update.createdAt} body={update.body}>
			{href ? (
				<SessionLabelLink href={href} className={cn(SENDER_NAME, "decoration-foreground/40 underline-offset-2 hover:underline")}>
					{name}
				</SessionLabelLink>
			) : null}
		</SenderRow>
	);
}

const SENDER_NAME = "float-left mr-2 text-[length:var(--chat-readable-font-size)] font-medium leading-[1.58] text-foreground";
function SenderRow({ avatar, createdAt, body, queued, children }: { avatar: ReactNode; createdAt: string; body: string; queued?: boolean; children: ReactNode }) {
	return (
		<div className="relative min-w-0 pl-6" data-testid="agent-message">
			{avatar}
			<Folded watch={body}>
				{/* The name and time float into the message's first line; the rest wraps under the name. */}
				<span className="float-right ml-3 text-xs leading-[calc(var(--chat-readable-font-size)*1.58)] tabular-nums text-muted-foreground">
					{formatTime(createdAt)}
				</span>
				{children}
				<AgentProse text={body} />
			</Folded>
			{queued ? <div className="mt-1"><QueuedNote /></div> : null}
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

// What `ao spawn` prints: `spawned session <id> "<name>" (<status>)`.
// ponytail: reads command output; #6507's session.spawned rows replace this once merged.
const SPAWNED = /\bspawned\s+session\s+([\w.-]+)/i;

export function spawnedSessionIds(items: readonly ConversationItem[]): string[] {
	const ids: string[] = [];
	for (const item of items) {
		if (item.kind !== "activity" || item.activityKind !== "command" || item.status !== "completed") continue;
		if (!/\bao'?\s+spawn\b/.test(item.detail?.command ?? item.summary)) continue;
		const match = SPAWNED.exec(item.detail?.output ?? "");
		if (match) ids.push(match[1]!);
	}
	return ids;
}

export function isDuplicateSpawnAction(item: ConversationItem, spawnedIds: ReadonlySet<string>): boolean {
	return item.kind === "activity" && item.activityKind === "ao_action" && item.status === "completed"
		&& item.detail?.action === "session.spawned"
		&& typeof item.detail.targetSessionId === "string"
		&& item.detail.operationId === item.detail.targetSessionId
		&& spawnedIds.has(item.detail.targetSessionId);
}

/**
 * The workers a turn started, one box each, until each one answers. The answer
 * reads as the worker's own message where it arrived, so the box would only
 * repeat it.
 */
export function SpawnedAgents({ sessionIds }: { sessionIds: readonly string[] }) {
	const latest = useContext(WorkerUpdateIndexContext)?.latest;
	const waiting = sessionIds.filter((id) => !latest?.has(id));
	if (waiting.length === 0) return null;
	return (
		<div className="flex min-w-0 flex-col gap-2">
			{waiting.map((id) => (
				<SpawnedAgentBox key={id} sessionId={id} />
			))}
		</div>
	);
}

type WaitingState = { label: string; tone: "working" | "attention" | "quiet" };

/** Where a worker that has not answered yet is now, read from its session's durable facts. */
function waitingState(session: WorkspaceSession | undefined): WaitingState {
	if (session?.status === "needs_input" || session?.activity?.state === "waiting_input" || session?.activity?.state === "blocked") {
		return { label: "Needs input", tone: "attention" };
	}
	if (session?.isTerminated || session?.status === "terminated") return { label: "Terminated", tone: "quiet" };
	if (session?.provisionState === "provisioning") return { label: "Spawning", tone: "working" };
	if (session?.provisionState === "failed") return { label: "Start failed", tone: "attention" };
	if (session?.activity?.state === "exited" || session?.status === "exited") return { label: "Exited", tone: "quiet" };
	if (session?.statusReadiness === "checking") return { label: "Checking status", tone: "quiet" };
	if (!session || session.statusReadiness === "unavailable"
		|| session.activity?.state === "unknown" || session.status === "unknown" || session.status === "no_signal") {
		return { label: "Status unavailable", tone: "quiet" };
	}
	if (session.activity?.state === "idle" || session.status === "idle") return { label: "Idle", tone: "quiet" };
	if (session.activity?.state === "active" || session.status === "working") return { label: "Working", tone: "working" };
	return { label: "Status unavailable", tone: "quiet" };
}

/** Time since `startedAt`, ticking each second; absent without a start. */
function useElapsed(startedAt: string | undefined): string | undefined {
	const started = startedAt ? Date.parse(startedAt) : Number.NaN;
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!Number.isFinite(started)) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, [started]);
	return Number.isFinite(started) ? formatDuration(Math.max(0, now - started)) : undefined;
}

function SpawnedAgentBox({ sessionId }: { sessionId: string }) {
	const session = useWorkerSession(sessionId);
	const state = waitingState(session);
	const working = state.tone === "working";
	const elapsed = useElapsed(working ? session?.createdAt : undefined);
	const name = session?.title || sessionId;
	const href = session ? sessionHref({ sessionId, projectId: session.workspaceId }) : undefined;
	// The daemon's phrase for the stage the worker is in, when it says more than the state does.
	const phrase = session?.provisionState === "failed" ? session.provisionError : session?.displayStatus;
	const phase = phrase && !["working", state.label.toLowerCase()].includes(phrase.toLowerCase()) ? phrase : undefined;
	return (
		<div data-testid="spawned-agent" className="cursor-chat-explore-box flex min-w-0 items-start gap-2.5 rounded-lg border px-3 py-2.5">
			{/* The spawn's own mark, as the turn's "Spawned" row had, then the agent that runs the worker. */}
			<Split aria-hidden="true" className="mt-[3px] size-3.5 shrink-0 text-muted-foreground" />
			{session?.provider ? (
				<AgentAvatar provider={session.provider} decorative className="mt-px size-4 shrink-0" />
			) : (
				<Bot aria-hidden="true" className="mt-px size-4 shrink-0 text-muted-foreground" />
			)}
			<div className="min-w-0 flex-1">
				<div className="flex min-w-0 items-center gap-3">
					<span className="min-w-0 flex-1 truncate text-[length:var(--font-size-base)] font-medium text-foreground">
						{href ? <SessionLabelLink href={href} className="decoration-foreground/40 underline-offset-2 hover:underline">{name}</SessionLabelLink> : name}
					</span>
					<span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
						{working ? (
							<ResponseSpinner />
						) : (
							<span aria-hidden="true" className={cn("mx-[2.5px] size-[7px] shrink-0 rounded-full", state.tone === "attention" ? "bg-status-needs-you" : "bg-status-idle")} />
						)}
						<span className={cn(state.tone === "attention" && "text-status-needs-you")}>{state.label}</span>
						{elapsed ? <span className="tabular-nums">· {elapsed}</span> : null}
					</span>
				</div>
				{phase ? <p className="mt-0.5 truncate text-xs text-muted-foreground" title={phase}>{phase}</p> : null}
			</div>
		</div>
	);
}
