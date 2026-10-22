/**
 * The orchestrator chat's view of its workers.
 *
 * A worker's message or report is drawn as one quiet row — who, state, a headline,
 * time — that opens to the full text, instead of a full-width card. What the
 * orchestrator did about it hangs under the row as an action line. Only a
 * question for the user stays a reply, so the chat reads as the user's
 * conversation rather than a log of worker traffic. A turn that spawned workers
 * lists them with where each one is now.
 */

import { useQuery } from "@tanstack/react-query";
import { Bot, ChevronRight, CornerDownRight, FileText, GitPullRequest, LoaderCircle, Split, Tag } from "lucide-react";
import { createContext, useContext, useState, type ReactNode } from "react";
import { workspaceQueryOptions } from "../../hooks/useWorkspaceQuery";
import { cn } from "../../lib/utils";
import type { ConversationItem, ConversationMessage } from "../../types/conversation";
import type { PullRequestFacts, WorkspaceSession } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { ArtifactPathCode, ChatMarkdown, SessionLabelLink } from "./ChatMarkdown";
import { formatTime, humanVisibleText, stripSteerSenderPrefix } from "./ChatTimelineItems";

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
// One entry per `[state] [name](ao://sessions/<project>/<session>)`, as report.PreparedBatch.Render writes it.
const REPORT_ENTRY = /\n\n\[([a-z_]+)\] \[((?:\\.|[^\]\\])*)\]\(ao:\/\/sessions\/([^/)\s]+)\/([^)\s]+)\)(?: \(repeated \d+ times\))?/g;
// The `artifact: <path>` line a report gets per attached artifact, as report.PreparedBatch.Render writes it.
const ARTIFACT_LINE = /^artifact: (\S+)$/;
const TITLE_UPDATE = "AO TASK TITLE UPDATE";
const RENAME_COMMAND = /\bao'?\s+session\s+rename\s+\S+\s+(["'])(.+?)\1/;

const STATES: Record<string, { label: string; attention?: boolean }> = {
	needs_input: { label: "needs you", attention: true },
	stuck: { label: "stuck", attention: true },
	done: { label: "done" },
};

function parseReports(text: string, createdAt: string, keyPrefix: string): WorkerUpdate[] {
	if (!text.startsWith(REPORTS_HEADER)) return [];
	const entries = [...text.matchAll(REPORT_ENTRY)];
	return entries.map((entry, index) => ({
		key: `${keyPrefix}:${index}`,
		state: entry[1],
		name: entry[2]!.replace(/\\(.)/g, "$1"),
		projectId: decodeURIComponent(entry[3]!),
		sessionId: decodeURIComponent(entry[4]!),
		body: text.slice(entry.index! + entry[0].length, entries[index + 1]?.index ?? text.length).trim(),
		createdAt,
	}));
}

/** What a worker sent, when this message is one: a direct `ao send` or a batch of reports. */
export function workerUpdatesOf(message: ConversationMessage): WorkerUpdate[] {
	if (message.role !== "user" || message.origin === "human") return [];
	const sender = message.senderSessionId?.trim();
	if (sender) {
		return [{
			key: message.id,
			name: message.senderDisplayName?.trim() || sender,
			sessionId: sender,
			projectId: message.senderProjectId,
			body: stripSteerSenderPrefix(message.text, sender).trim(),
			createdAt: message.createdAt,
		}];
	}
	return parseReports(message.text, message.createdAt, message.id);
}

/** Reports the daemon folded into a human message because the orchestrator was busy. */
export function piggybackedUpdates(message: ConversationMessage): WorkerUpdate[] {
	if (message.role !== "user" || message.origin !== "human") return [];
	const visible = humanVisibleText(message.text);
	if (visible === message.text) return [];
	const envelope = message.text
		.slice(visible.length)
		.trim()
		.replace(/^<ao-worker-reports>/, "")
		.replace(/<\/ao-worker-reports>$/, "")
		.trim();
	return parseReports(envelope, message.createdAt, `${message.id}:reports`);
}

export function isTaskTitleUpdate(message: ConversationMessage): boolean {
	return message.role === "user" && message.origin !== "human" && message.text.startsWith(TITLE_UPDATE);
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

/**
 * Paragraphs that ask the user something stay a reply; the rest are what the
 * orchestrator did.
 */
// ponytail: a "?" marks a question, so one inside a URL misfiles a paragraph; a structured ask would remove the guess.
export function splitOrchestratorReply(text: string): { actions: string[]; question: string } {
	const paragraphs = text.split(/\n{2,}/).map((paragraph) => paragraph.trim()).filter(Boolean);
	const firstQuestion = paragraphs.findIndex((paragraph) => paragraph.includes("?"));
	if (firstQuestion < 0) return { actions: paragraphs, question: "" };
	return { actions: paragraphs.slice(0, firstQuestion), question: paragraphs.slice(firstQuestion).join("\n\n") };
}

function finalReplyText(items: readonly ConversationItem[]): string {
	for (let index = items.length - 1; index >= 0; index -= 1) {
		const item = items[index];
		if (item?.kind === "message" && item.role === "assistant" && item.text.trim() !== "") return item.text;
	}
	return "";
}

/** A settled worker turn that draws only rows and action lines, so it can sit tight against its neighbours. */
export function isQuietWorkerTurn(items: readonly ConversationItem[]): boolean {
	const lead = items[0];
	if (lead?.kind !== "message") return false;
	if (isTaskTitleUpdate(lead)) return renamedTitle(items) !== undefined;
	if (workerUpdatesOf(lead).length === 0) return false;
	return splitOrchestratorReply(finalReplyText(items)).question === "";
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
		if (item.kind !== "message") continue;
		for (const update of [...piggybackedUpdates(item), ...workerUpdatesOf(item)]) {
			latest.set(update.sessionId ?? update.key, update);
		}
	}
	return { latest, latestKeys: new Set([...latest.values()].map((update) => update.key)) };
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

function useWorkerSession(sessionId: string | undefined): WorkspaceSession | undefined {
	const { data } = useQuery({
		...workspaceQueryOptions,
		// Read what the sidebar already loaded; a row never fetches on its own.
		enabled: false,
		select: (workspaces) =>
			workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === sessionId),
	});
	return sessionId ? data : undefined;
}

function WorkerUpdateRow({ update }: { update: WorkerUpdate }) {
	const [open, setOpen] = useState(false);
	const provider = useWorkerSession(update.sessionId)?.provider;
	const index = useContext(WorkerUpdateIndexContext);
	const { text, artifacts } = splitArtifacts(update.body);
	const { headline, detail } = splitHeadline(text);
	const state = update.state ? STATES[update.state] : undefined;
	const attention = state?.attention && isCurrent(index, update);
	const href = sessionHref(update);
	const name = <span className="block truncate">{update.name}</span>;
	return (
		<div className="min-w-0 text-xs" data-testid="worker-update">
			<div className="group -mx-1.5 flex h-7 w-[calc(100%+0.75rem)] min-w-0 items-center gap-2 rounded-md px-1.5 text-muted-foreground transition-colors hover:bg-interactive-hover">
				{provider ? (
					<AgentAvatar provider={provider} decorative className="size-4 shrink-0" />
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
						<ArtifactPathCode text={path}>{path.split("/").at(-1)}</ArtifactPathCode>
					</span>
				</p>
			))}
			{open ? (
				<div className="mb-2 ml-6 mt-1 min-w-0">
					<ChatMarkdown text={detail} muted safeOrigin className="whitespace-pre-wrap text-xs leading-relaxed" />
				</div>
			) : null}
		</div>
	);
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

/** What the orchestrator did, under the rows that prompted it. Opens to the work behind it. */
export function OrchestratorAction({ text, children }: { text: string; children?: ReactNode }) {
	const [open, setOpen] = useState(false);
	const line = (
		<>
			<CornerDownRight aria-hidden="true" className="mt-[3px] size-3 shrink-0 text-muted-foreground" />
			<span className="min-w-0 flex-1">{plain(text)}</span>
		</>
	);
	return (
		<div className="-mt-1.5 ml-6 min-w-0 text-xs" data-testid="orchestrator-action">
			{children ? (
				<button
					type="button"
					aria-expanded={open}
					onClick={() => setOpen((current) => !current)}
					className="flex w-full min-w-0 items-start gap-2 rounded text-left text-xs leading-relaxed text-foreground transition-colors hover:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					{line}
				</button>
			) : (
				<p className="flex min-w-0 items-start gap-2 text-xs leading-relaxed text-foreground">{line}</p>
			)}
			{open ? <div className="mt-2 flex min-w-0 flex-col gap-2.5">{children}</div> : null}
		</div>
	);
}

export function TaskNamedLine({ title }: { title: string }) {
	return (
		<p className="flex h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground">
			<Tag aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="min-w-0 truncate">
				New task named <span className="font-medium text-foreground">{title}</span>
			</span>
		</p>
	);
}

const readyToMerge = (pr: PullRequestFacts | undefined) =>
	pr?.state === "open" && pr.ci === "passing" && pr.review === "approved" && pr.mergeability !== "conflicting";

// ponytail: reads `ao spawn` output; #6507's session.spawned rows replace this once merged.
const SPAWNED = /\bSpawned\s+(?:session\s+)?([\w.-]+)/;

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
		lead = <GitPullRequest aria-hidden="true" className={cn("size-3.5 shrink-0", readyToMerge(pr) ? "text-status-ready" : "text-muted-foreground")} />;
		status = `#${pr.number}`;
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
