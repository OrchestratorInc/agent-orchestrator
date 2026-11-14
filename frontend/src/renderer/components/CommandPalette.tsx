import { apiErrorException } from "../lib/api-error-exception";
import { keepPreviousData, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ArrowLeft, Bell, Check, ChevronRight, CircleAlert, Coffee, Loader2, Smartphone } from "@/components/app-icons";
import { effectiveShortcutBindings, shortcutBindingKeys, type AppShortcutId } from "../../shared/shortcuts";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type AnimationEvent } from "react";
import { useTranslation } from "react-i18next";
import { useCommandPaletteEnabled } from "../hooks/useCommandPaletteEnabled";
import { useCloudCp } from "../hooks/useCloudCp";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { cloudSessionsQueryKey, useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { spawnCloudOrchestrator } from "../lib/cloud-orchestrator";
import {
	buildChatSearchCommands,
	buildCommands,
	buildContentSearchCommands,
	buildFileSessionCommands,
	buildProjectActions,
	buildSessionActions,
	buildSessionSearchCommands,
	withoutChatHitsCoveredBySessionSearch,
	buildWorkspaceFileCommands,
	bestMatch,
	goToCommands,
	findSubmenuEntries,
	menuEntryCommands,
	type PaletteMenuView,
	displayGroups,
	filterCommands,
	isSearchGroup,
	findSession,
	type CommandItem as CommandItemModel,
	type NavigateTarget,
	type WorkspaceFileSearchTarget,
	sessionSupportsFileSearch,
	titleHighlights,
	workspaceFileSearchTarget,
} from "../lib/command-palette";
import {
	type CommandPaletteFileSearchResponse,
	commandPaletteChatSearchQueryOptions,
	commandPaletteCloudChatSearchQueryOptions,
	commandPaletteContentSearchQueryOptions,
	commandPaletteFileSearchAvailable,
	commandPaletteFileSearchQueryOptions,
} from "../lib/command-palette-file-search";
import { iconForCommand } from "../lib/command-palette-icons";
import { startProblemReport } from "../lib/report-problem";
import { buildChoiceCommands, buildSettingsCommands, buildShortcutCommands, choiceValue, type PalettePreferences } from "../lib/palette-registry";
import { previewDocumentThemeStyle } from "../lib/theme";
import { renameCloudSession, renameSession } from "../lib/rename-session";
import { runAppMenuAction } from "../hooks/useAppMenuCommand";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useProjectAvatars } from "../hooks/useProjectAvatars";
import { ProjectIcon } from "./ProjectIcon";
import { useStayAwake } from "../hooks/useStayAwake";
import { useSessionSwitchStore } from "../stores/session-switch-store";
import { RoundedInterfaceIcon } from "./SessionInterfaceSwitch";
import { useLocaleStore } from "../stores/locale-store";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useSoundNotificationsStore } from "../stores/sound-notifications-store";
import { ConfirmDialog } from "./ConfirmDialog";
import { SessionArchiveDialog } from "./SessionArchiveDialog";
import { useCopyToClipboard, useSessionMenuEntries } from "./SessionContextMenuItems";
import type { MenuEntry } from "./menu-model";
import { useSplitPanesEnabled } from "../lib/split-panes";
import { isDialogOrMenuOpen } from "../lib/dom-selectors";
import { LOCAL_HOST } from "../lib/hosts";
import { isMacPlatform } from "../lib/platform";
import { revealSessionSearchMatch } from "../lib/session-search-reveal";
import { usePaletteSessionSearch, useSessionSearchIndexStatus } from "../hooks/usePaletteSessionSearch";
import { sessionReviewsQueryOptions, type PRReviewState } from "../lib/session-reviews";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { useShell } from "../lib/shell-context";
import { sessionUiKey } from "../lib/hosts";
import { beginModalHandoff } from "../lib/modal-handoff";
import { cn } from "../lib/utils";
import { getSessionRowIndicator } from "../lib/session-presentation";
import { SessionStatusDot } from "./Sidebar";
import { useKeybindingsStore } from "../stores/keybindings-store";
import {
	CLOUD_PROJECT_KIND,
	findProjectOrchestrator,
	hasConfiguredOrchestratorAgent,
	isOrchestratorSession,
	openPRs,
	STANDALONE_WORKSPACE_ID,
	workerSessions,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { useSessionImportStore } from "../stores/session-import-store";
import { runAppShortcut, useAppShortcut } from "../hooks/useAppShortcut";
import { CreateProjectFlow } from "./CreateProjectFlow";
import { requestOpenShareLink } from "./OpenShareLinkDialog";
import { Button } from "./ui/button";
import { CommandDialog, CommandEmpty, CommandFooter, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut } from "./ui/command";
import { Switch } from "./ui/switch";

const PALETTE_REVIEW_STALE_TIME_MS = 60_000;
const PALETTE_REVIEW_DEFER_MS = 120;
const PALETTE_FILE_SEARCH_DEBOUNCE_MS = 150;
const PALETTE_FILE_VIEW_ROWS = 50;
const isMac = isMacPlatform();
const PALETTE_FILE_SEARCH_MIN_CHARS = 2;
const PALETTE_SESSION_SEARCH_MIN_CHARS = 2;
const PALETTE_RECENTS_LIMIT = 5;
const EMPTY_REVIEW_STATES: Readonly<Record<string, PRReviewState[]>> = {};
type PaletteView =
	| { mode: "root" }
	| { mode: "session-actions"; sessionId: string }
	| { mode: "file-session-picker"; projectId: string }
	| { mode: "file-search"; target: WorkspaceFileSearchTarget; parent: "root" | "picker" }
	| (PaletteMenuView & { parent: PaletteView });

function findSubmenuOwner(entries: readonly MenuEntry[], entryId: string): Extract<MenuEntry, { kind: "submenu" }> | undefined {
	for (const entry of entries) {
		if (entry.kind !== "submenu") continue;
		if (entry.id === entryId) return entry;
		const nested = findSubmenuOwner(entry.items, entryId);
		if (nested) return nested;
	}
	return undefined;
}

function sameSearchTarget(previous: readonly unknown[] | undefined, current: readonly unknown[], query: string): boolean {
	if (!previous || previous.length !== current.length) return false;
	const queryAt = current.lastIndexOf(query);
	return queryAt >= 0 && previous.every((part, index) => index === queryAt || part === current[index]);
}

function isRowNavigationKey(event: { key: string; ctrlKey: boolean }): boolean {
	return ["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)
		|| (event.ctrlKey && ["n", "j", "p", "k"].includes(event.key));
}

export const CommandPalette = memo(function CommandPalette() {
	const { i18n, t } = useTranslation();
	const enabled = useCommandPaletteEnabled();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const cloud = useCloudCp();
	const cloudReady = cloud.ready;
	const restoreSessionById = useRestoreSession();
	const params = useParams({ strict: false }) as { hostId?: string; projectId?: string; sessionId?: string };
	const { cloneProject, createProject, daemonStatus, initializeProjectRepository, removeProject } = useShell();
	const resolvedTheme = useUiStore((s) => s.resolvedTheme);
	const setThemePreference = useUiStore((s) => s.setThemePreference);
	const isOpen = useUiStore((s) => s.isCommandPaletteOpen);
	const setOpen = useUiStore((s) => s.setCommandPaletteOpen);
	const restartingProjectIds = useUiStore((s) => s.restartingProjectIds);
	const developerMode = useUiStore((s) => s.developerMode);
	const diagnostics = useUiStore((s) => s.developerMode && s.diagnostics);
	const splitPanes = useSplitPanesEnabled();
	const workspaces = useWorkspaceQuery({ subscribed: isOpen }).data ?? [];

	const [view, setView] = useState<PaletteView>({ mode: "root" });
	const [query, setQuery] = useState("");
	const [debouncedQuery, setDebouncedQuery] = useState("");
	const [selectedValue, setSelectedValue] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [pendingId, setPendingId] = useState<string | null>(null);
	const [reviewStatesSnapshot, setReviewStatesSnapshot] = useState<Readonly<Record<string, PRReviewState[]>>>();
	const [reviewActionsReady, setReviewActionsReady] = useState(false);
	const [createProjectFlowMounted, setCreateProjectFlowMounted] = useState(false);
	const [createProjectFlowOpenSignal, setCreateProjectFlowOpenSignal] = useState(0);
	const [createProjectFlowPendingOpen, setCreateProjectFlowPendingOpen] = useState(false);
	const pendingRef = useRef<symbol | null>(null);
	const userPickRef = useRef(false);
	const [valueResync, setValueResync] = useState(false);
	const runGenerationRef = useRef(0);
	const viewRef = useRef(view);
	viewRef.current = view;
	const closeResetTimerRef = useRef<number | null>(null);
	const inputRef = useRef<HTMLInputElement>(null);
	const returnFocusRef = useRef<Element | null>(null);
	useEffect(() => useUiStore.subscribe((state, previous) => {
		if (state.isCommandPaletteOpen && !previous.isCommandPaletteOpen) returnFocusRef.current = document.activeElement;
	}), []);
	useEffect(() => {
		if (!isOpen) return;
		const frame = window.requestAnimationFrame(() => inputRef.current?.focus());
		return () => window.cancelAnimationFrame(frame);
	}, [isOpen, view]);

	const currentSessionContext = useMemo(() => {
		if (!params.sessionId) return undefined;
		for (const workspace of workspaces) {
			if ((workspace.hostId ?? "") !== (params.hostId ?? "")) continue;
			const session = workspace.sessions.find((candidate) => candidate.id === params.sessionId);
			if (session) return { workspace, session };
		}
		return undefined;
	}, [params.hostId, params.sessionId, workspaces]);
	const currentSession = currentSessionContext?.session;
	const currentProjectId = currentSession?.workspaceId ?? params.projectId;
	const currentProject = currentProjectId
		? workspaces.find((workspace) => workspace.id === currentProjectId && (workspace.hostId ?? "") === (params.hostId ?? ""))
		: undefined;
	const avatarProjects = useMemo(() => (currentProject ? [currentProject] : []), [currentProject]);
	const headingAvatars = useProjectAvatars(avatarProjects);
	const actionsHeading = currentProject ? (
		<span className="inline-flex min-w-0 items-center gap-2.5">
			<span className="flex size-icon-base shrink-0 items-center justify-center" aria-hidden="true">
				<ProjectIcon avatarUrl={headingAvatars.get(currentProject.id)} className="size-icon-sm" expanded={false} name={currentProject.name} seed={currentProject.path || currentProject.id} />
			</span>
			<span className="truncate">{currentProject.name}</span>
		</span>
	) : undefined;
	const sessionHeading = currentSession ? (
		<span className="inline-flex min-w-0 items-center gap-2.5">
			<span className="flex size-icon-base shrink-0 items-center justify-center" aria-hidden="true">
				<SessionStatusDot indicator={getSessionRowIndicator(currentSession)} session={currentSession} />
			</span>
			<span className="truncate">{currentSession.title}</span>
		</span>
	) : undefined;
	const currentWorkspace = useMemo(
		() => currentSessionContext?.workspace ?? workspaces.find((workspace) =>
			workspace.id === currentProjectId && (workspace.hostId ?? "") === (params.hostId ?? "")),
		[currentProjectId, currentSessionContext, params.hostId, workspaces],
	);
	const currentFileTarget = currentWorkspace && currentSession && sessionSupportsFileSearch(currentSession)
		? workspaceFileSearchTarget(currentWorkspace, currentSession)
		: undefined;

	useEffect(() => {
		if (!isOpen) {
			setDebouncedQuery("");
			return;
		}
		const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), PALETTE_FILE_SEARCH_DEBOUNCE_MS);
		return () => window.clearTimeout(timer);
	}, [isOpen, query, view.mode]);

	const activeFileTarget = view.mode === "file-search"
		? view.target
		: view.mode === "root"
			? currentFileTarget
			: undefined;
	const normalizedFileQuery = debouncedQuery.trim();
	const normalizedSessionQuery = debouncedQuery.trim();
	const fileSearchEnabled = isOpen
		&& normalizedFileQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS
		&& commandPaletteFileSearchAvailable(activeFileTarget, cloud.ready);
	const fileSearchOptions = commandPaletteFileSearchQueryOptions({
		target: activeFileTarget ?? { projectId: "", sessionId: "" },
		query: normalizedFileQuery,
		errorMessage: t("command.searchFilesError"),
		cloud,
	});
	const fileSearchQuery = useQuery<CommandPaletteFileSearchResponse>({
		...fileSearchOptions,
		enabled: fileSearchEnabled,
		placeholderData: (previous, previousQuery) =>
			sameSearchTarget(previousQuery?.queryKey, fileSearchOptions.queryKey, normalizedFileQuery) ? previous : undefined,
	});
	const searchQueryReady = isOpen && normalizedFileQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS;
	const contentTarget = activeFileTarget && (!activeFileTarget.cloudOrgId || cloudReady) ? activeFileTarget : undefined;
	const contentSearchEnabled = searchQueryReady && view.mode === "file-search" && Boolean(contentTarget);
	const contentSearchOptions = commandPaletteContentSearchQueryOptions(contentTarget ?? { projectId: "", sessionId: "" }, normalizedFileQuery, t("command.searchContentError"), cloud);
	const contentSearchQuery = useQuery({
		...contentSearchOptions,
		enabled: contentSearchEnabled,
		placeholderData: (previous, previousQuery) =>
			sameSearchTarget(previousQuery?.queryKey, contentSearchOptions.queryKey, normalizedFileQuery) ? previous : undefined,
	});
	const chatSearchEnabled = searchQueryReady && view.mode === "root";
	const chatSearchQuery = useQuery({
		...commandPaletteChatSearchQueryOptions(normalizedFileQuery, t("command.searchChatsError")),
		enabled: chatSearchEnabled,
		placeholderData: keepPreviousData,
	});
	const cloudChatOrgIds = useMemo(
		() => [...new Set(workspaces.flatMap((workspace) => workspace.kind === "cloud" && workspace.cloudOrgId ? [workspace.cloudOrgId] : []))],
		[workspaces],
	);
	const cloudChatSearchQuery = useQuery({
		...commandPaletteCloudChatSearchQueryOptions(cloud, cloudChatOrgIds, normalizedFileQuery),
		enabled: chatSearchEnabled && cloudReady && cloudChatOrgIds.length > 0,
		placeholderData: keepPreviousData,
	});

	const sessionSearchEnabled = isOpen
		&& view.mode === "root"
		&& (normalizedSessionQuery.length === 0
			|| normalizedSessionQuery.length >= PALETTE_SESSION_SEARCH_MIN_CHARS);
	const sessionSearchQuery = usePaletteSessionSearch(normalizedSessionQuery, LOCAL_HOST, sessionSearchEnabled);
	const sessionSearchIndexStatus = useSessionSearchIndexStatus(LOCAL_HOST, isOpen && sessionSearchEnabled);
	const sessionSearchHits = sessionSearchQuery.data?.results ?? [];

	const sessionsWithOpenPRs = useMemo(
		() =>
			(reviewActionsReady ? workspaces : []).flatMap((workspace) =>
				workerSessions(workspace.sessions).filter((session) => openPRs(session).length > 0),
			),
		[reviewActionsReady, workspaces],
	);
	useEffect(() => {
		if (!isOpen) {
			setReviewActionsReady(false);
			return;
		}
		const timer = window.setTimeout(() => setReviewActionsReady(true), PALETTE_REVIEW_DEFER_MS);
		return () => window.clearTimeout(timer);
	}, [isOpen]);
	const reviewQuerySummary = useQueries({
		subscribed: reviewActionsReady,
		queries: sessionsWithOpenPRs.map((session) =>
			sessionReviewsQueryOptions(session, reviewActionsReady, PALETTE_REVIEW_STALE_TIME_MS),
		),
		combine: (results) => {
			const reviewStatesBySessionId: Record<string, PRReviewState[]> = {};
			results.forEach((result, index) => {
				const session = sessionsWithOpenPRs[index];
				if (session && result.data && !result.isFetching && !result.isError) {
					reviewStatesBySessionId[session.id] = result.data.reviews ?? [];
				}
			});
			return { reviewStatesBySessionId };
		},
	});

	useEffect(() => {
		if (!isOpen) {
			setReviewStatesSnapshot({});
			return;
		}
		if (!reviewActionsReady) return;
		setReviewStatesSnapshot((previous) => {
			let changed = false;
			const next = { ...previous };
			for (const [sessionId, reviews] of Object.entries(reviewQuerySummary.reviewStatesBySessionId)) {
				if (!(sessionId in next)) {
					next[sessionId] = reviews;
					changed = true;
				}
			}
			return changed ? next : previous;
		});
	}, [isOpen, reviewActionsReady, reviewQuerySummary.reviewStatesBySessionId]);
	const reviewStatesForCommands = isOpen && reviewActionsReady ? reviewStatesSnapshot : EMPTY_REVIEW_STATES;

	const themePreference = useUiStore((s) => s.themePreference);
	const themeStyle = useUiStore((s) => s.themeStyle);
	const locale = useLocaleStore((s) => s.locale);
	const terminalClickMode = useUiStore((s) => s.terminalClickMode);
	const chatSendKeyMode = useUiStore((s) => s.chatSendKeyMode);
	const windowTranslucency = useUiStore((s) => s.windowTranslucency);
	const projectAvatars = useUiStore((s) => s.projectAvatars);
	const renderMath = useUiStore((s) => s.renderMath);
	const navigationGestures = useUiStore((s) => s.navigationGestures);
	const terminalCopyOnSelect = useUiStore((s) => s.terminalCopyOnSelect);
	const sideChats = useUiStore((s) => s.sideChats);
	const soundNotifications = useSoundNotificationsStore((s) => s.enabled);
	const stayAwake = useStayAwake(isOpen && isMac);
	const stayToggleRef = useRef(stayAwake.toggle);
	stayToggleRef.current = stayAwake.toggle;
	const switchMenus = useSessionSwitchStore((state) => state.menus);
	const switchItemFor = useCallback((sessionId: string | undefined): CommandItemModel[] => {
		const menu = sessionId ? switchMenus[sessionId] : undefined;
		const action = menu?.switchAction;
		if (!menu || !action) return [];
		return [{
			id: "session-switch-interface",
			group: "current",
			title: menu.switchTarget === "chat" ? t("agentSwitch.switchToChat") : t("agentSwitch.switchToTerminal"),
			icon: <RoundedInterfaceIcon className="size-icon-base" target={menu.switchTarget} />,
			keywords: ["tui", "terminal", "chat", "interface", "ui", "mode"],
			disabled: action.disabled,
			...(action.reason ? { disabledReason: action.reason } : {}),
			action: { kind: "run", run: action.run },
		}];
	}, [switchMenus, t]);

	const prefs = useMemo((): PalettePreferences => ({
		themePreference,
		themeStyle,
		locale,
		terminalClickMode,
		chatSendKeyMode,
		toggles: {
			transparency: windowTranslucency,
			"project-avatars": projectAvatars,
			"render-math": renderMath,
			"navigation-gestures": navigationGestures,
			"copy-on-select": terminalCopyOnSelect,
			sound: soundNotifications,
			"developer-mode": developerMode,
			"side-chats": sideChats,
			"split-panes": splitPanes,
		},
	}), [themePreference, themeStyle, locale, terminalClickMode, chatSendKeyMode, windowTranslucency, projectAvatars, renderMath, navigationGestures, terminalCopyOnSelect, soundNotifications, developerMode, sideChats, splitPanes]);

	const rootItems = useMemo(
		() => [
			...buildCommands({
				workspaces,
				currentProjectId,
				currentSessionId: params.sessionId,
				currentHostId: params.hostId,
				restartingProjectIds,
				reviewStatesBySessionId: reviewStatesForCommands,
				cloudReady,
				developerMode,
			}, t),
			...buildShortcutCommands(t, {
				splitPanes,
				...(params.sessionId
					? { session: currentSession && !currentSession.cloud && !currentSession.hostId && !params.hostId ? "local" as const : "elsewhere" as const }
					: {}),
			}),
			...buildSettingsCommands(t, { developerMode, diagnostics, mac: isMac, prefs }),
			{ id: "global-notifications", group: "global" as const, title: t("notify.title"), icon: <Bell aria-hidden="true" />, keywords: ["alerts", "inbox", "bell"], action: { kind: "run" as const, run: () => runAppMenuAction("toggle-notifications") } },
			...(isMac && stayAwake.supported
				? [{ id: "global-stay-awake", group: "global" as const, title: t("awake.title"), icon: <Coffee aria-hidden="true" />, keywords: ["awake", "sleep", "caffeinate", "lid closed", "keep awake"], searchOnly: true, toggled: stayAwake.on, keepOpen: true, action: { kind: "run" as const, run: () => stayToggleRef.current() } }]
				: []),
			{ id: "global-connect-mobile", group: "global" as const, title: t("settings.connectMobile"), icon: <Smartphone aria-hidden="true" />, keywords: ["phone", "pair", "ios", "android", "qr code"], searchOnly: true, action: { kind: "run" as const, run: () => useUiStore.getState().openGlobalSettings("mobile", { startPairing: true }) } },
		],
		[workspaces, currentProjectId, currentSession, params.hostId, params.sessionId, restartingProjectIds, reviewStatesForCommands, cloudReady, developerMode, diagnostics, splitPanes, prefs, stayAwake.on, stayAwake.supported, t, i18n.resolvedLanguage],
	);
	const scoped = useMemo(
		() => (view.mode === "session-actions" ? findSession(workspaces, view.sessionId) : undefined),
		[view, workspaces],
	);
	const menuSession = view.mode === "root"
		? currentSession
		: view.mode === "session-actions"
		? scoped?.session
		: view.mode === "menu-submenu" || view.mode === "rename-session"
			? findSession(workspaces, view.sessionId)?.session
			: undefined;
	const [archiveTarget, setArchiveTarget] = useState<WorkspaceSession | null>(null);
	const [removeTarget, setRemoveTarget] = useState<WorkspaceSummary | null>(null);
	const { mutate: terminateSession } = useTerminateSession();
	const copyToClipboard = useCopyToClipboard();
	const pinnedProjectIds = useSidebarOrderStore((state) => state.pinnedProjectIds);
	const sessionMenuEntries = useSessionMenuEntries(menuSession, {
		onArchive: menuSession && !menuSession.cloud?.shared ? () => {
			setArchiveTarget(menuSession);
			closePalette();
		} : undefined,
		onRename: menuSession && !isOrchestratorSession(menuSession) ? () => {
			pushView({ mode: "rename-session", sessionId: menuSession.id, parent: viewRef.current });
			setQuery(menuSession.title);
		} : undefined,
	});
	const sessionActionItems = useMemo(() => {
		if (!scoped) return [];
		const own = buildSessionActions(scoped.workspace, scoped.session, t);
		const skip = new Set(["menu:session-copy-branch", ...(own.some((item) => item.action?.kind === "resume-session") ? ["menu:session-resume"] : [])]);
		return [...own, ...switchItemFor(scoped.session.id), ...paletteMenuRows(sessionMenuEntries, scoped.session.id).filter((item) => !skip.has(item.id))];
	}, [scoped, sessionMenuEntries, switchItemFor, t]);
	const projectActionsFor = useCallback((workspace: WorkspaceSummary): CommandItemModel[] => {
		const canPin = workspace.id !== STANDALONE_WORKSPACE_ID && !workspace.hostId;
		return buildProjectActions(workspace, {
			canPin,
			isPinned: canPin && pinnedProjectIds.includes(workspace.id),
			restarting: restartingProjectIds.has(workspace.id),
			hasOrchestrator: Boolean(findProjectOrchestrator(workspaces, workspace.id)),
			setPinned: (pinned) => useSidebarOrderStore.getState().setProjectPinned(workspace.id, pinned),
			copyPath: () => copyToClipboard(workspace.path),
			...(removeProject && !workspace.hostId ? { remove: () => setRemoveTarget(workspace) } : {}),
		}, t);
	}, [workspaces, pinnedProjectIds, restartingProjectIds, copyToClipboard, removeProject, t]);
	const menuViewItems = useMemo((): CommandItemModel[] => {
		switch (view.mode) {
			case "menu-submenu":
				return menuSession ? menuEntryCommands(findSubmenuEntries(sessionMenuEntries, view.entryId) ?? [], menuSession.id) : [];
			case "go-to":
				return goToCommands(rootItems, view.target);
			case "choices":
				return buildChoiceCommands(view.menu, t, prefs);
			case "rename-session": {
				const name = query.trim();
				return [{
					id: "rename:commit",
					group: "current",
					title: name || (menuSession?.title ?? ""),
					subtitle: t("shell.rename"),
					disabled: !menuSession || !name || name === menuSession.title,
					action: { kind: "run", run: () => {
						if (!menuSession) return;
						const done = menuSession.cloud
							? renameCloudSession(queryClient, { id: menuSession.id, cloud: menuSession.cloud }, name)
							: renameSession(menuSession.id, name, menuSession.hostId).then(() => queryClient.invalidateQueries({ queryKey: workspaceQueryKey }));
						done.catch((error: unknown) => useUiStore.getState().showGlobalToast(t("shell.rename"), error instanceof Error ? error.message : undefined, "error"));
					} },
				}];
			}
			case "project-actions": {
				const workspace = workspaces.find((candidate) => candidate.id === view.projectId);
				return workspace ? projectActionsFor(workspace) : [];
			}
			default:
				return [];
		}
	}, [view, rootItems, menuSession, sessionMenuEntries, prefs, query, projectActionsFor, queryClient, t]);
	const rootContextItems = useMemo((): CommandItemModel[] => {
		if (view.mode !== "root") return [];
		const sessionRows = currentSession
			? [...switchItemFor(currentSession.id), ...paletteMenuRows(sessionMenuEntries, currentSession.id).filter((item) => item.id !== "menu:session-copy-branch")]
				.map((item) => ({ ...item, group: "session" as const, ...(BLANK_SESSION_ACTIONS.has(item.id) ? {} : { searchOnly: true }) }))
			: [];
		const projectRows = currentProject
			? projectActionsFor(currentProject)
				.filter((item) => ["project-action:board", "project-action:copy-path", "project-action:pin", "project-action:remove"].includes(item.id))
				.map((item) => (item.id === "project-action:remove" ? { ...item, searchOnly: true } : item))
			: [];
		return [...sessionRows, ...(currentSession ? projectRows.map((item) => ({ ...item, searchOnly: true })) : projectRows)];
	}, [view.mode, currentSession, currentProject, sessionMenuEntries, switchItemFor, projectActionsFor]);
	const filePickerWorkspace = view.mode === "file-session-picker"
		? workspaces.find((workspace) => workspace.id === view.projectId && (workspace.hostId ?? "") === (params.hostId ?? ""))
		: undefined;
	const fileSessionItems = useMemo(
		() => filePickerWorkspace ? buildFileSessionCommands(filePickerWorkspace) : [],
		[filePickerWorkspace],
	);
	const fileItems = useMemo(
		() => activeFileTarget && fileSearchEnabled && fileSearchQuery.data
			? buildWorkspaceFileCommands(activeFileTarget, fileSearchQuery.data.results)
			: [],
		[activeFileTarget, fileSearchEnabled, fileSearchQuery.data],
	);
	const contentItems = useMemo(
		() => contentTarget && contentSearchEnabled && contentSearchQuery.data
			? buildContentSearchCommands(contentTarget, contentSearchQuery.data.results)
			: [],
		[contentTarget, contentSearchEnabled, contentSearchQuery.data],
	);
	const findLocalChatWorkspace = useCallback(
		(sessionId: string | undefined) => workspaces.find((workspace) =>
			!workspace.hostId && workspace.kind !== "cloud"
			&& workspace.sessions.some((session) => session.id === sessionId && !session.hostId && !session.cloud)),
		[workspaces],
	);
	const findCloudChatWorkspace = useCallback(
		(sessionId: string | undefined) => workspaces.find((workspace) =>
			workspace.kind === "cloud" && workspace.sessions.some((session) => session.id === sessionId && session.cloud)),
		[workspaces],
	);
	const findChatWorkspace = useCallback(
		(sessionId: string | undefined) => findLocalChatWorkspace(sessionId) ?? findCloudChatWorkspace(sessionId),
		[findCloudChatWorkspace, findLocalChatWorkspace],
	);
	const chatItems = useMemo(
		() => {
			if (!chatSearchEnabled) return [];
			const hits = [
				...(chatSearchQuery.data?.results ?? []).filter((hit) => findLocalChatWorkspace(hit.sessionId)),
				...(cloudReady ? cloudChatSearchQuery.data?.results ?? [] : []).filter((hit) => findCloudChatWorkspace(hit.sessionId)),
			];
			return buildChatSearchCommands(hits, t, (hit) => {
				const workspace = findChatWorkspace(hit.sessionId);
				return {
					title: workspace?.sessions.find((session) => session.id === hit.sessionId)?.title,
					place: workspace && workspace.id !== currentProjectId ? workspace.name : undefined,
				};
			});
		},
		[currentProjectId, chatSearchEnabled, chatSearchQuery.data, cloudChatSearchQuery.data, cloudReady, findChatWorkspace, findCloudChatWorkspace, findLocalChatWorkspace, t],
	);
	const sessionSearchSettled = normalizedSessionQuery === query.trim();
	const sessionSearchItems = useMemo(
		() => (sessionSearchSettled ? buildSessionSearchCommands(sessionSearchHits, query.trim(), t) : []),
		[sessionSearchHits, query, sessionSearchSettled, t],
	);

	const groups = useMemo(() => {
		if (view.mode === "session-actions") {
			return [{ id: "actions", label: "", items: filterCommands(sessionActionItems, query) }];
		}
		if (view.mode === "file-session-picker") {
			return [{ id: "file-sessions", label: "", items: filterCommands(fileSessionItems, query) }];
		}
		if (view.mode === "file-search") {
			return displayGroups([...fileItems, ...contentItems], query, t, PALETTE_FILE_VIEW_ROWS);
		}
		if (view.mode !== "root") {
			return [{ id: "menu", label: "", items: view.mode === "rename-session" ? menuViewItems : filterCommands(menuViewItems, query) }];
		}
		const shownRootItems = currentSession
			? rootItems.map((item) => (item.group === "current" || item.id === "current-copy-branch" ? { ...item, searchOnly: true } : item))
			: rootItems;
		const settledChatItems = withoutChatHitsCoveredBySessionSearch(chatItems, sessionSearchItems);
		const commandGroups = displayGroups([...shownRootItems, ...rootContextItems, ...fileItems, ...settledChatItems], query, t);
		const recents = normalizedSessionQuery === "";
		const listed = new Set([
			...(currentSession ? [currentSession.id] : []),
			...commandGroups.flatMap((group) => (group.id === "attention" || group.id === "sessions"
				? group.items.map((item) => item.id.slice(item.id.indexOf(":") + 1))
				: [])),
		]);
		const searchRows = recents
			? sessionSearchItems.filter((item) => item.action?.kind !== "open-search-result" || !listed.has(item.action.sessionId)).slice(0, PALETTE_RECENTS_LIMIT)
			: sessionSearchItems;
		if (searchRows.length === 0) return commandGroups;
		const searchGroup = { id: "search" as const, label: recents ? t("command.recentSessions") : t("command.sessionText"), items: searchRows };
		if (recents) return [...commandGroups, searchGroup];
		const at = commandGroups.findIndex((group) => isSearchGroup(group.id as CommandItemModel["group"]));
		return at === -1 ? [...commandGroups, searchGroup] : [...commandGroups.slice(0, at), searchGroup, ...commandGroups.slice(at)];
	}, [view.mode, rootItems, rootContextItems, currentSession, sessionActionItems, fileSessionItems, menuViewItems, fileItems, contentItems, chatItems, sessionSearchItems, normalizedSessionQuery, query, normalizedFileQuery, t, i18n.resolvedLanguage]);

	const visibleItems = useMemo(() => groups.flatMap((group) => group.items), [groups]);
	const searchStale = normalizedFileQuery !== query.trim()
		|| fileSearchQuery.isPlaceholderData
		|| contentSearchQuery.isPlaceholderData
		|| chatSearchQuery.isPlaceholderData
		|| cloudChatSearchQuery.isPlaceholderData;
	const autoSelectable = (item: CommandItemModel) => !item.disabled && !(searchStale && isSearchGroup(item.group));
	const value =
		(visibleItems.some((item) => item.id === selectedValue && !item.disabled)
			? selectedValue
			: ((view.mode === "root" && query.trim() ? bestMatch(visibleItems.filter(autoSelectable), query) : undefined)
				?? visibleItems.find(autoSelectable) ?? (searchStale ? undefined : visibleItems[0]))?.id) ?? "";

	const previewTheme = view.mode === "choices" && view.menu === "color-theme" ? choiceValue(value) : undefined;
	useEffect(() => {
		if (!previewTheme) return;
		previewDocumentThemeStyle(previewTheme as Parameters<typeof previewDocumentThemeStyle>[0], useUiStore.getState().themeStyle);
		return () => previewDocumentThemeStyle(null, useUiStore.getState().themeStyle);
	}, [previewTheme]);

	const resetTransient = useCallback(() => {
		runGenerationRef.current += 1;
		setQuery("");
		setDebouncedQuery("");
		setSelectedValue("");
		setError(null);
	}, []);

	const closePalette = useCallback(() => {
		runGenerationRef.current += 1;
		pendingRef.current = null;
		setPendingId(null);
		setOpen(false);
		setView({ mode: "root" });
		if (closeResetTimerRef.current !== null) window.clearTimeout(closeResetTimerRef.current);
		closeResetTimerRef.current = window.setTimeout(() => {
			closeResetTimerRef.current = null;
			if (!useUiStore.getState().isCommandPaletteOpen) resetTransient();
		}, 150);
		const returnTo = returnFocusRef.current;
		returnFocusRef.current = null;
		window.setTimeout(() => {
			const active = document.activeElement;
			if (returnTo instanceof HTMLElement && returnTo.isConnected && (!active || active === document.body)) returnTo.focus();
		}, 160);
	}, [resetTransient, setOpen]);
	useEffect(() => {
		if (!createProjectFlowMounted || !createProjectFlowPendingOpen) return;
		setCreateProjectFlowPendingOpen(false);
		setCreateProjectFlowOpenSignal((current) => current + 1);
	}, [createProjectFlowMounted, createProjectFlowPendingOpen]);
	const openNewProject = useCallback(() => {
		closePalette();
		setCreateProjectFlowMounted(true);
		setCreateProjectFlowPendingOpen(true);
	}, [closePalette]);
	const openExistingProject = useCallback(
		(path: string) => {
			const workspace = workspaces.find((candidate) => candidate.path === path);
			if (!workspace) return;
			closePalette();
			void navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } });
		},
		[closePalette, navigate, workspaces],
	);

	const resetAfterClose = useCallback(() => {
		if (closeResetTimerRef.current !== null) {
			window.clearTimeout(closeResetTimerRef.current);
			closeResetTimerRef.current = null;
		}
		if (!useUiStore.getState().isCommandPaletteOpen) resetTransient();
	}, [resetTransient]);

	useEffect(
		() => () => {
			if (closeResetTimerRef.current !== null) window.clearTimeout(closeResetTimerRef.current);
		},
		[],
	);

	const handlePaletteAnimationEnd = useCallback(
		(event: AnimationEvent<HTMLDivElement>) => {
			if (event.target === event.currentTarget) resetAfterClose();
		},
		[resetAfterClose],
	);

	const popView = useCallback(() => {
		const current = viewRef.current;
		if (current.mode === "file-search" && current.parent === "picker") {
			setView({ mode: "file-session-picker", projectId: current.target.projectId });
		} else if ("parent" in current && typeof current.parent === "object") {
			setView(current.parent);
		} else {
			setView({ mode: "root" });
		}
		resetTransient();
	}, [resetTransient]);

	const pushView = useCallback(
		(next: PaletteView) => {
			setView(next);
			resetTransient();
		},
		[resetTransient],
	);

	const requestDismiss = useCallback(
		(target: "pop" | "close") => {
			if (target === "close" || viewRef.current.mode === "root") {
				closePalette();
			} else {
				popView();
			}
		},
		[closePalette, popView],
	);

	useEffect(() => {
		if (view.mode === "session-actions" && !scoped) {
			setView({ mode: "root" });
			setQuery("");
			setSelectedValue("");
			setError(t("command.sessionUnavailable"));
		}
	}, [view, scoped, t]);

	const toggleTheme = useCallback(() => {
		setThemePreference(resolvedTheme === "dark" ? "light" : "dark");
	}, [resolvedTheme, setThemePreference]);

	const navigateToTarget = useCallback(
		(target: NavigateTarget) => {
			switch (target.to) {
				case "/":
				case "/usage":
				case "/automations":
				case "/artifacts":
					void navigate({ to: target.to });
					break;
				case "/settings":
					useUiStore.getState().openGlobalSettings();
					break;
				case "/sessions/$sessionId":
					void navigate({ to: target.to, params: target.params });
					break;
				case "/projects/$projectId":
					void navigate({ to: target.to, params: target.params });
					break;
				case "/projects/$projectId/settings":
					useUiStore.getState().openProjectSettings(target.params.projectId, { cloudOrgId: workspaces.find((workspace) => workspace.id === target.params.projectId)?.cloudOrgId });
					break;
				case "/projects/$projectId/sessions/$sessionId":
				case "/host/$hostId/project/$projectId/session/$sessionId":
				case "/host/$hostId/session/$sessionId":
					void navigate({ to: target.to, params: target.params });
					break;
			}
		},
		[navigate, workspaces],
	);

	const sessionRoute = useCallback(
		(projectId: string, sessionId: string): Extract<NavigateTarget, { to: "/sessions/$sessionId" | "/projects/$projectId/sessions/$sessionId" }> => {
			return !projectId || projectId === STANDALONE_WORKSPACE_ID
				? { to: "/sessions/$sessionId", params: { sessionId } }
				: { to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } };
		},
		[],
	);

	const fileSessionRoute = useCallback((target: WorkspaceFileSearchTarget): NavigateTarget => {
		if (target.hostId) {
			return target.projectId === STANDALONE_WORKSPACE_ID
				? { to: "/host/$hostId/session/$sessionId", params: { hostId: target.hostId, sessionId: target.sessionId } }
				: {
					to: "/host/$hostId/project/$projectId/session/$sessionId",
					params: { hostId: target.hostId, projectId: target.projectId, sessionId: target.sessionId },
				};
		}
		return sessionRoute(target.projectId, target.sessionId);
	}, [sessionRoute]);

	const blockedByRestart = useCallback((projectId: string) => {
		if (!useUiStore.getState().restartingProjectIds.has(projectId)) return false;
		setError(t("command.orchestratorRestarting"));
		return true;
	}, [t]);

	const openOrchestrator = useCallback(
		async (projectId: string, isCurrentRun: () => boolean) => {
			if (blockedByRestart(projectId)) return;
			const orchestrator = findProjectOrchestrator(workspaces, projectId);
			if (orchestrator) {
				navigateToTarget(sessionRoute(projectId, orchestrator.id));
				closePalette();
				return;
			}
			const workspace = workspaces.find((candidate) => candidate.id === projectId);
			if (workspace?.kind === "cloud") {
				const sessionId = await spawnCloudOrchestrator(queryClient, projectId);
				await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
				navigateToTarget(sessionRoute(projectId, sessionId));
				if (isCurrentRun()) closePalette();
				return;
			}
			if (!hasConfiguredOrchestratorAgent(workspace)) {
				if (workspace) {
					navigateToTarget({ to: "/projects/$projectId/settings", params: { projectId } });
					closePalette();
				}
				return;
			}
			const sessionId = await spawnOrchestrator(projectId, "command_palette");
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			navigateToTarget(sessionRoute(projectId, sessionId));
			if (isCurrentRun()) closePalette();
		},
		[workspaces, navigateToTarget, queryClient, closePalette, blockedByRestart, sessionRoute],
	);

	const resumeSession = useCallback(
		async (sessionId: string) => {
			const result = await restoreSessionById(sessionId);
			if (result.status === "success") return null;
			if (result.status === "not_resumable") return t("command.resumeNotResumable");
			return result.message;
		},
		[restoreSessionById, t],
	);

	const runAction = useCallback(
		async (item: CommandItemModel) => {
			const action = item.action;
			if (!action) return;
			setError(null);
			const owner = Symbol(item.id);
			pendingRef.current = owner;
			setPendingId(item.id);
			const generation = runGenerationRef.current;
			const isCurrentRun = () => runGenerationRef.current === generation;
			try {
				const blockedTask = action.kind === "open-new-task" && (
					useUiStore.getState().restartingProjectIds.has(action.projectId)
					|| useUiStore.getState().provisioningProjectIds.has(sessionUiKey(action.projectId, params.hostId))
				);
				const taskOpensModal = action.kind === "open-new-task"
					&& workspaces.some((workspace) => workspace.id === action.projectId && workspace.kind === CLOUD_PROJECT_KIND);
				if (opensModal(action, taskOpensModal) && !blockedTask) beginModalHandoff();
				switch (action.kind) {
					case "navigate":
						navigateToTarget(action.target);
						closePalette();
						break;
					case "toggle-theme":
						toggleTheme();
						closePalette();
						break;
					case "copy-branch":
						await aoBridge.clipboard.writeText(action.branch);
						if (!isCurrentRun()) break;
						closePalette();
						break;
					case "open-shared-link":
						closePalette();
						requestOpenShareLink();
						break;
					case "report-problem": {
						const failure = await startProblemReport(daemonStatus.state);
						if (failure) throw new Error(failure);
						if (!isCurrentRun()) break;
						closePalette();
						break;
					}
				case "open-pr":
					await aoBridge.app.openExternal(action.url);
					if (!isCurrentRun()) break;
					closePalette();
					break;
				case "copy-pr-url":
					await aoBridge.clipboard.writeText(action.url);
					if (!isCurrentRun()) break;
					closePalette();
					break;
				case "trigger-review": {
					const { error: triggerError } = await apiClient.POST("/api/v1/sessions/{sessionId}/reviews/trigger", {
						params: { path: { sessionId: action.sessionId } },
					});
					if (triggerError) throw apiErrorException(triggerError, "Unable to start review");
					await queryClient.invalidateQueries({ queryKey: ["session-reviews", action.sessionId] });
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
					if (!isCurrentRun()) break;
					closePalette();
					break;
				}
				case "open-session-actions":
					pushView({ mode: "session-actions", sessionId: action.sessionId });
					break;
				case "open-view":
					pushView({ ...action.view, parent: viewRef.current });
					if (action.view.mode === "menu-submenu") findSubmenuOwner(sessionMenuEntries, action.view.entryId)?.onOpenChange?.(true);
					break;
				case "run":
					action.run();
					if (!item.keepOpen) closePalette();
					break;
				case "open-file-search":
					if (action.target) {
						pushView({
							mode: "file-search",
							target: action.target,
							parent: viewRef.current.mode === "file-session-picker" ? "picker" : "root",
						});
					} else {
						pushView({ mode: "file-session-picker", projectId: action.projectId });
					}
					break;
				case "open-workspace-file": {
					useUiStore.getState().requestWorkspaceFileOpen(
						action.target.sessionId,
						action.path,
						action.target.hostId,
						undefined,
						action.line,
					);
					const alreadyOpen = params.sessionId === action.target.sessionId
						&& (params.hostId ?? "") === (action.target.hostId ?? "");
					if (!alreadyOpen) navigateToTarget(fileSessionRoute(action.target));
					closePalette();
					break;
				}
				case "open-chat-message": {
					const workspace = findChatWorkspace(action.sessionId);
					if (!workspace || !action.sessionId) {
						setError(t("command.chatSessionUnavailable"));
						break;
					}
					navigateToTarget(sessionRoute(workspace.id, action.sessionId));
					useUiStore.getState().requestChatMessageFocus(action.sessionId, action.messageId, action.text, action.reviewId);
					closePalette();
					break;
				}
				case "open-settings-section":
					closePalette();
					useUiStore.getState().openGlobalSettings(action.section, action.focus ? { focusSetting: action.focus } : undefined);
					break;
				case "open-session-import":
					closePalette();
					if (action.projectId) useSessionImportStore.getState().openProjectImport(action.projectId);
					else useSessionImportStore.getState().openFolderImport();
					break;
				case "run-shortcut": {
					closePalette();
					const shortcut = action.shortcut;
					window.setTimeout(() => runAppShortcut({ id: shortcut }), 0);
					break;
				}
				case "resume-session": {
					const message = await resumeSession(action.sessionId);
					if (!isCurrentRun()) break;
					if (message) {
						setError(message);
						break;
					}
					navigateToTarget(sessionRoute(action.projectId, action.sessionId));
						closePalette();
						break;
					}
					case "open-search-result": {
						revealSessionSearchMatch(action);
						navigateToTarget(sessionRoute(action.projectId, action.sessionId));
						closePalette();
						break;
					}
					case "copy-resume-command":
						await aoBridge.clipboard.writeText(action.command);
						closePalette();
						break;
					case "open-new-task":
						if (blockedByRestart(action.projectId)) break;
						closePalette();
						useUiStore.getState().requestNewTask(action.projectId, params.hostId);
						break;
					case "open-new-project":
						openNewProject();
						break;
					case "open-orchestrator":
							await openOrchestrator(action.projectId, isCurrentRun);
							break;
					case "open-archive": {
						const returnFocus = returnFocusRef.current;
						useUiStore.getState().openArchive({
							...(action.projectId ? { projectId: action.projectId } : {}),
							hostId: params.hostId,
							returnFocus: returnFocus instanceof HTMLElement ? returnFocus : null,
						});
						closePalette();
						break;
					}
				}
			} catch (err) {
				if (isCurrentRun()) setError(err instanceof Error ? err.message : t("command.failed"));
			} finally {
				if (pendingRef.current === owner) {
					pendingRef.current = null;
					setPendingId(null);
				}
			}
		},
		[daemonStatus.state, findChatWorkspace, sessionMenuEntries, navigate, navigateToTarget, closePalette, toggleTheme, openOrchestrator, resumeSession, pushView, blockedByRestart, openNewProject, queryClient, t, params.hostId, params.sessionId, fileSessionRoute, sessionRoute, workspaces],
	);

	const onSelectItem = useCallback(
		(item: CommandItemModel, secondary = false) => {
			const chosen = secondary ? { ...item, action: item.secondaryAction } : item;
			if (chosen.disabled || !chosen.action) return;
			if (pendingRef.current) return;
			void runAction(chosen);
		},
		[runAction],
	);

	useAppShortcut(
		"command-palette",
		() => {
			if (isOpen) {
				requestDismiss("close");
				return;
			}
			if (isDialogOrMenuOpen()) return;
			setOpen(true);
		},
		enabled,
	);
	useAppShortcut(
		"quick-open-file",
		() => {
			if (isOpen) {
				requestDismiss("close");
				return;
			}
			if (isDialogOrMenuOpen()) return;
			resetTransient();
			setView(currentFileTarget ? { mode: "file-search", target: currentFileTarget, parent: "root" } : { mode: "root" });
			setOpen(true);
		},
		enabled,
	);

	useEffect(() => {
		if (!enabled || !isOpen) return;
		const swallowIndexedChords = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
				event.preventDefault();
				event.stopPropagation();
			}
		};
		window.addEventListener("keydown", swallowIndexedChords, true);
		return () => window.removeEventListener("keydown", swallowIndexedChords, true);
	}, [enabled, isOpen]);


	const keybindingOverrides = useKeybindingsStore((store) => store.overrides);
	const shortcutKeys = useCallback((id: AppShortcutId) => {
		const binding = effectiveShortcutBindings(id, isMac, keybindingOverrides)[0];
		return binding ? shortcutBindingKeys(binding, isMac) : undefined;
	}, [keybindingOverrides]);

	if (!enabled) return null;

	const fileSearchSession = view.mode === "file-search"
		? workspaces
			.find((workspace) => workspace.id === view.target.projectId
				&& (workspace.hostId ?? "") === (view.target.hostId ?? ""))
			?.sessions.find((session) => session.id === view.target.sessionId)
		: undefined;
	const contextLabel =
		view.mode === "session-actions" || view.mode === "rename-session"
			? (scoped?.session.title ?? menuSession?.title ?? t("command.sessionFallback"))
			: view.mode === "menu-submenu" || view.mode === "choices" || view.mode === "go-to"
				? view.title
				: view.mode === "project-actions"
					? (workspaces.find((workspace) => workspace.id === view.projectId)?.name ?? "")
			: view.mode === "file-session-picker"
					? t("command.chooseFileSession")
					: view.mode === "file-search"
						? (fileSearchSession?.title ?? t("command.sessionFallback"))
						: "";
	const typedQuery = query.trim();
	const fileSearchActive = view.mode === "file-search"
		|| (view.mode === "root" && Boolean(activeFileTarget) && typedQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS);
	const searchCatchingUp = typedQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS
		&& normalizedFileQuery !== typedQuery
		&& (view.mode === "root" || view.mode === "file-search");
	const searching = searchCatchingUp
		|| (fileSearchEnabled && fileSearchQuery.isFetching)
		|| (contentSearchEnabled && contentSearchQuery.isFetching)
		|| (chatSearchEnabled && chatSearchQuery.isFetching);
	const fileSearchError = fileSearchActive && !searchCatchingUp && fileSearchQuery.error
		? (fileSearchQuery.error instanceof Error ? fileSearchQuery.error.message : t("command.searchFilesError"))
		: null;
	const contentSearchError = contentSearchEnabled && !searchCatchingUp && contentSearchQuery.error
		? (contentSearchQuery.error instanceof Error ? contentSearchQuery.error.message : t("command.searchContentError"))
		: null;
	const chatSearchError = chatSearchEnabled && !searchCatchingUp && chatSearchQuery.error
		? (chatSearchQuery.error instanceof Error ? chatSearchQuery.error.message : t("command.searchChatsError"))
		: null;
	const sessionSearchError = sessionSearchEnabled && sessionSearchSettled && sessionSearchQuery.error
		? (sessionSearchQuery.error instanceof Error ? sessionSearchQuery.error.message : t("command.searchSessionsError"))
		: null;
	const notices = [...new Set([error, fileSearchError, contentSearchError, chatSearchError, sessionSearchError].filter((message): message is string => Boolean(message)))];
	const selectedItem = visibleItems.find((item) => item.id === value);
	const scopeSessionTitle = view.mode === "file-search"
		? (fileSearchSession?.title ?? t("command.sessionFallback"))
		: view.mode === "root" && activeFileTarget
			? (currentSession?.title ?? t("command.sessionFallback"))
			: undefined;
	const scope = view.mode === "root"
		? [scopeSessionTitle ? t("command.scopeFiles", { session: scopeSessionTitle }) : null, t("command.scopeChats")].filter(Boolean).join(" · ")
		: "";
	const backButton = view.mode === "root" ? undefined : (
		<Button
			type="button"
			onClick={() => requestDismiss("pop")}
			className="-m-1"
			aria-label={t("command.back")}
			size="icon-xs"
			variant="quiet"
		>
			<ArrowLeft className="size-icon-base" aria-hidden="true" />
		</Button>
	);

	return (
		<>
				<CommandDialog
					modal={false}
					open={isOpen}
					title={t("command.palette")}
					description={t("command.searchEverywherePlaceholder")}
				onOpenChange={(open) => (open ? setOpen(true) : requestDismiss("close"))}
					contentProps={{
						onAnimationEnd: handlePaletteAnimationEnd,
					onEscapeKeyDown: (event) => {
						event.preventDefault();
						if (event.isComposing) return;
						requestDismiss(viewRef.current.mode === "root" ? "close" : "pop");
					},
				}}
				commandProps={{
					shouldFilter: false,
					value: valueResync ? `${value} ` : value,
					onValueChange: (next: string) => {
						if (userPickRef.current) setSelectedValue(next);
						else if (next !== value) setValueResync((on) => !on);
					},
					onKeyDownCapture: (event) => {
						if (isRowNavigationKey(event)) userPickRef.current = true;
					},
						onPointerMoveCapture: (event) => {
							if (event.target instanceof Element && event.target.closest("[cmdk-item]:not([aria-disabled='true'])")) {
								userPickRef.current = true;
							}
					},
					loop: true,
					label: contextLabel || t("command.palette"),
				}}
			>
				<>
						<CommandInput
							ref={inputRef}
							value={query}
							onValueChange={(next) => {
								setQuery(next);
								setError(null);
								userPickRef.current = false;
								setSelectedValue("");
							}}
							leading={backButton}
							trailing={searching
								? <Loader2 className="size-icon-md shrink-0 animate-spin text-muted-foreground" role="status" aria-label={t("command.searching")} />
								: null}
							placeholder={
								view.mode === "session-actions" || view.mode === "project-actions" || view.mode === "menu-submenu" || view.mode === "choices" || view.mode === "go-to"
									? `${contextLabel} · ${t("command.searchActionsPlaceholder")}`
									: view.mode === "file-session-picker"
										? t("command.chooseFileSession")
										: view.mode === "file-search"
											? t("command.searchFiles")
											: t("command.searchEverywherePlaceholder")
							}
							onKeyDown={(event) => {
								if (event.nativeEvent.isComposing) return;
								const wantsSecondary = (event.key === "Enter" && (event.metaKey || event.ctrlKey))
									|| (event.key === "ArrowRight" && !event.shiftKey && !event.metaKey && !event.altKey && !event.ctrlKey
										&& event.currentTarget.selectionStart === query.length);
								const cmdkSelectedId = event.currentTarget.closest("[cmdk-root]")
									?.querySelector("[cmdk-item][aria-selected='true']")?.getAttribute("data-value");
								const cmdkSelected = visibleItems.find((item) => item.id === cmdkSelectedId);
								if ((event.key === "Enter" || wantsSecondary) && cmdkSelected && !userPickRef.current && !autoSelectable(cmdkSelected)) {
									event.preventDefault();
									event.stopPropagation();
									return;
								}
								if (wantsSecondary && selectedItem?.secondaryAction) {
									event.preventDefault();
									event.stopPropagation();
									onSelectItem(selectedItem, true);
									return;
								}
								if (wantsSecondary && event.key === "ArrowRight" && selectedItem && !selectedItem.disabled && rowOpensList(selectedItem)) {
									event.preventDefault();
									event.stopPropagation();
									onSelectItem(selectedItem);
									return;
								}
								if (event.key === "Backspace" && query === "" && viewRef.current.mode !== "root") {
									event.preventDefault();
									requestDismiss("pop");
								}
								if (event.key === "ArrowLeft" && !event.shiftKey && !event.metaKey && !event.altKey && !event.ctrlKey
									&& viewRef.current.mode !== "root"
									&& event.currentTarget.selectionStart === 0 && event.currentTarget.selectionEnd === 0) {
									event.preventDefault();
									event.stopPropagation();
									requestDismiss("pop");
								}
							}}
						/>
						{notices.length > 0 && (
							<div
								role="alert"
								className="mx-[var(--size-command-item-inset)] mt-2 flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-control wrap-break-word text-destructive"
							>
								<CircleAlert className="mt-0.5 size-icon-md shrink-0" aria-hidden="true" />
								<span className="min-w-0">{notices.join(" ")}</span>
							</div>
						)}
						<CommandList>
							<CommandEmpty>
								{view.mode === "file-search"
									? typedQuery.length < PALETTE_FILE_SEARCH_MIN_CHARS
										? t("command.searchFilesHint")
										: searching
											? t("command.searchFilesLoading")
											: t("command.searchFilesEmpty")
									: searching
										? t("command.searching")
										: view.mode === "root" ? (
											<>
												<span className="block text-foreground">{t("command.noResults")}</span>
												<span className="mt-1 block text-caption">{t("command.noResultsHint")}</span>
											</>
										) : t("command.noResults")}
							</CommandEmpty>
							{groups.map((group) => (
								<CommandGroup key={group.id} heading={(group.id === "current" && actionsHeading) || (group.id === "session" && sessionHeading) || group.label || undefined}>
									{group.items.map((item) => (
										<PaletteRow
											key={item.id}
											item={item}
											query={typedQuery}
											keys={item.shortcut ? shortcutKeys(item.shortcut) : undefined}
											pending={pendingId === item.id}
											blocked={pendingId !== null && pendingId !== item.id}
											onSelect={() => onSelectItem(item)}
										/>
									))}
								</CommandGroup>
							))}
							{sessionSearchEnabled && sessionSearchSettled && sessionSearchQuery.isFetching && (
								<div role="status" className="px-[var(--size-command-pad-x)] py-2 text-caption text-muted-foreground">{t("command.searchingSessions")}</div>
							)}
							{sessionSearchEnabled && sessionSearchSettled && !sessionSearchQuery.error && sessionSearchItems.length === 0 && !sessionSearchQuery.isFetching && (
								<div role="status" className="px-[var(--size-command-pad-x)] py-2 text-caption text-muted-foreground">
									{sessionSearchIndexStatus.data?.state === "backfilling"
										? t("command.searchSessionsBackfilling", { count: sessionSearchIndexStatus.data.pendingSources })
										: sessionSearchIndexStatus.data?.state === "unavailable"
											? t("command.searchSessionsUnavailable")
											: t("command.noSessionMatches")}
									{sessionSearchIndexStatus.data?.unindexedHarnesses?.length
										? ` ${t("command.searchSessionsUnindexed", { harnesses: sessionSearchIndexStatus.data.unindexedHarnesses.join(", ") })}`
										: ""}
								</div>
							)}
						</CommandList>
						{view.mode === "file-search" && fileSearchEnabled && !searchCatchingUp && fileSearchQuery.data?.truncated && (
							<div role="status" className="px-[var(--size-command-pad-x)] py-2 text-caption text-muted-foreground">{t("command.searchFilesTruncated")}</div>
						)}
						<CommandFooter aria-hidden="true">
							<FooterHint keys={["↑", "↓"]} label={t("command.navigate")} />
							{selectedItem && <FooterHint keys={["Enter"]} label={t("command.open")} />}
							{selectedItem?.secondaryAction && (
								<FooterHint
									keys={[isMac ? "⌘" : "Ctrl", "Enter"]}
									label={selectedItem.secondaryAction.kind === "copy-resume-command" ? t("command.copyResumeCommand") : t("command.actions")}
								/>
							)}
							<FooterHint keys={["Esc"]} label={view.mode === "root" ? t("command.close") : t("command.back")} />
							<span className="ml-auto min-w-0 truncate">{scope}</span>
						</CommandFooter>
					</>
			</CommandDialog>

			<SessionArchiveDialog
				open={archiveTarget !== null}
				session={archiveTarget ?? undefined}
				onOpenChange={(open) => { if (!open) setArchiveTarget(null); }}
				onConfirm={({ deleteArtifacts }) => {
					if (archiveTarget) terminateSession({ ...archiveTarget, deleteArtifacts });
					setArchiveTarget(null);
				}}
			/>
			<ConfirmDialog
				open={removeTarget !== null}
				title={t("shell.removeProjectTitle")}
				description={removeTarget?.kind === CLOUD_PROJECT_KIND ? t("shell.deleteCloudProjectLead", { name: removeTarget.name }) : t("shell.removeProjectBody")}
				confirmLabel={t("shell.remove")}
				destructive
				onOpenChange={(open) => { if (!open) setRemoveTarget(null); }}
				onConfirm={() => {
					if (removeTarget) removeProject?.(removeTarget.id, removeTarget.kind === CLOUD_PROJECT_KIND);
					setRemoveTarget(null);
					closePalette();
				}}
			/>
			{createProjectFlowMounted ? (
				<CreateProjectFlow
					mode="choose"
					openSignal={createProjectFlowOpenSignal}
					onCloneProject={cloneProject}
					onCreateProject={createProject}
					onCreateStandaloneAgent={() => useUiStore.getState().requestNewTask(STANDALONE_WORKSPACE_ID)}
					onInitializeProject={initializeProjectRepository}
					onOpenExistingProject={openExistingProject}
					existingProjectPaths={workspaces.map((workspace) => workspace.path)}
					existingProjectNames={workspaces.map((workspace) => workspace.name)}
				/>
			) : null}
		</>
	);
});

function opensModal(action: NonNullable<CommandItemModel["action"]>, taskOpensModal: boolean): boolean {
	switch (action.kind) {
		case "open-new-task":
			return taskOpensModal;
		case "open-new-project":
		case "open-session-import":
		case "open-archive":
		case "open-shared-link":
		case "open-settings-section":
			return true;
		case "navigate":
			return action.target.to === "/settings" || action.target.to === "/projects/$projectId/settings";
		case "run-shortcut":
			return action.shortcut === "keyboard-shortcuts";
		default:
			return false;
	}
}

function FooterHint({ keys, label }: { keys: readonly string[]; label: string }) {
	return (
		<span className="inline-flex shrink-0 items-center gap-1.5">
			<CommandShortcut keys={keys} variant="caps" />
			<span>{label}</span>
		</span>
	);
}

const PATH_SUBTITLE_GROUPS = new Set<CommandItemModel["group"]>(["files", "content"]);

function PaletteRow({
	item,
	query,
	keys,
	pending,
	blocked,
	onSelect,
}: {
	item: CommandItemModel;
	query: string;
	keys?: readonly string[];
	pending: boolean;
	blocked: boolean;
	onSelect: () => void;
}) {
	const { t } = useTranslation();
	const Icon = iconForCommand(item);
	const pathSubtitle = PATH_SUBTITLE_GROUPS.has(item.group);
	const opensPicker = rowOpensList(item);
	return (
		<CommandItem
			value={item.id}
			disabled={item.disabled || blocked}
			onSelect={onSelect}
			className={item.snippet ? "items-start" : undefined}
		>
			<span className={cn("flex size-icon-base shrink-0 items-center justify-center", item.snippet && "mt-0.75")} aria-hidden="true">
				{item.status ? (
					<span className={cn("size-2 rounded-full", item.status.className, item.status.breathe && "animate-status-pulse")} />
				) : item.icon ? (
					item.icon
				) : Icon ? (
					<Icon />
				) : null}
			</span>
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="flex min-w-0 items-baseline gap-2">
					<span className="max-w-full shrink-0 truncate" data-slot="command-item-title">
						{item.group === "content" && item.title.includes(":") ? (
							<>
								{item.title.slice(0, item.title.lastIndexOf(":"))}
								<span className="text-muted-foreground">{item.title.slice(item.title.lastIndexOf(":"))}</span>
							</>
						) : (
							<Highlighted text={item.title} spans={item.contentMatch ? [] : titleHighlights(query, item.title)} />
						)}
					</span>
					{item.subtitle ? (
						pathSubtitle ? (
							<span dir="rtl" className="min-w-0 truncate text-left text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">
								<bdi>{item.subtitle}</bdi>
							</span>
						) : (
							<span className="min-w-0 truncate text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">{item.subtitle}</span>
						)
					) : null}
				</span>
				{item.snippet ? <HighlightedSnippet snippet={item.snippet} /> : null}
			</span>
			{pending ? (
				<Loader2 className="size-icon-md animate-spin" aria-hidden="true" />
			) : item.disabled && item.disabledReason ? (
				<span className="max-w-[45%] shrink-0 truncate text-caption text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">{item.disabledReason}</span>
			) : item.toggled !== undefined ? (
				<span className="flex shrink-0 items-center">
					<span className="sr-only">{item.toggled ? t("command.on") : t("command.off")}</span>
					<Switch checked={item.toggled} tabIndex={-1} aria-hidden="true" className="pointer-events-none" />
				</span>
			) : item.checked ? (
				<Check className="size-icon-md" aria-label={t("command.on")} />
			) : keys ? (
				<CommandShortcut keys={keys} />
			) : item.state && !opensPicker ? (
				<span className="shrink-0 text-caption tabular-nums text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">{item.state}</span>
			) : opensPicker ? (
				<span className="flex shrink-0 items-center gap-1 text-caption text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">
					{item.state}
					<ChevronRight className="size-icon-md" aria-hidden="true" />
				</span>
			) : null}
		</CommandItem>
	);
}

const BLANK_SESSION_ACTIONS = new Set(["session-switch-interface", "menu:open-in", "menu:session-rename", "menu:session-resume", "menu:session-open-pr", "menu:session-archive"]);

function paletteMenuRows(entries: readonly MenuEntry[], sessionId: string): CommandItemModel[] {
	return menuEntryCommands(entries, sessionId).map((item) => (item.id === "menu:session-rename" ? { ...item, keepOpen: true } : item));
}

function rowOpensList(item: CommandItemModel): boolean {
	return (item.action?.kind === "open-file-search" && !item.action.target) || item.action?.kind === "open-view";
}

function Highlighted({ text, spans }: { text: string; spans: readonly { start: number; end: number }[] }) {
	if (spans.length === 0) return <>{text}</>;
	const parts: { text: string; hit: boolean }[] = [];
	let cursor = 0;
	for (const { start, end } of spans) {
		if (start < cursor || end > text.length || start >= end) continue;
		if (start > cursor) parts.push({ text: text.slice(cursor, start), hit: false });
		parts.push({ text: text.slice(start, end), hit: true });
		cursor = end;
	}
	if (cursor < text.length) parts.push({ text: text.slice(cursor), hit: false });
	return (
		<>
			{parts.map((part, index) => part.hit
				? <span key={index} className="font-semibold text-foreground">{part.text}</span>
				: <span key={index}>{part.text}</span>)}
		</>
	);
}

const SNIPPET_LEAD = 16;

function HighlightedSnippet({ snippet }: { snippet: NonNullable<CommandItemModel["snippet"]> }) {
	const parts: { text: string; hit: boolean }[] = [];
	const firstMatch = snippet.highlights[0]?.start ?? 0;
	const offset = firstMatch > SNIPPET_LEAD * 2 ? firstMatch - SNIPPET_LEAD : 0;
	if (offset > 0) parts.push({ text: "…", hit: false });
	let cursor = offset;
	for (const { start, end } of snippet.highlights) {
		if (start < cursor || end > snippet.text.length || start >= end) continue;
		if (start > cursor) parts.push({ text: snippet.text.slice(cursor, start), hit: false });
		parts.push({ text: snippet.text.slice(start, end), hit: true });
		cursor = end;
	}
	if (cursor < snippet.text.length) parts.push({ text: snippet.text.slice(cursor), hit: false });
	return (
		<span className={cn("truncate text-caption leading-body text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75", snippet.code && "font-mono")}>
			{parts.map((part, index) => part.hit
				? <span key={index} className="font-medium text-foreground">{part.text}</span>
				: <span key={index}>{part.text}</span>)}
		</span>
	);
}
