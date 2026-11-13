// Development builds only: an in-memory stand-in for a paired machine's daemon.
// It answers the app's /api/v1 requests with generated projects, workers,
// orchestrators, notifications and chat transcripts, and keeps a few chats
// "replying" so the live-update path runs the way it does against a real agent.
// See ./transport.ts for how requests reach it and ./index.ts for the switch.

import { demoArtifacts, demoArtifactText, demoFile, demoManifest, demoSearch, demoTree } from "./workspace";

export const DEMO_HOST_ID = "demo-machine";
export const DEMO_HOSTNAME = "demo.ao.invalid";
export const DEMO_PORT = 3011;

/** How often a replying chat grows, matching the daemon's change poller. */
export const DEMO_TICK_MS = 100;
const REPLY_CHARS_PER_TICK = 24;
const AUTOPILOT_PAUSE_MS = 5_000;
const ARCHIVE_PAGE_DEFAULT = 25;

/** `text` answers with that body as is (an artifact's file) instead of `body` as JSON. */
export type DemoResponse = { status: number; body: unknown; text?: string; contentType?: string };
export type DemoEvent = { seq: number; projectId: string; sessionId: string; type: string; payload: { conversationId: string }; createdAt: string };

type Project = { id: string; name: string; kind: "single_repo" | "workspace" | "scratch"; sessionPrefix: string };
type WirePR = { url: string; number: number; state: string; ci: string; review: string; mergeability: string; reviewComments: boolean };
type WireSession = {
	id: string;
	projectId: string;
	terminalHandleId: string;
	kind: "worker" | "orchestrator";
	harness: string;
	mode: "chat" | "tui";
	displayName: string;
	activity: { state: string; lastActivityAt: string };
	isTerminated: boolean;
	status: string;
	kanbanColumn: string;
	displayStatus: string;
	branch: string;
	createdAt: string;
	updatedAt: string;
	isPinned: boolean;
	prs: WirePR[];
	previewUrl?: string;
	artifactFiles?: unknown[];
};
type Message = {
	kind: "message"; id: string; turnId: string; sequence: number; revision: number;
	role: "user" | "assistant"; origin: "human" | "provider"; text: string; streaming: boolean; createdAt: string;
};
type Activity = {
	kind: "activity"; id: string; turnId: string; sequence: number; revision: number;
	activityKind: "command"; status: "completed" | "running"; summary: string;
	detail: { command: string; output: string; exitCode: number; durationMs: number; cwd: string };
	createdAt: string;
};
type Turn = { id: string; state: "completed" | "running"; requestedAt: string; startedAt: string; completedAt?: string };
type Conversation = {
	session: WireSession;
	items: (Message | Activity)[];
	turns: Turn[];
	nextSequence: number;
	reply: { message: Message; turn: Turn; target: string } | null;
	/** Keeps starting new replies on its own, so there is always a live chat to open. */
	autopilot: boolean;
	nextPromptAt: number;
	prompts: number;
};

const PROJECTS: Project[] = [
	{ id: "web-app", name: "web-app", kind: "single_repo", sessionPrefix: "web" },
	{ id: "api-server", name: "api-server", kind: "single_repo", sessionPrefix: "api" },
	{ id: "mobile", name: "mobile", kind: "single_repo", sessionPrefix: "mob" },
	{ id: "platform", name: "platform", kind: "workspace", sessionPrefix: "plat" },
];

const HARNESSES = ["claude-code", "codex", "opencode", "cursor", "copilot", "amp"];

const TASKS = [
	"Fix flaky login test", "Add dark mode to settings", "Paginate the audit log", "Upgrade React Native to 0.86",
	"Cache avatar images", "Retry failed webhooks", "Speed up cold start", "Split the billing service",
	"Add CSV export to reports", "Migrate auth to OAuth 2.1", "Rate limit the public API", "Fix memory leak in uploader",
	"Add search to the file tree", "Rework onboarding copy", "Support SSO for teams", "Add e2e tests for checkout",
	"Debounce autosave", "Lazy-load the charts", "Stream large downloads", "Fix timezone bug in scheduler",
	"Add feature flags", "Remove the legacy v1 routes", "Show sync status in header", "Compress API responses",
];

// What a worker in each lifecycle stage looks like on the wire. The cycle
// spreads the workers over every board column and every badge.
const STAGES: { status: string; kanban: string; display: string; activity: string; pr?: Partial<WirePR> }[] = [
	{ status: "working", kanban: "building", display: "Working", activity: "active" },
	{ status: "needs_input", kanban: "building", display: "Needs input", activity: "waiting_input" },
	{ status: "working", kanban: "building", display: "Working", activity: "active" },
	{ status: "idle", kanban: "building", display: "Idle", activity: "idle" },
	{ status: "draft", kanban: "validating", display: "Draft", activity: "idle", pr: { state: "draft", ci: "pending" } },
	{ status: "ci_failed", kanban: "validating", display: "CI failing", activity: "idle", pr: { ci: "failing" } },
	{ status: "review_pending", kanban: "needs_review", display: "Awaiting review", activity: "idle", pr: { ci: "passing", review: "review_required" } },
	{ status: "changes_requested", kanban: "needs_review", display: "Changes requested", activity: "idle", pr: { ci: "passing", review: "changes_requested", reviewComments: true } },
	{ status: "mergeable", kanban: "ready", display: "Mergeable", activity: "idle", pr: { ci: "passing", review: "approved", mergeability: "mergeable" } },
	{ status: "approved", kanban: "ready", display: "Approved", activity: "idle", pr: { ci: "pending", review: "approved" } },
];

const WORKERS_PER_PROJECT = 10;
const ARCHIVED_PER_PROJECT = 12;

const LOREM = [
	"I traced the problem to the request layer, where a newer response replaces an older one before it lands.",
	"The fix keeps a single request in flight and queues at most one more, so every answer reaches the screen.",
	"Tests cover the slow path with fake timers: a 1.5 second response, a change every 150 ms, ten seconds of reply.",
	"I left the public API unchanged; callers still await the same promise and get the same shape back.",
	"Two call sites relied on the old ordering, so I updated them and added a regression test for each.",
	"The migration is idempotent and safe to re-run; it skips rows that already have the new column filled in.",
	"Bundle size is down 38 KB after removing the unused polyfills and splitting the chart code into its own chunk.",
];

function hash(text: string): number {
	let h = 2166136261;
	for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 16777619);
	return h >>> 0;
}

/** Deterministic, so a reload shows the same machine. */
function random(seed: string): () => number {
	let a = hash(seed);
	return () => {
		a = (a + 0x6d2b79f5) | 0;
		let t = Math.imul(a ^ (a >>> 15), 1 | a);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const pick = <T,>(rand: () => number, list: readonly T[]): T => list[Math.floor(rand() * list.length)];
const iso = (ms: number) => new Date(ms).toISOString();
const slug = (text: string) => text.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");

function assistantText(rand: () => number, task: string, paragraphs: number): string {
	const parts = [`Working on **${task.toLowerCase()}**.`];
	for (let i = 0; i < paragraphs; i++) parts.push(`${pick(rand, LOREM)} ${pick(rand, LOREM)}`);
	parts.push(
		"Changes:\n\n- `src/requests.ts`: queue instead of discard\n- `src/requests.test.ts`: slow-link cases\n- `docs/networking.md`: updated diagram",
		"```ts\nexport function createQueue() {\n\tlet running: Promise<void> | null = null;\n\tlet queued = false;\n\treturn { running, queued };\n}\n```",
		`${pick(rand, LOREM)}`,
	);
	return parts.join("\n\n");
}

function commandOutput(rand: () => number, lines: number): string {
	const out: string[] = [];
	for (let i = 0; i < lines; i++) {
		out.push(rand() < 0.15
			? `  ✓ ${pick(rand, TASKS).toLowerCase()} (${Math.floor(rand() * 900) + 10} ms)`
			: `src/${pick(rand, ["api", "lib", "ui", "store"])}/${slug(pick(rand, TASKS))}.ts:${Math.floor(rand() * 400) + 1}: ${pick(rand, LOREM)}`);
	}
	return out.join("\n");
}

export class DemoServer {
	private readonly started: number;
	private readonly workers: WireSession[] = [];
	private readonly archived: WireSession[] = [];
	private readonly orchestrators: WireSession[] = [];
	private readonly conversations = new Map<string, Conversation>();
	private readonly listeners = new Set<(event: DemoEvent) => void>();
	private eventSeq = 0;

	constructor(now = Date.now()) {
		this.started = now;
		let n = 0;
		for (const project of PROJECTS) {
			for (let i = 0; i < WORKERS_PER_PROJECT; i++) this.workers.push(this.makeWorker(project, i, n++, false));
			for (let i = 0; i < ARCHIVED_PER_PROJECT; i++) this.archived.push(this.makeWorker(project, WORKERS_PER_PROJECT + i, n++, true));
			this.orchestrators.push(this.makeOrchestrator(project));
		}
		// Two chats that keep replying: one opens the board, one is further down.
		for (const id of ["web-1", "api-3"]) {
			const conversation = this.conversation(id);
			if (conversation) conversation.autopilot = true;
		}
	}

	get latestEventSeq(): number {
		return this.eventSeq;
	}

	onEvent(listener: (event: DemoEvent) => void): () => void {
		this.listeners.add(listener);
		return () => { this.listeners.delete(listener); };
	}

	private makeWorker(project: Project, index: number, n: number, archived: boolean): WireSession {
		const rand = random(`${project.id}-${index}`);
		const id = `${project.sessionPrefix}-${index + 1}`;
		const task = TASKS[(n * 7 + index) % TASKS.length];
		const stage = archived ? undefined : STAGES[index % STAGES.length];
		const created = this.started - (index + 1) * 47 * 60_000 - Math.floor(rand() * 600_000);
		const updated = archived ? created + 3_600_000 : this.started - Math.floor(rand() * 40 * 60_000);
		const prNumber = 1200 + n;
		const pr = (base: Partial<WirePR>): WirePR => ({
			url: `https://github.com/acme/${project.id}/pull/${prNumber}`,
			number: prNumber,
			state: "open",
			ci: "unknown",
			review: "none",
			mergeability: "unknown",
			reviewComments: false,
			...base,
		});
		return {
			id,
			projectId: project.id,
			terminalHandleId: `term-${id}`,
			kind: "worker",
			harness: HARNESSES[n % HARNESSES.length],
			mode: index % 4 === 3 ? "tui" : "chat",
			displayName: task,
			activity: { state: archived ? "exited" : stage!.activity, lastActivityAt: iso(updated) },
			isTerminated: archived,
			status: archived ? (index % 3 === 0 ? "terminated" : "merged") : stage!.status,
			kanbanColumn: archived ? "archive" : stage!.kanban,
			displayStatus: archived ? (index % 3 === 0 ? "Terminated" : "Merged") : stage!.display,
			branch: `ao/${id}-${slug(task)}`,
			createdAt: iso(created),
			updatedAt: iso(updated),
			isPinned: !archived && index === 0,
			prs: archived
				? (index % 3 === 0 ? [] : [pr({ state: "merged", ci: "passing", review: "approved", mergeability: "mergeable" })])
				: stage!.pr ? [pr(stage!.pr)] : [],
			// A page to open in Preview (any reachable page: a WebView loads it natively).
			...(archived || index % 2 ? {} : { previewUrl: "https://example.com/" }),
			artifactFiles: archived ? [] : demoArtifacts(id, iso(updated)),
		};
	}

	private makeOrchestrator(project: Project): WireSession {
		const at = iso(this.started - 5 * 60_000);
		return {
			id: `${project.sessionPrefix}-orchestrator`,
			projectId: project.id,
			terminalHandleId: `term-${project.sessionPrefix}-orchestrator`,
			kind: "orchestrator",
			harness: "claude-code",
			mode: "chat",
			displayName: `${project.name} orchestrator`,
			activity: { state: "idle", lastActivityAt: at },
			isTerminated: false,
			status: "idle",
			kanbanColumn: "building",
			displayStatus: "Idle",
			branch: "main",
			createdAt: iso(this.started - 86_400_000),
			updatedAt: at,
			isPinned: false,
			prs: [],
		};
	}

	private session(id: string): WireSession | undefined {
		return this.workers.find((s) => s.id === id) ?? this.orchestrators.find((s) => s.id === id) ?? this.archived.find((s) => s.id === id);
	}

	/** A chat's history, generated the first time it is asked for. */
	private conversation(sessionId: string): Conversation | undefined {
		const existing = this.conversations.get(sessionId);
		if (existing) return existing;
		const session = this.session(sessionId);
		if (!session) return undefined;
		const rand = random(`chat-${sessionId}`);
		const conversation: Conversation = { session, items: [], turns: [], nextSequence: 1, reply: null, autopilot: false, nextPromptAt: 0, prompts: 0 };
		const turnCount = 8 + Math.floor(rand() * 16);
		let at = Date.parse(session.createdAt);
		for (let t = 0; t < turnCount; t++) {
			const turn: Turn = { id: `${sessionId}-turn-${t + 1}`, state: "completed", requestedAt: iso(at), startedAt: iso(at + 500) };
			conversation.turns.push(turn);
			this.push(conversation, { role: "user", origin: "human", text: t === 0 ? session.displayName : `${pick(rand, ["Also", "Next,", "Then", "Can you"])} ${pick(rand, TASKS).toLowerCase()}?`, turnId: turn.id, at });
			const commands = 1 + Math.floor(rand() * 3);
			for (let c = 0; c < commands; c++) {
				at += 4_000;
				const command = pick(rand, ["npm test", "rg -n \"fetch\" src", "git diff --stat", "npx tsc --noEmit", "go test ./...", "ls -la src/lib"]);
				conversation.items.push({
					kind: "activity",
					id: `${turn.id}-cmd-${c + 1}`,
					turnId: turn.id,
					sequence: conversation.nextSequence++,
					revision: 1,
					activityKind: "command",
					status: "completed",
					summary: command,
					detail: { command, output: commandOutput(rand, 20 + Math.floor(rand() * 40)), exitCode: rand() < 0.1 ? 1 : 0, durationMs: Math.floor(rand() * 9_000), cwd: `/work/${session.projectId}` },
					createdAt: iso(at),
				});
			}
			at += 6_000;
			this.push(conversation, { role: "assistant", origin: "provider", text: assistantText(rand, session.displayName, 1 + Math.floor(rand() * 3)), turnId: turn.id, at });
			turn.completedAt = iso(at);
			at += 60_000 + Math.floor(rand() * 600_000);
		}
		this.conversations.set(sessionId, conversation);
		return conversation;
	}

	private push(conversation: Conversation, m: { role: Message["role"]; origin: Message["origin"]; text: string; turnId: string; at: number; streaming?: boolean }): Message {
		const message: Message = {
			kind: "message",
			id: `${m.turnId}-${m.role}`,
			turnId: m.turnId,
			sequence: conversation.nextSequence++,
			revision: 1,
			role: m.role,
			origin: m.origin,
			text: m.text,
			streaming: m.streaming ?? false,
			createdAt: iso(m.at),
		};
		conversation.items.push(message);
		return message;
	}

	private changed(conversation: Conversation, now: number): void {
		const event: DemoEvent = {
			seq: ++this.eventSeq,
			projectId: conversation.session.projectId,
			sessionId: conversation.session.id,
			type: "conversation.updated",
			payload: { conversationId: `conv-${conversation.session.id}` },
			createdAt: iso(now),
		};
		for (const listener of this.listeners) listener(event);
	}

	/** Starts a reply in a chat, as if the user (or autopilot) had just sent `prompt`. */
	private startReply(conversation: Conversation, prompt: string, now: number): Turn {
		const rand = random(`reply-${conversation.session.id}-${conversation.prompts++}`);
		const turn: Turn = { id: `${conversation.session.id}-live-${conversation.prompts}`, state: "running", requestedAt: iso(now), startedAt: iso(now) };
		conversation.turns.push(turn);
		this.push(conversation, { role: "user", origin: "human", text: prompt, turnId: turn.id, at: now });
		const message = this.push(conversation, { role: "assistant", origin: "provider", text: "", turnId: turn.id, at: now, streaming: true });
		conversation.reply = { message, turn, target: assistantText(rand, conversation.session.displayName, 6 + Math.floor(rand() * 4)) };
		this.setActivity(conversation.session, "active", "working", "Working", now);
		this.changed(conversation, now);
		return turn;
	}

	private setActivity(session: WireSession, activity: string, status: string, display: string, now: number): void {
		session.activity = { state: activity, lastActivityAt: iso(now) };
		session.status = status;
		session.displayStatus = display;
		session.updatedAt = iso(now);
	}

	/** Advances every replying chat by one step. Call every DEMO_TICK_MS. */
	tick(now = Date.now()): void {
		for (const conversation of this.conversations.values()) {
			const reply = conversation.reply;
			if (reply) {
				const { message, target } = reply;
				message.text = target.slice(0, message.text.length + REPLY_CHARS_PER_TICK);
				message.revision++;
				if (message.text.length >= target.length) {
					message.streaming = false;
					reply.turn.state = "completed";
					reply.turn.completedAt = iso(now);
					conversation.reply = null;
					conversation.nextPromptAt = now + AUTOPILOT_PAUSE_MS;
					this.setActivity(conversation.session, "idle", "idle", "Idle", now);
				}
				this.changed(conversation, now);
			} else if (conversation.autopilot && now >= conversation.nextPromptAt) {
				this.startReply(conversation, `Keep going: ${pick(random(`p-${conversation.prompts}`), TASKS).toLowerCase()}.`, now);
			}
		}
	}

	handle(method: string, url: string, body?: string, now = Date.now()): DemoResponse {
		const parsed = new URL(url, `http://${DEMO_HOSTNAME}:${DEMO_PORT}`);
		const path = parsed.pathname.replace(/^\/api\/v1/, "");
		const q = parsed.searchParams;
		const segments = path.split("/").filter(Boolean).map(decodeURIComponent);
		const get = method === "GET";

		if (get && path === "/identity") return ok({ hostId: DEMO_HOST_ID, apiVersion: 1 });
		if (get && path === "/endpoints") return ok({ endpoints: [demoEndpoint()] });
		if (get && path === "/projects") return ok({ projects: PROJECTS });
		if (get && segments[0] === "projects" && segments.length === 2) {
			const project = PROJECTS.find((p) => p.id === segments[1]);
			return project ? ok({ project: { ...project, agent: "claude-code" } }) : notFound();
		}
		if (get && path === "/orchestrators") return ok({ sessions: this.orchestrators });
		if (get && path === "/sessions") return ok(this.listSessions(q));
		if (segments[0] === "sessions" && segments[1]) {
			const id = segments[1];
			const session = this.session(id);
			if (!session) return notFound();
			if (get && segments.length === 2) return ok({ session });
			if (segments[2] === "conversation") return this.handleConversation(method, id, segments.slice(3), q, body, now);
			const files = this.handleFiles(method, id, segments.slice(2), q, session);
			if (files) return files;
		}
		if (get && path === "/notifications") return ok(this.notifications(q));
		if (get && path === "/settings") return ok({ defaultSessionMode: "chat", chatHarnesses: HARNESSES });
		if (get && path === "/agents") {
			const agents = HARNESSES.map((id) => ({ id, label: id, authStatus: "authorized" }));
			return ok({ supported: agents, installed: agents, authorized: agents });
		}
		if (path === "/shell-terminals") return this.handleShells(method, body, now);
		if (get && segments[0] === "agents" && segments[2] === "models") {
			return ok({
				agentId: segments[1],
				selectionMode: "catalog",
				allowCustom: false,
				source: "demo",
				stale: false,
				fetchedAt: iso(now),
				models: [
					{ id: "demo-large", label: "Demo Large", isDefault: true, efforts: ["low", "medium", "high"], defaultEffort: "medium" },
					{ id: "demo-small", label: "Demo Small" },
				],
			});
		}
		// Push registration and unpairing succeed, so forgetting the demo machine
		// from Settings works like forgetting a real one.
		if (segments[0] === "push" && (method === "POST" || method === "DELETE")) return ok({});
		return notFound();
	}

	/** The Files, artifact and preview routes under /sessions/:id. */
	private handleFiles(method: string, sessionId: string, rest: string[], q: URLSearchParams, session: WireSession): DemoResponse | undefined {
		const route = rest.join("/");
		if (method === "GET") {
			if (route === "workspace/manifest") return ok(demoManifest(sessionId));
			if (route === "workspace/file") {
				const file = demoFile(sessionId, q.get("path") ?? "");
				return file ? ok(file) : notFound();
			}
			if (route === "workspace/tree") return ok(demoTree(sessionId, q.get("path") ?? ""));
			if (route === "workspace/search") return ok(demoSearch(sessionId, q.get("query") ?? ""));
			if (route === "workspace/branches") {
				return ok({
					branches: [
						{ kind: "local", name: session.branch, checkedOut: true, isDefault: false },
						{ kind: "local", name: "main", checkedOut: false, isDefault: true },
					],
					supported: true,
					truncated: false,
				});
			}
			if (route === "workspace/history") return ok({ commits: [], commitsTruncated: false, view: { kind: "checked_out", name: session.branch, baseRef: "main", rangeMode: "since_base" } });
			if (route === "preview") return ok(session.previewUrl ? { previewUrl: session.previewUrl } : {});
			if (route === "artifact-versions") return ok({ versions: [] });
			// The artifact's own route, and the preview route the viewer reads HTML through.
			const artifact = rest[0] === "artifact-files" ? rest.slice(1).join("/")
				: rest[0] === "preview" && rest[1] === "files" && rest[2] === "__ao_artifacts__" ? rest.slice(3).join("/")
				: undefined;
			if (artifact !== undefined) {
				const text = demoArtifactText(sessionId, artifact);
				return text === undefined ? notFound() : { status: 200, body: null, text, contentType: "text/plain; charset=utf-8" };
			}
		}
		// Review comments from the file viewer reach the agent.
		if (method === "POST" && route === "send") return ok({});
		return undefined;
	}

	private readonly shells: { handleId: string; projectId: string; sessionId: string; workingDir: string; title: string; createdAt: string }[] = [];

	/** Worktree shells: one per session, reused, as the daemon does. The mux socket answers their I/O. */
	private handleShells(method: string, body: string | undefined, now: number): DemoResponse {
		if (method === "GET") return ok({ shellTerminals: this.shells });
		let wanted: { projectId?: string; sessionId?: string } = {};
		try { wanted = JSON.parse(body ?? "{}") as typeof wanted; } catch { /* empty */ }
		const shell = {
			handleId: `shell-${wanted.sessionId ?? this.shells.length + 1}`,
			projectId: wanted.projectId ?? "",
			sessionId: wanted.sessionId ?? "",
			workingDir: `/work/${wanted.projectId ?? "demo"}`,
			title: "zsh",
			createdAt: iso(now),
		};
		this.shells.push(shell);
		return ok({ shellTerminal: shell });
	}

	private listSessions(q: URLSearchParams): unknown {
		const project = q.get("projectId");
		const inProject = (s: WireSession) => !project || s.projectId === project;
		if (q.get("active") === "true") {
			const byProject: Record<string, number> = {};
			for (const s of this.archived) byProject[s.projectId] = (byProject[s.projectId] ?? 0) + 1;
			return { sessions: this.workers.filter(inProject), archived: { total: this.archived.length, byProject } };
		}
		if (q.get("active") === "false") {
			const limit = Number(q.get("limit")) || ARCHIVE_PAGE_DEFAULT;
			const start = Number(q.get("cursor")) || 0;
			const rows = this.archived.filter(inProject);
			const next = start + limit < rows.length ? String(start + limit) : null;
			return { sessions: rows.slice(start, start + limit), nextCursor: next };
		}
		return { sessions: [...this.workers, ...this.archived].filter(inProject) };
	}

	private notifications(q: URLSearchParams): unknown {
		const rows = this.workers
			.filter((s) => s.status === "needs_input" || s.status === "mergeable" || s.status === "changes_requested")
			.map((s, i) => ({
				id: `n-${s.id}`,
				sessionId: s.id,
				projectId: s.projectId,
				prUrl: s.prs[0]?.url ?? "",
				type: s.status === "needs_input" ? "needs_input" : s.status === "mergeable" ? "ready_to_merge" : "review_changes_requested",
				title: s.displayName,
				body: s.status === "needs_input" ? "The agent is waiting for your answer." : s.status === "mergeable" ? "Checks passed and the PR is approved." : "A reviewer asked for changes.",
				status: i % 3 === 0 ? "read" : "unread",
				createdAt: s.updatedAt,
			}));
		const unread = rows.filter((r) => r.status === "unread");
		const shown = q.get("status") === "unread" ? unread : rows;
		const limit = Number(q.get("limit")) || shown.length;
		return { notifications: shown.slice(0, limit), unreadCount: unread.length };
	}

	private handleConversation(method: string, sessionId: string, rest: string[], q: URLSearchParams, body: string | undefined, now: number): DemoResponse {
		const conversation = this.conversation(sessionId);
		if (!conversation) return notFound();
		if (method === "GET" && rest.length === 0) return ok(this.page(conversation, q));
		if (method === "POST" && (rest[0] === "messages" || rest[0] === "steer-or-send")) {
			let text = "";
			try { text = String((JSON.parse(body ?? "{}") as { text?: unknown }).text ?? ""); } catch { /* empty prompt */ }
			if (conversation.reply) return error(409, "busy", "The agent is still replying.");
			const turn = this.startReply(conversation, text || "(empty message)", now);
			return ok({ turnId: turn.id, state: "running", duplicate: false });
		}
		if (method === "POST" && rest[0] === "interrupt") {
			if (conversation.reply) {
				conversation.reply.message.streaming = false;
				conversation.reply.turn.state = "completed";
				conversation.reply = null;
				conversation.autopilot = false;
				this.setActivity(conversation.session, "idle", "idle", "Idle", now);
				this.changed(conversation, now);
			}
			return ok({});
		}
		return notFound();
	}

	private page(conversation: Conversation, q: URLSearchParams): unknown {
		const limit = Number(q.get("limit")) || 50;
		const before = q.has("beforeSequence") ? Number(q.get("beforeSequence")) : Number.POSITIVE_INFINITY;
		const eligible = conversation.items.filter((item) => item.sequence < before);
		const items = eligible.slice(-limit);
		const turnIds = new Set(items.map((item) => item.turnId));
		const latest = conversation.nextSequence - 1;
		const replying = Boolean(conversation.reply);
		const session = conversation.session;
		return {
			supportsExcerpts: true,
			conversationId: `conv-${session.id}`,
			sessionId: session.id,
			harness: session.harness,
			mode: "chat",
			controller: replying ? "busy" : "ready",
			latestSequence: latest,
			oldestSequence: items[0]?.sequence ?? latest + 1,
			hasMoreBefore: eligible.length > items.length,
			settings: { model: "demo-model", reasoningEffort: "medium" },
			title: session.displayName,
			turns: conversation.turns.filter((turn) => turnIds.has(turn.id)),
			// Copies: a page is a snapshot, and the reply keeps growing in place.
			messages: items.filter((item): item is Message => item.kind === "message").map((item) => ({ ...item })),
			activities: items.filter((item): item is Activity => item.kind === "activity").map(({ kind: _kind, ...item }) => item),
		};
	}
}

export function demoEndpoint() {
	return { kind: "lan" as const, host: DEMO_HOSTNAME, port: DEMO_PORT, secure: false };
}

function ok(body: unknown): DemoResponse {
	return { status: 200, body };
}

function error(status: number, code: string, message: string): DemoResponse {
	return { status, body: { error: code, code, message, requestId: `demo-${Date.now()}` } };
}

function notFound(): DemoResponse {
	return error(404, "not_found", "The demo machine doesn't have this yet.");
}
