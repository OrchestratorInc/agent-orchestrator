import { sessionBranchPresentation } from "./session-branch";
import type { TFunction } from "i18next";
import {
	attentionZone,
	attentionZoneOrder,
	isOrchestratorSession,
	openPRs,
	sessionIsActive,
	workerSessions,
	type AgentProvider,
	type AttentionZone,
	type PullRequestFacts,
	type WorkspaceSession,
	type WorkspaceSummary,
	CLOUD_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";
import {
	openReviewStatesFor,
	reviewIsRunning,
	reviewSessionRunAction,
	type PRReviewState,
} from "./session-reviews";
import { appI18n, type MessageKey } from "../i18n";
import { isArchivedSession } from "./standalone-archive";
import type { components } from "../../api/schema";
import type { AppShortcutId } from "../../shared/shortcuts";
import type { GlobalSettingsSection } from "../stores/ui-store";
import { renderedSnippetText } from "./chat-annotation-navigation";
import { getSessionRowMarker, getSessionStatusDotView } from "./session-presentation";
import { importableProject } from "./session-import";
import type { ReactNode } from "react";
import type { MenuEntry } from "../components/menu-model";

export type CommandGroupId =
	| "session"
	| "current"
	| "attention"
	| "files"
	| "content"
	| "messages"
	| "projects"
	| "sessions"
	| "prs"
	| "settings"
	| "global"
	/** Session-text matches from the local search index. */
	| "search";

export type NavigateTarget =
	| { to: "/" }
	| { to: "/settings" }
	| { to: "/usage" }
	| { to: "/automations" }
	| { to: "/artifacts" }
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
	| { kind: "open-session-import"; projectId?: string }
	| { kind: "open-orchestrator"; projectId: string }
	/** No project: every project's archived sessions. */
	| { kind: "open-archive"; projectId?: string }
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
	| { kind: "open-shared-link" }
	| { kind: "report-problem" }
	/** Runs a menu's own handler, as its context menu would. */
	| { kind: "run"; run: () => void }
	/** Opens one of the palette's nested lists. */
	| { kind: "open-view"; view: PaletteMenuView }
	/**
	 * Opens a past session at the message a search matched, with the match
	 * carried out-of-band so the Chat timeline can scroll to it.
	 */
	| {
			kind: "open-search-result";
			projectId: string;
			sessionId: string;
			messageId: string;
			snippet: string;
			/** What matched (user, assistant, tool, command, path, title). */
			matchKind?: string;
			/** Set when the match is in one of the session's reviewer chats. */
			reviewId?: string;
			/** Matched spans in the snippet; the first is what the message highlights. */
			highlights?: ReadonlyArray<{ start: number; end: number }>;
			/** The matched row's conversation sequence; a tool or command match opens at it. */
			seq?: number;
	  }
	/** Copies the command that reopens a session from a terminal. */
	| { kind: "copy-resume-command"; command: string };

/**
 * The palette's nested lists beyond a session's actions and file search: a
 * project's actions, a session menu's submenu ("Open in"), a choice list
 * (Theme, Color theme, Language, ...) and renaming a session in the field.
 */
export type PaletteMenuView =
	| { mode: "project-actions"; projectId: string }
	| { mode: "menu-submenu"; sessionId: string; entryId: string; title: string }
	| { mode: "choices"; menu: string; title: string }
	| { mode: "rename-session"; sessionId: string }
	| { mode: "go-to"; target: "projects" | "sessions"; title: string };

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
	/** A menu entry's own icon, drawn in place of the group's. */
	icon?: ReactNode;
	/** A choice's current state: a check, or its value at the end of the row. */
	checked?: boolean;
	state?: string;
	/** A switch row: drawn as an on/off switch at the end of the row. */
	toggled?: boolean;
	/** The palette stays open after it runs, to show the new state (choices, toggles). */
	keepOpen?: boolean;
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
	developerMode?: boolean;
};

// Sessions sit above Projects, as in the sidebar. The palette places
// session-text matches itself (see CommandPalette's groups).
export const commandGroupOrder: CommandGroupId[] = ["session", "current", "attention", "search", "files", "content", "messages", "sessions", "projects", "prs", "settings", "global"];

const commandGroupLabelKeys: Record<CommandGroupId, MessageKey> = {
	// The palette draws this heading as the session itself.
	session: "command.sessionFallback",
	search: "command.sessionText",
	current: "command.group.current",
	attention: "command.group.attention",
	files: "command.group.files",
	content: "command.group.content",
	messages: "command.group.messages",
	projects: "command.group.projects",
	sessions: "command.group.sessions",
	prs: "command.group.prs",
	settings: "command.group.settings",
	// The app's own name for app-wide commands (the shortcut catalog's General).
	global: "shortcut.category.general",
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
	// One row per chat: its best match (the daemon ranks hits), with how many
	// more of its messages matched, rather than a row per message.
	const chats = new Map<string, { hit: ConversationSearchHit; count: number }>();
	for (const hit of hits) {
		const key = hit.reviewId ? `review:${hit.reviewId}` : `session:${hit.sessionId}`;
		const chat = chats.get(key);
		if (chat) chat.count += 1;
		else chats.set(key, { hit, count: 1 });
	}
	return [...chats.values()].map(({ hit, count }) => {
		// Line breaks and runs of spaces read as one line in a row.
		const snippet = markedSnippet(hit.text.replace(/\s+/g, " ").trim());
		const { title, place } = context?.(hit) ?? {};
		const subtitle = [place, hit.reviewId ? t("terminal.reviewer") : undefined].filter(Boolean).join(" · ");
		return {
			id: `message:${hit.messageId}`,
			group: "messages",
			title: hit.title || title || t("command.chatFallback"),
			subtitle: subtitle || undefined,
			...(count > 1 ? { state: t("command.matchCount", { count }) } : {}),
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

/** Where a row lives: its project, marked Cloud when it does not run on this machine. */
function workspacePlace(workspace: WorkspaceSummary, t: TFunction): string {
	return workspace.kind === CLOUD_PROJECT_KIND ? `${workspace.name} · ${t("shell.cloudProjectBadge")}` : workspace.name;
}

/** 0 when the sidebar flags a session red (it needs you), 1 for a review turn, -1 otherwise. */
function attentionMarkerRank(session: WorkspaceSession): number {
	const marker = getSessionRowMarker(session);
	return marker === "attention" ? 0 : marker === "review" ? 1 : -1;
}

/** When the person last worked in a session, as the sidebar's age shows it. */
function lastInteraction(session: WorkspaceSession): number {
	const at = session.lastInteractionAt ?? session.lastUserMessageAt;
	return at ? Date.parse(at) || 0 : 0;
}

function sessionCommand(
	workspace: WorkspaceSummary,
	session: WorkspaceSession,
	group: SessionCommandGroup,
	t: TFunction,
): CommandItem {
	return {
		id: `${SESSION_ID_PREFIX[group]}:${session.id}`,
		group,
		title: session.title,
		subtitle: workspacePlace(workspace, t),
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

/**
 * A context menu's entries as palette rows, so a session's actions here are
 * the ones its right-click menu offers. A submenu (Open in) becomes a row that
 * opens it as a nested list; separators and custom rows have no row.
 */
export function menuEntryCommands(entries: readonly MenuEntry[], sessionId: string): CommandItem[] {
	return entries.flatMap((entry): CommandItem[] => {
		if (entry.kind === "item") {
			return [{
				id: `menu:${entry.id}`,
				group: "current",
				title: entry.label,
				icon: entry.icon,
				shortcut: entry.shortcutId,
				disabled: entry.disabled || !entry.onSelect,
				...(entry.onSelect ? { action: { kind: "run" as const, run: entry.onSelect } } : {}),
			}];
		}
		if (entry.kind === "submenu") {
			return [{
				id: `menu:${entry.id}`,
				group: "current",
				title: entry.label,
				icon: entry.icon,
				action: { kind: "open-view", view: { mode: "menu-submenu", sessionId, entryId: entry.id, title: entry.label } },
			}];
		}
		return [];
	});
}

/** A submenu's entries by id, searched through nested submenus. */
export function findSubmenuEntries(entries: readonly MenuEntry[], entryId: string): MenuEntry[] | undefined {
	for (const entry of entries) {
		if (entry.kind !== "submenu") continue;
		if (entry.id === entryId) return entry.items;
		const nested = findSubmenuEntries(entry.items, entryId);
		if (nested) return nested;
	}
	return undefined;
}

export type ProjectActionContext = {
	isPinned: boolean;
	canPin: boolean;
	restarting: boolean;
	hasOrchestrator: boolean;
	setPinned: (pinned: boolean) => void;
	copyPath: () => void;
	remove?: () => void;
};

/** A project's actions, as its sidebar ⋮ and right-click menu offer them. */
export function buildProjectActions(workspace: WorkspaceSummary, ctx: ProjectActionContext, t: TFunction = appI18n.t): CommandItem[] {
	const isStandalone = workspace.id === STANDALONE_WORKSPACE_ID;
	const isAgent = Boolean(workspace.standaloneAgent);
	const archivedCount = isStandalone ? 0 : workerSessions(workspace.sessions).filter(isArchivedSession).length;
	const items: CommandItem[] = [];
	const row = (id: string, title: string, action: CommandAction, extra: Partial<CommandItem> = {}) =>
		items.push({ id: `project-action:${id}`, group: "current", title, action, ...extra });
	if (!isAgent) {
		row("new-task", t("shell.newTask"), { kind: "open-new-task", projectId: workspace.id }, { disabled: ctx.restarting, shortcut: "new-session" });
	}
	if (!isStandalone && !isAgent && (!workspace.shared || ctx.hasOrchestrator)) {
		row("orchestrator", ctx.hasOrchestrator ? t("shell.openOrchestrator") : t("shell.spawnOrchestratorLower"), { kind: "open-orchestrator", projectId: workspace.id }, { disabled: ctx.restarting });
	}
	if (!isStandalone && !isAgent) {
		row("board", t("shell.openDashboard"), { kind: "navigate", target: { to: "/projects/$projectId", params: { projectId: workspace.id } } });
	}
	if (archivedCount > 0) {
		row("archive", t("shell.archivedSessions"), { kind: "open-archive", projectId: workspace.id }, { state: String(archivedCount) });
	}
	if (workspace.path && !workspace.hostId) row("copy-path", t("editor.copyPath"), { kind: "run", run: ctx.copyPath });
	if (ctx.canPin) {
		row("pin", ctx.isPinned ? t("shell.unpinProject") : t("shell.pinProject"), { kind: "run", run: () => ctx.setPinned(!ctx.isPinned) });
	}
	if (!isStandalone && !isAgent) {
		row("settings", t("shell.projectSettings"), { kind: "navigate", target: { to: "/projects/$projectId/settings", params: { projectId: workspace.id } } });
		if (ctx.remove) row("remove", t("shell.removeProjectTitle"), { kind: "run", run: ctx.remove });
	}
	return items;
}

/** The project or session rows a Go to list shows, out of the palette's root rows. */
export function goToCommands(items: readonly CommandItem[], target: "projects" | "sessions"): CommandItem[] {
	return items.filter((item) => (target === "projects" ? item.group === "projects" : item.group === "attention" || item.group === "sessions"));
}

export function findSession(workspaces: WorkspaceSummary[], sessionId: string): WorkspaceSessionContext | undefined {
	for (const workspace of workspaces) {
		const match = workspace.sessions.find((session) => session.id === sessionId);
		if (match) return { workspace, session: match };
	}
	return undefined;
}

const IMPORT_SESSION_KEYWORDS = ["import", "chats", "sessions", "conversations", "history", "existing", "resume", "continue", "claude code", "codex", "terminal"];

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
		shortcut: "new-session",
		keywords: ["new chat", "new session", "worker", "chat", "start", "agent", currentProject?.name ?? ""],
		disabled: isProjectRestarting,
		disabledReason: isProjectRestarting ? t("command.orchestratorRestarting") : undefined,
		// Off a project it does what ⌘N does there (the last project, else Sessions).
		action: currentProject
			? { kind: "open-new-task", projectId: currentProject.id }
			: { kind: "run-shortcut", shortcut: "new-session" },
	});

	if (currentProject) {
		if (importableProject(currentProject)) {
			items.push({
				id: "current-import-sessions",
				group: "current",
				title: t("sessionImport.menuItem"),
				keywords: [...IMPORT_SESSION_KEYWORDS, currentProject.name],
				action: { kind: "open-session-import", projectId: currentProject.id },
			});
		}
		// A Cloud standalone agent has no orchestrator either.
		if (currentProject.id !== STANDALONE_WORKSPACE_ID && !currentProject.standaloneAgent) {
			items.push({
				id: "current-open-orchestrator",
				group: "current",
				title: t("command.openOrchestrator"),
				shortcut: "open-orchestrator",
				keywords: ["orchestrator", "spawn", currentProject.name],
				disabled: isProjectRestarting,
				disabledReason: isProjectRestarting ? t("command.orchestratorRestarting") : undefined,
				action: { kind: "open-orchestrator", projectId: currentProject.id },
			});
			const archivedCount = workerSessions(currentProject.sessions).filter(isArchivedSession).length;
			if (archivedCount > 0) {
				items.push({
					id: "current-open-archive",
					group: "current",
					title: t("command.archivedSessions"),
					subtitle: t("command.archivedCount", { count: archivedCount }),
					keywords: ["archive", "archived", "ended", "terminated", "restore", "history", currentProject.name],
					action: { kind: "open-archive", projectId: currentProject.id },
				});
			}
			items.push({
				id: "current-project-settings",
				group: "current",
				title: t("command.projectSettings"),
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
			group: "session",
			title: t("command.copyBranch"),
			subtitle: currentBranch,
			keywords: ["branch", "git", "copy", currentBranch, currentSession.title],
			action: { kind: "copy-branch", branch: currentBranch },
		});
	}

	// The sessions the sidebar flags with an alert: first those that need the
	// person now (input, CI, conflicts, changes requested), then their review
	// turn, each most recent first. A PR ready to merge waits on nobody.
	const attentionSessions = workspaces
		.flatMap((workspace) => workerSessions(workspace.sessions).map((session) => ({ workspace, session })))
		.filter(({ session }) => session.id !== currentSessionId && sessionIsActive(session))
		.map((entry) => ({ ...entry, rank: attentionMarkerRank(entry.session), at: lastInteraction(entry.session) }))
		.filter((entry) => entry.rank >= 0)
		.sort((a, b) => a.rank - b.rank || b.at - a.at);

	const attentionIds = new Set(attentionSessions.map(({ session }) => session.id));

	for (const { workspace, session } of attentionSessions) {
		items.push(sessionCommand(workspace, session, "attention", t));
	}

	// Cloud standalone agents belong to Sessions, not projects; their sessions are listed.
	for (const workspace of workspaces.filter((candidate) => candidate.id !== STANDALONE_WORKSPACE_ID && !candidate.standaloneAgent)) {
		items.push({
			id: `project:${workspace.id}`,
			group: "projects",
			title: workspace.name,
			...(workspace.kind === CLOUD_PROJECT_KIND ? { subtitle: t("shell.cloudProjectBadge") } : {}),
			keywords: [workspace.path],
			action: { kind: "navigate", target: { to: "/projects/$projectId", params: { projectId: workspace.id } } },
			secondaryAction: { kind: "open-view", view: { mode: "project-actions", projectId: workspace.id } },
		});
	}

	// Most recently worked in first, so the blank palette's few rows are the
	// sessions the person is likely to go back to. Stable for equal times.
	const sessionRows = workspaces
		.flatMap((workspace) => workerSessions(workspace.sessions)
			.filter((session) => !attentionIds.has(session.id) && session.id !== currentSessionId)
			.map((session) => ({ workspace, session })))
		.map((entry, index) => ({ ...entry, index, at: lastInteraction(entry.session) }))
		.sort((a, b) => b.at - a.at || a.index - b.index);
	for (const { workspace, session } of sessionRows) {
		items.push({ ...sessionCommand(workspace, session, "sessions", t), searchOnly: !sessionIsActive(session) });
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
					keywords: [...prKeywords, "pr", "pull request"],
					// The bare number means little until someone searches for it.
					searchOnly: true,
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
		keywords: ["home", "start", "get started", "clone"],
		action: { kind: "navigate", target: { to: "/" } },
	});
	// Projects and most sessions wait for a search; these list them all.
	items.push({
		id: "global-go-to-project",
		group: "global",
		title: t("command.goToProject"),
		keywords: ["projects", "repos", "open project", "switch project"],
		action: { kind: "open-view", view: { mode: "go-to", target: "projects", title: t("command.goToProject") } },
	});
	items.push({
		id: "global-go-to-session",
		group: "global",
		title: t("command.goToSession"),
		keywords: ["sessions", "chats", "agents", "open session", "switch session"],
		action: { kind: "open-view", view: { mode: "go-to", target: "sessions", title: t("command.goToSession") } },
	});
	items.push({
		id: "global-automations",
		group: "global",
		title: t("automations.title"),
		keywords: ["schedule", "scheduled", "cron", "recurring"],
		action: { kind: "navigate", target: { to: "/automations" } },
	});
	items.push({
		id: "global-artifacts",
		group: "global",
		title: t("artifacts.title"),
		keywords: ["deliverables", "outputs", "documents"],
		action: { kind: "navigate", target: { to: "/artifacts" } },
	});
	const archivedTotal = workspaces.reduce((sum, workspace) => sum + workerSessions(workspace.sessions).filter(isArchivedSession).length, 0);
	if (archivedTotal > 0) {
		items.push({
			id: "global-archived-sessions",
			group: "global",
			title: t("command.archivedSessions"),
			subtitle: t("command.archivedCount", { count: archivedTotal }),
			keywords: ["archive", "archived", "ended", "terminated", "restore", "history"],
			action: { kind: "open-archive" },
		});
	}
	items.push({
		id: "global-new-project",
		group: "global",
		title: t("command.newProject"),
		keywords: ["add", "import", "repo", "workspace"],
		action: { kind: "open-new-project" },
	});
	items.push({
		id: "global-import-sessions",
		group: "global",
		title: t("sessionImport.foldersTitle"),
		subtitle: t("settings.harness.thisComputer"),
		keywords: [...IMPORT_SESSION_KEYWORDS, "folders", "projects"],
		// A project's own Import chats leads the blank palette; this waits for a search.
		searchOnly: true,
		action: { kind: "open-session-import" },
	});
	if (ctx.developerMode) {
		items.push({
			id: "global-usage",
			group: "global",
			title: t("usage.title"),
			keywords: ["usage", "cost", "tokens", "limits", "spend", "billing", "quota"],
			action: { kind: "navigate", target: { to: "/usage" } },
		});
	}
	items.push({
		id: "global-settings",
		group: "global",
		title: t("shell.settings"),
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
	items.push({
		id: "global-report-problem",
		group: "global",
		title: t("report.action"),
		keywords: ["bug", "report", "issue", "triage", "feedback"],
		action: { kind: "report-problem" },
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
	return wordsOf(text).some((candidate) =>
		candidate[0] === word[0] && candidate.length >= word.length && candidate.length - word.length <= 2 && isSubsequence(word, candidate));
}

function wordsOf(text: string): string[] {
	return text.split(/[^\p{L}\p{N}]+/u);
}

/** Where `q` starts a word of `text` (at its start or after a non-letter), or -1. */
function wordStartIndex(text: string, q: string): number {
	let at = text.indexOf(q);
	while (at > 0 && /[\p{L}\p{N}]/u.test(text[at - 1] ?? "")) at = text.indexOf(q, at + 1);
	return at;
}

/** Loose letters in order, starting at the start of one of the title's words. */
function wordAnchoredSubsequence(q: string, title: string): boolean {
	for (let at = 0; at < title.length; at++) {
		const wordStart = at === 0 || !/[\p{L}\p{N}]/u.test(title[at - 1] ?? "");
		if (wordStart && title[at] === q[0] && isSubsequence(q, title.slice(at))) return true;
	}
	return false;
}

/**
 * How well a row matches the query, 0 for no match. In order: the row's exact
 * name; its name starting with the query; a word of its name starting with it;
 * one of its keywords exactly; a whole word, then a word start, of its
 * keywords or subtitle; every typed word somewhere; loose letters of its name.
 * Keywords never match inside a word, so "bug" finds Report a problem but not
 * Developer mode's "debug", and "keys" no keyword "monkeys".
 */
export function matchScore(query: string, item: CommandItem): number {
	const q = query.trim().toLowerCase();
	if (!q) return 1;
	if (item.contentMatch) return contentMatchScore(q, item);
	const title = item.title.toLowerCase();
	const extras = [item.subtitle ?? "", ...(item.keywords ?? [])].join(" ").toLowerCase();

	if (item.group === "files") {
		const titleIdx = title.indexOf(q);
		if (title === q) return 1200;
		if (titleIdx === 0) return 1100;
		if (titleIdx > 0) return 900 - titleIdx;
	}
	// A session, project or command named exactly what was typed comes first.
	if (title === q) return 1100;
	if (title.startsWith(q)) return 1000;
	const wordAt = wordStartIndex(title, q);
	if (wordAt > 0) return 900 - wordAt;
	if (item.keywords?.some((keyword) => keyword.toLowerCase() === q)) return 650;
	const extraWords = wordsOf(extras);
	if (extraWords.includes(q)) return 600;
	if (wordStartIndex(extras, q) >= 0) return 500;
	const titleIdx = title.indexOf(q);
	if (titleIdx > 0) return 400 - Math.min(titleIdx, 50);
	// Every word somewhere, in any order: "add codex account", "hide sidebar",
	// "toggle sidbar".
	const words = q.split(/\s+/);
	const inTitle = (word: string) => title.includes(word) || nearMissIn(word, title);
	if (words.length > 1 && words.every((word) => inTitle(word) || wordStartIndex(extras, word) >= 0 || nearMissIn(word, extras))) {
		return 300 + Math.round((100 * words.filter(inTitle).length) / words.length);
	}
	if (wordAnchoredSubsequence(q, title)) return 200;
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

/**
 * Rows the blank palette lists per navigation group. Sessions and projects
 * wait for a search (or Go to session / Go to project), since the sidebar
 * already lists them; only the sessions that need attention lead.
 */
export const BLANK_GROUP_ROWS: Partial<Record<CommandGroupId, number>> = { attention: 5, sessions: 0, projects: 0 };

function capBlankGroups(items: CommandItem[]): CommandItem[] {
	const seen = new Map<CommandGroupId, number>();
	return items.filter((item) => {
		const cap = BLANK_GROUP_ROWS[item.group];
		if (cap === undefined) return true;
		const count = seen.get(item.group) ?? 0;
		seen.set(item.group, count + 1);
		return count < cap;
	});
}

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
	if (!query.trim()) return [...groupCommands(capBlankGroups(visibleForQuery(local, query)), t), ...searchGroups];
	// A search lists actions and general commands first, then sessions, then
	// projects and PRs. The palette highlights the best match wherever it sits.
	const groups = groupCommands(visibleForQuery(local, query), t)
		.sort((a, b) => searchGroupRank(a.id) - searchGroupRank(b.id));
	return [...groups, ...searchGroups];
}

const SEARCH_GROUP_ORDER: CommandGroupId[] = ["session", "current", "global", "settings", "attention", "sessions", "projects", "prs"];

function searchGroupRank(id: string): number {
	const rank = SEARCH_GROUP_ORDER.indexOf(id as CommandGroupId);
	return rank === -1 ? SEARCH_GROUP_ORDER.length : rank;
}

/** The row Enter should run for a search: the best match, first on a tie. */
export function bestMatch(items: readonly CommandItem[], query: string): CommandItem | undefined {
	let best: CommandItem | undefined;
	let bestScore = 0;
	for (const item of items) {
		const score = matchScore(query, item);
		if (score > bestScore) {
			best = item;
			bestScore = score;
		}
	}
	return best;
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

/**
 * One session-text search hit as the palette renders it.
 *
 * The palette owns presentation (title, subtitle, snippet segments); the search
 * hook owns transport. Keeping the mapping here means the palette's command
 * model stays testable without a query layer.
 */
export type SessionSearchCommandHit = {
	sessionId: string;
	projectId: string;
	messageId?: string;
	/** The matched row's conversation sequence. */
	seq?: number;
	kind: string;
	title?: string;
	snippet: string;
	highlights?: ReadonlyArray<{ start: number; end: number }>;
	sessionName?: string;
	projectName?: string;
	harness?: string;
	sessionState?: string;
	/** The command that reopens the session from a terminal, as the daemon names it. */
	resumeCommand?: string;
	createdAt: string;
	/** How many messages in the session matched; the row is the best of them. */
	sessionMatches?: number;
	/** Set when the best match is in one of the session's reviewer chats. */
	reviewId?: string;
};

/**
 * buildSessionSearchCommands maps ranked hits onto palette rows.
 *
 * A hit with a message id opens that message; a hit without one (tool output,
 * a native transcript record, a path) opens the session. The distinction is
 * carried in the row rather than guessed later.
 */
export function buildSessionSearchCommands(
	hits: readonly SessionSearchCommandHit[],
	query: string,
	t: TFunction = appI18n.t,
): CommandItem[] {
	return hits.map((hit, index) => {
		const sessionName = hit.sessionName || hit.sessionId;
		// A recency browse has no query, so it has no match kind to report: the
		// row is the session itself, and claiming "session" as a match kind would
		// suggest a name match that did not happen. A reviewer-chat match says so,
		// with the same label the chat search gives it, because Enter opens the
		// reviewer chat rather than the session's own transcript.
		const kindLabel = query.trim() === ""
			? ""
			: hit.reviewId ? t("terminal.reviewer") : (SEARCH_KIND_LABELS[hit.kind] ?? hit.kind);
		// One row per session, shown at its best-matching message; the count is
		// how many of its messages matched, which the opened session's find in chat
		// steps through. A row found only by its name or metadata has none.
		const matches = (hit.sessionMatches ?? 0) > 0 ? t("command.sessionMatches", { count: hit.sessionMatches ?? 0 }) : "";
		return {
			id: `search:${hit.sessionId}:${hit.messageId || hit.kind}:${index}`,
			group: "search" as CommandGroupId,
			title: sessionName,
			subtitle: [kindLabel, matches, hit.projectName || hit.projectId, hit.harness].filter(Boolean).join(" · "),
			// The snippet is what the query matched, so it must also be searchable
			// text for cmdk's own filtering and for a keyword hit.
			keywords: [hit.snippet, hit.title ?? "", hit.projectName ?? "", hit.harness ?? ""],
			// The best-matching message, its matched words marked by the palette's
			// own snippet line. A recent session with nothing matched has none.
			...(hit.snippet
				? { snippet: { text: hit.snippet, highlights: [...(hit.highlights ?? [])].sort((left, right) => left.start - right.start) } }
				: {}),
			// ⌘↵ copies the command that reopens the session from a terminal: the
			// daemon's, which restores a terminated session instead of resuming it.
			secondaryAction: {
				kind: "copy-resume-command",
				command: hit.resumeCommand
					|| `ao session ${hit.sessionState === "terminated" ? "restore" : "resume-agent"} ${hit.sessionId}`,
			},
			action: {
				kind: "open-search-result",
				projectId: hit.projectId,
				sessionId: hit.sessionId,
				messageId: hit.messageId ?? "",
				snippet: hit.snippet,
				...(hit.kind ? { matchKind: hit.kind } : {}),
				...(hit.reviewId ? { reviewId: hit.reviewId } : {}),
				...(hit.highlights?.length ? { highlights: hit.highlights } : {}),
				...(hit.seq ? { seq: hit.seq } : {}),
			},
		};
	});
}

/**
 * Keeps the palette at one row per session.
 *
 * Both searches can find the same session: the session index covers every
 * transcript of a session (its own chat, its reviewer chats, terminal
 * transcripts, tool output) as one row with a match count, while the chat index
 * lists AO chat messages one by one. A chat row whose session the session-text
 * group already lists is dropped, reviewer chats included: that row's count
 * already includes them, and the opened session's find in chat steps through them.
 * Chat rows for sessions the session-text group does not list are kept.
 */
export function withoutChatHitsCoveredBySessionSearch(
	chatItems: readonly CommandItem[],
	sessionSearchItems: readonly CommandItem[],
): CommandItem[] {
	const covered = new Set<string>();
	for (const item of sessionSearchItems) {
		if (item.action?.kind === "open-search-result") covered.add(item.action.sessionId);
	}
	if (covered.size === 0) return [...chatItems];
	return chatItems.filter((item) => {
		const action = item.action;
		if (action?.kind !== "open-chat-message" || !action.sessionId) return true;
		return !covered.has(action.sessionId);
	});
}

const SEARCH_KIND_LABELS: Record<string, string> = {
	title: "session",
	user: "you",
	assistant: "agent",
	tool: "tool",
	command: "command",
	path: "file",
};
