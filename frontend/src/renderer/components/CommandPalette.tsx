import { keepPreviousData, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ArrowLeft, ChevronRight, CircleAlert, Folder, Loader2 } from "lucide-react";
import { effectiveShortcutBindings, shortcutBindingKeys, type AppShortcutId } from "../../shared/shortcuts";
import { useCallback, useEffect, useMemo, useRef, useState, type AnimationEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useCommandPaletteEnabled } from "../hooks/useCommandPaletteEnabled";
import { useCloudCp } from "../hooks/useCloudCp";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { cloudSessionsQueryKey, useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { spawnCloudOrchestrator } from "../lib/cloud-orchestrator";
import {
	buildChatSearchCommands,
	buildCommands,
	buildContentSearchCommands,
	buildFileSessionCommands,
	buildSessionActions,
	buildWorkspaceFileCommands,
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
	commandPaletteContentSearchQueryOptions,
	commandPaletteFileSearchAvailable,
	commandPaletteFileSearchQueryOptions,
} from "../lib/command-palette-file-search";
import { iconForCommand } from "../lib/command-palette-icons";
import { buildSettingsCommands, buildShortcutCommands } from "../lib/palette-registry";
import { isDialogOrMenuOpen } from "../lib/dom-selectors";
import { sessionReviewsQueryOptions, type PRReviewState } from "../lib/session-reviews";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { useShell } from "../lib/shell-context";
import { sessionUiKey } from "../lib/hosts";
import { beginModalHandoff } from "../lib/modal-handoff";
import { isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { useKeybindingsStore } from "../stores/keybindings-store";
import {
	CLOUD_PROJECT_KIND,
	findProjectOrchestrator,
	hasConfiguredOrchestratorAgent,
	openPRs,
	STANDALONE_WORKSPACE_ID,
	workerSessions,
} from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { runAppShortcut, useAppShortcut } from "../hooks/useAppShortcut";
import { CreateProjectFlow } from "./CreateProjectFlow";
import { requestOpenShareLink } from "./OpenShareLinkDialog";
import { CommandDialog, CommandEmpty, CommandFooter, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut } from "./ui/command";

const PALETTE_REVIEW_STALE_TIME_MS = 60_000;
const PALETTE_REVIEW_DEFER_MS = 120;
const PALETTE_FILE_SEARCH_DEBOUNCE_MS = 150;
/** Rows the file view lists; beside commands each search group shows a few. */
const PALETTE_FILE_VIEW_ROWS = 50;
const isMac = isMacPlatform();
const PALETTE_FILE_SEARCH_MIN_CHARS = 2;
const EMPTY_REVIEW_STATES: Readonly<Record<string, PRReviewState[]>> = {};
type PaletteView =
	| { mode: "root" }
	| { mode: "session-actions"; sessionId: string }
	| { mode: "file-session-picker"; projectId: string }
	| { mode: "file-search"; target: WorkspaceFileSearchTarget; parent: "root" | "picker" };

/**
 * Whether two search keys differ only in their query text, so an answer for
 * the previous key belongs to the same host, org and session.
 */
function sameSearchTarget(previous: readonly unknown[] | undefined, current: readonly unknown[], query: string): boolean {
	if (!previous || previous.length !== current.length) return false;
	const queryAt = current.lastIndexOf(query);
	return queryAt >= 0 && previous.every((part, index) => index === queryAt || part === current[index]);
}

/** Keys cmdk moves the selection with: arrows, Home/End and its Ctrl+N/J/P/K. */
function isRowNavigationKey(event: { key: string; ctrlKey: boolean }): boolean {
	return ["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)
		|| (event.ctrlKey && ["n", "j", "p", "k"].includes(event.key));
}

export function CommandPalette() {
	const { i18n, t } = useTranslation();
	const enabled = useCommandPaletteEnabled();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const cloud = useCloudCp();
	const cloudReady = cloud.ready;
	const restoreSessionById = useRestoreSession();
	const params = useParams({ strict: false }) as { hostId?: string; projectId?: string; sessionId?: string };
	const { cloneProject, createProject, initializeProjectRepository } = useShell();
	const resolvedTheme = useUiStore((s) => s.resolvedTheme);
	const setThemePreference = useUiStore((s) => s.setThemePreference);
	const isOpen = useUiStore((s) => s.isCommandPaletteOpen);
	const setOpen = useUiStore((s) => s.setCommandPaletteOpen);
	const restartingProjectIds = useUiStore((s) => s.restartingProjectIds);
	const developerMode = useUiStore((s) => s.developerMode);
	const diagnostics = useUiStore((s) => s.developerMode && s.diagnostics);
	// The palette stays mounted to preserve its close animation and global
	// shortcut. While closed, commands are invisible, so retain the cached
	// snapshot without subscribing this hidden surface to streamed updates.
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
	// The action that holds the palette, so a finished older one can't release a
	// newer one that is still running.
	const pendingRef = useRef<symbol | null>(null);
	// cmdk selects the first row by itself whenever the query changes; only a
	// navigation key or pointer move counts as the user choosing one.
	const userPickRef = useRef(false);
	const runGenerationRef = useRef(0);
	const viewRef = useRef(view);
	viewRef.current = view;
	const closeResetTimerRef = useRef<number | null>(null);
	const inputRef = useRef<HTMLInputElement>(null);
	// Where focus was when the palette opened, so closing hands it back.
	const returnFocusRef = useRef<Element | null>(null);
	useEffect(() => useUiStore.subscribe((state, previous) => {
		if (state.isCommandPaletteOpen && !previous.isCommandPaletteOpen) returnFocusRef.current = document.activeElement;
	}), []);
	// Every list view types into the search field: on open, after Back and
	// after leaving New task. The dialog's focus trap would otherwise pick
	// its first button.
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
	const fileSearchEnabled = isOpen
		&& normalizedFileQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS
		&& commandPaletteFileSearchAvailable(activeFileTarget, cloud.ready);
	const fileSearchOptions = commandPaletteFileSearchQueryOptions({
		target: activeFileTarget ?? { projectId: "", sessionId: "" },
		query: normalizedFileQuery,
		errorMessage: t("command.searchFilesError"),
		cloud,
	});
	// Each search keeps its last answer on screen until the next one lands, so
	// typing never blanks the rows the user is reading. File answers are kept
	// only for the same target (host, org and session): another target's paths
	// would open in this one.
	const fileSearchQuery = useQuery<CommandPaletteFileSearchResponse>({
		...fileSearchOptions,
		enabled: fileSearchEnabled,
		placeholderData: (previous, previousQuery) =>
			sameSearchTarget(previousQuery?.queryKey, fileSearchOptions.queryKey, normalizedFileQuery) ? previous : undefined,
	});
	// File contents (this session's daemon) and chats (this machine) are searched
	// beside file names; Cloud sessions have no daemon to search their contents.
	const searchQueryReady = isOpen && normalizedFileQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS;
	const contentTarget = activeFileTarget && !activeFileTarget.cloudOrgId ? activeFileTarget : undefined;
	const contentSearchEnabled = searchQueryReady && (view.mode === "root" || view.mode === "file-search") && Boolean(contentTarget);
	const contentSearchOptions = commandPaletteContentSearchQueryOptions(contentTarget ?? { projectId: "", sessionId: "" }, normalizedFileQuery, t("command.searchContentError"));
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

	const sessionsWithOpenPRs = useMemo(
		() =>
			(reviewActionsReady ? workspaces : []).flatMap((workspace) =>
				workerSessions(workspace.sessions).filter((session) => openPRs(session).length > 0),
			),
		[reviewActionsReady, workspaces],
	);
	// Opening the palette must first acknowledge the shortcut and focus its input.
	// Review actions are secondary and may require one query per PR-bearing session,
	// so defer their setup until the palette has had a chance to paint.
	useEffect(() => {
		if (!isOpen) {
			setReviewActionsReady(false);
			return;
		}
		const timer = window.setTimeout(() => setReviewActionsReady(true), PALETTE_REVIEW_DEFER_MS);
		return () => window.clearTimeout(timer);
	}, [isOpen]);
	// Review states are fetched only while the palette is open; the shared query
	// key means sessions already viewed in the inspector reuse the cached data.
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

	// Each session's review action stays hidden until we know whether it's
	// safe to trigger, then is frozen for the rest of this open — merging in
	// newly-resolved sessions but never overwriting one already captured, so
	// a later poll (e.g. a running review completing) can't retitle or
	// re-sort a row the user may already have selected.
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
			}, t),
			...buildShortcutCommands(t),
			...buildSettingsCommands(t, { developerMode, diagnostics }),
		],
		[workspaces, currentProjectId, params.hostId, params.sessionId, restartingProjectIds, reviewStatesForCommands, cloudReady, developerMode, diagnostics, t, i18n.resolvedLanguage],
	);
	const scoped = useMemo(
		() => (view.mode === "session-actions" ? findSession(workspaces, view.sessionId) : undefined),
		[view, workspaces],
	);
	const sessionActionItems = useMemo(
		() => (scoped ? buildSessionActions(scoped.workspace, scoped.session, t) : []),
		[scoped, t],
	);
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
	const chatItems = useMemo(
		() => chatSearchEnabled && chatSearchQuery.data
			? buildChatSearchCommands(
				chatSearchQuery.data.results.filter((hit) => findLocalChatWorkspace(hit.sessionId)),
				t,
				(hit) => {
					const workspace = findLocalChatWorkspace(hit.sessionId);
					return {
						title: workspace?.sessions.find((session) => session.id === hit.sessionId)?.title,
						place: workspace?.name,
					};
				},
			)
			: [],
		[chatSearchEnabled, chatSearchQuery.data, findLocalChatWorkspace, t],
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
		return displayGroups([...rootItems, ...fileItems, ...contentItems, ...chatItems], query, t);
	}, [view.mode, rootItems, sessionActionItems, fileSessionItems, fileItems, contentItems, chatItems, query, normalizedFileQuery, t, i18n.resolvedLanguage]);

	const visibleItems = useMemo(() => groups.flatMap((group) => group.items), [groups]);
	// Rows a search found for an earlier query stay visible while the next one
	// answers, but are never picked for Enter on their own.
	const searchStale = normalizedFileQuery !== query.trim()
		|| fileSearchQuery.isPlaceholderData
		|| contentSearchQuery.isPlaceholderData
		|| chatSearchQuery.isPlaceholderData;
	const autoSelectable = (item: CommandItemModel) => !item.disabled && !(searchStale && isSearchGroup(item.group));
	const value =
		(visibleItems.some((item) => item.id === selectedValue && !item.disabled)
			? selectedValue
			: (visibleItems.find(autoSelectable) ?? (searchStale ? undefined : visibleItems[0]))?.id) ?? "";

	const resetTransient = useCallback(() => {
		runGenerationRef.current += 1;
		setQuery("");
		setDebouncedQuery("");
		setSelectedValue("");
		setError(null);
	}, []);

	const closePalette = useCallback(() => {
		// Keep the current query visible while Radix plays the closing animation.
		// Clearing it here causes the palette to flash an empty search before it
		// is removed, especially when an action also changes the theme.
		runGenerationRef.current += 1;
		// An action still running belongs to the closed palette; the next open
		// must not have its rows blocked by it.
		pendingRef.current = null;
		setPendingId(null);
		setOpen(false);
		setView({ mode: "root" });
		if (closeResetTimerRef.current !== null) window.clearTimeout(closeResetTimerRef.current);
		// Reduced-motion mode disables the closing animation, so keep a fallback
		// reset for environments where no animationend event will arrive.
		closeResetTimerRef.current = window.setTimeout(() => {
			closeResetTimerRef.current = null;
			if (!useUiStore.getState().isCommandPaletteOpen) resetTransient();
		}, 150);
		// Hand focus back to where it was (a terminal, the composer), unless the
		// command already moved it somewhere on purpose.
		const returnTo = returnFocusRef.current;
		returnFocusRef.current = null;
		window.setTimeout(() => {
			const active = document.activeElement;
			if (returnTo instanceof HTMLElement && returnTo.isConnected && (!active || active === document.body)) returnTo.focus();
		}, 160);
	}, [resetTransient, setOpen]);
	// The import flow has its own cloud/query/dialog subtree. Mounting it beside a
	// permanently retained palette made every palette render pay for an unrelated
	// feature. Mount it only after the user chooses New project, then pulse its
	// existing programmatic-open signal on the following commit (the flow seeds its
	// signal ref on mount, so doing both in one render would intentionally no-op).
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
					void navigate({ to: target.to });
					break;
				case "/settings":
					// Modal — do not route to /settings (that legacy path redirects home).
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
			return projectId === STANDALONE_WORKSPACE_ID
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
			// Cloud projects carry no local orchestrator-agent config; spawn the
			// orchestrator as a cloud session in its own sandbox instead of falling
			// through to the project-settings page.
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
			// A palette reopened while the spawn ran stays open.
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
				// Actions that open another modal hand the backdrop over to it. A New
				// task blocked by a restart keeps the palette open, so it has none.
				const blockedTask = action.kind === "open-new-task" && (
					useUiStore.getState().restartingProjectIds.has(action.projectId)
					|| useUiStore.getState().provisioningProjectIds.has(sessionUiKey(action.projectId, params.hostId))
				);
				// New task opens a modal only for a cloud project; others get a page.
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
					if (triggerError) throw new Error(apiErrorMessage(triggerError, "Unable to start review"));
					await queryClient.invalidateQueries({ queryKey: ["session-reviews", action.sessionId] });
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
					if (!isCurrentRun()) break;
					closePalette();
					break;
				}
				case "open-session-actions":
					pushView({ mode: "session-actions", sessionId: action.sessionId });
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
					const workspace = findLocalChatWorkspace(action.sessionId);
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
				case "run-shortcut": {
					closePalette();
					// After the palette has closed, so the action sees the screen
					// it was asked about, not the palette.
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
					case "open-new-task":
						if (blockedByRestart(action.projectId)) break;
						// New task opens the new chat page (a cloud project, the New task
						// modal), like ⌘N, after the palette closes.
						closePalette();
						useUiStore.getState().requestNewTask(action.projectId, params.hostId);
						break;
					case "open-new-project":
						openNewProject();
						break;
					case "open-orchestrator":
							await openOrchestrator(action.projectId, isCurrentRun);
							break;
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
		[findLocalChatWorkspace, navigateToTarget, closePalette, toggleTheme, openOrchestrator, resumeSession, pushView, blockedByRestart, openNewProject, queryClient, t, params.hostId, params.sessionId, fileSessionRoute, sessionRoute, workspaces],
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

	// ⌘K / ⌘⇧P and ⌘P arrive from the main process (it also yields plain Ctrl+K and
	// Ctrl+P to a focused terminal on Windows/Linux), so they work from xterm and
	// the Browser view alike.
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
	// Quick open: the palette's file search for the session on screen. Pressed
	// again while it is showing, it closes, like ⌘K.
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

	// Digits belong to the palette's own list while it is open.
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
		view.mode === "session-actions"
			? (scoped?.session.title ?? t("command.sessionFallback"))
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
	// One notice for everything that went wrong, so a daemon that is down reads
	// as one problem rather than three red boxes.
	const notices = [...new Set([error, fileSearchError, contentSearchError, chatSearchError].filter((message): message is string => Boolean(message)))];
	const selectedItem = visibleItems.find((item) => item.id === value);
	const scopeSessionTitle = view.mode === "file-search"
		? (fileSearchSession?.title ?? t("command.sessionFallback"))
		: view.mode === "root" && activeFileTarget
			? (currentSession?.title ?? t("command.sessionFallback"))
			: undefined;
	const scope = view.mode === "root"
		? [scopeSessionTitle ? t("command.scopeFiles", { session: scopeSessionTitle }) : null, t("command.scopeChats")].filter(Boolean).join(" · ")
		: "";
	// Nested views keep the bar as it is: Back takes the search glyph's place,
	// so the field never moves.
	const backButton = view.mode === "root" ? undefined : (
		<button
			type="button"
			onClick={() => requestDismiss("pop")}
			className="-m-1 grid shrink-0 place-items-center rounded-sm p-1 text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:bg-interactive-hover focus-visible:text-foreground"
			aria-label={t("command.back")}
		>
			<ArrowLeft className="size-icon-base" aria-hidden="true" />
		</button>
	);

	return (
		<>
				<CommandDialog
					// CommandDialog supplies an overlay plus trapped focus without the
					// body-wide scroll/pointer lock that made palette opening scale with
					// every retained shell node.
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
					value,
					onValueChange: setSelectedValue,
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
								// A new query picks its own first row.
								userPickRef.current = false;
								setSelectedValue("");
							}}
							leading={backButton}
							trailing={searching
								? <Loader2 className="size-icon-md shrink-0 animate-spin text-muted-foreground" role="status" aria-label={t("command.searching")} />
								: null}
							placeholder={
								view.mode === "session-actions"
									? t("command.searchActionsPlaceholder")
									: view.mode === "file-session-picker"
										? t("command.chooseFileSession")
										: view.mode === "file-search"
											? t("command.searchFiles")
											: t("command.searchEverywherePlaceholder")
							}
							onKeyDown={(event) => {
								if (event.nativeEvent.isComposing) return;
								// ⌘↵ (or → at the end of the query) opens the selected row's
								// alternative, such as a session's actions.
								const wantsSecondary = (event.key === "Enter" && (event.metaKey || event.ctrlKey))
									|| (event.key === "ArrowRight" && !event.shiftKey && !event.metaKey && !event.altKey && !event.ctrlKey
										&& event.currentTarget.selectionStart === query.length);
								// A row an earlier search found, which cmdk selected on its own,
								// is not opened until the user moves onto it. cmdk keeps its own
								// copy of the selection, so ask the list which row it has.
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
								if (event.key === "Backspace" && query === "" && viewRef.current.mode !== "root") {
									event.preventDefault();
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
										: t("command.noResults")}
							</CommandEmpty>
							{groups.map((group) => (
								<CommandGroup key={group.id} heading={group.label || undefined}>
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
						</CommandList>
						{view.mode === "file-search" && fileSearchEnabled && !searchCatchingUp && fileSearchQuery.data?.truncated && (
							<div role="status" className="px-[var(--size-command-pad-x)] py-2 text-caption text-muted-foreground">{t("command.searchFilesTruncated")}</div>
						)}
						<CommandFooter aria-hidden="true">
							<FooterHint keys={["↑", "↓"]} label={t("command.navigate")} />
							{selectedItem && <FooterHint keys={["Enter"]} label={t("command.open")} />}
							{selectedItem?.secondaryAction && <FooterHint keys={[isMac ? "⌘" : "Ctrl", "Enter"]} label={t("command.actions")} />}
							<FooterHint keys={["Esc"]} label={view.mode === "root" ? t("command.close") : t("command.back")} />
							<span className="ml-auto min-w-0 truncate">{scope}</span>
						</CommandFooter>
					</>
			</CommandDialog>

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
}

/** Whether a palette action closes the palette into another modal. */
function opensModal(action: NonNullable<CommandItemModel["action"]>, taskOpensModal: boolean): boolean {
	switch (action.kind) {
		case "open-new-task":
			return taskOpensModal;
		case "open-new-project":
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

/** Groups whose subtitle is a folder path: cut from its start, where it repeats. */
const PATH_SUBTITLE_GROUPS = new Set<CommandItemModel["group"]>(["files", "content"]);

/**
 * One palette row: a fixed leading column (a session's status dot or the
 * command's icon), the title with what the query matched drawn brighter, its
 * context in a quieter tone, and at the end the row's keys or state.
 */
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
	const Icon = iconForCommand(item);
	const pathSubtitle = PATH_SUBTITLE_GROUPS.has(item.group);
	// A row that leads to a list of choices rather than doing something.
	const opensPicker = item.action?.kind === "open-file-search" && !item.action.target;
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
				) : Icon ? (
					<Icon strokeWidth={1.75} />
				) : null}
			</span>
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="flex min-w-0 items-baseline gap-2">
					<span className="max-w-full shrink-0 truncate" data-slot={item.emphasis && !query ? undefined : "command-item-title"}>
						{item.group === "content" && item.title.includes(":") ? (
							<>
								{item.title.slice(0, item.title.lastIndexOf(":"))}
								<span className="text-muted-foreground">{item.title.slice(item.title.lastIndexOf(":"))}</span>
							</>
						) : item.emphasis && !query ? (
							<TitleIn title={item.title} name={item.emphasis} />
						) : (
							<Highlighted text={item.title} spans={item.contentMatch ? [] : titleHighlights(query, item.title)} />
						)}
					</span>
					{item.subtitle ? (
						pathSubtitle ? (
							// Right-to-left layout puts the ellipsis at the start of a long
							// path; <bdi> keeps the path itself reading left to right.
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
				<span className="shrink-0 text-caption text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">{item.disabledReason}</span>
			) : keys ? (
				<CommandShortcut keys={keys} />
			) : opensPicker ? (
				<ChevronRight className="size-icon-md" aria-hidden="true" />
			) : null}
		</CommandItem>
	);
}

/** "New task in **app**", in the word order the locale gives. */
function TitleIn({ title, name }: { title: ReactNode; name: string }) {
	const { t } = useTranslation();
	const template = t("command.titleIn", { title: "\u0001", name: "\u0002", interpolation: { escapeValue: false } });
	return (
		<>
			{template.split(/([\u0001\u0002])/).map((part, index) =>
				part === "\u0001" ? <span key={index} data-slot="command-item-title">{title}</span>
					: part === "\u0002" ? (
						<span key={index} className="ml-1 font-medium text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">
							<Folder className="mr-1 inline size-icon-md! -translate-y-px align-middle" aria-hidden="true" />
							{name}
						</span>
					)
						: part)}
		</>
	);
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

/** Characters of context kept before the first match when the line is cut. */
const SNIPPET_LEAD = 16;

/**
 * One matched line with the matched spans in the foreground. A line whose
 * first match sits deep in it starts just before that match, so the reason
 * for the hit is not lost to truncation.
 */
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
