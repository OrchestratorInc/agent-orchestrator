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
	| { kind: "open-archive"; projectId?: string }
	| { kind: "open-session-actions"; sessionId: string }
	| { kind: "open-file-search"; projectId: string; target?: WorkspaceFileSearchTarget }
	| { kind: "open-workspace-file"; target: WorkspaceFileSearchTarget; path: string; line?: number }
	| { kind: "open-chat-message"; sessionId?: string; projectId?: string; reviewId?: string; messageId: string; text: string }
	| { kind: "open-settings-section"; section: GlobalSettingsSection; focus?: string }
	| { kind: "run-shortcut"; shortcut: AppShortcutId }
	| { kind: "resume-session"; projectId: string; sessionId: string }
	| { kind: "copy-branch"; branch: string }
	| { kind: "open-pr"; url: string }
	| { kind: "copy-pr-url"; url: string }
	| { kind: "trigger-review"; sessionId: string }
	| { kind: "toggle-theme" }
	| { kind: "open-shared-link" }
	| { kind: "report-problem" }
	| { kind: "run"; run: () => void }
	| { kind: "open-view"; view: PaletteMenuView }
	| {
			kind: "open-search-result";
			projectId: string;
			sessionId: string;
			messageId: string;
			snippet: string;
			matchKind?: string;
			reviewId?: string;
			highlights?: ReadonlyArray<{ start: number; end: number }>;
			seq?: number;
	  }
	| { kind: "copy-resume-command"; command: string };

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
	snippet?: { text: string; highlights: { start: number; end: number }[]; code?: boolean };
	contentMatch?: boolean;
	secondaryAction?: CommandAction;
	status?: { className: string; breathe: boolean };
	agent?: AgentProvider;
	shortcut?: AppShortcutId;
	icon?: ReactNode;
	checked?: boolean;
	state?: string;
	toggled?: boolean;
	keepOpen?: boolean;
};

export type CommandPaletteContext = {
	workspaces: WorkspaceSummary[];
	currentProjectId?: string;
	currentSessionId?: string;
	currentHostId?: string;
	restartingProjectIds?: ReadonlySet<string>;
	reviewStatesBySessionId?: Readonly<Record<string, PRReviewState[]>>;
	cloudReady?: boolean;
	developerMode?: boolean;
};

export const commandGroupOrder: CommandGroupId[] = ["session", "current", "attention", "search", "files", "content", "messages", "sessions", "projects", "prs", "settings", "global"];

const commandGroupLabelKeys: Record<CommandGroupId, MessageKey> = {
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

function markedSnippet(marked: string, code?: boolean): NonNullable<CommandItem["snippet"]> {
	const highlights: { start: number; end: number }[] = [];
	let text = "";
	for (const [index, part] of marked.split(/[\x02\x03]/).entries()) {
		if (index % 2 === 1) highlights.push({ start: text.length, end: text.length + part.length });
		text += part;
	}
	return { text, highlights, ...(code ? { code } : {}) };
}

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

export function buildChatSearchCommands(
	hits: readonly ConversationSearchHit[],
	t: TFunction = appI18n.t,
	context?: (hit: ConversationSearchHit) => { title?: string; place?: string },
): CommandItem[] {
	const chats = new Map<string, { hit: ConversationSearchHit; count: number }>();
	for (const hit of hits) {
		const key = hit.reviewId ? `review:${hit.reviewId}` : `session:${hit.sessionId}`;
		const chat = chats.get(key);
		if (chat) chat.count += 1;
		else chats.set(key, { hit, count: 1 });
	}
	return [...chats.values()].map(({ hit, count }) => {
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

function workspacePlace(workspace: WorkspaceSummary, t: TFunction): string {
	return workspace.kind === CLOUD_PROJECT_KIND ? `${workspace.name} · ${t("shell.cloudProjectBadge")}` : workspace.name;
}

function attentionMarkerRank(session: WorkspaceSession): number {
	const marker = getSessionRowMarker(session);
	return marker === "attention" ? 0 : marker === "review" ? 1 : -1;
}

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

function contentMatchScore(q: string, item: CommandItem): number {
	const words = q.split(/\s+/).filter((word) => word.length > 1);
	const snippet = item.snippet;
	if (!snippet || words.length === 0) return 200;
	const marked = snippet.highlights.map(({ start, end }) => snippet.text.slice(start, end).toLowerCase());
	const covered = words.filter((word) => marked.some((span) => span.includes(word) || word.startsWith(span))).length;
	return 200 + Math.round((60 * covered) / words.length);
}

function nearMissIn(word: string, text: string): boolean {
	if (word.length < 4) return false;
	return wordsOf(text).some((candidate) =>
		candidate[0] === word[0] && candidate.length >= word.length && candidate.length - word.length <= 2 && isSubsequence(word, candidate));
}

function wordsOf(text: string): string[] {
	return text.split(/[^\p{L}\p{N}]+/u);
}

function wordStartIndex(text: string, q: string): number {
	let at = text.indexOf(q);
	while (at > 0 && /[\p{L}\p{N}]/u.test(text[at - 1] ?? "")) at = text.indexOf(q, at + 1);
	return at;
}

function wordAnchoredSubsequence(q: string, title: string): boolean {
	for (let at = 0; at < title.length; at++) {
		const wordStart = at === 0 || !/[\p{L}\p{N}]/u.test(title[at - 1] ?? "");
		if (wordStart && title[at] === q[0] && isSubsequence(q, title.slice(at))) return true;
	}
	return false;
}

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
	const words = q.split(/\s+/);
	const inTitle = (word: string) => title.includes(word) || nearMissIn(word, title);
	if (words.length > 1 && words.every((word) => inTitle(word) || wordStartIndex(extras, word) >= 0 || nearMissIn(word, extras))) {
		return 300 + Math.round((100 * words.filter(inTitle).length) / words.length);
	}
	if (wordAnchoredSubsequence(q, title)) return 200;
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

const SEARCH_GROUPS: ReadonlySet<CommandGroupId> = new Set(["files", "content", "messages"]);

export function isSearchGroup(group: CommandGroupId): boolean {
	return SEARCH_GROUPS.has(group);
}

export const SEARCH_GROUP_ROWS = 6;

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
	const searched = items
		.filter((item) => SEARCH_GROUPS.has(item.group))
		.map((item, index) => ({ item, index, score: matchScore(query, item) }))
		.sort((a, b) => b.score - a.score || a.index - b.index)
		.map((entry) => entry.item);
	const searchGroups = groupCommands(searched, t, searchGroupRows);
	if (!query.trim()) return [...groupCommands(capBlankGroups(visibleForQuery(local, query)), t), ...searchGroups];
	const groups = groupCommands(visibleForQuery(local, query), t)
		.sort((a, b) => searchGroupRank(a.id) - searchGroupRank(b.id));
	return [...groups, ...searchGroups];
}

const SEARCH_GROUP_ORDER: CommandGroupId[] = ["session", "current", "global", "settings", "attention", "sessions", "projects", "prs"];

function searchGroupRank(id: string): number {
	const rank = SEARCH_GROUP_ORDER.indexOf(id as CommandGroupId);
	return rank === -1 ? SEARCH_GROUP_ORDER.length : rank;
}

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

export type SessionSearchCommandHit = {
	sessionId: string;
	projectId: string;
	messageId?: string;
	seq?: number;
	kind: string;
	title?: string;
	snippet: string;
	highlights?: ReadonlyArray<{ start: number; end: number }>;
	sessionName?: string;
	projectName?: string;
	harness?: string;
	sessionState?: string;
	resumeCommand?: string;
	createdAt: string;
	sessionMatches?: number;
	reviewId?: string;
};

export function buildSessionSearchCommands(
	hits: readonly SessionSearchCommandHit[],
	query: string,
	t: TFunction = appI18n.t,
): CommandItem[] {
	return hits.map((hit, index) => {
		const sessionName = hit.sessionName || hit.sessionId;
		const kindLabel = query.trim() === ""
			? ""
			: hit.reviewId ? t("terminal.reviewer") : (SEARCH_KIND_LABELS[hit.kind] ?? hit.kind);
		const matches = (hit.sessionMatches ?? 0) > 0 ? t("command.sessionMatches", { count: hit.sessionMatches ?? 0 }) : "";
		return {
			id: `search:${hit.sessionId}:${hit.messageId || hit.kind}:${index}`,
			group: "search" as CommandGroupId,
			title: sessionName,
			subtitle: [kindLabel, matches, hit.projectName || hit.projectId, hit.harness].filter(Boolean).join(" · "),
			keywords: [hit.snippet, hit.title ?? "", hit.projectName ?? "", hit.harness ?? ""],
			...(hit.snippet
				? { snippet: { text: hit.snippet, highlights: [...(hit.highlights ?? [])].sort((left, right) => left.start - right.start) } }
				: {}),
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
