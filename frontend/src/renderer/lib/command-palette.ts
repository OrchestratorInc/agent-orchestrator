import { sessionBranchPresentation } from "./session-branch";
import type { TFunction } from "i18next";
import {
	attentionZone,
	attentionZoneOrder,
	isOrchestratorSession,
	openPRs,
	sessionIsActive,
	sessionNeedsAttention,
	workerSessions,
	type AgentProvider,
	type AttentionZone,
	type PullRequestFacts,
	type WorkspaceSession,
	type WorkspaceSummary,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";
import {
	openReviewStatesFor,
	reviewIsRunning,
	reviewSessionRunAction,
	type PRReviewState,
} from "./session-reviews";
import { appI18n, type MessageKey } from "../i18n";
import type { components } from "../../api/schema";
import type { AppShortcutId } from "../../shared/shortcuts";
import type { GlobalSettingsSection } from "../stores/ui-store";
import { renderedSnippetText } from "./chat-annotation-navigation";
import { getSessionStatusDotView } from "./session-presentation";

export type CommandGroupId = "current" | "attention" | "files" | "content" | "messages" | "projects" | "sessions" | "prs" | "settings" | "global";

export type NavigateTarget =
	| { to: "/" }
	| { to: "/settings" }
	| { to: "/projects/$projectId"; params: { projectId: string } }
	| { to: "/projects/$projectId/settings"; params: { projectId: string } }
	| { to: "/projects/$projectId/sessions/$sessionId"; params: { projectId: string; sessionId: string } }
	| { to: "/sessions/$sessionId"; params: { sessionId: string } }
	| { to: "/host/$hostId/project/$projectId/session/$sessionId"; params: { hostId: string; projectId: string; sessionId: string } }
	| { to: "/host/$hostId/session/$sessionId"; params: { hostId: string; sessionId: string } };

export type WorkspaceFileSearchTarget = {
	projectId: string;
	sessionId: string;
	hostId?: string;
	cloudOrgId?: string;
};

export type WorkspaceFileSearchItem = {
	path: string;
	status?: string;
	binary?: boolean;
};

export type CommandAction =
	| { kind: "navigate"; target: NavigateTarget }
	| { kind: "open-new-task"; projectId: string }
	| { kind: "open-new-project" }
	| { kind: "open-orchestrator"; projectId: string }
	| { kind: "open-session-actions"; sessionId: string }
	| { kind: "open-file-search"; projectId: string; target?: WorkspaceFileSearchTarget }
	| { kind: "open-workspace-file"; target: WorkspaceFileSearchTarget; path: string; line?: number }
	| { kind: "open-chat-message"; sessionId?: string; projectId?: string; reviewId?: string; messageId: string; text: string }
	| { kind: "open-settings-section"; section: GlobalSettingsSection; /** Visible label of the row to land on. */ focus?: string }
	| { kind: "run-shortcut"; shortcut: AppShortcutId }
	| { kind: "resume-session"; projectId: string; sessionId: string }
	| { kind: "copy-branch"; branch: string }
	| { kind: "open-pr"; url: string }
	| { kind: "copy-pr-url"; url: string }
	| { kind: "trigger-review"; sessionId: string }
	| { kind: "toggle-theme" }
	| { kind: "open-shared-link" };

export type CommandItem = {
	id: string;
	group: CommandGroupId;
	title: string;
	subtitle?: string;
	keywords?: string[];
	disabled?: boolean;
	disabledReason?: string;
	searchOnly?: boolean;
	zone?: AttentionZone;
	action?: CommandAction;
	/** A matched line shown under the title, with the matched spans marked. */
	snippet?: { text: string; highlights: { start: number; end: number }[]; code?: boolean };
	/** Rows the daemon found by content: already matched, ranked by matchScore's content tier. */
	contentMatch?: boolean;
	/** The ⌘↵ alternative to `action`, such as a session's actions list. */
	secondaryAction?: CommandAction;
	/** A session row's live status, drawn as the sidebar's dot. */
	status?: { className: string; breathe: boolean };
	/** A session row's agent, drawn as its logo. */
	agent?: AgentProvider;
	/** The app shortcut this row runs, shown as its keys. */
	shortcut?: AppShortcutId;
	/** What the command acts on, drawn bold after the title: "New task in **app**". */
	emphasis?: string;
};

export type CommandPaletteContext = {
	workspaces: WorkspaceSummary[];
	currentProjectId?: string;
	currentSessionId?: string;
	currentHostId?: string;
	restartingProjectIds?: ReadonlySet<string>;
	/**
	 * Live review states per session, injected by the palette's queries. Omitting the
	 * whole prop means "don't gate on review state" (callers that build items without
	 * fetching, e.g. unit tests). Providing it but leaving a session's key absent means
	 * that session's state is not known yet, and its review row is omitted entirely
	 * rather than rendered with a guessed enabled state.
	 */
	reviewStatesBySessionId?: Readonly<Record<string, PRReviewState[]>>;
	/** Signed in to Cloud, so a shared session's link can be opened. */
	cloudReady?: boolean;
};

export const commandGroupOrder: CommandGroupId[] = ["current", "attention", "files", "content", "messages", "projects", "sessions", "prs", "settings", "global"];

const commandGroupLabelKeys: Record<CommandGroupId, MessageKey> = {
	current: "command.group.current",
	attention: "command.group.attention",
	files: "command.group.files",
	content: "command.group.content",
	messages: "command.group.messages",
	projects: "command.group.projects",
	sessions: "command.group.sessions",
	prs: "command.group.prs",
	settings: "command.group.settings",
	global: "command.group.global",
};

function isSyntheticBranch(session: WorkspaceSession): boolean {
	return session.branch === `session/${session.id}`;
}

type SessionCommandGroup = Extract<CommandGroupId, "attention" | "sessions">;

const SESSION_ID_PREFIX: Record<SessionCommandGroup, string> = { attention: "attention", sessions: "session" };

export type WorkspaceSessionContext = {
	workspace: WorkspaceSummary;
	session: WorkspaceSession;
};

function jumpTarget(workspace: WorkspaceSummary, session: WorkspaceSession): NavigateTarget {
	if (workspace.hostId) {
		if (workspace.id === STANDALONE_WORKSPACE_ID) {
			return { to: "/host/$hostId/session/$sessionId", params: { hostId: workspace.hostId, sessionId: session.id } };
		}
		return {
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: workspace.hostId, projectId: workspace.id, sessionId: session.id },
		};
	}
	if (workspace.id === STANDALONE_WORKSPACE_ID) {
		return { to: "/sessions/$sessionId", params: { sessionId: session.id } };
	}
	return {
		to: "/projects/$projectId/sessions/$sessionId",
		params: { projectId: workspace.id, sessionId: session.id },
	};
}

export function workspaceFileSearchTarget(
	workspace: WorkspaceSummary,
	session: WorkspaceSession,
): WorkspaceFileSearchTarget {
	return {
		projectId: workspace.id,
		sessionId: session.id,
		...(workspace.hostId ? { hostId: workspace.hostId } : {}),
		...(session.cloud?.orgId ? { cloudOrgId: session.cloud.orgId } : {}),
	};
}

export function sessionSupportsFileSearch(session: WorkspaceSession): boolean {
	return !isOrchestratorSession(session)
		&& session.provisionState !== "provisioning"
		&& session.provisionState !== "failed";
}

export function fileSearchSessions(workspace: WorkspaceSummary): WorkspaceSession[] {
	return workspace.sessions
		.filter(sessionSupportsFileSearch)
		.map((session, index) => ({ session, index }))
		.sort((a, b) => Number(!sessionIsActive(a.session)) - Number(!sessionIsActive(b.session)) || a.index - b.index)
		.map(({ session }) => session);
}

export function buildFileSessionCommands(
	workspace: WorkspaceSummary,
): CommandItem[] {
	return fileSearchSessions(workspace).map((session) => ({
		id: `file-session:${workspace.hostId ?? "local"}:${session.id}`,
		group: "sessions",
		title: session.title,
		subtitle: `${sessionBranchPresentation(session).branch || workspace.name} · ${session.displayStatus || session.status}`,
		keywords: [sessionBranchPresentation(session).branch ?? "", workspace.name, session.displayStatus ?? "", session.status],
		action: {
			kind: "open-file-search",
			projectId: workspace.id,
			target: workspaceFileSearchTarget(workspace, session),
		},
	}));
}

function workspacePathParts(path: string): { basename: string; directory?: string } {
	const segments = path.split("/");
	const basename = segments.pop() || path;
	const directory = segments.join("/");
	return { basename, ...(directory ? { directory } : {}) };
}

export function buildWorkspaceFileCommands(
	target: WorkspaceFileSearchTarget,
	files: readonly WorkspaceFileSearchItem[],
): CommandItem[] {
	return files.map((file) => {
		const { basename, directory } = workspacePathParts(file.path);
		return {
			id: `file:${target.hostId ?? "local"}:${target.sessionId}:${file.path}`,
			group: "files",
			title: basename,
			subtitle: directory,
			keywords: [file.path, directory ?? "", file.status ?? ""],
			action: { kind: "open-workspace-file", target, path: file.path },
		};
	});
}

type WorkspaceContentHit = components["schemas"]["WorkspaceContentSearchHitResponse"];
type ConversationSearchHit = components["schemas"]["ConversationSearchHitResponse"];

/** Splits daemon search text, whose matches are wrapped in \x02 and \x03, into text and spans. */
function markedSnippet(marked: string, code?: boolean): NonNullable<CommandItem["snippet"]> {
	const highlights: { start: number; end: number }[] = [];
	let text = "";
	for (const [index, part] of marked.split(/[\x02\x03]/).entries()) {
		if (index % 2 === 1) highlights.push({ start: text.length, end: text.length + part.length });
		text += part;
	}
	return { text, highlights, ...(code ? { code } : {}) };
}

/** One row per file: its best matching line, opened at that line. */
export function buildContentSearchCommands(
	target: WorkspaceFileSearchTarget,
	hits: readonly WorkspaceContentHit[],
): CommandItem[] {
	return hits.map((hit) => {
		const { basename, directory } = workspacePathParts(hit.path);
		return {
			id: `content:${target.hostId ?? "local"}:${target.sessionId}:${hit.path}`,
			group: "content",
			title: `${basename}:${hit.line}`,
			subtitle: directory,
			contentMatch: true,
			snippet: markedSnippet(hit.text, true),
			action: { kind: "open-workspace-file", target, path: hit.path, line: hit.line },
		};
	});
}

/** One row per chat message, opened scrolled to it. */
export function buildChatSearchCommands(
	hits: readonly ConversationSearchHit[],
	t: TFunction = appI18n.t,
	/** The chat's session title, used when the chat has none, and where it lives, shown beside it. */
	context?: (hit: ConversationSearchHit) => { title?: string; place?: string },
): CommandItem[] {
	return hits.map((hit) => {
		const snippet = markedSnippet(hit.text);
		const { title, place } = context?.(hit) ?? {};
		const subtitle = [place, hit.reviewId ? t("terminal.reviewer") : undefined].filter(Boolean).join(" · ");
		return {
			id: `message:${hit.messageId}`,
			group: "messages",
			title: hit.title || title || t("command.chatFallback"),
			subtitle: subtitle || undefined,
			contentMatch: true,
			snippet,
			action: {
				kind: "open-chat-message",
				...(hit.sessionId ? { sessionId: hit.sessionId } : {}),
				...(hit.projectId ? { projectId: hit.projectId } : {}),
				...(hit.reviewId ? { reviewId: hit.reviewId } : {}),
				messageId: hit.messageId,
				text: renderedSnippetText(snippet.text.replace(/^…|…$/g, "")),
			},
		};
	});
}

function sessionCommand(
	workspace: WorkspaceSummary,
	session: WorkspaceSession,
	group: SessionCommandGroup,
): CommandItem {
	return {
		id: `${SESSION_ID_PREFIX[group]}:${session.id}`,
		group,
		title: session.title,
		subtitle: workspace.name,
		zone: attentionZone(session),
		status: getSessionStatusDotView(session),
		agent: session.provider,
		keywords: [workspace.name, sessionBranchPresentation(session).branch ?? "", session.issueId ?? ""],
		action: { kind: "navigate", target: jumpTarget(workspace, session) },
		secondaryAction: { kind: "open-session-actions", sessionId: session.id },
	};
}

export function buildSessionActions(
	workspace: WorkspaceSummary,
	session: WorkspaceSession,
	t: TFunction = appI18n.t,
): CommandItem[] {
	const items: CommandItem[] = [];
	const branch = sessionBranchPresentation(session).branch;

	items.push({
		id: `session-action:jump:${session.id}`,
		group: "current",
		title: t("command.jumpToSession"),
		keywords: ["open", "go", "view", session.title],
		action: { kind: "navigate", target: jumpTarget(workspace, session) },
	});

	if (!sessionIsActive(session) && !isOrchestratorSession(session)) {
		items.push({
			id: `session-action:resume:${session.id}`,
			group: "current",
			title: t("command.resumeAgent"),
			subtitle: t("command.resumeAgentSubtitle"),
			keywords: ["restore", "restart", "retry", "resume", session.title],
			action: { kind: "resume-session", projectId: workspace.id, sessionId: session.id },
		});
	}

	if (branch && !isOrchestratorSession(session) && !isSyntheticBranch(session)) {
		items.push({
			id: `session-action:copy-branch:${session.id}`,
			group: "current",
			title: t("command.copyBranch"),
			subtitle: branch,
			keywords: ["branch", "git", branch, session.title],
			action: { kind: "copy-branch", branch },
		});
	}

	return items;
}

export function findSession(workspaces: WorkspaceSummary[], sessionId: string): WorkspaceSessionContext | undefined {
	for (const workspace of workspaces) {
		const match = workspace.sessions.find((session) => session.id === sessionId);
		if (match) return { workspace, session: match };
	}
	return undefined;
}

export function buildCommands(ctx: CommandPaletteContext, t: TFunction = appI18n.t): CommandItem[] {
	const { workspaces, currentProjectId, currentSessionId, currentHostId, restartingProjectIds, reviewStatesBySessionId } = ctx;
	const items: CommandItem[] = [];

	const currentProject = currentProjectId
		? workspaces.find((workspace) => workspace.id === currentProjectId
			&& (workspace.hostId ?? "") === (currentHostId ?? ""))
		: undefined;
	const currentSession = currentSessionId
		? currentProject?.sessions.find((session) => session.id === currentSessionId)
			?? findSession(workspaces, currentSessionId)?.session
		: undefined;
	const isProjectRestarting = Boolean(currentProject && restartingProjectIds?.has(currentProject.id));

	items.push({
		id: "current-new-task",
		group: "current",
		title: t("command.newTask"),
		emphasis: currentProject?.name,
		shortcut: "new-session",
		keywords: ["worker", "chat", "start", currentProject?.name ?? ""],
		disabled: !currentProject || isProjectRestarting,
		// Off a project the row can only explain why it is off, so it waits for a search.
		searchOnly: !currentProject,
		disabledReason: !currentProject
			? t("command.noCurrentProject")
			: isProjectRestarting
				? t("command.orchestratorRestarting")
				: undefined,
		...(currentProject ? { action: { kind: "open-new-task" as const, projectId: currentProject.id } } : {}),
	});

	if (currentProject) {
		if (currentProject.id !== STANDALONE_WORKSPACE_ID) {
			items.push({
				id: "current-open-orchestrator",
				group: "current",
				title: t("command.openOrchestrator"),
				emphasis: currentProject.name,
				shortcut: "open-orchestrator",
				keywords: ["orchestrator", "spawn", currentProject.name],
				disabled: isProjectRestarting,
				disabledReason: isProjectRestarting ? t("command.orchestratorRestarting") : undefined,
				action: { kind: "open-orchestrator", projectId: currentProject.id },
			});
			items.push({
				id: "current-project-settings",
				group: "current",
				title: t("command.projectSettings"),
				emphasis: currentProject.name,
				keywords: ["settings", "config", currentProject.name],
				action: {
					kind: "navigate",
					target: { to: "/projects/$projectId/settings", params: { projectId: currentProject.id } },
				},
			});
		}
	}

	const searchableCurrentSession = currentSession && sessionSupportsFileSearch(currentSession)
		? currentSession
		: undefined;
	const searchableProjectSessions = currentProject ? fileSearchSessions(currentProject) : [];
	if (currentProject && (searchableCurrentSession || searchableProjectSessions.length > 0)) {
		items.push({
			id: "current-search-files",
			group: "current",
			title: t("command.goToFile"),
			emphasis: currentProject.name,
			...(searchableCurrentSession ? { shortcut: "quick-open-file" as const } : {}),
			keywords: ["search files", "file", "files", "path", "workspace", "worktree", "code", searchableCurrentSession?.title ?? currentProject.name],
			action: {
				kind: "open-file-search",
				projectId: currentProject.id,
				...(searchableCurrentSession
					? { target: workspaceFileSearchTarget(currentProject, searchableCurrentSession) }
					: {}),
			},
		});
	}

	const currentBranch = sessionBranchPresentation(currentSession).branch;
	if (currentSession && currentBranch && !isOrchestratorSession(currentSession) && !isSyntheticBranch(currentSession)) {
		items.push({
			id: "current-copy-branch",
			group: "current",
			title: t("command.copyBranch"),
			subtitle: currentBranch,
			keywords: ["branch", "git", "copy", currentBranch, currentSession.title],
			action: { kind: "copy-branch", branch: currentBranch },
		});
	}

	const attentionSessions = workspaces
		.flatMap((workspace) => workerSessions(workspace.sessions).map((session) => ({ workspace, session })))
		.filter(
			({ session }) =>
				session.id !== currentSessionId &&
				sessionIsActive(session) &&
				(attentionZone(session) === "merge" || sessionNeedsAttention(session)),
		)
		.sort(
			(a, b) =>
				attentionZoneOrder.indexOf(attentionZone(a.session)) - attentionZoneOrder.indexOf(attentionZone(b.session)),
		);

	const attentionIds = new Set(attentionSessions.map(({ session }) => session.id));

	for (const { workspace, session } of attentionSessions) {
		items.push(sessionCommand(workspace, session, "attention"));
	}

	for (const workspace of workspaces.filter((candidate) => candidate.id !== STANDALONE_WORKSPACE_ID)) {
		items.push({
			id: `project:${workspace.id}`,
			group: "projects",
			title: workspace.name,
			keywords: [workspace.path],
			action: { kind: "navigate", target: { to: "/projects/$projectId", params: { projectId: workspace.id } } },
		});
	}

	for (const workspace of workspaces) {
		for (const session of workerSessions(workspace.sessions).filter(
			(session) => !attentionIds.has(session.id) && session.id !== currentSessionId,
		)) {
			items.push({ ...sessionCommand(workspace, session, "sessions"), searchOnly: !sessionIsActive(session) });
		}
	}

	for (const workspace of workspaces) {
		for (const session of workerSessions(workspace.sessions)) {
			const sessionReviewStates = reviewStatesBySessionId?.[session.id];
			// Whole prop omitted (e.g. a test that doesn't care about review state) means
			// "assume eligible"; a defined map with this session's key absent means the
			// live app hasn't loaded this session's review state yet — don't expose a
			// mutating action until we know it's safe to trigger.
			const dataLoadedForSession = reviewStatesBySessionId === undefined || sessionReviewStates !== undefined;
			const openReviewStates = sessionReviewStates ? openReviewStatesFor(session, sessionReviewStates) : undefined;
			const sessionReviewRunning = openReviewStates ? reviewIsRunning(openReviewStates) : false;
			for (const pr of openPRs(session)) {
				const subtitle = `${session.title} · ${workspace.name}`;
				const prKeywords = [
					`#${pr.number}`,
					String(pr.number),
					pr.url,
					session.title,
					sessionBranchPresentation(session).branch ?? "",
					workspace.name,
					pr.state,
				];
			items.push({
					id: `pr:${session.id}:${pr.number}`,
					group: "prs",
					title: `#${pr.number}`,
					subtitle,
					keywords: prKeywords,
					action: { kind: "navigate", target: jumpTarget(workspace, session) },
				});
				items.push({
					id: `pr-open:${session.id}:${pr.number}`,
					group: "prs",
					title: t("command.openPr", { number: pr.number }),
					subtitle,
					keywords: [...prKeywords, "open", "open pr", "github", "browser"],
					searchOnly: true,
					action: { kind: "open-pr", url: pr.url },
				});
				items.push({
					id: `pr-copy:${session.id}:${pr.number}`,
					group: "prs",
					title: t("command.copyPrUrl", { number: pr.number }),
					subtitle,
					keywords: [...prKeywords, "copy", "copy pr", "url", "link", "share"],
					searchOnly: true,
					action: { kind: "copy-pr-url", url: pr.url },
				});
				if (sessionIsActive(session) && dataLoadedForSession) {
					items.push(prReviewCommand(session, pr, openReviewStates, sessionReviewRunning, subtitle, prKeywords, t));
				}
			}
		}
	}

	items.push({
		id: "global-home",
		group: "global",
		title: t("shell.goHome"),
		keywords: ["home", "start", "get started", "clone", "import", "standalone"],
		action: { kind: "navigate", target: { to: "/" } },
	});
	items.push({
		id: "global-new-project",
		group: "global",
		title: t("command.newProject"),
		keywords: ["add", "import", "repo", "workspace"],
		action: { kind: "open-new-project" },
	});
	items.push({
		id: "global-settings",
		group: "global",
		title: t("command.globalSettings"),
		shortcut: "open-settings",
		keywords: ["settings", "preferences", "config"],
		action: { kind: "navigate", target: { to: "/settings" } },
	});
	items.push({
		id: "global-theme",
		group: "global",
		title: t("command.toggleTheme"),
		keywords: ["dark", "light", "appearance"],
		action: { kind: "toggle-theme" },
	});
	if (ctx.cloudReady) {
		items.push({
			id: "global-open-shared-link",
			group: "global",
			title: t("command.openSharedLink"),
			keywords: ["share", "shared", "link", "invite", "cloud"],
			action: { kind: "open-shared-link" },
		});
	}

	return items;
}

/**
 * Per-PR review command. The trigger endpoint is session-scoped (one run covers
 * all of the session's eligible open PRs, same as the Reviews tab button), so
 * the disabled state mirrors the shared ReviewPanel eligibility logic — both
 * sides read it from `lib/session-reviews`, so the two can't drift. Callers only
 * reach here once the session's review state is known; until then the row is
 * omitted rather than shown with a guessed enabled state.
 */
function prReviewCommand(
	session: WorkspaceSession,
	pr: PullRequestFacts,
	openReviewStates: PRReviewState[] | undefined,
	sessionReviewRunning: boolean,
	subtitle: string,
	keywords: string[],
	t: TFunction,
): CommandItem {
	const prReviewState = openReviewStates?.find((reviewState) => reviewState.prUrl === pr.url);
	const ineligible = openReviewStates !== undefined && (!prReviewState || prReviewState.status === "ineligible");
	const disabled = sessionReviewRunning || ineligible;
	const disabledReason = sessionReviewRunning
		? t("command.reviewAlreadyRunning")
		: ineligible
			? t("command.notEligibleForReview")
			: undefined;
	const runLabel = prReviewState
		? reviewSessionRunAction([prReviewState], false)
		: t("inspector.review.run");
	return {
		id: `pr-review:${session.id}:${pr.number}`,
		group: "prs",
		title: t("command.reviewPr", { action: runLabel, number: pr.number }),
		subtitle,
		keywords: [...keywords, "review", "run review", "re-run review", "ao review"],
		searchOnly: true,
		disabled,
		disabledReason,
		action: { kind: "trigger-review", sessionId: session.id },
	};
}

function isSubsequence(query: string, haystack: string): boolean {
	let i = 0;
	for (let j = 0; j < haystack.length && i < query.length; j++) {
		if (haystack[j] === query[i]) i++;
	}
	return i === query.length;
}

/**
 * Content rows rank below any name that holds every typed word, so "hide
 * sidebar" runs Toggle sidebar instead of opening a file that mentions it, and
 * above loose fuzzy name matches. Among content rows, the share of typed words
 * the snippet marks puts an exact chat sentence above a file matching one word.
 */
function contentMatchScore(q: string, item: CommandItem): number {
	const words = q.split(/\s+/).filter((word) => word.length > 1);
	const snippet = item.snippet;
	if (!snippet || words.length === 0) return 200;
	const marked = snippet.highlights.map(({ start, end }) => snippet.text.slice(start, end).toLowerCase());
	const covered = words.filter((word) => marked.some((span) => span.includes(word) || word.startsWith(span))).length;
	return 200 + Math.round((60 * covered) / words.length);
}

/** A typed word of 4+ letters missing at most two letters of a word in text: "sidbar" for "sidebar". */
function nearMissIn(word: string, text: string): boolean {
	if (word.length < 4) return false;
	return text.split(/[^\p{L}\p{N}]+/u).some((candidate) =>
		candidate[0] === word[0] && candidate.length >= word.length && candidate.length - word.length <= 2 && isSubsequence(word, candidate));
}

export function matchScore(query: string, item: CommandItem): number {
	const q = query.trim().toLowerCase();
	if (!q) return 1;
	if (item.contentMatch) return contentMatchScore(q, item);
	const title = item.title.toLowerCase();
	const extras = [item.subtitle ?? "", ...(item.keywords ?? [])].join(" ").toLowerCase();

	const titleIdx = title.indexOf(q);
	if (item.group === "files") {
		if (title === q) return 1200;
		if (titleIdx === 0) return 1100;
		if (titleIdx > 0) return 900 - titleIdx;
	}
	if (titleIdx === 0) return 1000;
	if (titleIdx > 0) return 800 - titleIdx;
	if (extras.includes(q)) return 500;
	// Every word somewhere, in any order: "add codex account", "hide sidebar",
	// "toggle sidbar".
	const words = q.split(/\s+/);
	const inTitle = (word: string) => title.includes(word) || nearMissIn(word, title);
	if (words.length > 1 && words.every((word) => inTitle(word) || extras.includes(word) || nearMissIn(word, extras))) {
		return 300 + Math.round((100 * words.filter(inTitle).length) / words.length);
	}
	if (isSubsequence(q, title)) return 200;
	// Loose letters scattered across every keyword match almost anything ("side"
	// in "subscriptions"), so only a short query may match them.
	if (q.length <= 2 && isSubsequence(q, extras)) return 100;
	return 0;
}

function isAttentionItem(item: CommandItem): boolean {
	return item.zone === "action" || item.zone === "merge";
}

function attentionRank(item: CommandItem): number {
	return item.zone ? attentionZoneOrder.indexOf(item.zone) : attentionZoneOrder.length;
}

export function filterCommands(items: CommandItem[], query: string): CommandItem[] {
	if (!query.trim()) return items.filter((item) => !item.searchOnly);
	return items
		.map((item, index) => ({ item, index, score: matchScore(query, item) }))
		.filter((entry) => entry.score > 0)
		.sort((a, b) => b.score - a.score || attentionRank(a.item) - attentionRank(b.item) || a.index - b.index)
		.map((entry) => entry.item);
}

export const MAX_ITEMS_PER_GROUP = 20;

export const MAX_SEARCH_RESULTS = 20;

export function groupCommands(
	items: CommandItem[],
	t: TFunction = appI18n.t,
	rowsPerGroup: number = MAX_ITEMS_PER_GROUP,
): { id: CommandGroupId; label: string; items: CommandItem[] }[] {
	return commandGroupOrder
		.map((id) => ({
			id,
			label: t(commandGroupLabelKeys[id]),
			items: items.filter((item) => item.group === id).slice(0, rowsPerGroup),
		}))
		.filter((group) => group.items.length > 0);
}

export type DisplayGroup = { id: string; label: string; items: CommandItem[] };

export const MAX_ATTENTION_SEARCH_RESULTS = Math.floor(MAX_SEARCH_RESULTS / 2);

export function visibleForQuery(items: CommandItem[], query: string): CommandItem[] {
	const ranked = filterCommands(items, query);
	if (!query.trim()) return ranked;

	const attentionRanked = ranked.filter(isAttentionItem);
	const nonAttentionRanked = ranked.filter((item) => !isAttentionItem(item));

	const attention = attentionRanked.slice(0, MAX_ATTENTION_SEARCH_RESULTS);
	const attentionIds = new Set(attention.map((item) => item.id));
	const rest = nonAttentionRanked.slice(0, MAX_SEARCH_RESULTS - attention.length);

	const backfillBudget = MAX_SEARCH_RESULTS - attention.length - rest.length;
	const backfill =
		backfillBudget > 0 ? attentionRanked.filter((item) => !attentionIds.has(item.id)).slice(0, backfillBudget) : [];

	return [...attention, ...rest, ...backfill];
}

/**
 * Groups the daemon fills (file names, file contents, chats). They arrive after
 * the local rows, so they always sit below them: what Enter opens does not
 * change while a search is still answering.
 */
const SEARCH_GROUPS: ReadonlySet<CommandGroupId> = new Set(["files", "content", "messages"]);

export function isSearchGroup(group: CommandGroupId): boolean {
	return SEARCH_GROUPS.has(group);
}

/** Rows each search group shows beside commands; the file view shows them all. */
export const SEARCH_GROUP_ROWS = 6;

export function displayGroups(
	items: CommandItem[],
	query: string,
	t: TFunction = appI18n.t,
	searchGroupRows: number = SEARCH_GROUP_ROWS,
): DisplayGroup[] {
	const local = items.filter((item) => !SEARCH_GROUPS.has(item.group));
	// The daemon already matched these, so rank them without dropping any.
	const searched = items
		.filter((item) => SEARCH_GROUPS.has(item.group))
		.map((item, index) => ({ item, index, score: matchScore(query, item) }))
		.sort((a, b) => b.score - a.score || a.index - b.index)
		.map((entry) => entry.item);
	const searchGroups = groupCommands(searched, t, searchGroupRows);
	// Keep matches under their category headings (Cursor-style), including while typing.
	const groups = groupCommands(visibleForQuery(local, query), t);
	if (!query.trim()) return [...groups, ...searchGroups];
	// The palette runs cmdk with shouldFilter:false and selects the first item in DOM
	// order, so Enter follows category order. Rank categories by their best match to
	// keep the highest-scoring hit — and therefore the Enter target — first.
	const ranked = groups
		.map((group, index) => ({
			group,
			index,
			score: Math.max(...group.items.map((item) => matchScore(query, item))),
		}))
		.sort((a, b) => b.score - a.score || a.index - b.index)
		.map((entry) => entry.group);
	return [...ranked, ...searchGroups];
}

/**
 * The spans of a row title the query matched, for drawing them brighter: the
 * typed text where it appears whole, else each typed word, else the letters a
 * loose match picked in order.
 */
export function titleHighlights(query: string, title: string): { start: number; end: number }[] {
	const q = query.trim().toLowerCase();
	if (!q) return [];
	const lower = title.toLowerCase();
	const whole = lower.indexOf(q);
	if (whole >= 0) return [{ start: whole, end: whole + q.length }];
	const spans: { start: number; end: number }[] = [];
	for (const word of q.split(/\s+/)) {
		const at = lower.indexOf(word);
		if (at >= 0) spans.push({ start: at, end: at + word.length });
	}
	if (spans.length > 0) return mergeSpans(spans);
	let at = 0;
	for (const char of q) {
		if (char === " ") continue;
		const found = lower.indexOf(char, at);
		if (found < 0) return [];
		spans.push({ start: found, end: found + 1 });
		at = found + 1;
	}
	return mergeSpans(spans);
}

function mergeSpans(spans: { start: number; end: number }[]): { start: number; end: number }[] {
	const sorted = [...spans].sort((a, b) => a.start - b.start);
	const merged: { start: number; end: number }[] = [];
	for (const span of sorted) {
		const last = merged[merged.length - 1];
		if (last && span.start <= last.end) last.end = Math.max(last.end, span.end);
		else merged.push({ ...span });
	}
	return merged;
}
